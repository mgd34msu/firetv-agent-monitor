// Package muse detects open Muse Code sessions (Meta's coding agent).
//
// A session is open while a muse-bin-<version> process runs. Its session is
// the one named on its command line ("resume <id>"), or else the most
// recently active session of its working directory in
// ~/.local/share/muse/session-index.db. The status comes from the tail of
// that session's session.jsonl event log:
//
//	runtime.user_intent.materialized, outcome top_level_turn_started  working
//	run event user_input_prompt_requested                             waiting (a question for you)
//	run event user_input_prompt_settled                               working
//	run event terminal, terminal "completed" or "cancelled"           waiting
//	run event terminal with another outcome, model_failure_terminal_intent  error
//
// Only the latest of these counts. A tail without any of them belongs to a
// long turn when the log moved within the last two minutes.
package muse

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/tonylook/managents/helper/internal/agent"
	"github.com/tonylook/managents/helper/internal/detect"
	"github.com/tonylook/managents/helper/internal/process"

	_ "modernc.org/sqlite" // database/sql driver "sqlite"
)

// KindMuse is the protocol kind of a Muse Code session.
const KindMuse agent.Kind = "muse"

// tailBytes is how much of a session log is read. Muse logs a lot per turn,
// so this covers the last few hundred events.
const tailBytes = 4 << 20

// recentWrite is how long after its last write a log without a turn marker in
// its tail still counts as a turn in progress.
const recentWrite = 2 * time.Minute

// ProcessFinder lists processes. process.System implements it.
type ProcessFinder interface {
	FindByNamePrefix(ctx context.Context, prefix string) ([]process.Info, error)
}

// Source detects Muse Code sessions.
type Source struct {
	Processes ProcessFinder
	// DataDir is Muse's data directory, ~/.local/share/muse.
	DataDir string
	// Activity adds a line on what the session is doing or asking.
	Activity bool
	// Titles sends the session's name.
	Titles bool
}

// NewSource returns a Source for the user whose home directory is home.
func NewSource(home string, processes ProcessFinder) *Source {
	return &Source{Processes: processes, DataDir: filepath.Join(home, ".local", "share", "muse"), Activity: true, Titles: true}
}

// Name implements detect.Source.
func (s *Source) Name() string { return string(KindMuse) }

type indexed struct {
	ID      string
	LogPath string
	Dir     string
	Title   string
}

var sessionIDArg = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Sessions implements detect.Source.
func (s *Source) Sessions(ctx context.Context, now time.Time) ([]agent.Session, error) {
	procs, err := s.Processes.FindByNamePrefix(ctx, "muse-bin")
	if err != nil || len(procs) == 0 {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn(filepath.Join(s.DataDir, "session-index.db")))
	if err != nil {
		return nil, err
	}
	defer db.Close()

	var sessions []agent.Session
	var errs []error
	seen := map[string]bool{}
	for _, p := range procs {
		if p.Dir == "" {
			continue
		}
		rec, ok, err := lookup(ctx, db, p)
		if err != nil {
			errs = append(errs, err)
		}
		if !ok || seen[rec.ID] {
			continue
		}
		seen[rec.ID] = true
		session := agent.Session{
			ID:        string(KindMuse) + ":" + rec.ID,
			Kind:      KindMuse,
			Dir:       cmp.Or(rec.Dir, p.Dir),
			Status:    agent.StatusWaiting,
			Since:     p.StartedAt,
			StartedAt: p.StartedAt,
		}
		if s.Titles {
			session.Title = rec.Title
		}
		state, err := readState(rec.LogPath, now)
		if err != nil {
			errs = append(errs, err)
		} else if state.found {
			session.Status = state.status
			session.Since = state.since
			if s.Activity {
				session.Activity = state.activity
			}
			session.Context = state.context
			if state.context != nil {
				state.context.Limit = s.contextLimit(state.model)
			}
		}
		if session.Status == agent.StatusWaiting && now.Sub(session.Since) >= agent.DormantAfter {
			session.Status = agent.StatusIdle
		}
		sessions = append(sessions, session)
	}
	return sessions, errors.Join(errs...)
}

const byID = `SELECT session_id, session_log_path, COALESCE(workspace_root, ''),
  COALESCE(session_name, '')
FROM sessions WHERE session_id = ?`

const byDir = `SELECT session_id, session_log_path, COALESCE(workspace_root, ''),
  COALESCE(session_name, '')
FROM sessions WHERE workspace_root = ?
ORDER BY COALESCE(msp_last_activity_at_us, updated_at_us, 0) DESC LIMIT 1`

func lookup(ctx context.Context, db *sql.DB, p process.Info) (indexed, bool, error) {
	query, arg := byDir, p.Dir
	for _, a := range p.Args {
		if sessionIDArg.MatchString(a) {
			query, arg = byID, a
		}
	}
	var r indexed
	err := db.QueryRowContext(ctx, query, arg).Scan(&r.ID, &r.LogPath, &r.Dir, &r.Title)
	if errors.Is(err, sql.ErrNoRows) {
		return r, false, nil
	}
	if err != nil {
		return r, false, err
	}
	r.Title = oneLine(r.Title, 120)
	return r, true, nil
}

// contextLimit is the model's context window from Muse's model catalog, or 0.
func (s *Source) contextLimit(model string) int {
	if model == "" {
		return 0
	}
	files, _ := filepath.Glob(filepath.Join(s.DataDir, "model-catalog", "*.json"))
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var catalog struct {
			Rows []struct {
				ModelID      string `json:"model_id"`
				ContextLimit *int   `json:"context_limit"`
			} `json:"rows"`
		}
		if json.Unmarshal(data, &catalog) != nil {
			continue
		}
		for _, r := range catalog.Rows {
			if r.ModelID == model && r.ContextLimit != nil {
				return *r.ContextLimit
			}
		}
	}
	return 0
}

// --- the session log -----------------------------------------------------------

type state struct {
	found    bool
	status   agent.Status
	since    time.Time
	activity string
	context  *agent.ContextUsage
	model    string
}

type record struct {
	RecordedAt int64 `json:"recorded_at"` // microseconds
	Payload    struct {
		Kind    string `json:"kind"`
		Outcome *struct {
			Kind string `json:"kind"`
		} `json:"outcome"`
		Event *struct {
			Kind      string `json:"kind"`
			Terminal  string `json:"terminal"`
			Questions []struct {
				Header   string `json:"header"`
				Question string `json:"question"`
			} `json:"questions"`
			Model string `json:"model"`
			Usage *struct {
				InputTokens int `json:"input_tokens"` // includes cached tokens
			} `json:"usage"`
		} `json:"event"`
	} `json:"payload"`
}

func readState(path string, now time.Time) (state, error) {
	data, complete, err := detect.ReadTail(path, tailBytes)
	if err != nil {
		return state{}, err
	}
	if !complete {
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			data = data[i+1:]
		}
	}
	var st state
	var last time.Time
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64<<10), tailBytes)
	for sc.Scan() {
		line := sc.Bytes()
		// Skip the bulk of the log (model output, tool results) unparsed.
		if !bytes.Contains(line, []byte(`top_level_turn_started`)) && !bytes.Contains(line, []byte(`"kind":"run"`)) {
			if t := recordedAt(line); !t.IsZero() {
				last = t
			}
			continue
		}
		var r record
		if json.Unmarshal(line, &r) != nil {
			continue
		}
		at := time.UnixMicro(r.RecordedAt)
		last = at
		if r.Payload.Outcome != nil && r.Payload.Outcome.Kind == "top_level_turn_started" {
			st.set(agent.StatusWorking, at, "")
			continue
		}
		ev := r.Payload.Event
		if r.Payload.Kind != "run" || ev == nil {
			continue
		}
		switch ev.Kind {
		case "user_input_prompt_requested":
			ask := ""
			if len(ev.Questions) > 0 {
				ask = cmp.Or(ev.Questions[0].Question, ev.Questions[0].Header)
			}
			st.set(agent.StatusWaiting, at, oneLine(ask, 100))
		case "user_input_prompt_settled":
			st.set(agent.StatusWorking, at, "")
		case "terminal":
			if ev.Terminal == "completed" || ev.Terminal == "cancelled" {
				st.set(agent.StatusWaiting, at, "")
			} else {
				st.set(agent.StatusError, at, "")
			}
		case "model_failure_terminal_intent":
			st.set(agent.StatusError, at, "")
		case "model_completed":
			if u := ev.Usage; u != nil && u.InputTokens > 0 {
				st.context = &agent.ContextUsage{Used: u.InputTokens}
				st.model = ev.Model
			}
		}
	}
	if !st.found && !last.IsZero() {
		// The turn marker is older than the tail: a long turn while the log
		// still moves, else finished since the last write.
		if now.Sub(last) < recentWrite {
			st.set(agent.StatusWorking, last, "")
		} else {
			st.set(agent.StatusWaiting, last, "")
		}
	}
	return st, nil
}

func (st *state) set(status agent.Status, at time.Time, activity string) {
	if !st.found || st.status != status {
		st.since = at
	}
	st.found = true
	st.status = status
	st.activity = activity
}

var recordedAtField = []byte(`"recorded_at":`)

// recordedAt reads a record's timestamp without parsing the whole line.
func recordedAt(line []byte) time.Time {
	i := bytes.Index(line, recordedAtField)
	if i < 0 {
		return time.Time{}
	}
	var us int64
	for _, c := range line[i+len(recordedAtField):] {
		if c < '0' || c > '9' {
			break
		}
		us = us*10 + int64(c-'0')
	}
	if us == 0 {
		return time.Time{}
	}
	return time.UnixMicro(us)
}

func oneLine(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= limit {
		return s
	}
	cut := limit - 3
	for cut > 0 && !utf8Start(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

// dsn opens the index read-only; Muse keeps writing it.
func dsn(path string) string {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	q := url.Values{}
	q.Set("mode", "ro")
	q.Add("_pragma", "busy_timeout(1000)")
	u.RawQuery = q.Encode()
	return u.String()
}

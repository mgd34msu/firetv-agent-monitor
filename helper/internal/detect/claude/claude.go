// Package claude detects Claude Code sessions (terminal and desktop app).
//
// Claude Code keeps a live registry of open sessions in ~/.claude/sessions,
// one <pid>.json per session, including its status. Files left behind by
// crashed sessions are filtered out by checking that the pid is running and
// started when the record says it did. Like Claude Code, the configuration
// directory can be moved with $CLAUDE_CONFIG_DIR.
package claude

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tonylook/managents/helper/internal/agent"
)

// procStartLayout is how Claude Code stores the process start time: the
// output of `ps -o lstart=` in UTC.
const procStartLayout = "Mon Jan _2 15:04:05 2006"

// procStartTolerance absorbs the sub-second precision lost by procStartLayout.
const procStartTolerance = 2 * time.Second

// registryFile matches the registry's file names, as Claude Code's own reader
// does: anything else in the directory is not a session record.
var registryFile = regexp.MustCompile(`^\d+\.json$`)

// Context-window sizes of Claude models. Neither the registry nor the
// transcript says which one a session has, so by default it is inferred:
// standard until the usage proves it must be the extended one.
const (
	StandardContextWindow = 200_000
	ExtendedContextWindow = 1_000_000
)

// StartTimes tells when processes started; process.System is the real one.
type StartTimes interface {
	// StartTime returns when the process started, or false if it is not running.
	StartTime(ctx context.Context, pid int) (time.Time, bool)
}

// Source reads the Claude Code session registry.
type Source struct {
	SessionsDir string // <config dir>/sessions
	ProjectsDir string // <config dir>/projects (transcripts)
	Processes   StartTimes
	// ContextWindow forces the context limit in tokens; 0 infers it.
	ContextWindow int
	// Titles makes sessions carry their title (see title.go). Titles are
	// derived from the conversation, so only network displays the user set up
	// turn this on; the USB display never gets them.
	Titles bool
	// Activity makes sessions carry a few words on what they are doing or need
	// (see activity.go). Like titles, it comes from the conversation, so only
	// network displays turn it on.
	Activity bool

	titles titleReader
}

// NewSource returns a Source for the Claude Code installation of the user
// whose home directory is home: in $CLAUDE_CONFIG_DIR when it is set,
// otherwise in ~/.claude.
func NewSource(home string, processes StartTimes) *Source {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		dir = filepath.Join(home, ".claude")
	}
	return &Source{
		SessionsDir: filepath.Join(dir, "sessions"),
		ProjectsDir: filepath.Join(dir, "projects"),
		Processes:   processes,
	}
}

// Name implements detect.Source.
func (s *Source) Name() string { return string(agent.KindClaude) }

// Sessions implements detect.Source.
func (s *Source) Sessions(ctx context.Context, now time.Time) ([]agent.Session, error) {
	paths, err := filepath.Glob(filepath.Join(s.SessionsDir, "*.json"))
	if err != nil {
		return nil, err
	}
	var sessions []agent.Session
	for _, path := range paths {
		if !registryFile.MatchString(filepath.Base(path)) {
			continue
		}
		record, err := readRecord(path)
		if err != nil || !record.isInteractive() || !s.isAlive(ctx, record) {
			continue
		}
		sessions = append(sessions, s.toSession(record, now))
	}
	if s.Titles {
		s.titles.sweep()
	}
	return sessions, nil
}

// record is the subset of a registry file this package needs.
type record struct {
	PID             int    `json:"pid"`
	SessionID       string `json:"sessionId"`
	Cwd             string `json:"cwd"`
	StartedAt       int64  `json:"startedAt"` // unix ms
	ProcStart       string `json:"procStart"`
	Status          string `json:"status"`
	WaitingFor      any    `json:"waitingFor"`
	StatusUpdatedAt int64  `json:"statusUpdatedAt"` // unix ms
	UpdatedAt       int64  `json:"updatedAt"`       // unix ms
	Spare           bool   `json:"spare"`
	ParkedJobID     string `json:"parkedJobId"`
}

func readRecord(path string) (record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return record{}, err
	}
	var r record
	err = json.Unmarshal(data, &r)
	return r, err
}

// isInteractive excludes pre-spawned spare processes and parked background jobs.
func (r record) isInteractive() bool {
	return r.PID > 0 && !r.Spare && r.ParkedJobID == ""
}

func (r record) isWaitingForUser() bool {
	switch v := r.WaitingFor.(type) {
	case nil:
		return false
	case string:
		return v != ""
	case bool:
		return v
	default:
		return true
	}
}

// changedAt is when the status last changed, falling back to coarser timestamps.
func (r record) changedAt(now time.Time) time.Time {
	for _, ms := range []int64{r.StatusUpdatedAt, r.UpdatedAt, r.StartedAt} {
		if ms > 0 {
			return time.UnixMilli(ms)
		}
	}
	return now
}

func (s *Source) isAlive(ctx context.Context, r record) bool {
	started, running := s.Processes.StartTime(ctx, r.PID)
	if !running {
		return false
	}
	if r.ProcStart == "" {
		return true
	}
	recorded, err := time.ParseInLocation(procStartLayout, strings.Join(strings.Fields(r.ProcStart), " "), time.UTC)
	if err != nil {
		// Unknown format: trust the pid rather than hide a live session.
		return true
	}
	// A different start time means the pid was reused by another process.
	diff := started.Sub(recorded)
	return diff > -procStartTolerance && diff < procStartTolerance
}

func (s *Source) toSession(r record, now time.Time) agent.Session {
	session := agent.Session{
		ID:        string(agent.KindClaude) + ":" + strconv.Itoa(r.PID),
		Kind:      agent.KindClaude,
		Dir:       r.Cwd,
		Since:     r.changedAt(now),
		StartedAt: time.UnixMilli(r.StartedAt),
	}
	if path, err := s.transcriptPath(r.SessionID); err == nil {
		session.Transcript = path
	}
	summary, err := s.readTranscript(r.SessionID)
	if err == nil && summary.context != nil {
		session.Context = &agent.ContextUsage{Used: summary.context.Used, Limit: s.contextLimit(summary.context.Used)}
	}
	session.Status = r.status(err == nil && summary.endsInError, now)
	if s.Titles {
		session.Title = s.title(r.SessionID)
	}
	if s.Activity && err == nil {
		approval := r.Status == "waiting" || r.isWaitingForUser()
		session.Activity = activity(summary.tail, session.Status, approval)
	}
	return session
}

// status maps the registry status, which Claude Code writes for its running,
// requires_action and idle states, to what the display shows:
//
//	busy     working
//	waiting  waiting until the user answers the permission prompt or question
//	idle     waiting for the next prompt, or error if the turn ended in an
//	         API error; idle once untouched for agent.DormantAfter
//
// A pending waitingFor means waiting whatever the status says, and a status
// this package does not know is treated as idle.
func (r record) status(endsInError bool, now time.Time) agent.Status {
	switch {
	case r.Status == "waiting" || r.isWaitingForUser():
		return agent.StatusWaiting
	case r.Status == "busy":
		return agent.StatusWorking
	case now.Sub(r.changedAt(now)) >= agent.DormantAfter:
		return agent.StatusIdle
	case endsInError:
		return agent.StatusError
	default:
		return agent.StatusWaiting
	}
}

func (s *Source) contextLimit(used int) int {
	switch {
	case s.ContextWindow > 0:
		return s.ContextWindow
	case used > StandardContextWindow:
		return ExtendedContextWindow
	default:
		return StandardContextWindow
	}
}

func (s *Source) readTranscript(sessionID string) (transcriptSummary, error) {
	path, err := s.transcriptPath(sessionID)
	if err != nil {
		return transcriptSummary{}, err
	}
	return summarizeTranscript(path)
}

// title is the session's title, or "" when it has none or it cannot be read.
func (s *Source) title(sessionID string) string {
	path, err := s.transcriptPath(sessionID)
	if err != nil {
		return ""
	}
	title, err := s.titles.read(path)
	if err != nil {
		return ""
	}
	return title
}

func (s *Source) transcriptPath(sessionID string) (string, error) {
	if sessionID == "" || strings.ContainsAny(sessionID, `/\`) {
		return "", fs.ErrNotExist
	}
	matches, err := filepath.Glob(filepath.Join(s.ProjectsDir, "*", sessionID+".jsonl"))
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", fs.ErrNotExist
	}
	return matches[0], nil
}

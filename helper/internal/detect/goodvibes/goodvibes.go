// Package goodvibes detects open GoodVibes sessions through the GoodVibes
// daemon's local HTTP API (127.0.0.1:3421 by default).
//
// GET /api/sessions lists the sessions; the active ones are shown. A
// terminal-UI or agent session also needs a fresh liveness marker
// ($GOODVIBES_HOME/.goodvibes/<tui|agent>/liveness/<id>.json, refreshed every
// minute), so a session left "active" by a crashed process disappears.
// GET /api/approvals supplies pending permission prompts.
//
// Status:
//
//	pending or claimed approval for the session   waiting, with the request as activity
//	lastError set                                 error
//	activeAgentId set                             working
//	terminal UI: prompt in the transcript journal working (files.go)
//	terminal UI: otherwise                        waiting
//	otherwise                                     open
//
// The daemon does not see the turns of terminal-UI sessions, which run inside
// their own process; files.go reads them from the session's own files.
//
// The daemon wants the operator token from
// $GOODVIBES_DAEMON_HOME/operator-tokens.json.
package goodvibes

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tonylook/managents/helper/internal/agent"
	"github.com/tonylook/managents/helper/internal/detect"
)

// KindGoodVibes is the protocol kind of a GoodVibes session.
const KindGoodVibes agent.Kind = "goodvibes"

// StatusOpen is a session whose turn state is unknown.
const StatusOpen agent.Status = "open"

const livenessStale = 150 * time.Second

// Source detects GoodVibes sessions.
type Source struct {
	// BaseURL is the daemon, such as http://127.0.0.1:3421.
	BaseURL string
	// Home is GOODVIBES_HOME; DaemonHome is GOODVIBES_DAEMON_HOME.
	Home, DaemonHome string
	Client           *http.Client
	Titles           bool
	Activity         bool

	mu    sync.Mutex
	since map[string]seen
}

// NewSource returns a Source for the GoodVibes install of the user whose home
// directory is home. GOODVIBES_HOME, GOODVIBES_DAEMON_HOME and
// GOODVIBES_DAEMON_URL override the defaults of the goodvibes-jev install.
func NewSource(home string) *Source {
	gvHome := cmp.Or(os.Getenv("GOODVIBES_HOME"), filepath.Join(home, ".local", "share", "goodvibes-jev-data"))
	return &Source{
		BaseURL:    strings.TrimRight(cmp.Or(os.Getenv("GOODVIBES_DAEMON_URL"), "http://127.0.0.1:3421"), "/"),
		Home:       gvHome,
		DaemonHome: cmp.Or(os.Getenv("GOODVIBES_DAEMON_HOME"), filepath.Join(gvHome, ".goodvibes", "daemon")),
		Client:     &http.Client{Timeout: 2 * time.Second},
		Titles:     true,
		Activity:   true,
	}
}

// Name implements detect.Source.
func (s *Source) Name() string { return string(KindGoodVibes) }

type sessionRecord struct {
	ID             string `json:"id"`
	Kind           string `json:"kind"`
	Project        string `json:"project"`
	Title          string `json:"title"`
	Status         string `json:"status"`
	CreatedAt      int64  `json:"createdAt"` // milliseconds
	LastActivityAt int64  `json:"lastActivityAt"`
	ActiveAgentID  string `json:"activeAgentId"`
	LastError      any    `json:"lastError"`
}

type approval struct {
	SessionID string `json:"sessionId"`
	Status    string `json:"status"`
	CreatedAt int64  `json:"createdAt"`
	Request   struct {
		Tool     string `json:"tool"`
		Analysis struct {
			Summary string `json:"summary"`
		} `json:"analysis"`
	} `json:"request"`
}

// Sessions implements detect.Source. Without a daemon or a token there is
// nothing to show and nothing wrong: GoodVibes is simply not running.
func (s *Source) Sessions(ctx context.Context, now time.Time) ([]agent.Session, error) {
	token, err := s.token()
	if err != nil {
		return nil, nil
	}
	var list struct {
		Sessions []sessionRecord `json:"sessions"`
	}
	if err := s.get(ctx, token, "/api/sessions", &list); err != nil {
		if errors.Is(err, errUnreachable) {
			return nil, nil
		}
		return nil, err
	}
	var approvals struct {
		Approvals []approval `json:"approvals"`
	}
	approvalErr := s.get(ctx, token, "/api/approvals", &approvals)
	pending := map[string]approval{}
	for _, a := range approvals.Approvals {
		if a.Status == "pending" || a.Status == "claimed" {
			if old, ok := pending[a.SessionID]; !ok || a.CreatedAt < old.CreatedAt {
				pending[a.SessionID] = a
			}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.since == nil {
		s.since = map[string]seen{}
	}
	var sessions []agent.Session
	live := map[string]bool{}
	for _, r := range list.Sessions {
		if r.Status != "active" || !s.alive(r, now) {
			continue
		}
		live[r.ID] = true
		session := agent.Session{
			ID:        string(KindGoodVibes) + ":" + r.ID,
			Kind:      KindGoodVibes,
			Dir:       r.Project,
			Status:    StatusOpen,
			StartedAt: time.UnixMilli(r.CreatedAt),
		}
		title := r.Title
		if r.Kind == "tui" {
			// The daemon does not see a terminal session's turns; its files do.
			conv, ok := s.readConversation(r.Project, r.ID)
			session.Transcript = recoveryPath(r.Project, r.ID)
			switch {
			case s.turnRunning(r.ID):
				session.Status = agent.StatusWorking
				session.Activity = conv.doing
			case ok:
				session.Status = agent.StatusWaiting
				session.Activity = conv.said
			}
			session.Context = conv.context
			title = cmp.Or(conv.title, title)
		}
		switch a, ok := pending[r.ID]; {
		case ok:
			session.Status = agent.StatusWaiting
			session.Activity = cmp.Or(a.Request.Analysis.Summary, "Approve "+a.Request.Tool)
		case r.LastError != nil && r.LastError != "":
			session.Status = agent.StatusError
		case r.ActiveAgentID != "":
			session.Status = agent.StatusWorking
		}
		if s.Titles && title != "" && title != "Terminal UI session" {
			session.Title = detect.Truncate(title, 120)
		}
		if !s.Activity {
			session.Activity = ""
		}
		session.Activity = detect.Truncate(session.Activity, 100)

		prev, known := s.since[r.ID]
		switch {
		case known && prev.status == session.Status:
			session.Since = prev.since
		case known:
			session.Since = now
		default:
			// First sight: when the session started, as good a guess as any.
			session.Since = session.StartedAt
		}
		s.since[r.ID] = seen{session.Status, session.Since}
		if session.Status == agent.StatusWaiting && now.Sub(session.Since) >= agent.DormantAfter {
			session.Status = agent.StatusIdle
		}
		sessions = append(sessions, session)
	}
	for id := range s.since {
		if !live[id] {
			delete(s.since, id)
		}
	}
	return sessions, approvalErr
}

// alive checks the liveness marker of terminal-UI and agent sessions; other
// kinds live in the daemon itself.
func (s *Source) alive(r sessionRecord, now time.Time) bool {
	if r.Kind != "tui" && r.Kind != "agent" {
		return true
	}
	data, err := os.ReadFile(filepath.Join(s.Home, ".goodvibes", r.Kind, "liveness", r.ID+".json"))
	if err != nil {
		return errors.Is(err, os.ErrNotExist) && !s.hasLiveness(r.Kind)
	}
	var marker struct {
		UpdatedAt int64 `json:"updatedAt"`
	}
	if json.Unmarshal(data, &marker) != nil {
		return true
	}
	return now.Sub(time.UnixMilli(marker.UpdatedAt)) < livenessStale
}

// hasLiveness reports whether this GoodVibes writes liveness markers at all;
// older versions do not, and their sessions are trusted as listed.
func (s *Source) hasLiveness(kind string) bool {
	_, err := os.Stat(filepath.Join(s.Home, ".goodvibes", kind, "liveness"))
	return err == nil
}

func (s *Source) token() (string, error) {
	data, err := os.ReadFile(filepath.Join(s.DaemonHome, "operator-tokens.json"))
	if err != nil {
		return "", err
	}
	var t struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data, &t); err != nil || t.Token == "" {
		return "", errors.New("no token in operator-tokens.json")
	}
	return t.Token, nil
}

var errUnreachable = errors.New("goodvibes daemon unreachable")

func (s *Source) get(ctx context.Context, token, path string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.BaseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := s.Client.Do(req)
	if err != nil {
		return errUnreachable
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", path, res.Status)
	}
	return json.NewDecoder(res.Body).Decode(into)
}

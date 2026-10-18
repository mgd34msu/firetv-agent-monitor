// Package codex detects Codex CLI sessions.
//
// Every running `codex` process that is not one of its services is a session
// for its working directory. Its thread is the newest one Codex recorded for
// that directory in its state database, and the status comes from the end of
// the thread's rollout, the JSON-lines log of its turns. Like Codex, the data
// directory can be moved with $CODEX_HOME.
//
// Codex does not record a pending approval prompt. Its tell is a tool call
// that has had no output for a while although no command of the turn runs: a
// running command is a child process of Codex (of the TUI, or of the app
// server that runs the TUI's turns), started during the turn, in the
// session's directory. A long command is polled by later tool calls while it
// runs, so it counts from the start of the turn, not of the newest call.
package codex

import (
	"cmp"
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tonylook/managents/helper/internal/agent"
	"github.com/tonylook/managents/helper/internal/detect"
	"github.com/tonylook/managents/helper/internal/process"
)

const (
	// toolStaleAfter: a tool call this old without its output, while no
	// command of the turn runs, is waiting on an approval or a question.
	toolStaleAfter = 6 * time.Second
	// spawnTolerance absorbs the clock and rounding differences between when
	// Codex wrote the start of a turn and when the operating system says a
	// command of the turn started.
	spawnTolerance = 2 * time.Second
)

// notSessions are the codex subcommands that are not a session the user
// works in: the app server that runs the TUI's turns in the background, the
// MCP server, the cloud tasks browser and the helpers, and `codex exec`, which
// never waits for the user and is mostly run by scripts and other agents.
var notSessions = []string{
	"exec", "e", "app-server", "mcp-server", "mcp", "cloud", "responses-api-proxy", "stdio-to-uds",
	"sandbox", "debug", "login", "logout", "completion", "apply", "features",
}

// Thread is the part of a Codex thread this package needs.
type Thread struct {
	RolloutPath string
}

// Store finds the thread of a folder.
type Store interface {
	LastThread(ctx context.Context, dir string) (Thread, bool, error)
}

// ProcessFinder lists running processes; process.System is the real one.
type ProcessFinder interface {
	// FindByName lists running processes whose executable name is exactly name.
	FindByName(ctx context.Context, name string) ([]process.Info, error)
	// Children lists the running processes whose parent is one of parents.
	Children(ctx context.Context, parents []int) ([]process.Info, error)
}

// Source detects running Codex sessions.
type Source struct {
	Processes ProcessFinder
	Store     Store
	// Activity adds a line on what a session is doing or last said, read
	// from the rollout's messages and tool calls.
	Activity bool
}

// NewSource returns a Source for the Codex installation of the user whose
// home directory is home.
func NewSource(home string, processes ProcessFinder) *Source {
	return &Source{Processes: processes, Store: SQLiteStore{Home: DefaultHome(home)}}
}

// Name implements detect.Source.
func (s *Source) Name() string { return string(agent.KindCodex) }

// Sessions implements detect.Source. A folder whose history cannot be read
// is still shown, as waiting, and the errors are returned.
func (s *Source) Sessions(ctx context.Context, now time.Time) ([]agent.Session, error) {
	procs, err := s.Processes.FindByName(ctx, "codex")
	if err != nil {
		return nil, err
	}
	// The oldest process in a folder owns its card, as for OpenCode.
	slices.SortFunc(procs, func(a, b process.Info) int {
		return cmp.Or(a.StartedAt.Compare(b.StartedAt), cmp.Compare(a.PID, b.PID))
	})
	commands := &commandFinder{processes: s.Processes, codex: procs}
	var sessions []agent.Session
	var errs []error
	seen := make(map[string]bool)
	for _, p := range procs {
		if p.Dir == "" || seen[p.Dir] || !isSession(p.Args) {
			continue
		}
		seen[p.Dir] = true
		session, err := s.toSession(ctx, p, commands, now)
		sessions = append(sessions, session)
		errs = append(errs, err)
	}
	return sessions, errors.Join(errs...)
}

// isSession reports a codex command line that is a session: the TUI, with
// or without a first prompt, or one of its subcommands such as resume. The
// first argument is the program; flags may come before the subcommand.
func isSession(args []string) bool {
	for _, arg := range args[min(1, len(args)):] {
		if !strings.HasPrefix(arg, "-") {
			return !slices.Contains(notSessions, arg)
		}
	}
	return true
}

func (s *Source) toSession(ctx context.Context, p process.Info, commands *commandFinder, now time.Time) (agent.Session, error) {
	session := agent.Session{
		ID:        string(agent.KindCodex) + ":" + strconv.Itoa(p.PID),
		Kind:      agent.KindCodex,
		Dir:       p.Dir,
		Status:    agent.StatusWaiting,
		Since:     p.StartedAt,
		StartedAt: p.StartedAt,
	}
	thread, found, err := s.Store.LastThread(ctx, p.Dir)
	if err != nil || !found {
		return session, err
	}
	summary, err := summarizeRollout(thread.RolloutPath)
	if errors.Is(err, fs.ErrNotExist) {
		return session, nil // Codex writes the rollout with the first prompt
	}
	if err != nil {
		return session, err
	}
	session.Context = summary.context
	session.Status, session.Since = statusOf(summary, p.StartedAt, now)
	if s.Activity {
		if tail, _, err := detect.ReadTail(thread.RolloutPath, rolloutTail); err == nil {
			session.Activity = activityOf(tail, session.Status)
		}
	}
	if session.Status != agent.StatusWorking || summary.openCall.IsZero() || now.Sub(summary.openCall) <= toolStaleAfter {
		return session, nil
	}
	running, err := commands.runningSince(ctx, p.Dir, summary.at)
	if !running && err == nil {
		session.Status, session.Since = agent.StatusWaiting, summary.openCall
	}
	return session, err
}

// statusOf maps the newest turn of a thread to a status and when it began.
// started is when the codex process started: a turn written before that
// belongs to a previous run, so it is neither still working nor this run's
// error, and the session waits since it was opened.
func statusOf(summary rolloutSummary, started, now time.Time) (agent.Status, time.Time) {
	live := !summary.at.Before(started)
	since := summary.at
	if !live {
		since = started
	}
	switch {
	case summary.turn == noTurn:
		return agent.StatusWaiting, started
	case live && summary.turn == turnRunning:
		return agent.StatusWorking, since
	case now.Sub(since) >= agent.DormantAfter:
		return agent.StatusIdle, since
	case live && summary.failed:
		return agent.StatusError, since
	default:
		return agent.StatusWaiting, since
	}
}

// commandFinder looks for the commands Codex runs. The children of every
// codex process are listed at most once per detection, and only if a session
// needs them.
type commandFinder struct {
	processes ProcessFinder
	codex     []process.Info
	children  []process.Info
	listed    bool
	err       error
}

// runningSince reports a command started since the turn began at turn,
// running in dir or below. Servers started with the session and commands left
// running by an earlier turn started before it; Codex's own helpers (the
// code-mode host, the sandbox) are not commands.
func (c *commandFinder) runningSince(ctx context.Context, dir string, turn time.Time) (bool, error) {
	if !c.listed {
		c.listed = true
		pids := make([]int, len(c.codex))
		for i, p := range c.codex {
			pids[i] = p.PID
		}
		c.children, c.err = c.processes.Children(ctx, pids)
	}
	for _, child := range c.children {
		if !strings.HasPrefix(child.Name, "codex") && !child.StartedAt.Before(turn.Add(-spawnTolerance)) && within(child.Dir, dir) {
			return true, nil
		}
	}
	return false, c.err
}

// within reports whether path is dir or inside it.
func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return path != "" && err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

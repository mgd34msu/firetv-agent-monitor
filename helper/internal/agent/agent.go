// Package agent holds the domain model: coding-agent sessions running on this
// computer, their status, and how they are presented on the display.
package agent

import "time"

// Kind identifies the agent product that owns a session.
type Kind string

const (
	KindClaude   Kind = "claude"
	KindOpenCode Kind = "opencode"
	KindCodex    Kind = "codex"
)

// Status is what the agent is doing right now, from the user's point of view.
type Status string

const (
	// StatusWorking means the agent is running a turn.
	StatusWorking Status = "working"
	// StatusWaiting means the agent is blocked on the user: a permission prompt,
	// a question, or a finished turn awaiting the next prompt.
	StatusWaiting Status = "waiting"
	// StatusError means the last turn ended in an API or model error.
	StatusError Status = "error"
	// StatusIdle means the session is open but untouched for a long time.
	StatusIdle Status = "idle"
)

// DormantAfter is how long a session whose turn has ended stays "waiting" or
// "error" before it is shown as idle (dimmed). A session blocked on a prompt
// keeps waiting.
const DormantAfter = 2 * time.Hour

// ContextUsage is how full the session's context window is. Limit is zero when
// unknown.
type ContextUsage struct {
	Used  int
	Limit int
}

// Session is one open agent session as detected on this computer.
type Session struct {
	// ID is stable for the life of the session: "<kind>:<pid or session id>".
	ID        string
	Kind      Kind
	Dir       string
	Status    Status
	Since     time.Time // when Status last changed
	StartedAt time.Time // orders cards: oldest session first
	Context   *ContextUsage
	// Title is the session's title as the agent shows it, when the detector
	// was asked to read titles. Empty otherwise.
	Title string
	// Activity is a few words on what the session is doing, or what it needs
	// while it waits, when the detector was asked for it. Empty otherwise.
	Activity string
	// Transcript is the session's log file, for the read-only session view
	// of network displays. It never goes over the wire. Empty if unknown.
	Transcript string
}

// Age is how long the session has been in its current status.
func (s Session) Age(now time.Time) time.Duration {
	if s.Since.IsZero() || now.Before(s.Since) {
		return 0
	}
	return now.Sub(s.Since)
}

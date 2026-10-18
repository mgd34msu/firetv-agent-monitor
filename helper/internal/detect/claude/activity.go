package claude

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/tonylook/managents/helper/internal/agent"
	"github.com/tonylook/managents/helper/internal/detect"
)

// MaxActivityBytes bounds an activity line; cards show it on one or two lines.
const MaxActivityBytes = 100

// Tool names whose input says what the session is doing or asking.
const (
	toolPlan     = "mcp__ccd_turn__update_plan" // the Claude app's task list
	toolWrapUp   = "mcp__ccd_turn__wrap_up"     // the Claude app's end-of-turn question
	toolTodo     = "TodoWrite"                  // the terminal's task list
	toolQuestion = "AskUserQuestion"
)

// activityEntry declares only what an activity line is made from: the text of
// the model's replies, and the few tool inputs that describe an action. It is
// decoded only when Source.Activity is set; the entry type in transcript.go
// stays free of conversation content.
type activityEntry struct {
	Type        string `json:"type"`
	IsSidechain bool   `json:"isSidechain"`
	Message     *struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type activityBlock struct {
	Type  string         `json:"type"`
	Text  string         `json:"text"`
	Name  string         `json:"name"`
	Input *activityInput `json:"input"`
}

type activityInput struct {
	Description string `json:"description"` // Bash, Agent
	FilePath    string `json:"file_path"`   // Read, Edit, Write
	Path        string `json:"notebook_path"`
	Ask         string `json:"ask"` // wrap_up
	Questions   []struct {
		Question string `json:"question"`
	} `json:"questions"` // AskUserQuestion
	Items []struct {
		Content string `json:"content"`
		Status  string `json:"status"`
	} `json:"items"` // update_plan
	Advance bool  `json:"advance"`
	Done    []int `json:"done"`
	Start   int   `json:"start"`
	Todos   []struct {
		Content    string `json:"content"`
		Status     string `json:"status"`
		ActiveForm string `json:"activeForm"`
	} `json:"todos"` // TodoWrite
}

// activity is a few words on what the session is doing or what it needs, made
// from the end of its transcript:
//
//	working                the task list's step in progress, else the last tool call
//	waiting for approval   "Approve: " and the tool call, or the question asked
//	waiting otherwise      the question the turn ended with, else its last sentence
//
// Idle and failed sessions have none.
func activity(tail []byte, status agent.Status, approval bool) string {
	var line string
	switch status {
	case agent.StatusWorking:
		line = workingActivity(tail)
	case agent.StatusWaiting:
		line = waitingActivity(tail, approval)
	}
	return truncateUTF8(line, MaxActivityBytes)
}

func workingActivity(tail []byte) string {
	turn := currentTurn(tail)
	if step := planStep(tail); step != "" {
		return step
	}
	for i := len(turn) - 1; i >= 0; i-- {
		if turn[i].Type == "tool_use" {
			return describeTool(turn[i])
		}
	}
	return ""
}

func waitingActivity(tail []byte, approval bool) string {
	turn := currentTurn(tail)
	if approval {
		for i := len(turn) - 1; i >= 0; i-- {
			if turn[i].Type != "tool_use" {
				continue
			}
			if q := question(turn[i]); q != "" {
				return q
			}
			if d := describeTool(turn[i]); d != "" {
				return "Approve: " + d
			}
			return ""
		}
		return ""
	}
	for i := len(turn) - 1; i >= 0; i-- {
		block := turn[i]
		if block.Type == "tool_use" {
			if q := question(block); q != "" {
				return q
			}
		}
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			return closingSentence(block.Text)
		}
	}
	return ""
}

// question is what a tool call asks the user, if it asks anything.
func question(block activityBlock) string {
	if block.Input == nil {
		return ""
	}
	switch block.Name {
	case toolWrapUp:
		return clean(block.Input.Ask)
	case toolQuestion:
		if len(block.Input.Questions) > 0 {
			return clean(block.Input.Questions[0].Question)
		}
	}
	return ""
}

// describeTool says in a few words what a tool call does.
func describeTool(block activityBlock) string {
	in := block.Input
	if in == nil {
		in = &activityInput{}
	}
	file := in.FilePath
	if file == "" {
		file = in.Path
	}
	switch block.Name {
	case "Bash", "Agent", "Task":
		if in.Description != "" {
			return clean(in.Description)
		}
		if block.Name == "Bash" {
			return "Running a command"
		}
		return "Running a subagent"
	case "Read":
		return "Reading " + filepath.Base(file)
	case "Edit", "MultiEdit", "Write", "NotebookEdit":
		return "Editing " + filepath.Base(file)
	case "Grep", "Glob":
		return "Searching the code"
	case "WebFetch", "WebSearch":
		return "Searching the web"
	case toolPlan, toolTodo, toolWrapUp:
		return ""
	}
	if strings.HasPrefix(block.Name, "mcp__") {
		return "Using " + mcpServer(block.Name)
	}
	return "Using " + block.Name
}

// mcpServer is the readable part of an MCP tool name, mcp__<server>__<tool>.
func mcpServer(name string) string {
	parts := strings.Split(strings.TrimPrefix(name, "mcp__"), "__")
	if len(parts) == 2 && !looksLikeID(parts[0]) {
		return parts[0] + " " + strings.ReplaceAll(parts[1], "_", " ")
	}
	return strings.ReplaceAll(parts[len(parts)-1], "_", " ")
}

func looksLikeID(s string) bool { return len(s) >= 32 && strings.Count(s, "-") >= 4 }

// planStep is the step in progress on the session's task list: the Claude
// app's update_plan (a full list, then advance/done/start changes) or the
// terminal's TodoWrite (always the full list).
func planStep(tail []byte) string {
	var steps []struct{ content, status string }
	known := false
	for _, block := range toolCalls(tail) {
		in := block.Input
		if in == nil {
			continue
		}
		switch block.Name {
		case toolTodo:
			steps = steps[:0]
			for _, t := range in.Todos {
				content := t.ActiveForm
				if content == "" {
					content = t.Content
				}
				steps = append(steps, struct{ content, status string }{content, t.Status})
			}
			known = true
		case toolPlan:
			if len(in.Items) > 0 {
				steps = steps[:0]
				for _, item := range in.Items {
					steps = append(steps, struct{ content, status string }{item.Content, item.Status})
				}
				known = true
				continue
			}
			if !known {
				continue // changes to a list that started before the tail
			}
			current := -1
			for i, step := range steps {
				if step.status == "in_progress" {
					current = i
				}
			}
			switch {
			case in.Advance && current >= 0:
				steps[current].status = "completed"
				for i := current + 1; i < len(steps); i++ {
					if steps[i].status == "pending" {
						steps[i].status = "in_progress"
						break
					}
				}
			case len(in.Done) > 0 || in.Start > 0:
				for _, n := range in.Done {
					if n >= 1 && n <= len(steps) {
						steps[n-1].status = "completed"
					}
				}
				if in.Start >= 1 && in.Start <= len(steps) {
					for i := range steps {
						if steps[i].status == "in_progress" {
							steps[i].status = "pending"
						}
					}
					steps[in.Start-1].status = "in_progress"
				}
			}
		case toolWrapUp:
			for i := range steps { // the turn's list is finished
				if steps[i].status == "in_progress" {
					steps[i].status = "completed"
				}
			}
		}
	}
	for _, step := range steps {
		if step.status == "in_progress" {
			return clean(step.content)
		}
	}
	return ""
}

// currentTurn is the main thread's assistant blocks since the user's last
// prompt (tool results do not start a turn).
func currentTurn(tail []byte) []activityBlock {
	var turn []activityBlock
	for _, e := range entries(tail) {
		blocks, prompt := e.blocks()
		if e.Type == "user" {
			if prompt {
				turn = turn[:0]
			}
			continue
		}
		turn = append(turn, blocks...)
	}
	return turn
}

func toolCalls(tail []byte) []activityBlock {
	var calls []activityBlock
	for _, e := range entries(tail) {
		if e.Type != "assistant" {
			continue
		}
		blocks, _ := e.blocks()
		for _, b := range blocks {
			if b.Type == "tool_use" {
				calls = append(calls, b)
			}
		}
	}
	return calls
}

// entries are the main thread's user and assistant entries in tail, oldest
// first. The first line may be cut by the tail and is skipped if it is.
func entries(tail []byte) []activityEntry {
	var result []activityEntry
	for _, line := range bytes.Split(tail, []byte("\n")) {
		var e activityEntry
		if len(bytes.TrimSpace(line)) == 0 || json.Unmarshal(line, &e) != nil {
			continue
		}
		if e.IsSidechain || e.Message == nil || (e.Type != "user" && e.Type != "assistant") {
			continue
		}
		result = append(result, e)
	}
	return result
}

// blocks decodes an assistant entry's content. A user entry's blocks are
// decoded by type only, so the user's prompts are never read; prompt is true
// for an entry the user typed, as opposed to one carrying tool results.
func (e activityEntry) blocks() (blocks []activityBlock, prompt bool) {
	content := bytes.TrimSpace(e.Message.Content)
	if e.Type == "user" {
		if len(content) > 0 && content[0] == '"' {
			return nil, true
		}
		var types []struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(content, &types) != nil {
			return nil, false
		}
		for _, t := range types {
			if t.Type == "tool_result" {
				return nil, false
			}
		}
		return nil, len(types) > 0
	}
	if json.Unmarshal(content, &blocks) != nil {
		return nil, false
	}
	return blocks, false
}

// The prose helpers live in package detect, shared with the other detectors.
func closingSentence(text string) string { return detect.ClosingSentence(text) }
func clean(text string) string           { return detect.Clean(text) }

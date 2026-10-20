// Package transcript reads the recent conversation of one session, for the
// read-only session view of network displays (GET /session in serve, behind
// --transcripts). Unlike the detectors, it reads conversation content on
// purpose: prompts, replies, tool calls and the start of tool output.
//
// Every agent keeps a different log; Read turns each into the same short list
// of messages, oldest first.
package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"strings"

	"github.com/tonylook/managents/helper/internal/detect"
)

// Message is one entry of the view.
type Message struct {
	Role string `json:"role"` // user, assistant, tool
	Text string `json:"text,omitempty"`
	// Tool is the tool a tool call runs (role assistant) or answered (role tool).
	Tool string `json:"tool,omitempty"`
	// Error marks a failed tool call.
	Error bool `json:"error,omitempty"`
}

const (
	tailBytes   = 2 << 20 // how much of a log is read
	maxMessages = 200     // the newest ones are kept
	maxText     = 4000    // bytes per reply or prompt
	maxTool     = 600     // bytes of tool input or output
)

// ErrUnknownKind is returned for an agent this package cannot read.
var ErrUnknownKind = errors.New("no transcript reader for this agent")

// Read returns the newest messages of the log at path, written by kind.
func Read(kind, path string) ([]Message, error) {
	var parse func([]byte, func(Message))
	switch kind {
	case "claude":
		parse = claudeLine
	case "codex":
		parse = codexLine
	case "muse":
		parse = museLine
	case "goodvibes":
		parse = goodvibesLine
	default:
		return nil, ErrUnknownKind
	}
	data, whole, err := detect.ReadTail(path, tailBytes)
	if err != nil {
		return nil, err
	}
	if !whole {
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			data = data[i+1:]
		}
	}
	out := []Message{}
	add := func(m Message) {
		m.Text = strings.TrimSpace(m.Text)
		if m.Text == "" && m.Tool == "" {
			return
		}
		limit := maxText
		if m.Role == "tool" || m.Tool != "" {
			limit = maxTool
		}
		m.Text = detect.Truncate(m.Text, limit)
		out = append(out, m)
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64<<10), tailBytes)
	for sc.Scan() {
		parse(sc.Bytes(), add)
	}
	if len(out) > maxMessages {
		out = out[len(out)-maxMessages:]
	}
	return out, nil
}

// toolText is a tool call's input in one line: its command, path or query
// when it has an obvious one, else the compact JSON.
func toolText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return string(raw)
	}
	for _, key := range []string{"command", "cmd", "description", "file_path", "path", "pattern", "query", "url", "prompt"} {
		if v, ok := fields[key]; ok {
			var s string
			if json.Unmarshal(v, &s) == nil && s != "" {
				return s
			}
		}
	}
	if v, ok := fields["commands"]; ok { // GoodVibes exec
		var cmds []struct {
			Cmd string `json:"cmd"`
		}
		if json.Unmarshal(v, &cmds) == nil && len(cmds) > 0 {
			parts := make([]string, len(cmds))
			for i, c := range cmds {
				parts[i] = c.Cmd
			}
			return strings.Join(parts, "\n")
		}
	}
	var buf bytes.Buffer
	if json.Compact(&buf, raw) == nil {
		return buf.String()
	}
	return string(raw)
}

// --- Claude Code ---------------------------------------------------------------

type claudeEntry struct {
	Type        string `json:"type"`
	IsSidechain bool   `json:"isSidechain"`
	IsMeta      bool   `json:"isMeta"`
	Message     *struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type claudeBlock struct {
	Type    string          `json:"type"` // text, tool_use, tool_result, thinking
	Text    string          `json:"text"`
	Name    string          `json:"name"`
	Input   json.RawMessage `json:"input"`
	Content json.RawMessage `json:"content"` // tool_result: string or blocks
	IsError bool            `json:"is_error"`
}

func claudeLine(line []byte, add func(Message)) {
	var e claudeEntry
	if json.Unmarshal(line, &e) != nil || e.IsSidechain || e.IsMeta || e.Message == nil {
		return
	}
	if e.Type != "user" && e.Type != "assistant" {
		return
	}
	var text string
	if json.Unmarshal(e.Message.Content, &text) == nil {
		if e.Type == "user" && strings.HasPrefix(strings.TrimSpace(text), "<") {
			return // command output and reminders Claude Code injects
		}
		add(Message{Role: e.Type, Text: text})
		return
	}
	var blocks []claudeBlock
	if json.Unmarshal(e.Message.Content, &blocks) != nil {
		return
	}
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if e.Type == "user" && strings.HasPrefix(strings.TrimSpace(b.Text), "<") {
				continue
			}
			add(Message{Role: e.Type, Text: b.Text})
		case "tool_use":
			add(Message{Role: "assistant", Tool: b.Name, Text: toolText(b.Input)})
		case "tool_result":
			add(Message{Role: "tool", Text: claudeResultText(b.Content), Error: b.IsError})
		}
	}
}

func claudeResultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []claudeBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// --- Codex -----------------------------------------------------------------------

type codexEntry struct {
	Type    string `json:"type"`
	Payload struct {
		Type    string          `json:"type"`
		Role    string          `json:"role"`
		Name    string          `json:"name"`
		Input   string          `json:"input"`
		Args    string          `json:"arguments"`
		Output  json.RawMessage `json:"output"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	} `json:"payload"`
}

func codexLine(line []byte, add func(Message)) {
	var e codexEntry
	if json.Unmarshal(line, &e) != nil || e.Type != "response_item" {
		return
	}
	p := e.Payload
	switch p.Type {
	case "message":
		if p.Role != "user" && p.Role != "assistant" {
			return
		}
		for _, c := range p.Content {
			if p.Role == "user" && strings.HasPrefix(strings.TrimSpace(c.Text), "<") {
				continue // environment context and instructions Codex injects
			}
			add(Message{Role: p.Role, Text: c.Text})
		}
	case "function_call", "custom_tool_call", "local_shell_call":
		input := p.Input
		if input == "" {
			input = toolText(json.RawMessage(p.Args))
		}
		add(Message{Role: "assistant", Tool: p.Name, Text: input})
	case "function_call_output", "custom_tool_call_output":
		add(Message{Role: "tool", Text: codexOutput(p.Output)})
	}
}

// codexOutput is a tool call's output, which Codex writes as a string, as
// {"output": ...}, or as a list of text blocks, the first of them a
// "Script completed / Wall time / Output:" preamble.
func codexOutput(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var o struct {
		Output string `json:"output"`
	}
	if json.Unmarshal(raw, &o) == nil && o.Output != "" {
		return o.Output
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		parts = append(parts, b.Text)
	}
	text := strings.Join(parts, "")
	if i := strings.Index(text, "Output:\n"); i >= 0 && strings.HasPrefix(text, "Script ") {
		text = text[i+len("Output:\n"):]
	}
	return text
}

// --- Muse Code -------------------------------------------------------------------

type museRecord struct {
	PayloadType string `json:"payload_type"`
	Payload     struct {
		ModelMessages []struct {
			Content []struct {
				Kind string `json:"kind"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"model_messages"` // runtime.user_intent.accepted
		Event *struct {
			Kind      string `json:"kind"`
			Text      string `json:"text"`
			ToolCalls []struct {
				Name string `json:"name"`
				Args string `json:"args"`
			} `json:"tool_calls"`
			Results []struct {
				Text string `json:"text"`
			} `json:"results"`
		} `json:"event"`
	} `json:"payload"`
}

func museLine(line []byte, add func(Message)) {
	// Most of the log is bookkeeping; parse only the records shown.
	if !bytes.Contains(line, []byte(`user_intent.accepted`)) && !bytes.Contains(line, []byte(`_committed"`)) {
		return
	}
	var r museRecord
	if json.Unmarshal(line, &r) != nil {
		return
	}
	if r.PayloadType == "runtime.user_intent.accepted" {
		for _, m := range r.Payload.ModelMessages {
			for _, c := range m.Content {
				if c.Kind == "text" {
					add(Message{Role: "user", Text: c.Text})
				}
			}
		}
		return
	}
	ev := r.Payload.Event
	if ev == nil {
		return
	}
	switch ev.Kind {
	case "assistant_message_committed":
		add(Message{Role: "assistant", Text: ev.Text})
	case "assistant_tool_calls_committed":
		for _, c := range ev.ToolCalls {
			add(Message{Role: "assistant", Tool: c.Name, Text: toolText(json.RawMessage(c.Args))})
		}
	case "tool_result_batch_committed":
		for _, res := range ev.Results {
			add(Message{Role: "tool", Text: res.Text})
		}
	}
}

// --- GoodVibes -------------------------------------------------------------------

type goodvibesLineT struct {
	Type      string `json:"type"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	ToolName  string `json:"toolName"`
	Outcome   string `json:"outcome"`
	ToolCalls []struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"toolCalls"`
}

func goodvibesLine(line []byte, add func(Message)) {
	var m goodvibesLineT
	if json.Unmarshal(line, &m) != nil || m.Type != "message" {
		return
	}
	switch m.Role {
	case "user":
		add(Message{Role: "user", Text: m.Content})
	case "assistant":
		add(Message{Role: "assistant", Text: m.Content})
		for _, c := range m.ToolCalls {
			add(Message{Role: "assistant", Tool: c.Name, Text: toolText(c.Arguments)})
		}
	case "tool":
		add(Message{Role: "tool", Tool: m.ToolName, Text: m.Content, Error: m.Outcome == "error"})
	}
}

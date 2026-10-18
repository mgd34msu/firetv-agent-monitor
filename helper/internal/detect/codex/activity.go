package codex

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/tonylook/managents/helper/internal/agent"
	"github.com/tonylook/managents/helper/internal/detect"
)

// maxActivityBytes bounds an activity line, like the Claude detector's.
const maxActivityBytes = 100

// activityEntry decodes the content the activity line needs. It is used only
// when Source.Activity is set; entry, in rollout.go, never reads content.
type activityEntry struct {
	Type    string `json:"type"`
	Payload struct {
		Type    string `json:"type"`
		Name    string `json:"name"`
		Input   string `json:"input"`     // custom_tool_call
		Args    string `json:"arguments"` // function_call
		Role    string `json:"role"`
		Phase   string `json:"phase"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		LastAgentMessage string `json:"last_agent_message"` // task_complete
	} `json:"payload"`
}

// activityOf is what a working session is doing (its newest progress note or
// command in the current turn), or what a waiting one last said.
func activityOf(tail []byte, status agent.Status) string {
	var doing, said string
	for _, line := range bytes.Split(tail, []byte("\n")) {
		if !bytes.Contains(line, []byte(`"custom_tool_call"`)) && !bytes.Contains(line, []byte(`"function_call"`)) &&
			!bytes.Contains(line, []byte(`"message"`)) && !bytes.Contains(line, []byte(`"task_`)) {
			continue
		}
		var e activityEntry
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		p := e.Payload
		switch {
		case e.Type == "event_msg" && p.Type == "task_started":
			doing, said = "", ""
		case e.Type == "event_msg" && p.Type == "task_complete":
			said = detect.ClosingSentence(p.LastAgentMessage)
		case e.Type == "response_item" && p.Type == "message" && p.Role == "assistant":
			text := ""
			for _, c := range p.Content {
				text += c.Text + "\n"
			}
			if s := detect.ClosingSentence(text); s != "" {
				said = s
				if p.Phase == "commentary" {
					doing = s
				}
			}
		case e.Type == "response_item" && (p.Type == "custom_tool_call" || p.Type == "function_call"):
			if d := describeCall(p.Name, p.Input+p.Args); d != "" {
				doing = d
			}
		}
	}
	line := said
	if status == agent.StatusWorking {
		line = doing
	}
	return detect.Truncate(line, maxActivityBytes)
}

var (
	cmdField   = regexp.MustCompile(`"?cmd"?\s*:\s*("(?:[^"\\]|\\.)*")`)
	patchFile  = regexp.MustCompile(`\*\*\* (?:Update|Add|Delete) File: ([^\\\n"]+)`)
	shellArray = regexp.MustCompile(`"command"\s*:\s*\[((?:"(?:[^"\\]|\\.)*",?\s*)+)\]`)
	jsonString = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`)
)

// describeCall reads a tool call: Codex's "exec" runs a small script around
// tools.exec_command, tools.apply_patch and friends; older versions call
// shell or apply_patch directly.
func describeCall(name, input string) string {
	if m := patchFile.FindStringSubmatch(input); m != nil {
		return "Editing " + filepath.Base(strings.TrimSpace(m[1]))
	}
	if m := cmdField.FindStringSubmatch(input); m != nil {
		if cmd, err := strconv.Unquote(m[1]); err == nil {
			return detect.DescribeCommand(cmd)
		}
	}
	if m := shellArray.FindStringSubmatch(input); m != nil {
		var parts []string
		for _, s := range jsonString.FindAllStringSubmatch(m[1], -1) {
			parts = append(parts, s[1])
		}
		if len(parts) >= 3 && (parts[0] == "bash" || parts[0] == "sh" || parts[0] == "zsh") && parts[1] == "-lc" {
			parts = parts[2:]
		}
		return detect.DescribeCommand(strings.Join(parts, " "))
	}
	switch {
	case strings.Contains(input, "tools.web"):
		return "Searching the web"
	case strings.Contains(input, "tools.write_stdin"):
		return "" // feeding a running command: keep the previous line
	case name == "exec" || name == "":
		return ""
	}
	return "Using " + strings.ReplaceAll(name, "_", " ")
}

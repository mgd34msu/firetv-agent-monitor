package goodvibes

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tonylook/managents/helper/internal/agent"
	"github.com/tonylook/managents/helper/internal/detect"
)

// A terminal-UI session keeps two files the daemon never sees:
//
//   - $GOODVIBES_HOME/.goodvibes/tui/transcript-<id>.journal gets a
//     "user_message" record when a prompt is submitted and is deleted when
//     the turn completes: a record in it means a turn is running.
//   - <project>/.goodvibes/tui/recovery/recovery-<id>.jsonl, the whole
//     conversation, rewritten every minute: a "meta" line, then one
//     "message" line per message, with tool calls and token usage.

// turnRunning reports whether the session's journal holds a submitted prompt.
func (s *Source) turnRunning(id string) bool {
	data, err := os.ReadFile(filepath.Join(s.Home, ".goodvibes", "tui", "transcript-"+id+".journal"))
	if err != nil {
		return false
	}
	return bytes.Contains(data, []byte(`"type":"user_message"`))
}

type recoveryLine struct {
	Type      string `json:"type"`
	Title     string `json:"title"` // meta
	Role      string `json:"role"`
	Content   string `json:"content"`
	ToolCalls []struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"toolCalls"`
	Usage *struct {
		InputTokens     int `json:"inputTokens"`
		CacheReadTokens int `json:"cacheReadTokens"`
	} `json:"usage"`
	Model    string `json:"model"`
	Provider string `json:"provider"`
}

type conversation struct {
	title   string
	doing   string // the newest tool call
	said    string // the newest assistant text
	context *agent.ContextUsage
	model   string
}

// readConversation reads the session's recovery snapshot, or reports false.
func (s *Source) readConversation(project, id string) (conversation, bool) {
	path := recoveryPath(project, id)
	var c conversation
	if head, err := readHead(path, 8<<10); err == nil {
		var meta recoveryLine
		if json.Unmarshal(head, &meta) == nil && meta.Type == "meta" {
			c.title = meta.Title
		}
	}
	tail, whole, err := detect.ReadTail(path, 512<<10)
	if err != nil {
		return c, false
	}
	if !whole {
		if i := bytes.IndexByte(tail, '\n'); i >= 0 {
			tail = tail[i+1:]
		}
	}
	sc := bufio.NewScanner(bytes.NewReader(tail))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var m recoveryLine
		if json.Unmarshal(sc.Bytes(), &m) != nil || m.Type != "message" {
			continue
		}
		switch m.Role {
		case "user":
			c.doing, c.said = "", ""
		case "assistant":
			if said := detect.ClosingSentence(m.Content); said != "" {
				c.said = said
			}
			for _, call := range m.ToolCalls {
				if d := describeTool(call.Name, call.Arguments); d != "" {
					c.doing = d
				}
			}
			if u := m.Usage; u != nil && u.InputTokens+u.CacheReadTokens > 0 {
				c.context = &agent.ContextUsage{Used: u.InputTokens + u.CacheReadTokens}
				c.model = m.Model
			}
		}
	}
	if c.context != nil {
		c.context.Limit = s.contextLimit(c.model)
	}
	return c, true
}

func recoveryPath(project, id string) string {
	return filepath.Join(project, ".goodvibes", "tui", "recovery", "recovery-"+id+".jsonl")
}

func readHead(path string, n int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	line, err := bufio.NewReaderSize(f, n).ReadSlice('\n')
	if err != nil && len(line) == 0 {
		return nil, err
	}
	return line, nil
}

// describeTool says in a few words what a GoodVibes tool call does.
func describeTool(name string, raw json.RawMessage) string {
	var args struct {
		Commands []struct {
			Cmd string `json:"cmd"`
		} `json:"commands"`
		Path     string `json:"path"`
		FilePath string `json:"file_path"`
		Mode     string `json:"mode"`
	}
	_ = json.Unmarshal(raw, &args)
	file := filepath.Base(args.Path + args.FilePath)
	switch {
	case name == "exec" && len(args.Commands) > 0:
		return detect.DescribeCommand(args.Commands[len(args.Commands)-1].Cmd)
	case name == "agent":
		if args.Mode == "batch-spawn" || args.Mode == "spawn" {
			return "Running subagents"
		}
		return "Checking on subagents"
	case strings.Contains(name, "read"):
		if file != "." {
			return "Reading " + file
		}
		return "Reading files"
	case strings.Contains(name, "edit") || strings.Contains(name, "write") || strings.Contains(name, "patch"):
		if file != "." {
			return "Editing " + file
		}
		return "Editing files"
	case strings.Contains(name, "grep") || strings.Contains(name, "search") || strings.Contains(name, "find") || name == "repo_map":
		return "Searching the code"
	case strings.Contains(name, "web") || strings.Contains(name, "fetch"):
		return "Searching the web"
	}
	return "Using " + strings.ReplaceAll(name, "_", " ")
}

// contextLimit is the model's context window from GoodVibes' model limits,
// matched on the model id with or without its provider prefix.
func (s *Source) contextLimit(model string) int {
	if model == "" {
		return 0
	}
	data, err := os.ReadFile(filepath.Join(s.Home, ".goodvibes", "tui", "model-limits.json"))
	if err != nil {
		return 0
	}
	var limits struct {
		Models map[string]struct {
			ContextLength int `json:"contextLength"`
		} `json:"models"`
	}
	if json.Unmarshal(data, &limits) != nil {
		return 0
	}
	if m, ok := limits.Models[model]; ok {
		return m.ContextLength
	}
	for id, m := range limits.Models {
		if strings.HasSuffix(id, "/"+model) {
			return m.ContextLength
		}
	}
	return 0
}

// seen remembers when each session's status last changed, since neither the
// daemon (whose activity stamp moves with every heartbeat) nor the files say.
type seen struct {
	status agent.Status
	since  time.Time
}

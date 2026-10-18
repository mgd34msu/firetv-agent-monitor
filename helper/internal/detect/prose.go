package detect

import (
	"strings"
	"unicode"
)

// ClosingSentence is the question a reply ends with, or else the last
// sentence of its last line of prose, without Markdown.
func ClosingSentence(text string) string {
	lines := cleanLines(text)
	all := sentences(strings.Join(lines, " "))
	for i := len(all) - 1; i >= 0; i-- {
		if strings.HasSuffix(all[i], "?") {
			return all[i]
		}
	}
	if len(lines) == 0 {
		return ""
	}
	last := sentences(lines[len(lines)-1])
	if len(last) == 0 {
		return ""
	}
	return last[len(last)-1]
}

func sentences(text string) []string {
	var result []string
	start := 0
	runes := []rune(text)
	for i, r := range runes {
		end := (r == '.' || r == '?' || r == '!') && (i+1 == len(runes) || unicode.IsSpace(runes[i+1]))
		if end {
			if s := strings.TrimSpace(string(runes[start : i+1])); s != "" {
				result = append(result, s)
			}
			start = i + 1
		}
	}
	if s := strings.TrimSpace(string(runes[start:])); s != "" {
		result = append(result, s)
	}
	return result
}

// Clean flattens Markdown and whitespace to one plain line.
func Clean(text string) string { return strings.Join(cleanLines(text), " ") }

// cleanLines is text's lines of prose without Markdown: code blocks and tables
// are left out, and so are empty lines.
func cleanLines(text string) []string {
	var lines []string
	inCode := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inCode = !inCode
			continue
		}
		if inCode || trimmed == "" || strings.HasPrefix(trimmed, "|") {
			continue
		}
		trimmed = strings.TrimLeft(trimmed, "#>-*0123456789. ")
		for _, mark := range []string{"**", "__", "`"} {
			trimmed = strings.ReplaceAll(trimmed, mark, "")
		}
		if trimmed = strings.Join(strings.Fields(stripLinks(trimmed)), " "); trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	return lines
}

// stripLinks turns [text](url) into text.
func stripLinks(s string) string {
	for {
		open := strings.Index(s, "](")
		if open < 0 {
			return s
		}
		start := strings.LastIndex(s[:open], "[")
		end := strings.Index(s[open:], ")")
		if start < 0 || end < 0 {
			return s
		}
		s = s[:start] + s[start+1:open] + s[open+end+1:]
	}
}

// Truncate shortens s to at most n bytes at a UTF-8 boundary, with "..." when
// anything was cut.
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n - 3
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:max(cut, 0)] + "..."
}

// DescribeCommand says in a few words what a shell command does, for agents
// whose command calls carry no description of their own.
func DescribeCommand(cmd string) string {
	words := strings.Fields(cmd)
	// Skip "cd dir &&" and leading VAR=value assignments.
	for len(words) > 0 {
		switch {
		case words[0] == "cd" && len(words) > 2 && (words[2] == "&&" || words[2] == ";"):
			words = words[3:]
		case strings.Contains(words[0], "=") && !strings.HasPrefix(words[0], "-"):
			words = words[1:]
		default:
			goto done
		}
	}
done:
	if len(words) == 0 {
		return "Running a command"
	}
	name := words[0]
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	rest := strings.Join(words[1:], " ")
	switch name {
	case "rg", "grep", "ag", "ack", "find", "fd":
		return "Searching the code"
	case "cat", "sed", "head", "tail", "nl", "less", "bat", "wc":
		if file := lastPath(words[1:]); file != "" {
			return "Reading " + file
		}
		return "Reading files"
	case "ls", "tree":
		return "Looking at files"
	case "git":
		if len(words) > 1 {
			return "Running git " + words[1]
		}
	}
	if strings.Contains(" "+rest+" ", " test ") || strings.HasPrefix(rest, "test") || name == "pytest" || name == "vitest" || name == "jest" {
		return "Running tests"
	}
	return Truncate("Running "+strings.Join(words, " "), 80)
}

// lastPath is the base name of the last argument that looks like a file.
func lastPath(args []string) string {
	for i := len(args) - 1; i >= 0; i-- {
		a := strings.Trim(args[i], `"'`)
		if strings.HasPrefix(a, "-") || !strings.ContainsAny(a, "./") || strings.ContainsAny(a, "|&;<>") {
			continue
		}
		if j := strings.LastIndexByte(a, '/'); j >= 0 {
			a = a[j+1:]
		}
		if a != "" {
			return a
		}
	}
	return ""
}

// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

// Package aicmd is the AI-command prompt format: markdown files with a
// strict YAML-style frontmatter declaring the data scope. The context
// builder (context.go) enforces that declaration - it is the only path
// mail content takes toward an LLM.
package aicmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// maxCommandSize bounds a prompt file: a runaway file must not balloon
// memory or the assembled prompt.
const maxCommandSize = 64 << 10

// dataFields is the context allowlist: the ONLY thread data a command
// can declare. Everything else - headers, attachment names, maildir
// paths - never reaches the model (the privacy boundary).
var dataFields = map[string]bool{
	"participants": true,
	"subjects":     true,
	"dates":        true,
	"count":        true,
	"bodies":       true,
	"last_body":    true,
	"structure":    true,
}

// Command is one user-authored AI command: the frontmatter declares the
// data scope (Data) and the action; Body is the prompt text.
type Command struct {
	Name           string
	Description    string
	Provider       string // optional; "" = first configured [ai] provider
	Action         string // "view" | "compose"
	Data           []string
	AccountContext bool
	// SummaryContext appends the previous AI summary (the last view
	// command's output) to the prompt context - the chaining opt-in.
	SummaryContext bool
	Body           string
}

// LoadCommand parses one prompt file (strict): a `---` frontmatter block
// with known keys only, then the markdown body. Unknown keys, missing
// required keys, a bad action, or a data field outside the allowlist
// are load errors - the same strictness as config.toml.
func LoadCommand(path string) (*Command, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseCommand(data, path)
}

// parseCommand is the fuzzable core: the size cap lives here so the
// bound holds on the raw bytes, not just the read path. It must never
// panic or allocate unboundedly - see FuzzLoadCommand.
func parseCommand(data []byte, path string) (*Command, error) {
	if len(data) > maxCommandSize {
		return nil, fmt.Errorf("%s: exceeds %d KiB", path, maxCommandSize>>10)
	}
	text := string(data)
	if !strings.HasPrefix(text, "---") {
		return nil, fmt.Errorf("%s: missing frontmatter (start with ---)", path)
	}
	lines := strings.Split(text, "\n")
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return nil, fmt.Errorf("%s: missing closing ---", path)
	}
	cmd := &Command{}
	seen := map[string]bool{}
	for i := 1; i < end; i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("%s:%d: expected key: value", path, i+1)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if seen[key] {
			return nil, fmt.Errorf("%s:%d: duplicate key %q", path, i+1, key)
		}
		seen[key] = true
		switch key {
		case "name":
			cmd.Name = value
		case "description":
			cmd.Description = value
		case "provider":
			cmd.Provider = value
		case "action":
			cmd.Action = value
		case "data":
			fields, err := parseList(path, i+1, value)
			if err != nil {
				return nil, err
			}
			cmd.Data = fields
		case "account_context":
			switch value {
			case "true":
				cmd.AccountContext = true
			case "false":
				cmd.AccountContext = false
			default:
				return nil, fmt.Errorf("%s:%d: account_context must be true or false", path, i+1)
			}
		case "summary_context":
			switch value {
			case "true":
				cmd.SummaryContext = true
			case "false":
				cmd.SummaryContext = false
			default:
				return nil, fmt.Errorf("%s:%d: summary_context must be true or false", path, i+1)
			}
		default:
			return nil, fmt.Errorf("%s:%d: unknown key %q", path, i+1, key)
		}
	}
	if cmd.Name == "" {
		return nil, fmt.Errorf("%s: missing name", path)
	}
	if cmd.Description == "" {
		return nil, fmt.Errorf("%s: missing description", path)
	}
	if cmd.Action != "view" && cmd.Action != "compose" {
		return nil, fmt.Errorf("%s: action must be view or compose, got %q", path, cmd.Action)
	}
	if len(cmd.Data) == 0 {
		return nil, fmt.Errorf("%s: missing data (the context allowlist)", path)
	}
	for _, f := range cmd.Data {
		if !dataFields[f] {
			return nil, fmt.Errorf("%s: unknown data field %q", path, f)
		}
	}
	body := strings.TrimSpace(strings.Join(lines[end+1:], "\n"))
	if body == "" {
		return nil, fmt.Errorf("%s: empty prompt body", path)
	}
	cmd.Body = body
	return cmd, nil
}

// parseList parses a `[a, b, c]` flow list (the frontmatter data key).
func parseList(path string, line int, value string) ([]string, error) {
	if !strings.HasPrefix(value, "[") || !strings.HasSuffix(value, "]") {
		return nil, fmt.Errorf("%s:%d: data must be a list like [a, b]", path, line)
	}
	var out []string
	for _, part := range strings.Split(value[1:len(value)-1], ",") {
		if f := strings.TrimSpace(part); f != "" {
			out = append(out, f)
		}
	}
	return out, nil
}

// LoadCommands scans dir for *.md prompt files (sorted by name) and
// loads each strictly; the first broken file errors the whole load so a
// typo cannot silently drop commands.
func LoadCommands(dir string) ([]Command, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	var cmds []Command
	seen := map[string]bool{}
	for _, n := range names {
		c, err := LoadCommand(filepath.Join(dir, n))
		if err != nil {
			return nil, err
		}
		if seen[c.Name] {
			return nil, fmt.Errorf("%s: duplicate command name %q", n, c.Name)
		}
		seen[c.Name] = true
		cmds = append(cmds, *c)
	}
	return cmds, nil
}

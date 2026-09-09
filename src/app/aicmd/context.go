// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package aicmd

import (
	"fmt"
	netmail "net/mail"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"notmutt/core"
	"notmutt/mail"
)

const (
	// BodyCap is one message's body text, chars; the MCP bodies tool and
	// the command builder share the same per-message ceiling.
	BodyCap = 4000
	// totalBodyCap is the all-bodies ceiling (chars) the command
	// builder applies across a thread - the prompt stays bounded. The
	// MCP bodies tool bounds by message count instead ([mcp.bodies]).
	totalBodyCap = 20000
	// bodyHTMLWidth is the render width (cells) for an html-only body fed
	// to a model: no terminal width exists here, 100 reads comfortably
	// and keeps email tables on the page.
	bodyHTMLWidth = 100
)

// BuildContext assembles the prompt context for a command: a labeled
// section for exactly the declared data fields, nothing more. Bodies are
// cleaned (quoted lines, signatures dropped; an html-only body renders
// to text; capped), sender
// metadata is bare addresses only, attachments never appear. This is the
// only path mail content takes toward an LLM - the Data allowlist is
// enforced here, structurally. allowed is the account's [ai-data] grant:
// a declared field not in it renders no section (nil = no gate). own is
// the account's own bare lowercase addresses (empty = no exclusion).
func BuildContext(cmd *Command, msgs []core.Message, own []string, allowed []string, styleNote, accountNote string) (string, error) {
	if len(msgs) == 0 {
		return "", fmt.Errorf("empty thread")
	}
	sorted := make([]core.Message, len(msgs))
	copy(sorted, msgs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Timestamp < sorted[j].Timestamp })
	declared := make(map[string]bool, len(cmd.Data))
	for _, f := range cmd.Data {
		declared[f] = true
	}
	// a field must be declared AND granted (nil grant = unconstrained)
	allows := func(f string) bool { return declared[f] && (allowed == nil || slices.Contains(allowed, f)) }
	var b strings.Builder
	if allows("count") {
		fmt.Fprintf(&b, "Message count: %d\n\n", len(sorted))
	}
	if allows("participants") {
		b.WriteString("Participants: " + strings.Join(participants(sorted, own), ", ") + "\n\n")
	}
	if allows("subjects") {
		b.WriteString("Subjects:\n")
		for i, m := range sorted {
			fmt.Fprintf(&b, "%d. %s\n", i+1, core.SanitizeControls(m.Subject))
		}
		b.WriteString("\n")
	}
	if allows("dates") {
		b.WriteString("Dates:\n")
		for i, m := range sorted {
			fmt.Fprintf(&b, "%d. %s\n", i+1, dateOf(m))
		}
		b.WriteString("\n")
	}
	if allows("structure") {
		b.WriteString("Messages:\n")
		for i, m := range sorted {
			fmt.Fprintf(&b, "%d. %s | %s | %s\n", i+1, senderOf(m), core.SanitizeControls(m.Subject), dateOf(m))
		}
		b.WriteString("\n")
	}
	if allows("bodies") {
		b.WriteString("Messages:\n")
		remaining := totalBodyCap
		for i, m := range sorted {
			if remaining <= 0 {
				break
			}
			limit := remaining
			if limit > BodyCap {
				limit = BodyCap
			}
			body := BodyText(m, limit)
			remaining -= len(body)
			fmt.Fprintf(&b, "%d. From: %s\nSubject: %s\nDate: %s\nBody:\n%s\n\n",
				i+1, senderOf(m), core.SanitizeControls(m.Subject), dateOf(m), body)
		}
	}
	if allows("last_body") {
		m := sorted[len(sorted)-1]
		fmt.Fprintf(&b, "Latest message:\nFrom: %s\nSubject: %s\nDate: %s\nBody:\n%s\n",
			senderOf(m), core.SanitizeControls(m.Subject), dateOf(m), BodyText(m, BodyCap))
	}
	if styleNote != "" {
		b.WriteString("\nStyle:\n" + styleNote + "\n")
	}
	// the account context comes after the default context so it reads as
	// the override on top of the shared style
	if cmd.AccountContext && accountNote != "" {
		b.WriteString("\nAccount context:\n" + accountNote + "\n")
	}
	return b.String(), nil
}

// bodyText returns a message's cleaned plain text: quoted lines (Quoted >
// 0) and signature lines dropped, text/plain lines joined, capped at
// limit chars. An html-only body (no plain alternative) is rendered to
// readable text instead - the stage-2 engine draws prose and tables, so
// the feed never sees raw markup. A missing or unparseable file yields
// "" - the metadata sections still carry the message.
func BodyText(m core.Message, limit int) string {
	if len(m.Paths) == 0 {
		return ""
	}
	parsed, err := mail.ParseMessage(m.Paths[0])
	if err != nil {
		return ""
	}
	var b strings.Builder
	html := ""
	for _, p := range parsed.Parts {
		if p.HTML {
			if html == "" {
				html = p.Body
			}
			continue
		}
		if p.Quoted > 0 || p.Signature {
			continue
		}
		left := limit - b.Len()
		if left <= 0 {
			break
		}
		line := p.Body
		if len(line) > left {
			line = line[:left]
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	if b.Len() == 0 && html != "" {
		writeHTMLText(&b, html, limit)
	}
	return core.SanitizeControls(strings.TrimRight(b.String(), "\n"))
}

// writeHTMLText renders an html body to its readable text (the stage-2
// engine, tables included) and appends it capped at limit chars. Rows
// with no visible text and trailing spaces drop - the text, not the
// layout. Nil lines (an unparseable doc) append nothing.
func writeHTMLText(b *strings.Builder, body string, limit int) {
	for _, ln := range mail.RenderHTML(body, nil, bodyHTMLWidth) {
		if b.Len() >= limit {
			return
		}
		line := strings.TrimRight(ln.Text, " ")
		if line == "" {
			continue
		}
		left := limit - b.Len()
		if len(line) > left {
			line = line[:left]
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
}

// senderOf is the message's bare sender address (the notmuch author); a
// parse failure falls back to the raw author text.
func senderOf(m core.Message) string {
	return bareAddr(m.Author)
}

func bareAddr(s string) string {
	if a, err := netmail.ParseAddress(s); err == nil {
		return a.Address
	}
	return strings.TrimSpace(s)
}

// dateOf renders the message timestamp as a readable local time.
func dateOf(m core.Message) string {
	return time.Unix(m.Timestamp, 0).Format("2006-01-02 15:04")
}

// participants is the thread's distinct non-own sender addresses, sorted.
func participants(msgs []core.Message, own []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range msgs {
		a := strings.ToLower(bareAddr(m.Author))
		if a == "" || isOwn(a, own) {
			continue
		}
		if !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	sort.Strings(out)
	return out
}

func isOwn(a string, own []string) bool {
	for _, o := range own {
		if a == o {
			return true
		}
	}
	return false
}

// note reads one context file: trimmed and F1-sanitized (controls
// stripped); a missing or empty file is "".
func note(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return core.SanitizeControls(strings.TrimSpace(string(data)))
}

// LoadAccountContext reads the account's default context
// (<dir>/ai/accounts/<account>/default.md); a missing file or empty note
// is "". BuildContext places it after the default context.
func LoadAccountContext(dir, account string) string {
	if account == "" {
		return ""
	}
	return note(filepath.Join(dir, "ai", "accounts", account, "default.md"))
}

// LoadDefaultContext reads the default style note (<dir>/ai/context/
// default.md) every command runs under; a missing file is "". The user
// edits it to switch the AI's speaking style.
func LoadDefaultContext(dir string) string {
	return note(filepath.Join(dir, "ai", "context", "default.md"))
}

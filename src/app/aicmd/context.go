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
	perBodyCap   = 4000  // one message's body text, chars
	totalBodyCap = 20000 // all bodies together, chars - the prompt stays bounded
)

// BuildContext assembles the prompt context for a command: a labeled
// section for exactly the declared data fields, nothing more. Bodies are
// cleaned (quoted lines, signatures, and html dropped; capped), sender
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
			if limit > perBodyCap {
				limit = perBodyCap
			}
			body := bodyText(m, limit)
			remaining -= len(body)
			fmt.Fprintf(&b, "%d. From: %s\nSubject: %s\nDate: %s\nBody:\n%s\n\n",
				i+1, senderOf(m), core.SanitizeControls(m.Subject), dateOf(m), body)
		}
	}
	if allows("last_body") {
		m := sorted[len(sorted)-1]
		fmt.Fprintf(&b, "Latest message:\nFrom: %s\nSubject: %s\nDate: %s\nBody:\n%s\n",
			senderOf(m), core.SanitizeControls(m.Subject), dateOf(m), bodyText(m, perBodyCap))
	}
	if cmd.AccountContext && accountNote != "" {
		b.WriteString("\nAccount context:\n" + accountNote + "\n")
	}
	if styleNote != "" {
		b.WriteString("\nStyle:\n" + styleNote + "\n")
	}
	return b.String(), nil
}

// bodyText returns a message's cleaned plain text: quoted lines (Quoted >
// 0), signature lines, and html parts dropped; text/plain lines joined,
// capped at limit chars. A missing or unparseable file yields "" - the
// metadata sections still carry the message.
func bodyText(m core.Message, limit int) string {
	if len(m.Paths) == 0 {
		return ""
	}
	parsed, err := mail.ParseMessage(m.Paths[0])
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, p := range parsed.Parts {
		if p.HTML || p.Quoted > 0 || p.Signature {
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
	return core.SanitizeControls(strings.TrimRight(b.String(), "\n"))
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

// LoadAccountNote reads the per-account context note
// (<dir>/ai/accounts/<account>.md); a missing file or empty note is "".
func LoadAccountNote(dir, account string) string {
	if account == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(dir, "ai", "accounts", account+".md"))
	if err != nil {
		return ""
	}
	return core.SanitizeControls(strings.TrimSpace(string(data)))
}

// LoadDefaultContext reads the default style note (<dir>/ai/context/
// default.md) every command runs under; a missing file is "". The user
// edits it to switch the AI's speaking style.
func LoadDefaultContext(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "ai", "context", "default.md"))
	if err != nil {
		return ""
	}
	return core.SanitizeControls(strings.TrimSpace(string(data)))
}

// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"fmt"
	netmail "net/mail"
	"sort"
	"strings"

	"notmutt/compose"
	"notmutt/config"
	"notmutt/core"
	"notmutt/mail"
)

// resolveAIProvider selects the [ai] provider a command runs on: an empty
// name takes the first configured (sorted - deterministic). A missing or
// empty provider set errors - the gate that keeps AI commands quiet until
// a provider is configured.
func resolveAIProvider(cfg config.Config, name string) (config.AIProvider, error) {
	if len(cfg.AI) == 0 {
		return config.AIProvider{}, fmt.Errorf("no [ai] provider configured")
	}
	if name != "" {
		if p, ok := cfg.AI[name]; ok {
			return p, nil
		}
		return config.AIProvider{}, fmt.Errorf("ai provider %q not configured", name)
	}
	names := make([]string, 0, len(cfg.AI))
	for n := range cfg.AI {
		names = append(names, n)
	}
	sort.Strings(names)
	return cfg.AI[names[0]], nil
}

// aiDraftCompose builds a compose dialogue from an AI draft: the account
// chain supplies the sender identity, To = the thread's distinct non-own
// senders, Subject = the newest message's subject, threading headers from
// the newest message (the Reply construction, minus the quote), Body = the
// model output. Nil when the thread has no parseable message - the draft
// then stays a summary, never a blank mail.
func aiDraftCompose(cfg config.Config, root string, msgs []core.Message, body string) *compose.State {
	newest, parsed := newestParseable(msgs)
	if parsed == nil {
		return nil
	}
	st := newCompose(cfg, root, tagsOf(newest), nil)
	st.To = threadSenders(msgs, cfg.MyAddrs())
	st.Subject = core.SanitizeControls(newest.Subject)
	width := cfg.Compose.WrapWidth
	if width <= 0 {
		width = config.DefaultWrapWidth
	}
	st.Body = wrapEmail(core.SanitizeText(body), width)
	refs := newest.References
	if parsed.MessageID != "" {
		refs = append(append([]string{}, newest.References...), parsed.MessageID)
	}
	st.MessageID = parsed.MessageID
	st.References = refs
	st.OriginalID = newest.ID
	return st
}

// newestOf returns the thread's newest message by timestamp; nil on empty.
func newestOf(msgs []core.Message) *core.Message {
	if len(msgs) == 0 {
		return nil
	}
	best := 0
	for i := 1; i < len(msgs); i++ {
		if msgs[i].Timestamp > msgs[best].Timestamp {
			best = i
		}
	}
	return &msgs[best]
}

// newestParseable returns the newest message that parses as mail, with its
// parsed form; nil when none does. Parsing runs newest-first on a single
// scan (no copy or sort): a message is only parsed when it could beat the
// current best.
func newestParseable(msgs []core.Message) (*core.Message, *mail.Message) {
	var best *core.Message
	var parsed *mail.Message
	for i := range msgs {
		if len(msgs[i].Paths) == 0 || best != nil && msgs[i].Timestamp <= best.Timestamp {
			continue
		}
		p, err := mail.ParseMessage(msgs[i].Paths[0])
		if err != nil {
			continue
		}
		best = &msgs[i]
		parsed = p
	}
	return best, parsed
}

// threadSenders is the thread's distinct bare sender addresses, minus the
// account's own (MyAddrs) - the draft's recipients.
func threadSenders(msgs []core.Message, own []string) []string {
	seen := map[string]bool{}
	for _, a := range own {
		seen[a] = true
	}
	var out []string
	for _, m := range msgs {
		if a := bareLower(m.Author); a != "" && !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	sort.Strings(out)
	return out
}

// bareLower is the message author's bare lowercased address; a parse
// failure keeps the trimmed raw text.
func bareLower(s string) string {
	if a, err := netmail.ParseAddress(s); err == nil {
		return strings.ToLower(strings.TrimSpace(a.Address))
	}
	return strings.ToLower(strings.TrimSpace(s))
}

// wrapEmail hard-wraps text at width, the email norm: every newline run
// (the AI's paragraph breaks, however many) collapses to a blank line and
// each paragraph is wrapped in isolation - its inner whitespace collapses
// to single spaces, lines fill to the cap. A newline the AI used as a
// paragraph break is never merged into the surrounding text.
func wrapEmail(text string, width int) string {
	var paras []string
	for _, para := range strings.Split(text, "\n") {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		paras = append(paras, wrapLines(para, width))
	}
	return strings.Join(paras, "\n\n")
}

// wrapLines fills one paragraph's words to width at word boundaries,
// keeping the word order; a word longer than the cap stays whole.
func wrapLines(para string, width int) string {
	var lines, words []string
	col := 0
	for _, w := range strings.Fields(para) {
		if col > 0 && col+1+len(w) > width {
			lines = append(lines, strings.Join(words, " "))
			words = words[:0]
			col = 0
		}
		if col > 0 {
			col++ // the joining space
		}
		words = append(words, w)
		col += len(w)
	}
	if len(words) > 0 {
		lines = append(lines, strings.Join(words, " "))
	}
	return strings.Join(lines, "\n")
}

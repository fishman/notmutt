// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"fmt"
	netmail "net/mail"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"notmutt/compose"
	"notmutt/config"
	"notmutt/core"
	"notmutt/mail"
	"notmutt/notmuch"
)

// sigDir is the signatures root (spec section 9): the send wiring
// (Task 14) sets it from the config path; the tests set it directly.
var sigDir string

// resolveAccount is the detection chain (spec section 6): the message's
// account tag, the view cursor's, else the first configured account
// (sorted - deterministic). Same machinery as the status bar
// (core.AccountTag).
func resolveAccount(cfg config.Config, msgTags, cursorTags []string) string {
	set := cfg.AccountTags()
	if t := core.AccountTag(msgTags, set); t != "" {
		return t
	}
	if t := core.AccountTag(cursorTags, set); t != "" {
		return t
	}
	names := make([]string, 0, len(cfg.Accounts))
	for n := range cfg.Accounts {
		names = append(names, n)
	}
	sort.Strings(names)
	if len(names) > 0 {
		return names[0]
	}
	return ""
}

// defaultSig loads the account's default signature file (the configured
// name in the account's signatures dir); a missing file or unset name
// resolves to no signature.
func defaultSig(cfg config.Config, account string) (name, body string) {
	file := cfg.Accounts[account].DefaultSignature
	if file == "" {
		return "", ""
	}
	data, err := os.ReadFile(filepath.Join(sigDir, account, file))
	if err != nil {
		return "", ""
	}
	return file, strings.TrimSuffix(string(data), "\n")
}

// accountFrom resolves the dialogue's sender identity: the account
// (message tags, cursor tags, else the first configured - resolveAccount),
// its from address, and the default signature. One derivation for every
// dialogue builder (compose, reply/forward, mailto).
func accountFrom(cfg config.Config, msgTags, cursorTags []string) (account, from, sigName, sigBody string) {
	account = resolveAccount(cfg, msgTags, cursorTags)
	from = cfg.Accounts[account].From
	sigName, sigBody = defaultSig(cfg, account)
	return account, from, sigName, sigBody
}

// newCompose builds the compose-mode dialogue shell: the sender
// identity (accountFrom) and the fcc path. Shared by the compose key
// and the mailto link; reply/forward layer the parsed original on top
// (buildCompose).
func newCompose(cfg config.Config, root string, msgTags, cursorTags []string) *compose.State {
	account, from, sigName, sigBody := accountFrom(cfg, msgTags, cursorTags)
	st := compose.NewCompose(account, from, sigName, sigBody)
	st.Fcc = sentPath(root, account, cfg.Accounts[account])
	return st
}

// buildCompose prefills a dialogue for mode ("compose" | "reply" |
// "reply-all" | "forward"): account detection, the parsed original
// (reply/forward), the default signature. Nil when the original cannot
// be parsed - the open key then no-ops.
func buildCompose(cfg config.Config, view *core.View, msg *core.Message, mode, root string) *compose.State {
	var st *compose.State
	if mode == "compose" {
		st = newCompose(cfg, root, tagsOf(msg), cursorTags(view))
	} else if msg != nil && len(msg.Paths) > 0 {
		parsed, err := mail.ParseMessage(msg.Paths[0])
		if err == nil {
			account, from, sigName, sigBody := accountFrom(cfg, tagsOf(msg), cursorTags(view))
			switch mode {
			case "reply":
				st = compose.Reply(*msg, parsed, account, from, sigName, sigBody)
			case "reply-all":
				own := ""
				if p, err := netmail.ParseAddress(from); err == nil {
					own = p.Address
				}
				st = compose.ReplyAll(*msg, parsed, account, from, own, sigName, sigBody)
			case "forward":
				st = compose.Forward(*msg, parsed, account, from, sigName, sigBody)
			}
			if st != nil {
				st.Fcc = sentPath(root, account, cfg.Accounts[account])
			}
		}
	}
	return st
}

// replyPrefill builds the reply dialogue: buildCompose on the cursor
// message, falling back to a thread fetch when the row carries no
// paths (index rows are thread summaries - paths load on open, R1).
// Newest message is the reply original; messages are tried in recency
// order so a broken newest falls through to the next parseable one. A
// non-nil error means nothing could be built - a reply must never
// fail silently.
func replyPrefill(cfg config.Config, view *core.View, worker *notmuch.Worker, msg *core.Message, mode, root string) (*compose.State, error) {
	if st := buildCompose(cfg, view, msg, mode, root); st != nil {
		return st, nil
	}
	if msg == nil || msg.ThreadID == "" {
		return nil, nil
	}
	rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActThread, ThreadID: msg.ThreadID})
	if err != nil || rpl.Err != nil {
		return nil, fmt.Errorf("thread %s: %v %v", msg.ThreadID, err, rpl.Err)
	}
	sort.Slice(rpl.Msgs, func(i, j int) bool { return rpl.Msgs[i].Timestamp > rpl.Msgs[j].Timestamp })
	// prefer the requested msgID, then the newest parseable
	if msg.ID != "" {
		for i := range rpl.Msgs {
			if rpl.Msgs[i].ID == msg.ID {
				if st := buildCompose(cfg, view, &rpl.Msgs[i], mode, root); st != nil {
					return st, nil
				}
				break
			}
		}
	}
	for i := range rpl.Msgs {
		if st := buildCompose(cfg, view, &rpl.Msgs[i], mode, root); st != nil {
			return st, nil
		}
	}
	return nil, fmt.Errorf("thread %s: no parseable message", msg.ThreadID)
}

// mailtoCompose opens a dialogue from a mailto: link (the pager F key
// seam): To from the URL path, subject/cc/bcc/body from the query
// (RFC 6068, x-www-form-urlencoded). The sender identity comes from
// the account chain, never the link.
func mailtoCompose(cfg config.Config, root, rawURL string) (*compose.State, error) {
	u, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(u.Scheme, "mailto") {
		return nil, fmt.Errorf("not a mailto url: %q", rawURL)
	}
	st := newCompose(cfg, root, nil, nil)
	if addr, err := url.PathUnescape(u.Opaque); err == nil {
		st.To = mailtoAddresses(addr)
	}
	for k, vs := range u.Query() {
		for _, v := range vs {
			switch strings.ToLower(k) {
			case "to":
				st.To = append(st.To, mailtoAddresses(v)...)
			case "cc":
				st.Cc = append(st.Cc, mailtoAddresses(v)...)
			case "bcc":
				st.Bcc = append(st.Bcc, mailtoAddresses(v)...)
			case "reply-to":
				st.ReplyTo = append(st.ReplyTo, mailtoAddresses(v)...)
			case "subject":
				if st.Subject == "" {
					st.Subject = v
				}
			case "body":
				if st.Body == "" {
					st.Body = v
				}
			}
		}
	}
	return st, nil
}

// mailtoAddresses splits a mailto address list (comma-separated, RFC
// 6068) into trimmed non-empty entries.
func mailtoAddresses(s string) []string {
	var out []string
	for _, a := range strings.Split(s, ",") {
		if a = strings.TrimSpace(a); a != "" {
			out = append(out, a)
		}
	}
	return out
}

func tagsOf(msg *core.Message) []string {
	if msg == nil {
		return nil
	}
	return msg.Tags
}

// cursorTags resolves the view cursor message's tags - the view's
// active account context (spec section 6).
func cursorTags(view *core.View) []string {
	row, ok := view.CursorRow()
	if !ok || row.Msg == nil {
		return nil
	}
	return row.Msg.Tags
}

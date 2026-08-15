package app

import (
	netmail "net/mail"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"notmutt/compose"
	"notmutt/config"
	"notmutt/core"
	"notmutt/mail"
)

// sigDir is the signatures root (spec section 9): the send wiring
// (Task 14) sets it from the config path; the tests set it directly.
var sigDir string

// resolveAccount is the detection chain (spec section 6): the
// message's account tag, the view cursor's account tag, the first
// configured account (sorted - deterministic). The same account-tag
// machinery as the status bar (core.AccountTag, DRY).
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

// defaultSig loads the account's default signature file (the
// configured name in the account's signatures dir); a missing file or
// an unset name resolves to no signature.
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

// buildCompose prefills a dialogue for mode ("compose" | "reply" |
// "reply-all" | "forward"): account detection, the parsed original
// (reply/forward), the default signature. Nil when the original
// cannot be parsed - the open key then no-ops.
func buildCompose(cfg config.Config, view *core.View, msg *core.Message, mode string) *compose.State {
	account := resolveAccount(cfg, tagsOf(msg), cursorTags(view))
	from := cfg.Accounts[account].From
	sigName, sigBody := defaultSig(cfg, account)
	var st *compose.State
	if mode == "compose" {
		st = compose.NewCompose(account, from, sigName, sigBody)
	} else if msg != nil && len(msg.Paths) > 0 {
		parsed, err := mail.ParseMessage(msg.Paths[0])
		if err == nil {
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
		}
	}
	if st != nil {
		st.Fcc = cfg.Accounts[account].SentFolder
	}
	return st
}

func tagsOf(msg *core.Message) []string {
	if msg == nil {
		return nil
	}
	return msg.Tags
}

// cursorTags resolves the view cursor message's tags - the view's
// active account context (spec section 6). One flatten per dialogue
// open, not per keystroke.
func cursorTags(view *core.View) []string {
	row, ok := view.CursorRow()
	if !ok || row.Msg == nil {
		return nil
	}
	return row.Msg.Tags
}

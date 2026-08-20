// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package core

import (
	"net/mail"
	"slices"
	"sort"
	"strings"
)

// MsgMark is a message's thread-position marker: the five most recent
// messages of a thread render in one color, the most recent message
// from the other side in the prominent one - a long thread reads by
// its tail. The marks ride ThreadLoaded (keyed by message id) and the
// index tints the marked subject runs and tree indicators.
type MsgMark uint8

const (
	MarkNone MsgMark = iota
	MarkRecent
	MarkOther
)

// ClassifyMsgs marks every message by its position in the thread:
// among the five messages with the latest Timestamp -> MarkRecent; the
// most recent message not authored by me -> MarkOther (wins over
// recent). The classification is timestamp-based, so the fetch order
// never matters. "me" is the sent tag or the From address matching an
// address in me (the account from fields); an author that does not
// parse is never me.
func ClassifyMsgs(msgs []Message, me []string) map[string]MsgMark {
	marks := make(map[string]MsgMark, len(msgs))
	order := make([]int, len(msgs))
	for j := range msgs {
		order[j] = j
	}
	sort.SliceStable(order, func(a, b int) bool { return msgs[order[a]].Timestamp > msgs[order[b]].Timestamp })
	for _, j := range order[:min(5, len(order))] {
		marks[msgs[j].ID] = MarkRecent
	}
	for _, j := range order {
		if !isMe(msgs[j], me) {
			marks[msgs[j].ID] = MarkOther
			break
		}
	}
	return marks
}

// isMe reports whether the message is authored by the user: the sent
// tag (my sent folder copy) or a From address matching a me address.
func isMe(m Message, me []string) bool {
	if slices.Contains(m.Tags, "sent") {
		return true
	}
	if len(me) == 0 || m.Author == "" {
		return false
	}
	p, err := mail.ParseAddress(m.Author)
	if err != nil {
		return false
	}
	// me addresses are pre-normalized to lowercased bare addr-specs
	// (Config.MyAddrs); the message side normalizes here
	return slices.Contains(me, strings.ToLower(strings.TrimSpace(p.Address)))
}

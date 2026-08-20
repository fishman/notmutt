// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package core

import (
	"net/mail"
	"slices"
	"sort"
	"strings"
)

// MsgMark is the opened message's thread-position marker (the pager
// highlight): the five most recent messages of a thread render in one
// color, the most recent message from the other side in the prominent
// one - a long thread reads by its tail. The mark rides ThreadLoaded
// and the pager tints the whole message block.
type MsgMark uint8

const (
	MarkNone MsgMark = iota
	MarkRecent
	MarkOther
)

// ClassifyMsg marks msgs[i] (the opened message) by its position in
// the thread: among the five messages with the latest Timestamp ->
// MarkRecent; the most recent message not authored by me -> MarkOther
// (wins over recent). The classification is timestamp-based, so the
// fetch order never matters. "me" is the sent tag or the From address
// matching an address in me (the account from fields); an author that
// does not parse is never me.
func ClassifyMsg(msgs []Message, i int, me []string) MsgMark {
	if i < 0 || i >= len(msgs) {
		return MarkNone
	}
	order := make([]int, len(msgs))
	for j := range msgs {
		order[j] = j
	}
	sort.SliceStable(order, func(a, b int) bool { return msgs[order[a]].Timestamp > msgs[order[b]].Timestamp })
	other := -1
	for _, j := range order {
		if !isMe(msgs[j], me) {
			other = j
			break
		}
	}
	if i == other {
		return MarkOther
	}
	if slices.Contains(order[:min(5, len(order))], i) {
		return MarkRecent
	}
	return MarkNone
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

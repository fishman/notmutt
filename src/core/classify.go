// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package core

import (
	"net/mail"
	"slices"
	"sort"
	"strings"
)

// MsgMark is a message row's thread-position marker: the five most
// recent messages of a thread render in one color, the most recent
// message from the other side in the prominent one - a long thread
// reads by its tail. The view's flatten classifies a thread's rows
// only when the thread's tree is windowed (rows hidden above or
// below): a thread that fits its window renders unmarked. The index
// derives the marks from its own rows (the view flatten), so the tint
// shows without opening anything.
type MsgMark uint8

const (
	MarkNone MsgMark = iota
	MarkRecent
	MarkOther
)

// ClassifyRows marks every message row by its position in the thread:
// among the five rows with the latest Timestamp -> MarkRecent; the
// most recent row not authored by me -> MarkOther (wins over recent).
// The classification is timestamp-based, so the fetch order never
// matters; rows without a message (ghosts) stay unmarked. "me" is the
// sent tag or the From address matching an address in me (the account
// from fields); an author that does not parse is never me.
func ClassifyRows(rows []Row, me []string) []MsgMark {
	marks := make([]MsgMark, len(rows))
	idx := make([]int, 0, len(rows))
	for i, r := range rows {
		if r.Msg != nil {
			idx = append(idx, i)
		}
	}
	sort.SliceStable(idx, func(a, b int) bool { return rows[idx[a]].Msg.Timestamp > rows[idx[b]].Msg.Timestamp })
	for _, i := range idx[:min(5, len(idx))] {
		marks[i] = MarkRecent
	}
	for _, i := range idx {
		if !isMe(*rows[i].Msg, me) {
			marks[i] = MarkOther
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

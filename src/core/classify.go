// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package core

import (
	"net/mail"
	"slices"
	"sort"
	"strings"
)

// MsgMark is a row's thread-position marker: the five most recent
// messages render in one color, the most recent message from the other
// side in the prominent one - a long thread reads by its tail. The
// flatten classifies only windowed threads (rows hidden above or
// below); a thread that fits renders unmarked.
type MsgMark uint8

const (
	MarkNone MsgMark = iota
	MarkRecent
	MarkOther
)

// ClassifyRows marks rows by thread position: the five with the latest
// Timestamp -> MarkRecent; the most recent row not authored by me ->
// MarkOther (wins over recent). Timestamp-based, so fetch order never
// matters; ghost rows stay unmarked. "me" is the sent tag or a From
// matching a me address; an unparseable author is never me.
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

// isMe reports whether the message is the user's: the sent tag (my
// sent folder copy) or a From matching a me address.
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
	// (Config.MyAddrs); normalize the message side here
	return slices.Contains(me, strings.ToLower(strings.TrimSpace(p.Address)))
}

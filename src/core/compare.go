// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package core

// ThreadLess orders threads by last date desc, then id bytes.
func ThreadLess(a, b *Thread) bool {
	if a.LastDate != b.LastDate {
		return a.LastDate > b.LastDate
	}
	return a.ID < b.ID
}

// MsgLess orders messages by date asc, then id bytes: notmuch builds
// every thread oldest-first (lib/thread.cc), and the flatten follows the
// tree, so a deeply nested chain reads top-down with its replies.
func MsgLess(a, b *Message) bool {
	if a.Timestamp != b.Timestamp {
		return a.Timestamp < b.Timestamp
	}
	return a.ID < b.ID
}

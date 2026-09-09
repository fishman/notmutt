// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package compose

import (
	"reflect"
	"testing"
)

// TestEventRoundTripResumeID pins the resume identity on the dialogue's
// bus round trip: ResumePath and an attachment's DraftPart survive
// ToEvent -> FromEvent.
func TestEventRoundTripResumeID(t *testing.T) {
	st := &State{
		ID: "tab9", Mode: ModeCompose, Account: "gmail", From: "bob@example.com",
		To: []string{"alice@example.com"}, Subject: "the draft",
		Body: "line one\n\n-- \nsig", ResumePath: "/root/gmail/[Gmail]/Drafts/new/1.2.host",
		Attachments: []Attachment{{Name: "notes.txt", Path: "/root/gmail/[Gmail]/Drafts/new/1.2.host", DraftPart: 2}},
	}
	back := FromEvent(ToEvent(st))
	if back.ResumePath != st.ResumePath {
		t.Fatalf("ResumePath = %q, want %q", back.ResumePath, st.ResumePath)
	}
	if len(back.Attachments) != 1 || back.Attachments[0].DraftPart != 2 {
		t.Fatalf("attachment DraftPart must survive: %+v", back.Attachments)
	}
}

// TestFromEventDefaultsResumeID: the default (a fresh compose, or a
// spool record written before this field existed) is empty, never set.
func TestFromEventDefaultsResumeID(t *testing.T) {
	back := FromEvent(ToEvent(NewCompose("gmail", "bob@example.com", "", "")))
	if back.ResumePath != "" {
		t.Fatalf("fresh compose must carry no resume path: %q", back.ResumePath)
	}
}

// TestFromEventKeepsResumeID covers the ToEvent field-for-field mapping
// so a later field addition cannot silently drop ResumePath.
func TestFromEventKeepsResumeID(t *testing.T) {
	st := NewCompose("gmail", "bob@example.com", "", "")
	st.Body = "b"
	st.ResumePath = "/p"
	back := FromEvent(ToEvent(st))
	if !reflect.DeepEqual(back.Body, "b") || back.ResumePath != "/p" {
		t.Fatalf("round trip dropped fields: %+v", back)
	}
}

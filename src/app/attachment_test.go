// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"notmutt/core"
)

// TestAttachmentSeams pins the app's attachment view/save handlers:
// the worker round trip (ActThread), the on-demand extraction, the
// render, and the 0600 write all ride the published events.
func TestAttachmentSeams(t *testing.T) {
	bus := core.NewBus()
	ch := bus.Subscribe()
	path := filepath.Join(t.TempDir(), "msg")
	msg := "From: a@example.com\nTo: b@example.com\nSubject: atts\n" +
		"Date: Tue, 01 Jan 2019 00:00:00 +0000\nMIME-Version: 1.0\n" +
		"Content-Type: multipart/mixed; boundary=x\n\n" +
		"--x\nContent-Type: text/plain; charset=utf-8\n\nbody\n" +
		"--x\nContent-Type: text/plain\nContent-Disposition: attachment; filename=\"notes.txt\"\n\nnote one\n" +
		"--x--\n"
	if err := os.WriteFile(path, []byte(msg), 0o600); err != nil {
		t.Fatal(err)
	}
	fw := &fakeWorker{}
	fw.setMsgs([]core.Message{{ID: "a", ThreadID: "t1", Paths: []string{path}}})

	viewAttachment(fw, bus, nil, "t1", "", 0)
	select {
	case e := <-ch:
		ev, ok := e.(core.AttachmentLoaded)
		if !ok {
			t.Fatalf("the view must publish AttachmentLoaded, got %T", e)
		}
		if ev.Err != nil {
			t.Fatal(ev.Err)
		}
		if ev.Ordinal != 0 || ev.Name != "notes.txt" {
			t.Fatalf("loaded = %+v", ev)
		}
		if len(ev.Lines) != 1 || ev.Lines[0].Text != "note one" {
			t.Fatalf("lines = %+v", ev.Lines)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the attachment render")
	}

	out := filepath.Join(t.TempDir(), "out.txt")
	saveAttachment(fw, bus, nil, "t1", "", 0, out)
	select {
	case e := <-ch:
		ev, ok := e.(core.AttachmentSaved)
		if !ok {
			t.Fatalf("the save must publish AttachmentSaved, got %T", e)
		}
		if ev.Err != nil {
			t.Fatal(ev.Err)
		}
		if ev.Path != out {
			t.Fatalf("path = %q", ev.Path)
		}
		data, err := os.ReadFile(out)
		if err != nil || string(data) != "note one" {
			t.Fatalf("saved file = %q, %v", data, err)
		}
		if fi, err := os.Stat(out); err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("the saved file must be 0600, mode=%v", fi.Mode().Perm())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the save result")
	}
}

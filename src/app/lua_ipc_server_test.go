// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build lua

package app

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"notmutt/config"
	"notmutt/core"
)

// The lua IPC server (lua_ipc_server.go) end to end over a real unix
// socket: the chunk runs in the sandboxed VM, the reply returns to the
// client, staged effects ride the bus (drain) - and LuaResult never
// reaches the TUI (the caller owns the output).

func TestLuaIPCRun(t *testing.T) {
	f := filepath.Join(t.TempDir(), "mail1.eml")
	fixture := "From: sender@example.com\nTo: alpha@example.com\nSubject: quarterly report\n" +
		"Message-ID: <m1@example.com>\nDate: Mon, 17 Aug 2026 10:00:00 +0000\n" +
		"Content-Type: text/plain\n\nQ2 numbers attached.\n"
	if err := os.WriteFile(f, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	fw := &fakeWorker{}
	fw.setMsgs([]core.Message{{ID: "m1", ThreadID: "t1", Paths: []string{f}}})
	_, ch, sock := startIPC(t, fw)

	// a thread-carrying chunk reads the metadata surface AND the thread's
	// lines, and stages a tag op - all from one run
	reply, err := luaSend(sock, ipcRequest{
		ThreadID: "t1",
		Chunk:    `local r = ctx.search("tag:inbox") local l = ctx.mail_lines() print(ctx.thread_id .. "/" .. r[1].thread_id .. "/" .. tostring(#l)) tag_add("review")`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if reply.Err != "" {
		t.Fatalf("run error: %s", reply.Err)
	}
	if !strings.HasPrefix(reply.Output, "t1/t1/") {
		t.Fatalf("output = %q, want t1/t1/<n> lines", reply.Output)
	}
	if len(reply.Output) <= len("t1/t1/") {
		t.Fatalf("mail_lines rendered no rows: %q", reply.Output)
	}

	// staged effects reach the live session's bus (R14)...
	var staged *core.TagStaged
	deadline := time.After(200 * time.Millisecond)
	for staged == nil {
		select {
		case e := <-ch:
			if s, ok := e.(core.TagStaged); ok {
				staged = &s
			}
			if _, ok := e.(core.LuaResult); ok {
				t.Fatal("IPC must not publish LuaResult to the TUI bus")
			}
		case <-deadline:
			t.Fatal("no TagStaged published for the IPC tag_add")
		}
	}
	if staged.ThreadID != "t1" || len(staged.Ops) != 1 || staged.Ops[0].Tag != "review" || !staged.Ops[0].Add {
		t.Fatalf("staged ops = %+v", staged)
	}

	// ...and the erroring chunk returns its error, not a hang
	reply, err = luaSend(sock, ipcRequest{Chunk: `error("boom")`})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reply.Err, "boom") {
		t.Fatalf("error chunk err = %q", reply.Err)
	}

	// the bus carries no LuaResult across the error path either
	select {
	case e := <-ch:
		if _, ok := e.(core.LuaResult); ok {
			t.Fatal("IPC must not publish LuaResult to the TUI bus")
		}
	case <-time.After(50 * time.Millisecond):
	}
}

func TestLuaIPCOversizeChunk(t *testing.T) {
	_, _, sock := startIPC(t, &fakeWorker{})
	reply, err := luaSend(sock, ipcRequest{Chunk: strings.Repeat("a", maxIPCChunk+1)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reply.Err, "too large") {
		t.Fatalf("oversize chunk must be rejected, got %q", reply.Err)
	}
}

func TestLuaIPCInUse(t *testing.T) {
	_, _, sock := startIPC(t, &fakeWorker{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := serveLuaIPCat(ctx, core.NewBus(), &fakeWorker{}, &config.Config{}, sock); err == nil ||
		!strings.Contains(err.Error(), "in use") {
		t.Fatalf("a busy socket must refuse the second server, got %v", err)
	}
	// and the first server still answers
	if _, err := luaSend(sock, ipcRequest{Chunk: `print("ok")`}); err != nil {
		t.Fatal(err)
	}
}

func TestLuaIPCStaleSocket(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "ipc.sock")
	ctx, cancel := context.WithCancel(context.Background())
	go serveLuaIPCat(ctx, core.NewBus(), &fakeWorker{}, &config.Config{}, sock)
	waitIPC(t, sock)
	cancel()
	waitGone(t, sock)
	// a fresh server on the same path takes over the stale socket
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	go serveLuaIPCat(ctx2, core.NewBus(), &fakeWorker{}, &config.Config{}, sock)
	waitIPC(t, sock)
}

// startIPC serves the socket on a temp path and returns the bus, its
// subscription, and the socket path. The caller owns the server's
// lifetime via the t.Cleanup cancel.
func startIPC(t *testing.T, fw workerAPI) (*core.Bus, <-chan core.Event, string) {
	t.Helper()
	bus := core.NewBus()
	sock := filepath.Join(t.TempDir(), "ipc.sock")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go serveLuaIPCat(ctx, bus, fw, &config.Config{}, sock)
	waitIPC(t, sock)
	return bus, bus.Subscribe(), sock
}

func waitIPC(t *testing.T, sock string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if conn, err := net.Dial("unix", sock); err == nil {
			conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("ipc socket %s never came up", sock)
}

func waitGone(t *testing.T, sock string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sock); os.IsNotExist(err) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("ipc socket %s was not removed", sock)
}

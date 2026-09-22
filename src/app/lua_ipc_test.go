// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"encoding/json"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
)

// TestLuaSend pins the request/reply framing against a fake listener that
// decodes the request and answers with a canned reply: the client passes
// the chunk and thread through and returns the reply fields verbatim.
func TestLuaSend(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "ipc.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		raw, err := io.ReadAll(conn)
		if err != nil {
			return
		}
		var req ipcRequest
		if json.Unmarshal(raw, &req) != nil || req.Chunk != "print(1)" || req.ThreadID != "t1" {
			return
		}
		conn.Write([]byte(`{"Output":"hi\n","Err":""}`))
	}()

	reply, err := luaSend(sock, ipcRequest{ThreadID: "t1", Chunk: "print(1)"})
	if err != nil {
		t.Fatal(err)
	}
	if reply.Output != "hi\n" {
		t.Fatalf("output = %q", reply.Output)
	}
	if reply.Err != "" {
		t.Fatalf("err = %q", reply.Err)
	}
}

func TestLuaSendNoClient(t *testing.T) {
	_, err := luaSend(filepath.Join(t.TempDir(), "missing.sock"), ipcRequest{Chunk: "print(1)"})
	if err == nil || !strings.Contains(err.Error(), "no live notmutt client") {
		t.Fatalf("a dead socket must report no live client, got %v", err)
	}
}

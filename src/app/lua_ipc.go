// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fishman/notmutt/lib/localipc"
	"github.com/fishman/notmutt/lib/xdg"
)

// The lua IPC channel (R8, roadmap item 6): `notmutt lua '<chunk>'` relays
// a chunk to a LIVE session over a same-user unix socket and prints the
// reply. This file is the unconditional half (socket path, framing,
// client) - a relay needs no Lua; the server that runs the chunk lives in
// lua_ipc_server.go under the lua build tag.

// luaSocketPath is the one IPC socket: the runtime dir (or the state dir
// when XDG_RUNTIME_DIR is unset) plus the notmutt dir. Resolved from
// environment only - never the config dir, so a NOTMUTT_CONFIG override
// cannot split client from server.
func luaSocketPath() string {
	home := xdg.RuntimeHome()
	if home == "" {
		home = xdg.StateHome()
	}
	return filepath.Join(home, "notmutt", "ipc.sock")
}

// ipcRequest is the one request frame: the chunk, and the optional thread
// id that gives it a cursor (mail_lines, tag staging).
type ipcRequest struct {
	ThreadID string
	Chunk    string
}

// ipcReply is the one reply frame: the print-captured output, or the
// error the chunk raised.
type ipcReply struct {
	Output string
	Err    string
}

// maxIPCChunk caps the request body; a larger chunk is rejected, never
// buffered into memory.
const maxIPCChunk = 1 << 20


// luaSend carries one request to the socket and returns the reply. The
// path is injected so tests drive a temp listener. The read deadline is a
// literal past the server's actionDeadline (5m, lua-gated - a client that
// compiles without Lua cannot reference it) plus margin for the JSON.
func luaSend(sock string, req ipcRequest) (ipcReply, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return ipcReply{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute+5*time.Second)
	defer cancel()
	raw, err := localipc.Request(ctx, sock, body)
	if err != nil {
		return ipcReply{}, fmt.Errorf("no live notmutt client: %w", err)
	}
	var reply ipcReply
	if err := json.Unmarshal(raw, &reply); err != nil {
		return ipcReply{}, fmt.Errorf("lua ipc: bad reply: %w", err)
	}
	return reply, nil
}

// luaOnce is the `notmutt lua` subcommand: the chunk is the positional
// argument (joined with spaces), or stdin when absent - a pipe carries
// chunks beyond argv limits. -t names the thread context. Output prints
// to stdout; a raised error returns to main for stderr + a non-zero exit.
func luaOnce(args []string) error {
	fs := flag.NewFlagSet("lua", flag.ContinueOnError)
	tid := fs.String("t", "", "thread id context for the chunk")
	if err := fs.Parse(args); err != nil {
		return err
	}
	chunk := strings.Join(fs.Args(), " ")
	if chunk == "" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		chunk = strings.TrimSpace(string(b))
	}
	if chunk == "" {
		return errors.New("lua: no chunk (pass it as an argument or pipe it on stdin)")
	}
	if len(chunk) > maxIPCChunk {
		return fmt.Errorf("lua: chunk too large (%d bytes)", len(chunk))
	}
	reply, err := luaSend(luaSocketPath(), ipcRequest{ThreadID: *tid, Chunk: chunk})
	if err != nil {
		return err
	}
	if reply.Err != "" {
		return errors.New(reply.Err)
	}
	fmt.Print(reply.Output)
	return nil
}

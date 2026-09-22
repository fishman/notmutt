// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build lua

package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"time"

	"github.com/fishman/notmutt/lib/localipc"
	lua "github.com/yuin/gopher-lua"

	"notmutt/config"
	"notmutt/core"
)

// The lua IPC server (R8, roadmap item 6): a same-user unix socket that
// runs Lua chunks for the `notmutt lua` client inside the LIVE session.
// Every connection is one chunk run on a fresh sandbox VM (runLuaChunk,
// the :lua machinery) with the read-only metadata surface merged into the
// ctx so an external script can query mail without a cursor thread. The
// reply returns over the socket; the TUI bus stays quiet (no LuaResult) -
// only the chunk's staged effects (drain) ride it.

// wireLuaIPC starts the socket listener for the live session: a busy
// socket (a second client) is logged and skipped, never fatal.
func wireLuaIPC(ctx context.Context, bus *core.Bus, worker workerAPI, cfg *config.Config) {
	go serveLuaIPC(ctx, bus, worker, cfg)
}

func serveLuaIPC(ctx context.Context, bus *core.Bus, worker workerAPI, cfg *config.Config) {
	if err := serveLuaIPCat(ctx, bus, worker, cfg, luaSocketPath()); err != nil {
		log.Printf("lua ipc: %v", err)
	}
}

// serveLuaIPCat serves the socket at an explicit path (tests inject a temp
// dir). An existing socket is probed: a live peer answers = another
// session owns it (in use); a failed dial = stale, removed and re-listened.
// The socket file dies with the context.
func serveLuaIPCat(ctx context.Context, bus *core.Bus, worker workerAPI, cfg *config.Config, sock string) error {
	listener, err := localipc.Listen(ctx, sock)
	if err != nil {
		return err
	}
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			continue
		}
		go handleIPCConn(conn, bus, worker, cfg)
	}
}

// handleIPCConn serves one connection: peer check, size-capped request
// read, one chunk run, one reply. The chunk runs under its own VM
// deadline (runLuaChunk) - a busy loop or a parked picker cannot outlive
// the action budget, so a connection always closes.
func handleIPCConn(conn net.Conn, bus *core.Bus, worker workerAPI, cfg *config.Config) {
	defer conn.Close()
	if err := localipc.CheckPeer(conn); err != nil {
		writeIPReply(conn, ipcReply{Err: err.Error()})
		return
	}
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	raw, err := localipc.Read(conn, maxIPCChunk)
	if errors.Is(err, localipc.ErrTooLarge) {
		io.Copy(io.Discard, conn)
		writeIPReply(conn, ipcReply{Err: "lua ipc: chunk too large"})
		return
	}
	if err != nil {
		return
	}
	var req ipcRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		writeIPReply(conn, ipcReply{Err: "lua ipc: bad request: " + err.Error()})
		return
	}
	output, runErr := runLuaChunk(req.Chunk, req.ThreadID, bus, cfg, worker, ipcCtx)
	writeIPReply(conn, ipcReply{Output: output, Err: errString(runErr)})
}

// ipcCtx is the IPC chunk's context table: the action surface for the
// request's thread (thread_id, mail_lines) merged with the read-only
// metadata surface (search, count, thread_info) at a nil scope - the IPC
// caller is the session's own user, the same trust as :lua, never an MCP
// grant. Key sets are disjoint (thread_id/mail_lines vs
// search/count/thread_info), the same merge ctxTable(net) performs.
func ipcCtx(ac *actionCtx, vm *lua.LState) *lua.LTable {
	ctx := ac.ctxTable(vm, false)
	meta := metadataCtxTable(vm, ac.worker, nil, nil)
	meta.ForEach(func(k, v lua.LValue) { ctx.RawSet(k, v) })
	return ctx
}

func writeIPReply(conn net.Conn, reply ipcReply) {
	body, err := json.Marshal(reply)
	if err != nil {
		return
	}
	localipc.Write(conn, body)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

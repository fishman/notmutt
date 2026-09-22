// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

// Package localipc provides bounded same-user Unix socket transport primitives.
package localipc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"
)

const DefaultMaxMessage = 1 << 20

var (
	ErrInUse     = errors.New("local ipc socket is in use")
	ErrNotSocket = errors.New("local ipc path is not a socket")
	ErrTooLarge  = errors.New("local ipc message exceeds limit")
)

func Listen(ctx context.Context, path string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := removeStale(path); err != nil {
		return nil, err
	}

	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		listener.Close()
		os.Remove(path)
		return nil, err
	}
	go func() {
		<-ctx.Done()
		listener.Close()
		os.Remove(path)
	}()
	return listener, nil
}

func removeStale(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("%w: %s", ErrNotSocket, path)
	}
	if conn, err := net.DialTimeout("unix", path, 500*time.Millisecond); err == nil {
		conn.Close()
		return fmt.Errorf("%w: %s", ErrInUse, path)
	}
	return os.Remove(path)
}

func Request(ctx context.Context, path string, body []byte) ([]byte, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return nil, err
		}
	}
	if err := Write(conn, body); err != nil {
		return nil, err
	}
	unixConn, err := asUnix(conn)
	if err != nil {
		return nil, err
	}
	if err := unixConn.CloseWrite(); err != nil {
		return nil, err
	}
	return Read(conn, DefaultMaxMessage)
}

func Read(r io.Reader, limit int64) ([]byte, error) {
	if limit < 0 {
		return nil, fmt.Errorf("local ipc: negative message limit %d", limit)
	}
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, ErrTooLarge
	}
	return body, nil
}

func Write(w io.Writer, body []byte) error {
	for len(body) > 0 {
		n, err := w.Write(body)
		if err != nil {
			return err
		}
		body = body[n:]
	}
	return nil
}

func asUnix(conn net.Conn) (*net.UnixConn, error) {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return nil, errors.New("local ipc: non-unix connection")
	}
	return unixConn, nil
}

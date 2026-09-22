package localipc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRequestRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	listener, err := Listen(ctx, filepath.Join(t.TempDir(), "ipc.sock"))
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		body, err := Read(conn, 1024)
		if err != nil {
			return
		}
		Write(conn, append(body, "-reply"...))
	}()

	body, err := Request(ctx, listener.Addr().String(), []byte("request"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(body), "request-reply"; got != want {
		t.Fatalf("reply = %q, want %q", got, want)
	}
}

func TestListenPreservesRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ipc.sock")
	if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Listen(context.Background(), path)
	if err == nil {
		t.Fatal("Listen accepted a regular file")
	}
	if !errors.Is(err, ErrNotSocket) {
		t.Fatalf("Listen error = %v, want ErrNotSocket", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(body); got != "keep" {
		t.Fatalf("file = %q", got)
	}
}

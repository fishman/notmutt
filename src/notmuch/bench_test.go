// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build cli

package notmuch

import (
	"context"
	"os"
	"testing"
	"time"

	"notmutt/core"
	"notmutt/lib/testutil"
)

// Run: NOTMUCH_BENCH=1 go test -tags cli ./notmuch/ -run TestBench -v
// Requires notmuch dev headers on the default include/link paths (the
// binding links plain -lnotmuch, no pkg-config). Prints a comparison report.
func TestBench(t *testing.T) {
	if os.Getenv("NOTMUCH_BENCH") == "" {
		t.Skip("set NOTMUCH_BENCH=1 to run")
	}
	db := testutil.DevMailbox(t)
	ctx := context.Background()

	cli := NewCLI()
	cli.Open(ctx, db)
	defer cli.Close(ctx)
	cgoB := NewCGO()
	if err := cgoB.Open(ctx, db); err != nil {
		t.Fatalf("cgo open: %v", err)
	}
	defer cgoB.Close(ctx)

	report := func(name string, fn func() (int, error)) {
		t0 := time.Now()
		n, err := fn()
		if err != nil {
			t.Logf("%-30s ERROR %v", name, err)
			return
		}
		t.Logf("%-30s %8s  (%d rows)", name, time.Since(t0).Round(time.Millisecond), n)
	}

	collect := func(b Backend, q string, limit int) (int, error) {
		n := 0
		err := b.Query(ctx, q, limit, func(chunk []core.Message) bool {
			n += len(chunk)
			return true
		})
		return n, err
	}
	report("cli peek (50)", func() (int, error) {
		return collect(cli, "tag:inbox", 50)
	})
	report("cgo peek (50)", func() (int, error) {
		return collect(cgoB, "tag:inbox", 50)
	})
	report("cli full inbox", func() (int, error) {
		return collect(cli, "tag:inbox", 0)
	})
	report("cgo full inbox", func() (int, error) {
		return collect(cgoB, "tag:inbox", 0)
	})
	report("cli thread fetch", func() (int, error) {
		msgs, err := cli.Thread(ctx, firstThreadID(t, ctx, cli))
		return len(msgs), err
	})
	report("cgo thread fetch", func() (int, error) {
		msgs, err := cgoB.Thread(ctx, firstThreadID(t, ctx, cgoB))
		return len(msgs), err
	})
}

func firstThreadID(t *testing.T, ctx context.Context, b Backend) string {
	t.Helper()
	var msgs []core.Message
	if err := b.Query(ctx, "tag:inbox", 1, func(chunk []core.Message) bool {
		msgs = append(msgs, chunk...)
		return true
	}); err != nil || len(msgs) == 0 {
		t.Fatalf("seed query: %v %d", err, len(msgs))
	}
	return msgs[0].ThreadID
}

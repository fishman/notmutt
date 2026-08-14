//go:build notmuchcgo

package notmuch

import (
	"context"
	"os"
	"testing"
	"time"
)

// Run: NOTMUCH_BENCH=1 go test -tags notmuchcgo ./notmuch/ -run TestBench -v
// Requires notmuch dev headers on the default include/link paths (task 13;
// the binding links plain -lnotmuch, no pkg-config). Prints a comparison report.
func TestBench(t *testing.T) {
	if os.Getenv("NOTMUCH_BENCH") == "" {
		t.Skip("set NOTMUCH_BENCH=1 to run")
	}
	db := os.Getenv("NOTMUCH_DB")
	if db == "" {
		db = "/home/user/Mail"
	}
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
		t.Logf("%-30s %8s  (%d msgs)", name, time.Since(t0).Round(time.Millisecond), n)
	}

	report("cli first-page (50)", func() (int, error) {
		msgs, err := cli.Query(ctx, "tag:inbox", 50)
		return len(msgs), err
	})
	report("cgo first-page (50)", func() (int, error) {
		msgs, err := cgoB.Query(ctx, "tag:inbox", 50)
		return len(msgs), err
	})
	report("cli full inbox", func() (int, error) {
		msgs, err := cli.Query(ctx, "tag:inbox", 0)
		return len(msgs), err
	})
	report("cgo full inbox", func() (int, error) {
		msgs, err := cgoB.Query(ctx, "tag:inbox", 0)
		return len(msgs), err
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
	msgs, err := b.Query(ctx, "tag:inbox", 1)
	if err != nil || len(msgs) == 0 {
		t.Fatalf("seed query: %v %d", err, len(msgs))
	}
	return msgs[0].ThreadID
}

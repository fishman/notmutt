//go:build notmuchcgo

package notmuch

import (
	"context"
	"os"
	"testing"

	"notmutt/core"
)

func TestCGOSmoke(t *testing.T) {
	db := os.Getenv("NOTMUCH_DB")
	if db == "" {
		db = "/home/user/Mail"
	}
	b := NewCGO()
	if err := b.Open(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	defer b.Close(context.Background())
	uuid, rev, err := b.Revision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if uuid == "" || rev == 0 {
		t.Fatalf("revision: %q %d", uuid, rev)
	}
	n := 0
	err = b.Query(context.Background(), "tag:inbox", 10, func(chunk []core.Message) bool {
		for _, m := range chunk {
			// batched summaries are CLI-shaped stubs: thread-level
			// fields only, no message ids, paths, or references
			if m.ID != "" || m.Paths != nil || m.References != nil || m.ThreadID == "" || m.Timestamp == 0 {
				t.Fatalf("row is not a summary stub: %+v", m)
			}
			n++
		}
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 10 {
		t.Fatalf("limit must bound the walk to 10 threads, got %d", n)
	}
	t.Logf("got %d thread summaries, rev %d (counts only, no content)", n, rev)
}

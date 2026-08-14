//go:build notmuchcgo

package notmuch

import (
	"context"
	"os"
	"testing"
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
	msgs, err := b.Query(context.Background(), "tag:inbox", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("got %d messages, rev %d (counts only, no content)", len(msgs), rev)
}

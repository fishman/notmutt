package cache

import (
	"os"
	"path/filepath"
	"testing"

	"go.etcd.io/bbolt"

	"notmutt/core"
)

func TestBboltRoundtrip(t *testing.T) {
	c, err := Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	k := Key{Path: "/m/Mail/x", Size: 10, Mtime: 5}
	atts := []core.Attachment{{Name: "evil.txt", MimeType: "text/plain", Size: 3}}
	if err := c.Put(k, atts); err != nil {
		t.Fatal(err)
	}
	got, ok, err := c.Get(k)
	if err != nil || !ok || len(got) != 1 || got[0].Name != "evil.txt" {
		t.Fatalf("get: %v %v %v", got, ok, err)
	}
	miss := Key{Path: "/m/Mail/y", Size: 1, Mtime: 1}
	if _, ok, _ := c.Get(miss); ok {
		t.Fatal("expected miss")
	}
}

func TestBboltFilePerms(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	c, _ := Open(path)
	c.Close()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0600 {
		t.Fatalf("cache file must be 0600, got %v", fi.Mode().Perm())
	}
}

func TestBboltCorruptPayloadDiscarded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	c, _ := Open(path)
	k := Key{Path: "/m/Mail/z", Size: 2, Mtime: 2}
	c.Put(k, []core.Attachment{{Name: "ok"}})
	c.Close()

	db, err := bbolt.Open(path, 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte("atts")).Put([]byte(k.String()), []byte("garbage"))
	})
	db.Close()

	c2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if _, ok, err := c2.Get(k); ok || err != nil {
		t.Fatalf("corrupt entry must be a miss without error, got ok=%v err=%v", ok, err)
	}
	if _, ok, _ := c2.Get(k); ok {
		t.Fatal("corrupt entry must be deleted")
	}
}

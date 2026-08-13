package config

import (
	"sync"
	"testing"
)

func TestStoreSetKeymapNotifies(t *testing.T) {
	s := NewStore(Default())
	got := false
	s.Subscribe("ui", func() { got = true })
	if err := s.SetKeymap("emacs"); err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Fatal("ui observer not notified")
	}
	if k := s.Config().UI.Keymap; k != "emacs" {
		t.Fatalf("keymap not stored: %q", k)
	}
}

func TestStoreSetKeymapRejects(t *testing.T) {
	s := NewStore(Default())
	if err := s.SetKeymap("vi"); err == nil {
		t.Fatal("expected error for invalid keymap")
	}
}

func TestStoreSetViewQueryNotifies(t *testing.T) {
	s := NewStore(Default())
	got := false
	s.Subscribe("view", func() { got = true })
	if err := s.SetViewQuery("inbox", "tag:unread"); err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Fatal("view observer not notified")
	}
	if c := s.Config(); c.Views["inbox"].Query != "tag:unread" {
		t.Fatalf("query not stored: %+v", c.Views["inbox"])
	}
}

func TestStoreSetViewQueryUnknownViewErrors(t *testing.T) {
	s := NewStore(Default())
	if err := s.SetViewQuery("nope", "tag:unread"); err == nil {
		t.Fatal("expected error for unknown view")
	}
}

func TestStoreSetViewQueryEmptyErrors(t *testing.T) {
	s := NewStore(Default())
	if err := s.SetViewQuery("inbox", "  "); err == nil {
		t.Fatal("expected error for empty query")
	}
}

func TestStoreSnapshotNotMutatedBySetter(t *testing.T) {
	s := NewStore(Default())
	c := s.Config()
	if err := s.SetViewQuery("inbox", "tag:unread"); err != nil {
		t.Fatal(err)
	}
	if q := c.Views["inbox"].Query; q != "tag:inbox" {
		t.Fatalf("snapshot mutated by setter: %q", q)
	}
}

func TestStoreConcurrentConfigAndSet(t *testing.T) {
	s := NewStore(Default())
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			// Dereference the snapshot outside the lock, the pattern
			// T6/T9 will use; the aliased map races with the setter.
			_ = s.Config().Views["inbox"]
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			if err := s.SetViewQuery("inbox", "tag:unread"); err != nil {
				t.Error(err)
			}
		}
	}()
	wg.Wait()
}

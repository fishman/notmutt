package config

import "testing"

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

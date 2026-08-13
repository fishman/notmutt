package notmuch

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const searchJSON = `[{"thread":"t1","timestamp":1700000000,"authors":"A B","subject":"S","tags":["inbox","unread"],"id":"m1","total":1,"matched":1}]`

const showJSON = `[[{"id":"m1","thread":"t1","timestamp":1700000000,"authors":"A B","subject":"S","tags":["inbox"],"references":["p1"]}]]`

func fakeRun(b *CLIBackend, respond func(name string, args []string) ([]byte, error)) {
	b.run = func(ctx context.Context, name string, args []string) ([]byte, error) {
		return respond(name, args)
	}
}

func TestCLIQuery(t *testing.T) {
	b := NewCLI()
	fakeRun(b, func(name string, args []string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), "--output=files") {
			return []byte("/m/Mail/x/1\n"), nil
		}
		return []byte(searchJSON), nil
	})
	msgs, err := b.Query(context.Background(), "tag:inbox", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].ID != "m1" || msgs[0].ThreadID != "t1" || msgs[0].Author != "A B" {
		t.Fatalf("parse wrong: %+v", msgs)
	}
	if len(msgs[0].Paths) != 1 || msgs[0].Paths[0] != "/m/Mail/x/1" {
		t.Fatalf("paths pairing wrong: %+v", msgs[0].Paths)
	}
	if msgs[0].Tags[0] != "inbox" {
		t.Fatalf("tags wrong: %+v", msgs[0].Tags)
	}
}

func TestCLIThread(t *testing.T) {
	b := NewCLI()
	fakeRun(b, func(name string, args []string) ([]byte, error) {
		return []byte(showJSON), nil
	})
	msgs, err := b.Thread(context.Background(), "t1")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].References[0] != "p1" {
		t.Fatalf("thread parse wrong: %+v", msgs)
	}
}

func TestCLIRevision(t *testing.T) {
	b := NewCLI()
	fakeRun(b, func(name string, args []string) ([]byte, error) {
		return []byte("94443\t03b22d86-cf7e-4c5f-ac9d-1678e29d8232\t557071\n"), nil
	})
	uuid, rev, err := b.Revision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if uuid != "03b22d86-cf7e-4c5f-ac9d-1678e29d8232" || rev != 557071 {
		t.Fatalf("revision parse wrong: %q %d", uuid, rev)
	}
}

func TestCLITagArgs(t *testing.T) {
	b := NewCLI()
	var got []string
	fakeRun(b, func(name string, args []string) ([]byte, error) {
		got = args
		return nil, nil
	})
	if err := b.Tag(context.Background(), `id:"weird id"`, []TagOp{{Tag: "unread", Add: false}, {Tag: "inbox", Add: true}}); err != nil {
		t.Fatal(err)
	}
	want := []string{"tag", "-unread", "+inbox", `id:"weird id"`}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv wrong: %v", got)
	}
}

func TestCLIQueryError(t *testing.T) {
	b := NewCLI()
	fakeRun(b, func(name string, args []string) ([]byte, error) {
		return []byte("notmuch error: something"), errors.New("exit status 1")
	})
	if _, err := b.Query(context.Background(), "tag:inbox", 10); err == nil {
		t.Fatal("expected error")
	}
}

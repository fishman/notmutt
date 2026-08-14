package notmuch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"notmutt/core"
)

const searchJSON = `[{"thread":"t1","timestamp":1700000000,"date_relative":"1 hour ago","matched":1,"total":2,"authors":"Ann","subject":"hello","query":["thread:t1 and tag:inbox",null],"tags":["inbox","unread"]}]`

const showJSON = `[[[{"id":"m1","match":true,"excluded":false,"filename":["/m/Mail/x/1"],"timestamp":1700000000,"tags":["inbox"],"headers":{"Subject":"hello","From":"Ann <ann@x>"},"duplicate":0},[[{"id":"m2","match":true,"excluded":false,"filename":["/m/Mail/x/2"],"timestamp":1700000001,"tags":["inbox"],"headers":{"Subject":"re: hello","From":"Bob <bob@x>"},"duplicate":0},[]]]]]]`

const nullJSON = `[[[null,[[{"id":"m2","match":true,"excluded":false,"filename":["/m/Mail/x/2"],"timestamp":1700000001,"tags":["inbox"],"headers":{"Subject":"re: hello","From":"Bob <bob@x>"},"duplicate":0},[]]]]]]`

func fakeRun(b *CLIBackend, respond func(name string, args []string) ([]byte, error)) {
	b.run = func(ctx context.Context, name string, args []string) ([]byte, error) {
		return respond(name, args)
	}
}

func TestCLIQuery(t *testing.T) {
	b := NewCLI()
	var got []string
	fakeRun(b, func(name string, args []string) ([]byte, error) {
		got = args
		return []byte(searchJSON), nil
	})
	var msgs []core.Message
	if err := b.Query(context.Background(), "tag:inbox", 10, func(chunk []core.Message) bool {
		msgs = append(msgs, chunk...)
		return true
	}); err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].ThreadID != "t1" || msgs[0].Timestamp != 1700000000 || msgs[0].Author != "Ann" || msgs[0].Subject != "hello" {
		t.Fatalf("stub parse wrong: %+v", msgs)
	}
	if msgs[0].ID != "" {
		t.Fatalf("summary carries no message ids; ID must stay empty: %+v", msgs[0])
	}
	if msgs[0].Tags[0] != "inbox" {
		t.Fatalf("tags wrong: %+v", msgs[0].Tags)
	}
	want := []string{"search", "--format=json", "--sort=newest-first", "--limit=10", "tag:inbox"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv wrong: %v", got)
	}
}

func TestCLIQueryNoLimit(t *testing.T) {
	b := NewCLI()
	var got []string
	fakeRun(b, func(name string, args []string) ([]byte, error) {
		got = args
		return []byte(searchJSON), nil
	})
	if err := b.Query(context.Background(), "tag:inbox", 0, nil); err != nil {
		t.Fatal(err)
	}
	want := []string{"search", "--format=json", "--sort=newest-first", "tag:inbox"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("limit=0 must omit --limit: %v", got)
	}
}

// searchItemsJSON builds an N-item search result fixture.
func searchItemsJSON(n int) string {
	var sb strings.Builder
	sb.WriteByte('[')
	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `{"thread":"t%d","timestamp":%d,"authors":"A","subject":"s","tags":[]}`, i, 1700000000+i)
	}
	sb.WriteByte(']')
	return sb.String()
}

// TestCLIQueryChunks pins the chunk cadence: the whole result arrives in
// one call (no offset paging) and is emitted as 100, then 5000s - the
// render-batching contract: the first paint shows up after 100 threads,
// the rest lands in big batches.
func TestCLIQueryChunks(t *testing.T) {
	b := NewCLI()
	fakeRun(b, func(name string, args []string) ([]byte, error) {
		return []byte(searchItemsJSON(5150)), nil
	})
	var sizes []int
	if err := b.Query(context.Background(), "tag:inbox", 0, func(chunk []core.Message) bool {
		sizes = append(sizes, len(chunk))
		return true
	}); err != nil {
		t.Fatal(err)
	}
	want := []int{100, 5000, 50}
	if len(sizes) != len(want) {
		t.Fatalf("expected %d chunks, got %d (%v)", len(want), len(sizes), sizes)
	}
	for i, w := range want {
		if sizes[i] != w {
			t.Fatalf("chunk %d = %d, want %d", i, sizes[i], w)
		}
	}
}

func TestCLIQueryStopEarly(t *testing.T) {
	b := NewCLI()
	fakeRun(b, func(name string, args []string) ([]byte, error) {
		return []byte(searchItemsJSON(500)), nil
	})
	emits := 0
	if err := b.Query(context.Background(), "tag:inbox", 0, func(chunk []core.Message) bool {
		emits++
		return false
	}); err != nil {
		t.Fatal(err)
	}
	if emits != 1 {
		t.Fatalf("emit=false must stop the walk, got %d emits", emits)
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
	if len(msgs) != 2 || msgs[0].ID != "m1" || msgs[1].ID != "m2" {
		t.Fatalf("tree walk wrong: %+v", msgs)
	}
	if msgs[0].ThreadID != "t1" || msgs[1].ThreadID != "t1" {
		t.Fatalf("ThreadID must come from the argument: %+v", msgs)
	}
	if len(msgs[1].References) != 1 || msgs[1].References[0] != "m1" {
		t.Fatalf("references chain wrong: %+v", msgs[1].References)
	}
	if len(msgs[0].References) != 0 {
		t.Fatalf("root must have empty references: %+v", msgs[0].References)
	}
	if msgs[1].Subject != "re: hello" || msgs[1].Author != "Bob <bob@x>" {
		t.Fatalf("headers parse wrong: %+v", msgs[1])
	}
	if len(msgs[0].Paths) != 1 || msgs[0].Paths[0] != "/m/Mail/x/1" {
		t.Fatalf("filenames wrong: %+v", msgs[0].Paths)
	}
}

func TestCLIThreadSkipsNullRoot(t *testing.T) {
	b := NewCLI()
	fakeRun(b, func(name string, args []string) ([]byte, error) {
		return []byte(nullJSON), nil
	})
	msgs, err := b.Thread(context.Background(), "t1")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].ID != "m2" || msgs[0].ThreadID != "t1" {
		t.Fatalf("null root walk wrong: %+v", msgs)
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
	if err := b.Query(context.Background(), "tag:inbox", 10, nil); err == nil {
		t.Fatal("expected error")
	}
}


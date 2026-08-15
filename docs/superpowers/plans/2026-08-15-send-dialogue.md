# Send/Reply Dialogue Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** R/gr/F/m open compose dialogues (reply, reply-all, forward, new compose) as attached compose surfaces that park into tabs via `[`/`]`, with `$EDITOR` body editing, msmtp transport, fcc, and reply tagging.

**Architecture:** The dialogue state machine is a new pure-Go `compose` package (no UI, no notmuch handle - R5). The app owns the reply prefill (account detection, mail parsing, signature default) and the send job (assemble -> transport argv exec -> fcc -> `notmuch new` -> reply tag). The tui owns presentation: a tab list of dialogue states (`tabIdx` 0 = the mail surface, > 0 = an attached dialogue), the compose frame, the fuzzy picker popup, the attach path prompt, and the `$EDITOR` exec passthrough (tea.ExecProcess, dialogue state survives - R4 pause/restart). The dialogue and the tab are one mechanism, two presentations (spec section 5).

**Tech Stack:** Go, go-message v0.18.2 (vendored, `mail.CreateWriter`/`CreateSingleInline`/`CreateAttachment`), bubbletea v2 (`tea.ExecProcess`), BurntSushi/toml, `msmtp --read-envelope-from` as the default transport argv.

---

## Context (read before Task 1)

The codebase is `/home/user/git/opencode/notmutt/src` (module `notmutt`). Packages: `core` (types, bus, tag resolution), `config` (TOML, strict load, scheme bindings), `notmuch` (worker + CLI backend), `app` (wiring, refresher, apply), `tui` (bubbletea model + render), `mail` (go-message parse + sanitize).

Key facts the tasks build on (verified against the current code):

- `config.Account` is `struct { Folder *string toml:"folder" }` with `Tag(name)` and `Config.AccountTags()` (src/config/config.go:462-483). `Default()` hands out fresh binding maps via `cloneBindings(scheme("vim"))` (config.go:567-609).
- Binding schemes are data maps in config.go:487-518 (`vimScheme`/`emacsScheme`); `mergeBindings` overlays file bindings (config.go:536-555); `validate` rejects unknown contexts and empty tables (config.go:755-770).
- The tui's action vocabulary is `Actions` in src/tui/model.go:22-36; the app validates every binding against it at startup (src/app/app.go:151-176, `validateBindings`).
- The tui communicates with the app through func seams in src/tui/hooks.go (`onApply`/`SetApplyHandler`, `onOpen`/`SetOpenHandler`) and through bus events. The bus carries `core.Event` (any type); the model handles bus events wrapped in `EventMsg` (src/tui/bridge.go). The model's `Update` dispatch is a switch on the action string resolved by `actionForKey` (src/tui/model.go:166-235); vim prefix machinery (digit counts, the `g` chain for gg) lives at model.go:129-165.
- The frame discipline: `render()` must emit EXACTLY `m.height` lines, no trailing newline, keyhint row + status row last (src/tui/model.go:787-889). `padRow(line, w, outer)` truncates/pads to the width with the row style (src/tui/index.go:315-324). `keyhintRow(km, width)` derives the hint from the binding map (src/tui/keyhints.go). `statusLineWidth(st, ui, d, width)` composes the status row from a `statusData` struct.
- `core.Message` (src/core/types.go:3-13): ID (the message-id header value, queryable as `id:"..."`, see `idQuery` in src/app/apply.go:71-76), ThreadID, Timestamp, Author, Subject, Tags, References, Paths, Atts. It has NO To/Cc and no Message-ID header string - Task 3 adds those to `mail.Message`.
- `mail.Message` (src/mail/thread.go:80-86): From (bare address), Date, Subject, Parts, Attachments. `ParseMessage(path)` parses one file (thread.go:109-161).
- The notmuch worker (src/notmuch/worker.go): `Action{ID, Kind, Query, TagOps, ...}`; `Call(a Action) (Reply, error)` is synchronous. `ActNew` exists (backend `New`, lock-budgeted like ActTag). `notmuch.TagOp` aliases `core.TagOp`.
- The app's only backend access is `workerAPI` (src/app/refresh.go:13): `Call(a notmuch.Action) (notmuch.Reply, error)`.
- go-message (vendored, v0.18.2): `mail.Header` has `SetAddressList`, `SetSubject`, `SetDate`, `GenerateMessageID`, `SetMsgIDList`, `Set`, `Get` (vendor/.../mail/header.go); `mail.ParseAddress` (mail/address.go:27); `mail.CreateWriter` -> `CreateSingleInline(mail.InlineHeader{})` and `CreateAttachment(mail.AttachmentHeader{})` with `SetFilename` (mail/writer.go:43-114, mail/attachment.go:27).
- bubbletea v2: `tea.ExecProcess(c *exec.Cmd, fn ExecCallback) Cmd` (vendor/charm.land/bubbletea/v2/exec.go:50).
- The privacy hard rule: never submit mail content (bodies, headers, whole .eml files) to the LLM. Test fixtures use fabricated addresses; never read the user's real mail files into a prompt. Only `grep -m1`-style extracted values pass.
- READ-ONLY files, never modified: src/core/view.go, src/core/view_test.go, src/cache/*, src/app/cachejob.go. src/test.txt is never committed.
- Commit style: Conventional Commits, brief lowercase imperative subjects. Code commits carry NO AI trailer. Doc/spec commits carry `AI-assisted: deepseek`.
- Every task ends with the same verification: `go test ./...` from `/home/user/git/opencode/notmutt/src` (cd into src for all go commands).

Task dependencies: 1 is the tui Actions vocabulary - it must land FIRST, before the config task, so the app's binding validation (validateBindings runs `config.Default()` in tests) stays green on every commit; 2-3 the core/mail prerequisites; 4-7 build the compose package; 8 the config surface (needs 1); 9-10 the app side (need 2, 3, 7, 8); 11-13 the tui side (need 1, 4, 7); 14 wires and verifies. Tasks 4-7 and 11-12 are pure and independent of each other.

---

### Task 1: Tui - action vocabulary for compose, fuzzy, tabs

**Files:**
- Modify: `src/tui/model.go:22-36` (Actions map)
- Test: `src/tui/model_test.go` (fixture update)

- [ ] **Step 1: Write the failing test**

In `src/tui/model_test.go`, find the `testBindings` fixture (a map of context -> key -> action used to construct models in tests). Add the compose and fuzzy contexts and the new index/pager keys to it:

```go
		"compose": {
			"j": "form-down", "k": "form-up",
			"e": "edit", "a": "attach", "d": "detach",
			"c": "account", "C": "signature", "y": "send", "q": "abort",
			"[": "tab-prev", "]": "tab-next",
		},
		"fuzzy": {
			"j": "fuzzy-down", "k": "fuzzy-up",
			"ctrl+n": "fuzzy-down", "ctrl+p": "fuzzy-up",
			"enter": "fuzzy-select", "esc": "fuzzy-cancel",
		},
```

and add `"m": "compose", "R": "reply", "F": "forward", "[": "tab-prev", "]": "tab-next"` to the fixture's index context and `"[": "tab-prev", "]": "tab-next"` to its pager context.

Add a new test:

```go
func TestActionsCoverComposeAndFuzzy(t *testing.T) {
	for _, ctx := range []string{"compose", "fuzzy"} {
		if len(Actions[ctx]) == 0 {
			t.Fatalf("Actions[%q] must cover the context", ctx)
		}
	}
	for _, ctx := range []string{"index", "pager"} {
		if !Actions[ctx]["tab-prev"] || !Actions[ctx]["tab-next"] {
			t.Fatalf("Actions[%q] must carry tab-prev/tab-next", ctx)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./tui/ -run TestActionsCoverComposeAndFuzzy`
Expected: FAIL - the compose/fuzzy contexts are not in `Actions`.

- [ ] **Step 3: Implement**

In `src/tui/model.go:22-36`, replace the `Actions` map with:

```go
var Actions = map[string]map[string]bool{
	"index": {
		"cursor-down": true, "cursor-up": true,
		"cursor-top": true, "cursor-bottom": true,
		"page-down": true, "page-up": true,
		"open": true, "quit": true, "undo": true, "apply": true,
		"reply": true, "reply-all": true, "forward": true, "compose": true,
		"tab-prev": true, "tab-next": true,
	},
	"pager": {
		"scroll-down": true, "scroll-up": true,
		"page-down": true, "page-up": true,
		"half-page-down": true, "half-page-up": true,
		"scroll-top": true, "scroll-bottom": true,
		"back": true, "quit": true,
		"tab-prev": true, "tab-next": true,
	},
	"compose": {
		"form-down": true, "form-up": true,
		"edit": true, "attach": true, "detach": true,
		"account": true, "signature": true,
		"send": true, "abort": true,
		"tab-prev": true, "tab-next": true,
	},
	"fuzzy": {
		"fuzzy-down": true, "fuzzy-up": true,
		"fuzzy-select": true, "fuzzy-cancel": true,
	},
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./tui/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add src/tui/model.go src/tui/model_test.go
git commit -m "feat(tui): compose, fuzzy, and tab actions vocabulary"
```

---

### Task 2: Core - AccountTag helper, ComposeOpened and SendResult events

**Files:**
- Modify: `src/core/resolve.go`
- Modify: `src/core/bus.go`
- Modify: `src/tui/segments.go:64` (accountTag delegates)
- Modify: `src/tui/model.go:417` (call site)
- Test: `src/tui/statusline_test.go:78-81` (call sites)

- [ ] **Step 1: Write the failing test**

Add to `src/core/resolve_test.go`:

```go
func TestAccountTag(t *testing.T) {
	set := map[string]bool{"gmail": true, "dynamia": true}
	if AccountTag([]string{"inbox", "gmail", "work"}, set) != "gmail" {
		t.Fatal("must find the first account tag in the tag list")
	}
	if AccountTag([]string{"inbox", "work"}, set) != "" {
		t.Fatal("no account tag must resolve empty")
	}
	if AccountTag(nil, set) != "" {
		t.Fatal("nil tags must resolve empty")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./core/ -run TestAccountTag`
Expected: FAIL - undefined: AccountTag

- [ ] **Step 3: Implement**

In `src/core/resolve.go`, add (top of file, after the imports):

```go
// AccountTag is the message's account: the first account tag in the
// tag list (R2 - accounts map to folder-prefix tags). Empty when the
// message carries no account tag. The one definition: the status bar,
// the compose dialogue detection, and the account resolution all use
// it (DRY - never a second copy).
func AccountTag(tags []string, set map[string]bool) string {
	for _, tag := range tags {
		if set[tag] {
			return tag
		}
	}
	return ""
}
```

In `src/core/bus.go`, add after `ViewDiff` (bus.go:70):

```go
// ComposeOpened opens a compose dialogue tab (R4): the app builds the
// prefill (account detection, quoting, default signature) and the TUI
// attaches the dialogue. Mode is one of "compose" | "reply" |
// "reply-all" | "forward".
type ComposeOpened struct {
	TabID       string
	Mode        string
	Account     string
	From        string
	To, Cc      []string
	Subject     string
	Body        string
	Attachments []ComposeAttachment
	Signature   string
	SigContent  string
	MessageID   string
	References  []string
	OriginalID  string
}

// ComposeAttachment is the bus contract's attachment shape (core stays
// dependency-free; compose owns the mapping to its own type).
type ComposeAttachment struct {
	Name, Path string
	Size       int64
}

// SendResult reports the send job's outcome to the dialogue (R4): OK
// closes the tab; a failure keeps it open with Output for review.
type SendResult struct {
	TabID  string
	OK     bool
	Output string
	Err    error
}
```

In `src/tui/segments.go:61-71`, replace the `accountTag` function body with a delegation (the definition moves to core, DRY):

```go
// accountTag is the message's account: core owns the one definition
// (the compose dialogue's detection chain uses it too).
func accountTag(tags []string, set map[string]bool) string {
	return core.AccountTag(tags, set)
}
```

The tui already imports `core` in segments.go (check the import block; it is used by `progressSegment`).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./core/ ./tui/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add src/core/resolve.go src/core/resolve_test.go src/core/bus.go src/tui/segments.go
git commit -m "feat(core): account tag helper, compose opened and send result events"
```

Note: `src/tui/model.go:417` and `src/tui/statusline_test.go:78-81` keep calling the tui-local `accountTag` wrapper - no change needed there.

---

### Task 3: Mail - MessageID, To, Cc on the parsed message

**Files:**
- Modify: `src/mail/thread.go`
- Test: `src/mail/thread_test.go` (new)

- [ ] **Step 1: Write the failing test**

Create `src/mail/thread_test.go`:

```go
package mail

import (
	"os"
	"path/filepath"
	"testing"
)

// fixtureMail is a fabricated message - never real mail content.
const fixtureMail = `From: Alice <alice@example.com>
To: Bob <bob@example.com>, Carol <carol@example.org>
Cc: Dave <dave@example.net>
Subject: hello
Message-Id: <abc123@example.com>
Date: Tue, 14 Aug 2026 10:00:00 +0000

body line one
body line two
`

func TestParseMessageHeaders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.eml")
	if err := os.WriteFile(path, []byte(fixtureMail), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := ParseMessage(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.MessageID != "<abc123@example.com>" {
		t.Fatalf("MessageID = %q", m.MessageID)
	}
	if m.From != "alice@example.com" {
		t.Fatalf("From = %q", m.From)
	}
	if len(m.To) != 2 || m.To[0] != "bob@example.com" || m.To[1] != "carol@example.org" {
		t.Fatalf("To = %v", m.To)
	}
	if len(m.Cc) != 1 || m.Cc[0] != "dave@example.net" {
		t.Fatalf("Cc = %v", m.Cc)
	}
	if len(m.Parts) != 2 || m.Parts[0].Body != "body line one" {
		t.Fatalf("Parts = %+v", m.Parts)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./mail/ -run TestParseMessageHeaders`
Expected: FAIL - `m.MessageID` (and `m.To`/`m.Cc`) do not exist on the struct.

- [ ] **Step 3: Implement**

In `src/mail/thread.go`:

Extend the `Message` struct (thread.go:80-86):

```go
type Message struct {
	From        string
	Date        string
	Subject     string
	MessageID   string
	To          []string // bare addresses, reply-all prefill
	Cc          []string
	Parts       []Part
	Attachments []Attachment
}
```

Extend `ParseMessage` (thread.go:120-127, next to the existing From parse):

```go
	hdr := mr.Header
	m := &Message{}
	if addrs, err := hdr.AddressList("From"); err == nil && len(addrs) > 0 {
		m.From = addrs[0].Address
	}
	m.MessageID = hdr.Get("Message-Id")
	if addrs, err := hdr.AddressList("To"); err == nil {
		for _, a := range addrs {
			m.To = append(m.To, a.Address)
		}
	}
	if addrs, err := hdr.AddressList("Cc"); err == nil {
		for _, a := range addrs {
			m.Cc = append(m.Cc, a.Address)
		}
	}
	m.Date = hdr.Get("Date")
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./mail/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add src/mail/thread.go src/mail/thread_test.go
git commit -m "feat(mail): parse message id and to/cc address lists"
```

---

### Task 4: Compose package - dialogue state and reply/forward prefills

**Files:**
- Create: `src/compose/state.go`
- Create: `src/compose/prefill.go`
- Test: `src/compose/prefill_test.go` (new)

- [ ] **Step 1: Write the failing test**

Create `src/compose/prefill_test.go`:

```go
package compose

import (
	"strings"
	"testing"
	"time"

	"notmutt/core"
	"notmutt/mail"
)

func fixture() (core.Message, *mail.Message) {
	orig := core.Message{
		ID: "<msg-1@example.com>", Timestamp: time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC).Unix(),
		Author: "Alice <alice@example.com>", Subject: "Re: Re: hello",
		References: []string{"<a@x>", "<b@x>"},
		Tags:       []string{"inbox", "gmail"},
	}
	parsed := &mail.Message{
		From: "alice@example.com", MessageID: "<msg-1@example.com>", Subject: "Re: Re: hello",
		To: []string{"bob@example.com"}, Cc: []string{"carol@example.org"},
		Parts: []mail.Part{
			{Body: "line one"},
			{Body: "> quoted line"},
			{Body: "-- ", Signature: true},
			{Body: "alice", Signature: true},
		},
	}
	return orig, parsed
}

func TestReplyPrefill(t *testing.T) {
	orig, parsed := fixture()
	s := Reply(orig, parsed, "gmail", "Bob <bob@example.com>", "gmail", "bob")
	if s.Mode != ModeReply || s.Account != "gmail" || s.From != "Bob <bob@example.com>" {
		t.Fatalf("mode/account/from: %+v", s)
	}
	if len(s.To) != 1 || s.To[0] != "alice@example.com" {
		t.Fatalf("To = %v", s.To)
	}
	if len(s.Cc) != 0 {
		t.Fatalf("Cc = %v", s.Cc)
	}
	if s.Subject != "Re: hello" {
		t.Fatalf("Subject = %q", s.Subject)
	}
	if s.OriginalID != "<msg-1@example.com>" || s.MessageID != "<msg-1@example.com>" {
		t.Fatalf("ids: %q %q", s.OriginalID, s.MessageID)
	}
	if len(s.References) != 2 || s.References[0] != "<a@x>" {
		t.Fatalf("References = %v", s.References)
	}
	if s.Signature != "gmail" || s.SignatureBody != "bob" {
		t.Fatalf("signature = %q %q", s.Signature, s.SignatureBody)
	}
	body := s.Body
	if !strings.HasPrefix(body, "On Fri, Aug 14 2026, Alice <alice@example.com> wrote:\n") {
		t.Fatalf("attribution missing: %q", body)
	}
	if !strings.Contains(body, "> line one\n") || !strings.Contains(body, ">> quoted line\n") {
		t.Fatalf("quoted lines wrong: %q", body)
	}
	if strings.Contains(body, "alice") {
		t.Fatalf("signature must not be quoted: %q", body)
	}
}

func TestReplyAllPrefill(t *testing.T) {
	orig, parsed := fixture()
	s := ReplyAll(orig, parsed, "gmail", "Bob <bob@example.com>", "carol@example.org", "gmail", "bob")
	if len(s.To) != 1 || s.To[0] != "alice@example.com" {
		t.Fatalf("To = %v", s.To)
	}
	// own address drops from the Cc; the rest of To+Cc carries over
	if len(s.Cc) != 1 || s.Cc[0] != "bob@example.com" {
		t.Fatalf("Cc = %v", s.Cc)
	}
}

func TestForwardPrefill(t *testing.T) {
	orig, parsed := fixture()
	s := Forward(orig, parsed, "gmail", "Bob <bob@example.com>", "gmail", "bob")
	if s.Mode != ModeForward || len(s.To) != 0 || len(s.Cc) != 0 {
		t.Fatalf("forward prefill: %+v", s)
	}
	if s.Subject != "Fwd: hello" {
		t.Fatalf("Subject = %q", s.Subject)
	}
	if !strings.Contains(s.Body, "> line one") {
		t.Fatalf("forward must quote the body: %q", s.Body)
	}
}

func TestNewCompose(t *testing.T) {
	s := NewCompose("dynamia", "Reza <reza@example.com>", "", "")
	if s.Mode != ModeCompose || s.Account != "dynamia" {
		t.Fatalf("new compose: %+v", s)
	}
	if len(s.To) != 0 || s.Subject != "" || s.Body != "" || s.Signature != "" {
		t.Fatalf("new compose must be blank: %+v", s)
	}
}

func TestAddAttachment(t *testing.T) {
	s := NewCompose("gmail", "Bob <bob@example.com>", "", "")
	path := t.TempDir() + "/note.txt"
	if err := writeFixture(path); err != nil {
		t.Fatal(err)
	}
	if err := s.AddAttachment(path); err != nil {
		t.Fatal(err)
	}
	if len(s.Attachments) != 1 || s.Attachments[0].Name != "note.txt" || s.Attachments[0].Path != path || s.Attachments[0].Size == 0 {
		t.Fatalf("attachment = %+v", s.Attachments)
	}
	if err := s.AddAttachment(t.TempDir()); err == nil {
		t.Fatal("a directory must error")
	}
	if err := s.AddAttachment(t.TempDir() + "/nope"); err == nil {
		t.Fatal("a missing path must error")
	}
}

func writeFixture(path string) error {
	return osWriteFile(path, []byte("hello attachment"), 0600)
}

func TestQuoteDepthCap(t *testing.T) {
	orig := core.Message{Timestamp: 0, Author: "A <a@b>"}
	parsed := &mail.Message{
		Parts: []mail.Part{
			{Body: "plain"},
			{Body: "> one"},
			{Body: ">> two"},
			{Body: ">>>>> deep"},
			{Body: ">>>>>> six"},
		},
	}
	body := Quote(orig, parsed.Parts)
	lines := strings.Split(body, "\n")
	want := []string{"> plain", ">> one", ">>> two", ">>>>>> deep", ">>>>>> six"}
	for i, w := range want {
		if lines[i+1] != w {
			t.Fatalf("line %d = %q, want %q\n%s", i, lines[i+1], w, body)
		}
	}
}
```

The test needs `os` and `os.WriteFile` aliased - add `"os"` to the imports and define `osWriteFile = os.WriteFile` at the top of the test file (avoids a naming collision with the package's own helpers if any).

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./compose/`
Expected: FAIL - package compose does not exist (no buildable sources).

- [ ] **Step 3: Implement**

Create `src/compose/state.go`:

```go
// Package compose is the send/reply dialogue state machine (R4): pure
// Go - no UI code, no notmuch handle (R5). The tui renders it, the
// app runs its sends; the state survives pauses (tab parking, editor
// runs) because it lives here, not in a widget.
package compose

import (
	"fmt"
	"os"
	"path/filepath"
)

// Mode is the dialogue kind.
type Mode int

const (
	ModeCompose Mode = iota
	ModeReply
	ModeReplyAll
	ModeForward
)

func (m Mode) String() string {
	switch m {
	case ModeReply:
		return "reply"
	case ModeReplyAll:
		return "reply-all"
	case ModeForward:
		return "forward"
	}
	return "compose"
}

// Phase is the dialogue state machine (spec section 5): editing ->
// (y) sending -> sent (tab closes) | failed (Output kept, e retries);
// q arms aborting, a second q confirms.
type Phase int

const (
	PhaseEditing Phase = iota
	PhaseAborting
	PhaseSending
	PhaseFailed
)

// Attachment is one composed attachment: the file path, its base name
// on the wire, its size.
type Attachment struct {
	Name, Path string
	Size       int64
}

// State is one dialogue (R4): fields, attachments, send progress,
// error output. The signature is stored SEPARATELY from the body
// (SignatureBody) - the body is the user's edited text, the signature
// is re-attached at buffer build and assembly. A parsed-back buffer
// whose tail no longer matches the attached signature detaches it
// (the tail is the user's text now), so Body never carries an
// attached block - the spec's exact-tail replace is structural.
type State struct {
	ID            string
	Mode          Mode
	Account       string
	From          string
	To, Cc        []string
	Subject       string
	Body          string
	Attachments   []Attachment
	Signature     string // signature name ("" = none)
	SignatureBody string
	MessageID     string // original message-id (In-Reply-To)
	References    []string
	OriginalID    string // original notmuch id (reply/forward tagging)
	Phase         Phase
	Output        string // send job captured output (failed)
}

// NewCompose opens a blank compose dialogue.
func NewCompose(account, from, sigName, sigBody string) *State {
	return &State{
		Mode: ModeCompose, Account: account, From: from,
		Signature: sigName, SignatureBody: sigBody,
	}
}

// SetSignature switches the signature (the fuzzy picker): the body is
// untouched - the previous block, if still attached, is stored
// separately, so replacing it is a field swap.
func (s *State) SetSignature(name, body string) {
	s.Signature, s.SignatureBody = name, body
}

// AddAttachment stats path and appends it (name = base, size =
// stat). Directories and missing paths error - the prompt stays open.
func (s *State) AddAttachment(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		return fmt.Errorf("%s: is a directory", path)
	}
	s.Attachments = append(s.Attachments, Attachment{Name: filepath.Base(path), Path: path, Size: fi.Size()})
	return nil
}
```

Create `src/compose/prefill.go`:

```go
package compose

import (
	"fmt"
	"strings"
	"time"

	"notmutt/core"
	"notmutt/mail"
)

// quoteDepth is the quoted-depth cap, shared with the renderer: a line
// at the cap stays there, never deeper.
const quoteDepth = 5

// Quote builds the mutt-style quoted reply body (spec section 6): the
// attribution line and the original body with one extra quote level
// per line (capped at quoteDepth). Lines already quoted keep their
// depth plus one; the bare text re-prefixes so levels stay canonical.
// The original's signature is never quoted.
func Quote(orig core.Message, parts []mail.Part) string {
	var b strings.Builder
	fmt.Fprintf(&b, "On %s, %s wrote:\n", time.Unix(orig.Timestamp, 0).Format("Mon, Jan 2 2006"), orig.Author)
	for _, p := range parts {
		if p.Signature {
			continue
		}
		for _, line := range strings.Split(p.Body, "\n") {
			b.WriteString(quoteLine(line))
			b.WriteByte('\n')
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// quoteLine strips the line's existing quote markers, then re-prefixes
// one level deeper (a line already at the cap keeps the cap).
func quoteLine(line string) string {
	depth := 0
	for depth < quoteDepth {
		rest := strings.TrimPrefix(line, ">")
		if rest == line {
			break
		}
		depth++
		line = strings.TrimPrefix(rest, " ")
	}
	return strings.Repeat(">", depth+1) + " " + line
}

// subjectPrefix strips repeated Re:/Fwd:/Fw: prefixes and returns the
// subject with one prefix of p (mutt's rule: "Re: " replies, "Fwd: "
// forwards). An empty subject after stripping gets the prefix alone.
func subjectPrefix(subject, p string) string {
	for {
		t := strings.TrimSpace(subject)
		l := strings.ToLower(t)
		stripped := false
		for _, pre := range []string{"re:", "fwd:", "fw:"} {
			if strings.HasPrefix(l, pre) {
				subject = t[len(pre):]
				stripped = true
				break
			}
		}
		if !stripped {
			break
		}
	}
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return p
	}
	return p + subject
}

// Reply prefills a reply (spec section 6): To from the original's
// From, the quoted original as the body, one "Re: " prefix, the reply
// headers (In-Reply-To = original message-id, References = the
// original chain).
func Reply(orig core.Message, parsed *mail.Message, account, from, sigName, sigBody string) *State {
	return &State{
		Mode: ModeReply, Account: account, From: from,
		To:      []string{parsed.From},
		Subject: subjectPrefix(parsed.Subject, "Re: "),
		Body:    Quote(orig, parsed.Parts),
		Signature: sigName, SignatureBody: sigBody,
		MessageID: parsed.MessageID, References: orig.References,
		OriginalID: orig.ID,
	}
}

// ReplyAll adds the original's To+Cc minus the account's own address
// (milestone 1 matches the exact bare address - normalization is
// future work) as the Cc. The original's From stays the To.
func ReplyAll(orig core.Message, parsed *mail.Message, account, from, own string, sigName, sigBody string) *State {
	s := Reply(orig, parsed, account, from, sigName, sigBody)
	s.Mode = ModeReplyAll
	for _, a := range parsed.To {
		if a != own {
			s.Cc = append(s.Cc, a)
		}
	}
	for _, a := range parsed.Cc {
		if a != own {
			s.Cc = append(s.Cc, a)
		}
	}
	return s
}

// Forward prefills a forward: no recipients, one "Fwd: " prefix, the
// quoted original as the body.
func Forward(orig core.Message, parsed *mail.Message, account, from, sigName, sigBody string) *State {
	return &State{
		Mode: ModeForward, Account: account, From: from,
		Subject: subjectPrefix(parsed.Subject, "Fwd: "),
		Body:    Quote(orig, parsed.Parts),
		Signature: sigName, SignatureBody: sigBody,
		MessageID: parsed.MessageID, References: orig.References,
		OriginalID: orig.ID,
	}
}
```

Note: the fixture's Timestamp is `time.Date(2026, 8, 14, ...).Unix()` and the format is "Mon, Jan 2 2006" - 2026-08-14 is a Friday, so the attribution line above is correct as written.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./compose/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add src/compose/state.go src/compose/prefill.go src/compose/prefill_test.go
git commit -m "feat(compose): dialogue state and reply/forward prefill"
```

---

### Task 5: Compose - editor buffer build and parse-back

**Files:**
- Create: `src/compose/buffer.go`
- Test: `src/compose/buffer_test.go` (new)

- [ ] **Step 1: Write the failing test**

Create `src/compose/buffer_test.go`:

```go
package compose

import (
	"strings"
	"testing"
)

func TestBuildBuffer(t *testing.T) {
	s := NewCompose("gmail", "Bob <bob@example.com>", "gmail", "bob\nbob2")
	s.To = []string{"a@example.com", "b@example.org"}
	s.Cc = []string{"c@example.net"}
	s.Subject = "hello"
	s.Body = "line one"
	want := "To: a@example.com, b@example.org\n" +
		"Cc: c@example.net\n" +
		"Subject: hello\n\n" +
		"line one\n\n" +
		"-- \nbob\nbob2"
	if got := s.BuildBuffer(); got != want {
		t.Fatalf("buffer:\n%q\nwant:\n%q", got, want)
	}
}

func TestParseBufferRoundTrip(t *testing.T) {
	s := NewCompose("gmail", "Bob <bob@example.com>", "gmail", "bob")
	s.To = []string{"a@example.com", "b@example.org"}
	s.Cc = []string{"c@example.net"}
	s.Subject = "Re: hello"
	s.Body = "> quoted\nsecond line"
	to, cc, subject, body, sigName, sigBody := ParseBuffer(s.BuildBuffer(), s.Signature, s.SignatureBody)
	if len(to) != 2 || to[0] != "a@example.com" || to[1] != "b@example.org" {
		t.Fatalf("to = %v", to)
	}
	if len(cc) != 1 || cc[0] != "c@example.net" {
		t.Fatalf("cc = %v", cc)
	}
	if subject != "Re: hello" || body != "> quoted\nsecond line" {
		t.Fatalf("subject/body = %q %q", subject, body)
	}
	if sigName != "gmail" || sigBody != "bob" {
		t.Fatalf("sig = %q %q", sigName, sigBody)
	}
}

func TestParseBufferEditedSignatureDetaches(t *testing.T) {
	buf := "To: a@example.com\nCc: \nSubject: x\n\nbody\n\n-- \nbob\nEDITED"
	to, _, subject, body, sigName, sigBody := ParseBuffer(buf, "gmail", "bob")
	if to[0] != "a@example.com" || subject != "x" {
		t.Fatalf("headers = %v %q", to, subject)
	}
	// the edited tail stays as the user's text; the signature detaches
	if body != "body\n\n-- \nbob\nEDITED" {
		t.Fatalf("body = %q", body)
	}
	if sigName != "" || sigBody != "" {
		t.Fatalf("edited tail must detach the signature: %q %q", sigName, sigBody)
	}
}

func TestParseBufferNoSeparator(t *testing.T) {
	// the spec contract: a buffer without the separator blank line
	// parses as all-headers, empty body
	to, _, subject, body, _, _ := ParseBuffer("To: a@example.com\nSubject: x", "", "")
	if len(to) != 1 || subject != "x" || body != "" {
		t.Fatalf("no-separator parse = %v %q %q", to, subject, body)
	}
}

func TestParseBufferDropsUnknownHeaders(t *testing.T) {
	to, _, _, body, _, _ := ParseBuffer("To: a@example.com\nX-Custom: keep\nSubject: x\n\nbody", "", "")
	if len(to) != 1 || body != "body" {
		t.Fatalf("unknown header dropped: %v %q", to, body)
	}
}

func TestParseBufferBlankFields(t *testing.T) {
	to, cc, _, _, _, _ := ParseBuffer("To: a@example.com, , b@example.org\nCc: \nSubject: \n\nbody", "", "")
	if len(to) != 2 || to[1] != "b@example.org" {
		t.Fatalf("to = %v", to)
	}
	if len(cc) != 0 {
		t.Fatalf("cc = %v", cc)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./compose/`
Expected: FAIL - undefined: ParseBuffer, BuildBuffer, SigBlock, BodyWithSig.

- [ ] **Step 3: Implement**

Create `src/compose/buffer.go`:

```go
package compose

import (
	"fmt"
	"strings"
)

// SigBlock is the signature block below the body: a blank line, the
// "-- " marker, the content. ONE definition - the editor buffer, the
// preview, and the assembled message all use it (DRY).
func SigBlock(content string) string {
	return "\n\n-- \n" + content
}

// BodyWithSig joins the body and the signature block: the body
// normalizes its trailing newlines, one blank line separates. Without
// a signature the body passes through untouched.
func BodyWithSig(body, sigBody string) string {
	if sigBody == "" {
		return body
	}
	return strings.TrimRight(body, "\n") + SigBlock(sigBody)
}

// BuildBuffer is the editor buffer contract (spec section 7): the
// header block, one blank separator line, the body, the signature
// block. No trailing newline (the file write may add one; the parse
// strips it).
func (s *State) BuildBuffer() string {
	var b strings.Builder
	fmt.Fprintf(&b, "To: %s\n", strings.Join(s.To, ", "))
	fmt.Fprintf(&b, "Cc: %s\n", strings.Join(s.Cc, ", "))
	fmt.Fprintf(&b, "Subject: %s\n\n", s.Subject)
	b.WriteString(BodyWithSig(s.Body, s.SignatureBody))
	return b.String()
}

// ParseBuffer parses the editor buffer back into the fields (spec
// section 7): headers up to the first blank line, the rest the body.
// Address lists split on commas; blank entries drop. Unknown header
// lines are dropped (the three fields own the block - pinned
// contract). The signature tail detaches by exact match with the
// previously attached block: a matched tail keeps the signature, an
// edited tail stays as user text and detaches it. A buffer without
// the separator parses as all-headers, empty body.
func ParseBuffer(buf, prevSigName, prevSigBody string) (to, cc []string, subject, body, sigName, sigBody string) {
	buf = strings.TrimSuffix(buf, "\n")
	head, rest := buf, ""
	if i := strings.Index(buf, "\n\n"); i >= 0 {
		head, rest = buf[:i], buf[i+2:]
	}
	parse := func(pref string) []string {
		var out []string
		for _, l := range strings.Split(head, "\n") {
			if v, ok := strings.CutPrefix(l, pref); ok {
				for _, a := range strings.Split(v, ",") {
					if a = strings.TrimSpace(a); a != "" {
						out = append(out, a)
					}
				}
			}
		}
		return out
	}
	to, cc = parse("To:"), parse("Cc:")
	for _, l := range strings.Split(head, "\n") {
		if v, ok := strings.CutPrefix(l, "Subject:"); ok {
			subject = strings.TrimSpace(v)
		}
	}
	body = rest
	if prevSigBody != "" {
		block := SigBlock(prevSigBody)
		if strings.HasSuffix(body, block) {
			body = strings.TrimSuffix(body, block)
			return to, cc, subject, body, prevSigName, prevSigBody
		}
	}
	return to, cc, subject, body, "", ""
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./compose/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add src/compose/buffer.go src/compose/buffer_test.go
git commit -m "feat(compose): editor buffer build and parse-back contract"
```

---

### Task 6: Compose - message assembly (go-message)

**Files:**
- Create: `src/compose/assemble.go`
- Test: `src/compose/assemble_test.go` (new)

- [ ] **Step 1: Write the failing test**

Create `src/compose/assemble_test.go`:

```go
package compose

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emersion/go-message"
	"github.com/emersion/go-message/mail"
)

func TestAssemble(t *testing.T) {
	att := filepath.Join(t.TempDir(), "doc.txt")
	if err := os.WriteFile(att, []byte("attachment bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	s := NewCompose("gmail", "Bob <bob@example.com>", "gmail", "sig line")
	s.To = []string{"Alice <alice@example.com>"}
	s.Cc = []string{"cc@example.net"}
	s.Subject = "hello"
	s.Body = "body line"
	s.MessageID = "<orig@example.com>"
	s.References = []string{"<a@x>"}
	s.OriginalID = "<orig@example.com>"
	if err := s.AddAttachment(att); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := s.Assemble(&buf); err != nil {
		t.Fatal(err)
	}

	mr, err := mail.CreateReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()
	hdr := mr.Header
	if from, _ := hdr.AddressList("From"); len(from) != 1 || from[0].Address != "bob@example.com" {
		t.Fatalf("From = %v", from)
	}
	if to, _ := hdr.AddressList("To"); len(to) != 1 || to[0].Address != "alice@example.com" {
		t.Fatalf("To = %v", to)
	}
	if cc, _ := hdr.AddressList("Cc"); len(cc) != 1 || cc[0].Address != "cc@example.net" {
		t.Fatalf("Cc = %v", cc)
	}
	if hdr.Get("Subject") != "hello" {
		t.Fatalf("Subject = %q", hdr.Get("Subject"))
	}
	if hdr.Get("Message-Id") == "" {
		t.Fatal("Message-Id must be generated")
	}
	if hdr.Get("In-Reply-To") != "<orig@example.com>" {
		t.Fatalf("In-Reply-To = %q", hdr.Get("In-Reply-To"))
	}
	if refs, _ := hdr.MsgIDList("References"); len(refs) != 2 || refs[0] != "<a@x>" || refs[1] != "<orig@example.com>" {
		t.Fatalf("References = %v", refs)
	}

	var inline, attached []byte
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(p.Body)
		if err != nil {
			t.Fatal(err)
		}
		switch p.Header.(type) {
		case *mail.InlineHeader:
			inline = data
		case *mail.AttachmentHeader:
			attached = data
		}
	}
	if !strings.Contains(string(inline), "body line\n\n-- \nsig line") {
		t.Fatalf("inline part = %q", inline)
	}
	if string(attached) != "attachment bytes" {
		t.Fatalf("attachment part = %q", attached)
	}
}

func TestAssembleBadAddressFails(t *testing.T) {
	s := NewCompose("gmail", "bob@example.com", "", "")
	s.To = []string{"not an address"}
	var buf bytes.Buffer
	if err := s.Assemble(&buf); err == nil {
		t.Fatal("a bad recipient address must fail assembly")
	}
}

func TestAssembleNoSignature(t *testing.T) {
	s := NewCompose("gmail", "bob@example.com", "", "")
	s.To = []string{"a@b.c"}
	s.Subject = "x"
	s.Body = "only body"
	var buf bytes.Buffer
	if err := s.Assemble(&buf); err != nil {
		t.Fatal(err)
	}
	mr, err := mail.CreateReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()
	p, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(p.Body)
	if string(data) != "only body" {
		t.Fatalf("body = %q", data)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./compose/`
Expected: FAIL - undefined: (State).Assemble.

- [ ] **Step 3: Implement**

Create `src/compose/assemble.go`:

```go
package compose

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/emersion/go-message/mail"
)

// Assemble writes the message bytes: headers (From/To/Cc/Subject/Date/
// Message-ID, In-Reply-To and References for replies), one text/plain
// body part (signature attached), one part per attachment. Pure bytes
// - the send job writes the same buffer to transport and fcc. The
// body is the user's own text; nothing here sanitizes (sanitize is
// render-only, F1).
func (s *State) Assemble(w io.Writer) error {
	hdr := mail.Header{}
	setAddrs := func(name string, addrs []string) error {
		var parsed []*mail.Address
		for _, a := range addrs {
			p, err := mail.ParseAddress(a)
			if err != nil {
				return fmt.Errorf("%s: %v", name, err)
			}
			parsed = append(parsed, p)
		}
		return hdr.SetAddressList(name, parsed)
	}
	if err := setAddrs("From", []string{s.From}); err != nil {
		return err
	}
	if err := setAddrs("To", s.To); err != nil {
		return err
	}
	if err := setAddrs("Cc", s.Cc); err != nil {
		return err
	}
	hdr.SetSubject(s.Subject)
	hdr.SetDate(time.Now())
	hdr.GenerateMessageID()
	if s.MessageID != "" {
		hdr.Set("In-Reply-To", s.MessageID)
		hdr.SetMsgIDList("References", append(append([]string(nil), s.References...), s.MessageID))
	}
	mw, err := mail.CreateWriter(w, hdr)
	if err != nil {
		return err
	}
	b, err := mw.CreateSingleInline(mail.InlineHeader{})
	if err != nil {
		return err
	}
	if _, err := io.WriteString(b, BodyWithSig(s.Body, s.SignatureBody)); err != nil {
		return err
	}
	if err := b.Close(); err != nil {
		return err
	}
	for _, a := range s.Attachments {
		f, err := os.Open(a.Path)
		if err != nil {
			return err
		}
		ah := mail.AttachmentHeader{}
		ah.SetFilename(a.Name)
		ab, err := mw.CreateAttachment(ah)
		if err != nil {
			f.Close()
			return err
		}
		if _, err := io.Copy(ab, f); err != nil {
			f.Close()
			return err
		}
		f.Close()
		if err := ab.Close(); err != nil {
			return err
		}
	}
	return mw.Close()
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./compose/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add src/compose/assemble.go src/compose/assemble_test.go
git commit -m "feat(compose): assemble the message with go-message"
```

---

### Task 7: Compose - bus mapping and path expansion

**Files:**
- Create: `src/compose/event.go`
- Test: `src/compose/event_test.go` (new)

- [ ] **Step 1: Write the failing test**

Create `src/compose/event_test.go`:

```go
package compose

import (
	"testing"

	"notmutt/core"
)

func TestEventRoundTrip(t *testing.T) {
	s := NewCompose("gmail", "Bob <bob@example.com>", "gmail", "sig")
	s.ID = "t1"
	s.Mode = ModeReplyAll
	s.To = []string{"a@b.c"}
	s.Cc = []string{"c@d.e"}
	s.Subject = "Re: x"
	s.Body = "quoted body"
	s.Attachments = []Attachment{{Name: "n.txt", Path: "/tmp/n.txt", Size: 3}}
	s.MessageID = "<m@x>"
	s.References = []string{"<r@x>"}
	s.OriginalID = "<m@x>"

	e := ToEvent(s)
	if e.TabID != "t1" || e.Mode != "reply-all" || e.Account != "gmail" {
		t.Fatalf("event = %+v", e)
	}
	if len(e.Attachments) != 1 || e.Attachments[0].Path != "/tmp/n.txt" {
		t.Fatalf("event attachments = %+v", e.Attachments)
	}

	got := FromEvent(e)
	if got.ID != "t1" || got.Mode != ModeReplyAll || got.Account != "gmail" {
		t.Fatalf("state = %+v", got)
	}
	if got.Body != "quoted body" || got.SignatureBody != "sig" {
		t.Fatalf("state body/sig = %q %q", got.Body, got.SignatureBody)
	}
	if len(got.Attachments) != 1 || got.Attachments[0].Name != "n.txt" {
		t.Fatalf("state attachments = %+v", got.Attachments)
	}
	if got.OriginalID != "<m@x>" || got.MessageID != "<m@x>" {
		t.Fatalf("state ids = %q %q", got.OriginalID, got.MessageID)
	}
	if len(got.References) != 1 || got.References[0] != "<r@x>" {
		t.Fatalf("state references = %v", got.References)
	}
}

func TestParseModeUnknown(t *testing.T) {
	if parseMode("bogus") != ModeCompose {
		t.Fatal("unknown mode must fall back to compose")
	}
}

func TestExpandHome(t *testing.T) {
	if ExpandHome("/abs/path") != "/abs/path" {
		t.Fatal("absolute paths pass through")
	}
	got := ExpandHome("~/Mail/sent")
	if got == "~/Mail/sent" || got[:1] != "/" {
		t.Fatalf("~ must expand to the home dir, got %q", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./compose/`
Expected: FAIL - undefined: ToEvent, FromEvent, parseMode, ExpandHome.

- [ ] **Step 3: Implement**

Create `src/compose/event.go`:

```go
package compose

import (
	"os"
	"strings"

	"notmutt/core"
)

// ToEvent/FromEvent map the dialogue across the bus: the app opens a
// dialogue (builds the State) and publishes core.ComposeOpened; the
// TUI rebuilds the State from the event. The bus contract is core
// types only (core cannot import compose) - compose owns the mapping.
func ToEvent(s *State) core.ComposeOpened {
	e := core.ComposeOpened{
		TabID: s.ID, Mode: s.Mode.String(), Account: s.Account, From: s.From,
		To: s.To, Cc: s.Cc, Subject: s.Subject, Body: s.Body,
		Signature: s.Signature, SigContent: s.SignatureBody,
		MessageID: s.MessageID, References: s.References, OriginalID: s.OriginalID,
	}
	for _, a := range s.Attachments {
		e.Attachments = append(e.Attachments, core.ComposeAttachment{Name: a.Name, Path: a.Path, Size: a.Size})
	}
	return e
}

func FromEvent(e core.ComposeOpened) *State {
	s := &State{
		ID: e.TabID, Mode: parseMode(e.Mode), Account: e.Account, From: e.From,
		To: e.To, Cc: e.Cc, Subject: e.Subject, Body: e.Body,
		Signature: e.Signature, SignatureBody: e.SigContent,
		MessageID: e.MessageID, References: e.References, OriginalID: e.OriginalID,
	}
	for _, a := range e.Attachments {
		s.Attachments = append(s.Attachments, Attachment{Name: a.Name, Path: a.Path, Size: a.Size})
	}
	return s
}

func parseMode(s string) Mode {
	switch s {
	case "reply":
		return ModeReply
	case "reply-all":
		return ModeReplyAll
	case "forward":
		return ModeForward
	}
	return ModeCompose
}

// ExpandHome expands a leading ~ to the user's home dir (the config
// path style; the client knows no mail root - the sent_folder is a
// full path, muttrc record shape).
func ExpandHome(p string) string {
	if !strings.HasPrefix(p, "~") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return home + p[1:]
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./compose/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add src/compose/event.go src/compose/event_test.go
git commit -m "feat(compose): bus mapping and home expansion"
```

---

### Task 8: Config surface - [send] table, account send fields, compose/fuzzy bindings

**Files:**
- Modify: `src/config/config.go`
- Test: `src/config/config_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `src/config/config_test.go`:

```go
func TestLoadSendDefaults(t *testing.T) {
	cfg := Default()
	if cfg.Send.Command != "msmtp" || !slices.Equal(cfg.Send.Args, []string{"--read-envelope-from"}) {
		t.Fatalf("default send = %+v", cfg.Send)
	}
}

func TestSendOverrides(t *testing.T) {
	cfg, err := Load(write(t, `
[send]
command = "stub-send"
args = ["-v"]
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Send.Command != "stub-send" || !slices.Equal(cfg.Send.Args, []string{"-v"}) {
		t.Fatalf("send overrides = %+v", cfg.Send)
	}
}

func TestValidateSendCommand(t *testing.T) {
	_, err := Load(write(t, "\n[send]\ncommand = \"\"\n"))
	if err == nil || !strings.Contains(err.Error(), "send.command") {
		t.Fatalf("want send.command error, got %v", err)
	}
}

func TestAccountSendFields(t *testing.T) {
	cfg, err := Load(write(t, `
[accounts.gmail]
from = "Reza <reza@example.com>"
sent_folder = "/home/me/Mail/gmail/Sent"
default_signature = "gmail"
`))
	if err != nil {
		t.Fatal(err)
	}
	a := cfg.Accounts["gmail"]
	if a.From != "Reza <reza@example.com>" || a.SentFolder != "/home/me/Mail/gmail/Sent" || a.DefaultSignature != "gmail" {
		t.Fatalf("account send fields = %+v", a)
	}
}
```

Update `TestDefaultBindings` in the same file. Replace the two `want` maps and add the new contexts:

```go
	want := map[string]string{
		"j": "cursor-down", "k": "cursor-up", "o": "open", "q": "quit",
		"r": "toggle-read", "a": "archive", "d": "delete",
		"u": "undo", "$": "apply",
		"pgdown": "page-down", "pgup": "page-up",
		"m": "compose", "R": "reply", "F": "forward",
		"[": "tab-prev", "]": "tab-next",
	}
	if !maps.Equal(cfg.Bindings["index"], want) {
		t.Fatalf("default index bindings = %v, want %v", cfg.Bindings["index"], want)
	}
	wantPager := map[string]string{
		"j": "scroll-down", "k": "scroll-up",
		"ctrl+d": "half-page-down", "ctrl+u": "half-page-up",
		"pgdown": "page-down", "pgup": "page-up",
		"g": "scroll-top", "G": "scroll-bottom",
		"q": "back",
		"[": "tab-prev", "]": "tab-next",
	}
	if !maps.Equal(cfg.Bindings["pager"], wantPager) {
		t.Fatalf("default pager bindings = %v, want %v", cfg.Bindings["pager"], wantPager)
	}
	wantCompose := map[string]string{
		"j": "form-down", "k": "form-up",
		"e": "edit", "a": "attach", "d": "detach",
		"c": "account", "C": "signature", "y": "send", "q": "abort",
		"[": "tab-prev", "]": "tab-next",
	}
	if !maps.Equal(cfg.Bindings["compose"], wantCompose) {
		t.Fatalf("default compose bindings = %v, want %v", cfg.Bindings["compose"], wantCompose)
	}
	wantFuzzy := map[string]string{
		"j": "fuzzy-down", "k": "fuzzy-up",
		"ctrl+n": "fuzzy-down", "ctrl+p": "fuzzy-up",
		"enter": "fuzzy-select", "esc": "fuzzy-cancel",
	}
	if !maps.Equal(cfg.Bindings["fuzzy"], wantFuzzy) {
		t.Fatalf("default fuzzy bindings = %v, want %v", cfg.Bindings["fuzzy"], wantFuzzy)
	}
```

Add to `TestKeymapSchemes` (after the existing emacs index assertions):

```go
	if cfg.Bindings["compose"]["ctrl+n"] != "form-down" {
		t.Fatalf("emacs compose movement missing: %v", cfg.Bindings["compose"])
	}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./config/` (from `src/`)
Expected: FAIL - `Send` field does not exist on Config, `Bindings["compose"]` is empty in the default maps, the new account fields fail to load.

- [ ] **Step 3: Implement**

In `src/config/config.go`:

Add the `Send` type next to `Account` (config.go:450):

```go
// Send is the send transport argv (R4): ONE configurable command,
// tokenized at load, exec'd as argv (F4). The default reads the
// envelope sender from the message's own From header, so msmtp's
// account table resolves per message - the client never sees it.
type Send struct {
	Command string   `toml:"command"`
	Args    []string `toml:"args"`
}
```

Extend `Account` (config.go:462-464):

```go
type Account struct {
	Folder           *string `toml:"folder"`
	From             string  `toml:"from"`
	SentFolder       string  `toml:"sent_folder"`
	DefaultSignature string  `toml:"default_signature"`
}
```

Add `Send Send` to the `Config` struct (config.go:15-24, after `Accounts`):

```go
	Accounts   map[string]Account           `toml:"accounts"`
	Send       Send                         `toml:"send"`
```

Add to `Default()` (config.go:567-609, after `Accounts`):

```go
		Send: Send{
			Command: "msmtp",
			Args:    []string{"--read-envelope-from"},
		},
```

Extend `vimScheme` (config.go:487-501). Add to the `index` table:

```go
		"index": {
			"j": "cursor-down", "k": "cursor-up", "o": "open", "q": "quit",
			"r": "toggle-read", "a": "archive", "d": "delete",
			"u": "undo", "$": "apply",
			"pgdown": "page-down", "pgup": "page-up",
			"m": "compose", "R": "reply", "F": "forward",
			"[": "tab-prev", "]": "tab-next",
		},
```

Add `"[": "tab-prev", "]": "tab-next"` to the vim `pager` table, and add the two new context tables after it:

```go
	"pager": {
		"j": "scroll-down", "k": "scroll-up",
		"ctrl+d": "half-page-down", "ctrl+u": "half-page-up",
		"pgdown": "page-down", "pgup": "page-up",
		"g": "scroll-top", "G": "scroll-bottom",
		"q": "back",
		"[": "tab-prev", "]": "tab-next",
	},
	"compose": {
		"j": "form-down", "k": "form-up",
		"e": "edit", "a": "attach", "d": "detach",
		"c": "account", "C": "signature", "y": "send", "q": "abort",
		"[": "tab-prev", "]": "tab-next",
	},
	"fuzzy": {
		"j": "fuzzy-down", "k": "fuzzy-up",
		"ctrl+n": "fuzzy-down", "ctrl+p": "fuzzy-up",
		"enter": "fuzzy-select", "esc": "fuzzy-cancel",
	},
```

Extend `emacsScheme` (config.go:505-518) the same way: add `"m": "compose", "R": "reply", "F": "forward", "[": "tab-prev", "]": "tab-next"` to `index`; `"[": "tab-prev", "]": "tab-next"` to `pager`; add:

```go
	"compose": {
		"ctrl+n": "form-down", "ctrl+p": "form-up",
		"e": "edit", "a": "attach", "d": "detach",
		"c": "account", "C": "signature", "y": "send", "q": "abort",
		"[": "tab-prev", "]": "tab-next",
	},
	"fuzzy": {
		"j": "fuzzy-down", "k": "fuzzy-up",
		"ctrl+n": "fuzzy-down", "ctrl+p": "fuzzy-up",
		"enter": "fuzzy-select", "esc": "fuzzy-cancel",
	},
```

Add to `validate` (config.go:725-809, in the accounts loop area):

```go
	if strings.TrimSpace(cfg.Send.Command) == "" {
		return fmt.Errorf("send.command: must not be empty")
	}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./config/`
Expected: PASS (all config tests including the updated TestDefaultBindings/TestKeymapSchemes).

- [ ] **Step 5: Commit**

```bash
git add src/config/config.go src/config/config_test.go
git commit -m "feat(config): send transport argv, account send fields, compose bindings"
```

---

### Task 9: App - reply prefill (account detection, parsing, default signature)

**Files:**
- Create: `src/app/compose.go`
- Test: `src/app/compose_test.go` (new)

- [ ] **Step 1: Write the failing test**

Create `src/app/compose_test.go`:

```go
package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"notmutt/config"
	"notmutt/core"
	"notmutt/mail"
)

func TestResolveAccountChain(t *testing.T) {
	cfg := config.Default()
	if got := resolveAccount(cfg, []string{"inbox", "gmail", "work"}, nil); got != "gmail" {
		t.Fatalf("message tag first: %q", got)
	}
	if got := resolveAccount(cfg, []string{"inbox"}, []string{"dynamia"}); got != "dynamia" {
		t.Fatalf("cursor fallback: %q", got)
	}
	if got := resolveAccount(cfg, nil, nil); got != "dynamia" {
		// default accounts are gmail, jelveh, toptal, dynamia - sorted,
		// first is dynamia
		t.Fatalf("first account fallback: %q", got)
	}
}

func TestDefaultSig(t *testing.T) {
	cfg := config.Default()
	cfg.Accounts["gmail"].DefaultSignature = "personal"
	cfg.Accounts["dynamia"] = config.Account{}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "gmail"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gmail", "personal"), []byte("sig text\n"), 0600); err != nil {
		t.Fatal(err)
	}
	old := sigDir
	sigDir = dir
	defer func() { sigDir = old }()

	name, body := defaultSig(cfg, "gmail")
	if name != "personal" || body != "sig text" {
		t.Fatalf("default sig = %q %q", name, body)
	}
	if name, _ := defaultSig(cfg, "dynamia"); name != "" {
		t.Fatalf("account without default must resolve empty, got %q", name)
	}
}

func TestBuildComposeReply(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "msg.eml")
	eml := "From: Alice <alice@example.com>\n" +
		"To: Bob <bob@example.com>\n" +
		"Subject: hello\n" +
		"Message-Id: <m1@example.com>\n" +
		"Date: Tue, 14 Aug 2026 10:00:00 +0000\n\n" +
		"body line\n"
	if err := os.WriteFile(path, []byte(eml), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Accounts["gmail"].From = "Bob <bob@example.com>"
	cfg.Accounts["gmail"].SentFolder = "/tmp/sent"
	cfg.Accounts["gmail"].DefaultSignature = ""
	view := core.NewView("inbox", "tag:inbox")
	msg := &core.Message{
		ID: "<m1@example.com>", ThreadID: "t1",
		Timestamp: time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC).Unix(),
		Author:    "Alice <alice@example.com>", Subject: "hello",
		Tags:   []string{"inbox", "gmail"},
		Paths:  []string{path},
	}

	st := buildCompose(cfg, view, msg, "reply")
	if st == nil {
		t.Fatal("reply must build")
	}
	if st.Account != "gmail" || st.From != "Bob <bob@example.com>" {
		t.Fatalf("account/from = %q %q", st.Account, st.From)
	}
	if len(st.To) != 1 || st.To[0] != "alice@example.com" {
		t.Fatalf("To = %v", st.To)
	}
	if st.MessageID != "<m1@example.com>" || st.OriginalID != "<m1@example.com>" {
		t.Fatalf("ids = %q %q", st.MessageID, st.OriginalID)
	}

	if st := buildCompose(cfg, view, msg, "reply-all"); st.Mode != 2 {
		t.Fatalf("reply-all mode = %v", st.Mode)
	}
	if st := buildCompose(cfg, view, msg, "forward"); len(st.To) != 0 {
		t.Fatalf("forward must have no To: %+v", st)
	}
	if st := buildCompose(cfg, view, nil, "compose"); st == nil || st.Mode != 0 {
		t.Fatalf("blank compose must build: %+v", st)
	}
	if st := buildCompose(cfg, view, nil, "reply"); st != nil {
		t.Fatal("reply without a message must return nil")
	}
}
```

Notes for the implementer: `core.NewView` and `view.CursorRow` are used by `cursorTags`; with no cursor set, `CursorRow()` returns `(Row, false)` and the function returns nil - fine. The `Mode` constants: `ModeCompose` = 0, `ModeReply` = 1, `ModeReplyAll` = 2, `ModeForward` = 3 (do not reference the constants by number in the test if you prefer readability - `compose.ModeReplyAll` needs a `"notmutt/compose"` import; either is fine, keep the numbers only if you match the const order in Task 4).

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./app/`
Expected: FAIL - undefined: resolveAccount, defaultSig, buildCompose, sigDir.

- [ ] **Step 3: Implement**

Create `src/app/compose.go`:

```go
package app

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"notmutt/compose"
	"notmutt/config"
	"notmutt/core"
	"notmutt/mail"
)

// sigDir is the signatures root (spec section 9): the app resolves it
// from the config path and hands it to the tui; the tests set it
// directly.
var sigDir string

// resolveAccount is the detection chain (spec section 6): the
// message's account tag, the view cursor's account tag, the first
// configured account (sorted - deterministic). The same account-tag
// machinery as the status bar (core.AccountTag, DRY).
func resolveAccount(cfg config.Config, msgTags, cursorTags []string) string {
	set := cfg.AccountTags()
	if t := core.AccountTag(msgTags, set); t != "" {
		return t
	}
	if t := core.AccountTag(cursorTags, set); t != "" {
		return t
	}
	names := make([]string, 0, len(cfg.Accounts))
	for n := range cfg.Accounts {
		names = append(names, n)
	}
	sort.Strings(names)
	if len(names) > 0 {
		return names[0]
	}
	return ""
}

// defaultSig loads the account's default signature file (the
// configured name in the account's signatures dir); a missing file or
// an unset name resolves to no signature.
func defaultSig(cfg config.Config, account string) (name, body string) {
	file := cfg.Accounts[account].DefaultSignature
	if file == "" {
		return "", ""
	}
	data, err := os.ReadFile(filepath.Join(sigDir, account, file))
	if err != nil {
		return "", ""
	}
	return file, strings.TrimSuffix(string(data), "\n")
}

// buildCompose prefills a dialogue for mode ("compose" | "reply" |
// "reply-all" | "forward"): account detection, the parsed original
// (reply/forward), the default signature. Nil when the original
// cannot be parsed - the open key then no-ops.
func buildCompose(cfg config.Config, view *core.View, msg *core.Message, mode string) *compose.State {
	account := resolveAccount(cfg, tagsOf(msg), cursorTags(view))
	from := cfg.Accounts[account].From
	sigName, sigBody := defaultSig(cfg, account)
	if mode == "compose" {
		return compose.NewCompose(account, from, sigName, sigBody)
	}
	if msg == nil || len(msg.Paths) == 0 {
		return nil
	}
	parsed, err := mail.ParseMessage(msg.Paths[0])
	if err != nil {
		return nil
	}
	switch mode {
	case "reply":
		return compose.Reply(*msg, parsed, account, from, sigName, sigBody)
	case "reply-all":
		own := ""
		if p, err := mail.ParseAddress(from); err == nil {
			own = p.Address
		}
		return compose.ReplyAll(*msg, parsed, account, from, own, sigName, sigBody)
	case "forward":
		return compose.Forward(*msg, parsed, account, from, sigName, sigBody)
	}
	return nil
}

func tagsOf(msg *core.Message) []string {
	if msg == nil {
		return nil
	}
	return msg.Tags
}

// cursorTags resolves the view cursor message's tags - the view's
// active account context (spec section 6). One flatten per dialogue
// open, not per keystroke.
func cursorTags(view *core.View) []string {
	row, ok := view.CursorRow()
	if !ok || row.Msg == nil {
		return nil
	}
	return row.Msg.Tags
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./app/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add src/app/compose.go src/app/compose_test.go
git commit -m "feat(app): reply prefill with account detection"
```

---

### Task 10: App - the send job (transport, fcc, reindex, reply tag)

**Files:**
- Create: `src/app/send.go`
- Test: `src/app/send_test.go` (new)

- [ ] **Step 1: Write the failing test**

Create `src/app/send_test.go`:

```go
package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"notmutt/compose"
	"notmutt/config"
	"notmutt/core"
	"notmutt/notmuch"
)

// stubWorker records the actions a send job issues (ActNew, ActTag).
type stubWorker struct {
	actions []notmuch.Action
}

func (w *stubWorker) Call(a notmuch.Action) (notmuch.Reply, error) {
	w.actions = append(w.actions, a)
	return notmuch.Reply{}, nil
}

func TestSendJobDelivers(t *testing.T) {
	dir := t.TempDir()
	captured := filepath.Join(dir, "captured")
	stub := "#!/bin/sh\ncat > " + captured + "\n"
	if err := os.WriteFile(filepath.Join(dir, "send-stub"), []byte(stub), 0755); err != nil {
		t.Fatal(err)
	}
	sent := filepath.Join(dir, "sent")
	cfg := config.Default()
	cfg.Send = config.Send{Command: filepath.Join(dir, "send-stub")}
	cfg.Accounts["gmail"] = config.Account{SentFolder: sent}
	cfg.Accounts["jelveh"] = config.Account{}

	bus := core.NewBus()
	ch := bus.Subscribe()
	view := core.NewView("inbox", "tag:inbox")
	w := &stubWorker{}

	st := compose.NewCompose("gmail", "Bob <bob@example.com>", "", "")
	st.ID = "tab1"
	st.To = []string{"alice@example.com"}
	st.Subject = "hello"
	st.Body = "the message body"

	sendJob(bus, w, view, cfg, *st)

	e := (<-ch).(core.SendResult)
	if !e.OK {
		t.Fatalf("send failed: %v %q", e.Err, e.Output)
	}
	data, err := os.ReadFile(captured)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Subject: hello") || !strings.Contains(string(data), "the message body") {
		t.Fatalf("transport must receive the assembled message:\n%s", data)
	}
	// fcc: one file in sent/new, 0600, the same bytes
	entries, err := os.ReadDir(filepath.Join(sent, "new"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("fcc files: %v %v", entries, err)
	}
	fi, err := entries[0].Info()
	if err != nil || fi.Mode().Perm() != 0600 {
		t.Fatalf("fcc perms: %v %v", fi.Mode(), err)
	}
	fcc, err := os.ReadFile(filepath.Join(sent, "new", entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if string(fcc) != string(data) {
		t.Fatal("fcc must be the exact assembled bytes")
	}
	// ActNew ran, and no reply tag without an original
	if len(w.actions) != 1 || w.actions[0].Kind != notmuch.ActNew {
		t.Fatalf("actions = %+v", w.actions)
	}
}

func TestSendJobTagsOriginalOnReply(t *testing.T) {
	dir := t.TempDir()
	stub := "#!/bin/sh\ncat > " + filepath.Join(dir, "captured") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "send-stub"), []byte(stub), 0755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Send = config.Send{Command: filepath.Join(dir, "send-stub")}
	cfg.Accounts["gmail"] = config.Account{SentFolder: filepath.Join(dir, "sent")}

	bus := core.NewBus()
	view := core.NewView("inbox", "tag:inbox")
	w := &stubWorker{}

	st := compose.NewCompose("gmail", "bob@example.com", "", "")
	st.ID = "tab2"
	st.To = []string{"a@b.c"}
	st.Subject = "x"
	st.Body = "y"
	st.Mode = compose.ModeReply
	st.OriginalID = "<orig@example.com>"

	sendJob(bus, w, view, cfg, *st)
	// the SendResult on the bus goes unread (drop-on-full is fine for
	// one message); the assertions run on the recorded actions
	if len(w.actions) != 2 {
		t.Fatalf("actions = %+v", w.actions)
	}
	if w.actions[1].Kind != notmuch.ActTag {
		t.Fatalf("second action must be the reply tag: %+v", w.actions[1])
	}
	if w.actions[1].Query != "id:\"<orig@example.com>\"" {
		t.Fatalf("tag query = %q", w.actions[1].Query)
	}
	if len(w.actions[1].TagOps) != 1 || w.actions[1].TagOps[0].Tag != "replied" || !w.actions[1].TagOps[0].Add {
		t.Fatalf("tag ops = %+v", w.actions[1].TagOps)
	}
}

func TestSendJobFailureKeepsDialogue(t *testing.T) {
	dir := t.TempDir()
	stub := "#!/bin/sh\necho 'msmtp exploded' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "send-stub"), []byte(stub), 0755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Send = config.Send{Command: filepath.Join(dir, "send-stub")}
	cfg.Accounts["gmail"] = config.Account{SentFolder: filepath.Join(dir, "sent")}

	bus := core.NewBus()
	ch := bus.Subscribe()
	view := core.NewView("inbox", "tag:inbox")
	w := &stubWorker{}

	st := compose.NewCompose("gmail", "bob@example.com", "", "")
	st.ID = "tab3"
	st.To = []string{"a@b.c"}
	st.Subject = "x"
	st.Body = "y"

	sendJob(bus, w, view, cfg, *st)

	e := (<-ch).(core.SendResult)
	if e.OK {
		t.Fatal("a failed transport must not report OK")
	}
	if !strings.Contains(e.Output, "msmtp exploded") {
		t.Fatalf("the captured output must be kept: %q", e.Output)
	}
	if len(w.actions) != 0 {
		t.Fatalf("a failed send must not fcc or tag: %+v", w.actions)
	}
	if _, err := os.Stat(filepath.Join(dir, "sent")); !os.IsNotExist(err) {
		t.Fatal("a failed send must not create the sent dir")
	}
}

func TestSendJobFccErrorNotesButDelivers(t *testing.T) {
	dir := t.TempDir()
	stub := "#!/bin/sh\ncat > /dev/null\n"
	if err := os.WriteFile(filepath.Join(dir, "send-stub"), []byte(stub), 0755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Send = config.Send{Command: filepath.Join(dir, "send-stub")}
	// an unwritable sent path (the dir does not exist and its parent
	// path is a FILE)
	if err := os.WriteFile(filepath.Join(dir, "blocker"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg.Accounts["gmail"] = config.Account{SentFolder: filepath.Join(dir, "blocker", "sent")}

	bus := core.NewBus()
	ch := bus.Subscribe()
	view := core.NewView("inbox", "tag:inbox")
	w := &stubWorker{}

	st := compose.NewCompose("gmail", "bob@example.com", "", "")
	st.ID = "tab4"
	st.To = []string{"a@b.c"}
	st.Subject = "x"
	st.Body = "y"

	sendJob(bus, w, view, cfg, *st)

	e := (<-ch).(core.SendResult)
	if !e.OK {
		t.Fatalf("a delivered message must report OK even with a fcc error: %v", e.Err)
	}
	if !strings.Contains(e.Output, "fcc") {
		t.Fatalf("the fcc note must surface: %q", e.Output)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./app/ -run TestSendJob`
Expected: FAIL - undefined: sendJob, writeFcc.

- [ ] **Step 3: Implement**

Create `src/app/send.go`:

```go
package app

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"notmutt/compose"
	"notmutt/core"
	"notmutt/notmuch"
)

// sendJob runs the send (spec section 8): assemble once, transport
// argv exec with the message on stdin and output captured (F4 - no
// shell, no interpolation), then fcc + reindex + reply tag. Order is
// transport first: what was not delivered is not stored. A delivered
// message never fails the dialogue on a fcc error (a retry would
// double-send) - the note surfaces in the SendResult output. A
// missing sent_folder skips fcc silently.
func sendJob(bus *core.Bus, worker workerAPI, view *core.View, cfg config.Config, st compose.State) {
	var buf bytes.Buffer
	if err := st.Assemble(&buf); err != nil {
		bus.Publish(core.SendResult{TabID: st.ID, OK: false, Err: err})
		return
	}
	cmd := exec.Command(cfg.Send.Command, cfg.Send.Args...)
	cmd.Stdin = &buf
	out, err := cmd.CombinedOutput()
	if err != nil {
		bus.Publish(core.SendResult{TabID: st.ID, OK: false, Output: string(out), Err: err})
		return
	}
	var note string
	if sent := cfg.Accounts[st.Account].SentFolder; sent != "" {
		if err := writeFcc(compose.ExpandHome(sent), buf.Bytes()); err != nil {
			note = "fcc failed: " + err.Error()
		}
	}
	// the sent copy is in the maildir now: index it so the folder rule
	// tags it sent (the R2 filter engine is its own milestone - the
	// copy is physically in the sent folder regardless)
	worker.Call(notmuch.Action{Kind: notmuch.ActNew})
	if st.OriginalID != "" {
		tag := "replied"
		if st.Mode == compose.ModeForward {
			tag = "forwarded"
		}
		worker.Call(notmuch.Action{
			Kind:   notmuch.ActTag,
			Query:  "id:\"" + strings.ReplaceAll(st.OriginalID, `"`, `""`) + "\"",
			TagOps: []notmuch.TagOp{{Tag: tag, Add: true}},
		})
	}
	bus.Publish(core.SendResult{TabID: st.ID, OK: true, Output: note})
	bus.Publish(core.ViewDiff{View: view.Name})
}

// writeFcc lands the sent copy in the maildir new/ slot (maildir
// convention: delivery lands in new, the sync tool flags into cur).
// Unique name, 0600 (F5).
func writeFcc(dir string, data []byte) error {
	sub := filepath.Join(dir, "new")
	if err := os.MkdirAll(sub, 0700); err != nil {
		return err
	}
	name := filepath.Join(sub, fmt.Sprintf("%d.%d.notmutt", time.Now().UnixNano(), os.Getpid()))
	return os.WriteFile(name, data, 0600)
}
```

Note: `sendJob` references `config.Config` - add the `"notmutt/config"` import. The `notmuch.TagOp` type is `notmuch.TagOp{Tag string, Add bool}` (aliased from core in the notmuch package).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./app/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add src/app/send.go src/app/send_test.go
git commit -m "feat(app): send job with fcc, reindex, and reply tagging"
```

---

### Task 11: Tui - the fuzzy selector (matcher + popup state)

**Files:**
- Create: `src/tui/fuzzy.go`
- Test: `src/tui/fuzzy_test.go` (new)

- [ ] **Step 1: Write the failing test**

Create `src/tui/fuzzy_test.go`:

```go
package tui

import "testing"

func TestFuzzyMatch(t *testing.T) {
	if pos, ok := fuzzyMatch("gm", "gmail"); !ok || pos != 0 {
		t.Fatalf("gmail/gm = %d %v", pos, ok)
	}
	if pos, ok := fuzzyMatch("gmail", "gmail/me"); !ok || pos != 0 {
		t.Fatalf("gmail/me/gmail = %d %v", pos, ok)
	}
	if pos, ok := fuzzyMatch("gm", "dynamia"); ok {
		t.Fatalf("dynamia/gm must not match, pos = %d", pos)
	}
	if _, ok := fuzzyMatch("", "anything"); !ok {
		t.Fatal("empty query matches everything")
	}
}

func TestFuzzyFilteredRanking(t *testing.T) {
	f := newFuzzy("account", []string{"gmail", "jelveh", "gmail-work"})
	f.query = "gmail"
	got := f.filtered()
	// first-match position ranks: "gmail" (pos 0) before "gmail-work"
	// (pos 0 too - tie breaks by entry order)
	if len(got) != 2 || got[0] != 0 || got[1] != 2 {
		t.Fatalf("filtered = %v", got)
	}
	f.query = "wl"
	if got := f.filtered(); len(got) != 1 || f.entries[got[0]] != "gmail-work" {
		t.Fatalf("wl filtered = %v", got)
	}
}

func TestFuzzyMoveAndSelect(t *testing.T) {
	f := newFuzzy("signature", []string{"a", "b"})
	f.move(1)
	if f.sel != 1 {
		t.Fatalf("sel = %d", f.sel)
	}
	f.move(1)
	if f.sel != 1 {
		t.Fatalf("sel must clamp: %d", f.sel)
	}
	f.move(-3)
	if f.sel != 0 {
		t.Fatalf("sel must clamp at 0: %d", f.sel)
	}
	if entry, ok := f.selected(); !ok || entry != "a" {
		t.Fatalf("selected = %q %v", entry, ok)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./tui/ -run TestFuzzy`
Expected: FAIL - undefined: fuzzyMatch, newFuzzy, fuzzy.

- [ ] **Step 3: Implement**

Create `src/tui/fuzzy.go`:

```go
package tui

import (
	"sort"
	"strings"
)

// fuzzyMatch reports whether s contains query as a case-insensitive
// subsequence, plus the first match position (earlier = better rank).
func fuzzyMatch(query, s string) (int, bool) {
	if query == "" {
		return 0, true
	}
	q, str := strings.ToLower(query), strings.ToLower(s)
	qi, si, start := 0, 0, -1
	for si < len(str) && qi < len(q) {
		if str[si] == q[qi] {
			if qi == 0 {
				start = si
			}
			qi++
		}
		si++
	}
	if qi != len(q) {
		return 0, false
	}
	return start, true
}

// fuzzy is the selector dialogue (R4): entries, the filter query, the
// selection. In-process matcher - no fzf subprocess, no new exec
// surface. kind is the picker's identity ("account" | "signature").
type fuzzy struct {
	kind    string
	title   string
	entries []string
	query   string
	sel     int
}

func newFuzzy(kind, title string, entries []string) *fuzzy {
	sort.Strings(entries)
	return &fuzzy{kind: kind, title: title, entries: entries}
}

// filtered returns the matching entry indices, ranked by first match
// position then entry order.
func (f *fuzzy) filtered() []int {
	var out []int
	for i, e := range f.entries {
		if _, ok := fuzzyMatch(f.query, e); ok {
			out = append(out, i)
		}
	}
	sort.SliceStable(out, func(a, b int) bool {
		pa, _ := fuzzyMatch(f.query, f.entries[out[a]])
		pb, _ := fuzzyMatch(f.query, f.entries[out[b]])
		return pa < pb
	})
	return out
}

func (f *fuzzy) move(n int) {
	f.sel += n
	if f.sel < 0 {
		f.sel = 0
	}
	if max := len(f.filtered()) - 1; f.sel > max {
		f.sel = max
	}
}

func (f *fuzzy) selected() (string, bool) {
	idx := f.filtered()
	if len(idx) == 0 || f.sel >= len(idx) {
		return "", false
	}
	return f.entries[idx[f.sel]], true
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./tui/ -run TestFuzzy`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add src/tui/fuzzy.go src/tui/fuzzy_test.go
git commit -m "feat(tui): in-process fuzzy selector"
```

---

### Task 12: Tui - the editor flow (buffer file, $EDITOR argv, parse-back)

**Files:**
- Create: `src/tui/editor.go`
- Test: `src/tui/editor_test.go` (new)

- [ ] **Step 1: Write the failing test**

Create `src/tui/editor_test.go`:

```go
package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"notmutt/compose"
)

func TestEditorBufferRoundTrip(t *testing.T) {
	s := compose.NewCompose("gmail", "Bob <bob@example.com>", "gmail", "bob")
	s.To = []string{"a@b.c"}
	s.Subject = "hello"
	s.Body = "body text"

	path, err := writeEditorBuffer(*s)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0600 {
		t.Fatalf("buffer perms = %v, want 0600 (F5)", fi.Mode().Perm())
	}

	got, err := applyEditorResult(*s, path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Subject != "hello" || got.Body != "body text" {
		t.Fatalf("round trip: %q %q", got.Subject, got.Body)
	}
	if got.SignatureBody != "bob" {
		t.Fatalf("signature must survive the round trip: %q", got.SignatureBody)
	}
}

func TestApplyEditorResultParsesEdits(t *testing.T) {
	s := compose.NewCompose("gmail", "Bob <bob@example.com>", "gmail", "bob")
	s.To = []string{"a@b.c"}
	s.Subject = "old"
	s.Body = "old body"

	path := filepath.Join(t.TempDir(), "buf")
	content := "To: x@y.z\nCc: \nSubject: new subject\n\nnew body\n\n-- \nnew sig"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	got, err := applyEditorResult(s, path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Subject != "new subject" || got.Body != "new body" {
		t.Fatalf("edits = %q %q", got.Subject, got.Body)
	}
	if len(got.To) != 1 || got.To[0] != "x@y.z" {
		t.Fatalf("to = %v", got.To)
	}
	// the edited signature tail no longer matches "bob": it stays as
	// body text and the signature detaches
	if !strings.Contains(got.Body, "new sig") || got.SignatureBody != "" {
		t.Fatalf("edited tail must stay as text, signature detach: %q %q", got.Body, got.SignatureBody)
	}
}

func TestEditorCmd(t *testing.T) {
	t.Setenv("EDITOR", "emacs -nw")
	cmd := editorCmd("/tmp/buf")
	if cmd.Path == "" || cmd.Args[len(cmd.Args)-1] != "/tmp/buf" {
		t.Fatalf("cmd = %+v", cmd)
	}
	t.Setenv("EDITOR", "")
	if cmd := editorCmd("/tmp/buf"); cmd.Args[0] != "vi" {
		t.Fatalf("fallback editor = %+v", cmd)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./tui/ -run TestEditor`
Expected: FAIL - undefined: writeEditorBuffer, applyEditorResult, editorCmd.

- [ ] **Step 3: Implement**

Create `src/tui/editor.go`:

```go
package tui

import (
	"os"
	"os/exec"
	"strings"

	"notmutt/compose"
)

// writeEditorBuffer writes the editor buffer to a 0600 temp file (F5)
// and returns its path. The write is the buffer contract's local half
// - the dialogue state itself never leaves the model.
func writeEditorBuffer(st compose.State) (string, error) {
	f, err := os.CreateTemp("", "notmutt-compose-*")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString(st.BuildBuffer()); err != nil {
		return "", err
	}
	return f.Name(), nil
}

// editorCmd builds the $EDITOR run (fallback vi): whitespace-tokenized
// argv, the buffer path appended (F4 - no shell, no interpolation).
// The EDITOR value is trusted config, not mail content.
func editorCmd(path string) *exec.Cmd {
	e := os.Getenv("EDITOR")
	if strings.TrimSpace(e) == "" {
		e = "vi"
	}
	parts := strings.Fields(e)
	return exec.Command(parts[0], append(parts[1:], path)...)
}

// applyEditorResult reads the edited buffer and applies it back to
// the state: headers parsed, the signature tail detached per its rule
// (an edited tail stays as the user's text).
func applyEditorResult(st compose.State, path string) (compose.State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return st, err
	}
	to, cc, subject, body, sigName, sigBody := compose.ParseBuffer(string(data), st.Signature, st.SignatureBody)
	st.To, st.Cc, st.Subject, st.Body = to, cc, subject, body
	st.Signature, st.SignatureBody = sigName, sigBody
	return st, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./tui/ -run TestEditor`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add src/tui/editor.go src/tui/editor_test.go
git commit -m "feat(tui): editor buffer file and parse-back"
```

---

### Task 13: Tui - compose mode: tabs, dialogue render, prompt, picker, editor exec

This is the largest task. The model gains the dialogue tab list, the compose/fuzzy/prompt key capture, the compose frame render, and the seams.

**Files:**
- Modify: `src/tui/model.go`
- Modify: `src/tui/hooks.go`
- Create: `src/tui/compose.go` (render helpers)
- Test: `src/tui/model_test.go` (new tests)

- [ ] **Step 1: Write the failing tests**

Add to `src/tui/model_test.go` (the existing `model()` helper builds a fixture model with the testBindings map - Task 1 already extended it with the compose/fuzzy contexts; `press(t, m, key)` presses a text key as `KeyPressMsg{Text, Code}`, `pressType(t, m, k)` presses a special key by Code - both exist in the file):

```go
func openDialogue(t *testing.T, m Model, id string) Model {
	t.Helper()
	m, _ = m.Update(EventMsg{Event: core.ComposeOpened{
		TabID: id, Mode: "reply", Account: "gmail", From: "Bob <bob@example.com>",
		To: []string{"a@b.c"}, Subject: "Re: x", Body: "> quoted",
	}})
	return m
}

func TestComposeOpenedAttachesDialogue(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	if m.mode != "compose" {
		t.Fatalf("mode = %q", m.mode)
	}
	if len(m.tabs) != 1 || m.tabIdx != 1 || m.tabs[0].Subject != "Re: x" {
		t.Fatalf("tabs = %+v idx %d", m.tabs, m.tabIdx)
	}
}

func TestTabSwitchParksDialogue(t *testing.T) {
	m := openDialogue(t, openDialogue(t, model(), "t1"), "t2")
	if m.tabIdx != 2 {
		t.Fatalf("tabIdx = %d", m.tabIdx)
	}
	// ] steps to the mail surface, then back through the dialogues
	m = press(t, m, "]")
	if m.mode != "index" || m.tabIdx != 0 {
		t.Fatalf("park: mode %q idx %d", m.mode, m.tabIdx)
	}
	if len(m.tabs) != 2 {
		t.Fatalf("parking must keep the dialogues: %d", len(m.tabs))
	}
	m = press(t, m, "]")
	if m.mode != "compose" || m.tabIdx != 1 {
		t.Fatalf("re-attach: mode %q idx %d", m.mode, m.tabIdx)
	}
	if m.tabs[0].Subject != "Re: x" {
		t.Fatalf("dialogue state must survive: %+v", m.tabs[0])
	}
	m = press(t, m, "[")
	if m.mode != "compose" || m.tabIdx != 2 {
		t.Fatalf("[ must reach the last dialogue: mode %q idx %d", m.mode, m.tabIdx)
	}
}

func TestReplyKeyOpensDialogue(t *testing.T) {
	got := ""
	SetReplyHandler(func(msg *core.Message, mode string) { got = mode })
	defer SetReplyHandler(func(msg *core.Message, mode string) {})
	// the model() fixture view has a cursor message at row 0 (message
	// "a" of thread t1)
	m := model()
	m = press(t, m, "R")
	if got != "reply" {
		t.Fatalf("R must open a reply, got %q", got)
	}
	m = press(t, m, "F")
	if got != "forward" {
		t.Fatalf("F must open a forward, got %q", got)
	}
	m = press(t, m, "m")
	if got != "compose" {
		t.Fatalf("m must open a blank compose, got %q", got)
	}
	// the gr chain: g then r
	m = press(t, m, "g")
	m = press(t, m, "r")
	if got != "reply-all" {
		t.Fatalf("g r must open a reply-all, got %q", got)
	}
}

func TestSendResultClosesTab(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	m, _ = m.Update(EventMsg{Event: core.SendResult{TabID: "t1", OK: true}})
	if len(m.tabs) != 0 || m.mode != "index" {
		t.Fatalf("success must close the tab: %d %q", len(m.tabs), m.mode)
	}
}

func TestSendResultFailureKeepsDialogue(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	m, _ = m.Update(EventMsg{Event: core.SendResult{TabID: "t1", OK: false, Output: "boom"}})
	if len(m.tabs) != 1 || m.tabs[0].Phase != compose.PhaseFailed || m.tabs[0].Output != "boom" {
		t.Fatalf("failure must keep the dialogue: %+v", m.tabs)
	}
	if m.mode != "compose" {
		t.Fatalf("mode = %q", m.mode)
	}
}

func TestSendArmsSeam(t *testing.T) {
	got := compose.State{}
	SetSendHandler(func(st compose.State) { got = st })
	defer SetSendHandler(func(st compose.State) {})
	m := openDialogue(t, model(), "t1")
	m = press(t, m, "y")
	if got.ID != "t1" {
		t.Fatalf("send seam must receive the dialogue: %+v", got)
	}
	if m.tabs[0].Phase != compose.PhaseSending {
		t.Fatalf("phase = %v", m.tabs[0].Phase)
	}
}

func TestAbortTwoPress(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	m = press(t, m, "q")
	if m.tabs[0].Phase != compose.PhaseAborting {
		t.Fatalf("first q arms aborting: %v", m.tabs[0].Phase)
	}
	m = press(t, m, "j")
	if m.tabs[0].Phase != compose.PhaseEditing {
		t.Fatalf("any other key cancels the abort: %v", m.tabs[0].Phase)
	}
	m = press(t, m, "q")
	m = press(t, m, "q")
	if len(m.tabs) != 0 || m.mode != "index" {
		t.Fatalf("second q confirms: %d %q", len(m.tabs), m.mode)
	}
}

func TestAttachPromptAndDetach(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	path := filepath.Join(t.TempDir(), "att.txt")
	if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	m = press(t, m, "a")
	if m.prompt == nil {
		t.Fatal("a must open the prompt")
	}
	// type the absolute path rune by rune (the prompt appends each Text)
	for _, r := range path {
		m = press(t, m, string(r))
	}
	m = pressType(t, m, '\r') // String() resolves to "enter"
	if m.prompt != nil {
		t.Fatal("enter must close the prompt")
	}
	if len(m.tabs[0].Attachments) != 1 || m.tabs[0].Attachments[0].Name != "att.txt" {
		t.Fatalf("attachments = %+v", m.tabs[0].Attachments)
	}
	// form cursor to the attachment slot (slot 4), then d detaches
	for i := 0; i < 4; i++ {
		m = press(t, m, "j")
	}
	m = press(t, m, "d")
	if len(m.tabs[0].Attachments) != 0 {
		t.Fatalf("d must detach the cursor attachment: %+v", m.tabs[0].Attachments)
	}
}

func TestFuzzyPickerSwitchesAccount(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	m = press(t, m, "c")
	if m.fuzzy == nil || m.fuzzy.kind != "account" {
		t.Fatalf("c must open the account picker: %+v", m.fuzzy)
	}
	// type-to-filter, one key at a time
	for _, r := range "gmail" {
		m = press(t, m, string(r))
	}
	m = pressType(t, m, '\r') // enter selects
	if m.fuzzy != nil {
		t.Fatal("enter must close the picker")
	}
	if m.tabs[0].Account != "gmail" {
		t.Fatalf("account = %q", m.tabs[0].Account)
	}
}

func TestEditorEditArmsExec(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	next, cmd := m.Update(tea.KeyPressMsg{Text: "e", Code: 'e'})
	if cmd == nil {
		t.Fatal("e must return an exec command")
	}
	if next.(Model).tabs[0].Phase != compose.PhaseEditing {
		t.Fatalf("phase = %v", next.(Model).tabs[0].Phase)
	}
}

func TestComposeFrameShape(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	frame := m.render()
	if got := strings.Count(frame, "\n") + 1; got != 24 {
		t.Fatalf("the compose frame must be exactly 24 lines, got %d:\n%s", got, frame)
	}
	last := stripANSI(strings.Split(frame, "\n")[23])
	if !strings.Contains(last, "gmail") {
		t.Fatalf("the status row must show the dialogue's account: %q", last)
	}
	if !strings.Contains(frame, "Re: x") || !strings.Contains(frame, "a@b.c") {
		t.Fatalf("the form must show the fields:\n%s", frame)
	}
}

func TestComposeRenderFuzzyPopup(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = press(t, m, "c")
	frame := m.render()
	if got := strings.Count(frame, "\n") + 1; got != 24 {
		t.Fatalf("the popup frame must be exactly 24 lines, got %d", got)
	}
	if !strings.Contains(frame, "account:") || !strings.Contains(frame, "dynamia") {
		t.Fatalf("the popup must show the title and entries:\n%s", frame)
	}
}
```

Notes for the implementer:
- The `openDialogue` tests drive `EventMsg` directly (the model's own event channel is nil in tests - the Update path handles EventMsg regardless).
- `press`/`pressType` are the file's existing helpers; the special-key canonical names ("enter", "esc", "backspace", "ctrl+n") come from `KeyPressMsg.String()` over the Code field - the same mechanism the existing ctrl+d tests use.
- The new tests need ONE new import: `"notmutt/compose"` (core, os, path/filepath, strings, tea are already in the file's import block).
- `TestAttachPromptAndDetach` types the absolute path rune by rune - the prompt appends each Text; the leading "/" is plain text, and the enter key resolves the path.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./tui/ -run 'TestCompose|TestTabSwitch|TestReplyKey|TestSend|TestAbort|TestAttach|TestFuzzyPicker|TestEditorEdit'`
Expected: FAIL - the model has no tabs/mode/seams (compose mode keys dispatch to the default branch, ComposeOpened/SendResult are unhandled events).

- [ ] **Step 3: Implement**

In `src/tui/hooks.go`, add:

```go
// onReply is the reply seam: the app builds the prefill (account
// detection, parsing) and publishes ComposeOpened; msg is nil for a
// blank compose.
var onReply = func(msg *core.Message, mode string) {}

func SetReplyHandler(fn func(*core.Message, string)) {
	onReply = fn
}

// onSend is the send seam: the app runs the send job (transport, fcc,
// tags) and publishes SendResult.
var onSend = func(st compose.State) {}

func SetSendHandler(fn func(compose.State)) {
	onSend = fn
}

// sigDir is the signatures root (spec section 9); the app resolves it
// from the config path, the tests set it directly.
var sigDir string

func SetSignaturesDir(dir string) {
	sigDir = dir
}
```

Add the imports `"notmutt/compose"` and `"notmutt/core"` to hooks.go.

In `src/tui/model.go`:

Add to the Model struct (after `ggPending`):

```go
	// compose tabs: the dialogue stack (R4). tabIdx 0 = the mail
	// surface (index/pager); tabIdx > 0 = tabs[tabIdx-1] attached as
	// the compose dialogue. Stepping off a dialogue parks it - state
	// intact - while the mail surface keeps working; stepping back
	// re-attaches it (spec section 5: the dialogue IS the tab).
	tabs   []compose.State
	tabIdx int
	// formIdx is the compose form cursor slot: 0-3 = From/To/Cc/
	// Subject, 4+i = attachment i (d detaches there).
	formIdx int
	// fuzzy is the selector popup (account/signature); non-nil
	// renders the popup frame and captures the fuzzy context.
	fuzzy *fuzzy
	// prompt is the attach path input row; non-nil captures the
	// prompt keys and replaces the compose keyhint row.
	prompt *pathPrompt
```

Add the imports `"notmutt/compose"` and `"os"` (os is already imported? check - model.go imports slices, strconv, strings, time, tea, config, core, mail; add `"os"` and `"notmutt/compose"`).

In `Update`, `case tea.KeyPressMsg:` - add at the top of the case body (before `km := m.bindings[m.mode]`):

```go
		if m.prompt != nil {
			m.promptKey(msg)
			return m, nil
		}
		if m.fuzzy != nil {
			m.fuzzyKey(msg, m.bindings["fuzzy"])
			return m, nil
		}
```

In the same case, after the `g`-chain block and BEFORE `m.ggPending = false`, add the gr chain:

```go
		if m.ggPending {
			// the g-prefix chain: gg = top (above), g r = reply-all.
			// Any other next key consumes the chain and dispatches
			// normally.
			m.ggPending = false
			if r == "r" && (m.mode == "index" || m.mode == "pager") {
				m.openReply("reply-all")
				return m, nil
			}
		}
```

Add to the dispatch switch (before the `default:` branch):

```go
		case "reply":
			m.openReply("reply")
		case "forward":
			m.openReply("forward")
		case "compose":
			m.openReply("compose")
		case "tab-prev":
			m.tabPrev()
		case "tab-next":
			m.tabNext()
		case "form-down":
			st := &m.tabs[m.tabIdx-1]
			if st.Phase == compose.PhaseAborting {
				st.Phase = compose.PhaseEditing
			}
			m.formIdx++
			if max := 4 + len(st.Attachments); m.formIdx > max {
				m.formIdx = max
			}
		case "form-up":
			if m.formIdx > 0 {
				m.formIdx--
			}
		case "edit":
			if st := &m.tabs[m.tabIdx-1]; st.Phase == compose.PhaseFailed {
				st.Phase = compose.PhaseEditing
			}
			st := m.tabs[m.tabIdx-1]
			path, err := writeEditorBuffer(st)
			if err != nil {
				return m, nil
			}
			return m, tea.ExecProcess(editorCmd(path), func(err error) tea.Msg {
				return editorDoneMsg{err: err, path: path}
			})
		case "attach":
			m.prompt = &pathPrompt{}
		case "detach":
			st := &m.tabs[m.tabIdx-1]
			if i := m.formIdx - 4; i >= 0 && i < len(st.Attachments) {
				st.Attachments = append(st.Attachments[:i], st.Attachments[i+1:]...)
			}
		case "account":
			m.openPicker("account")
		case "signature":
			m.openPicker("signature")
		case "send":
			if st := &m.tabs[m.tabIdx-1]; st.Phase != compose.PhaseFailed {
				st.Phase = compose.PhaseSending
			}
			onSend(m.tabs[m.tabIdx-1])
		case "abort":
			st := &m.tabs[m.tabIdx-1]
			if st.Phase == compose.PhaseAborting {
				m.closeComposeTab(m.tabIdx - 1)
			} else {
				st.Phase = compose.PhaseAborting
			}
```

The dispatch cases below index `m.tabs[m.tabIdx-1]` directly. That is safe by invariant: compose actions only resolve in the compose context (the keypress dispatches against `m.bindings[m.mode]`, so a compose action cannot fire outside compose mode), and `mode == "compose"` implies `tabIdx >= 1` (attachTab sets it only with an attached dialogue, closeComposeTab never leaves an empty compose mode). Keep the invariant in a comment at the dispatch:

```go
		// compose actions index m.tabs[m.tabIdx-1] safely: they only
		// resolve in the compose context, and compose mode implies an
		// attached dialogue (attachTab sets mode "compose" only with
		// tabIdx > 0; closeComposeTab lands on the mail surface)
```

Add the helper methods (bottom of model.go):

```go
// pathPrompt is the attach path input row.
type pathPrompt struct {
	input string
}

// promptKey captures the prompt keys: printable text appends,
// backspace pops, enter resolves (invalid paths keep the prompt
// open), esc cancels. The prompt only exists while a dialogue is
// attached (the attach action is compose-context), so the direct
// index is safe.
func (m *Model) promptKey(msg tea.KeyPressMsg) bool {
	p := m.prompt
	switch {
	case msg.String() == "enter":
		path := compose.ExpandHome(strings.TrimSpace(p.input))
		if st := &m.tabs[m.tabIdx-1]; st.AddAttachment(path) == nil {
			m.prompt = nil
		}
	case msg.String() == "esc":
		m.prompt = nil
	case msg.String() == "backspace":
		if p.input != "" {
			p.input = p.input[:len(p.input)-1]
		}
	case msg.Text != "":
		p.input += msg.Text
	}
	return true
}

// fuzzyKey captures the fuzzy context: bound actions dispatch,
// unbound printable keys filter the query, backspace trims it.
func (m *Model) fuzzyKey(msg tea.KeyPressMsg, km map[string]string) bool {
	if a := actionForKey(msg, km); a != "" {
		switch a {
		case "fuzzy-down":
			m.fuzzy.move(1)
		case "fuzzy-up":
			m.fuzzy.move(-1)
		case "fuzzy-select":
			m.fuzzySelect()
		case "fuzzy-cancel":
			m.fuzzy = nil
		}
		return true
	}
	switch {
	case msg.String() == "backspace":
		if m.fuzzy.query != "" {
			m.fuzzy.query = m.fuzzy.query[:len(m.fuzzy.query)-1]
		}
	case msg.Text != "":
		m.fuzzy.query += msg.Text
	}
	m.fuzzy.sel = 0
	return true
}

// fuzzySelect applies the picker's selection to the dialogue: an
// account switch sets Account and From; a signature switch loads the
// file and attaches it. The picker only exists while a dialogue is
// attached (the account/signature actions are compose-context).
func (m *Model) fuzzySelect() {
	entry, ok := m.fuzzy.selected()
	if !ok {
		m.fuzzy = nil
		return
	}
	st := &m.tabs[m.tabIdx-1]
	if m.fuzzy.kind == "account" {
		a := m.st.Config().Accounts[entry]
		st.Account, st.From = entry, a.From
	} else {
		if data, err := os.ReadFile(filepath.Join(sigDir, st.Account, entry)); err == nil {
			st.SetSignature(entry, strings.TrimSuffix(string(data), "\n"))
		}
	}
	m.fuzzy = nil
}

// openPicker opens the account or signature selector: the entries are
// the configured accounts, or the account's signature files.
func (m *Model) openPicker(kind string) {
	st := &m.tabs[m.tabIdx-1]
	if kind == "account" {
		names := make([]string, 0, len(m.st.Config().Accounts))
		for n := range m.st.Config().Accounts {
			names = append(names, n)
		}
		m.fuzzy = newFuzzy("account", "account:", names)
		return
	}
	var names []string
	if sigDir != "" {
		if entries, err := os.ReadDir(filepath.Join(sigDir, st.Account)); err == nil {
			for _, e := range entries {
				if !e.IsDir() {
					names = append(names, e.Name())
				}
			}
		}
	}
	m.fuzzy = newFuzzy("signature", "signature:", names)
}

// openReply hands the reply context to the app seam: the cursor row's
// message in the index, the open thread's first message in the
// pager, nil for a blank compose.
func (m *Model) openReply(mode string) {
	var msg *core.Message
	if m.mode == "index" {
		if row, ok := m.view.CursorRow(); ok {
			msg = row.Msg
		}
	} else if m.mode == "pager" && m.pager != nil {
		for _, r := range m.rows {
			if r.Msg != nil && r.ThreadID == m.pager.threadID {
				msg = r.Msg
				break
			}
		}
	}
	if mode == "reply" || mode == "reply-all" || mode == "forward" {
		if msg == nil {
			return
		}
	}
	onReply(msg, mode)
}

// tabNext/tabPrev cycle the tab list: the mail surface (index 0) and
// every open dialogue. Stepping off a dialogue parks it; stepping
// back re-attaches it. The pager state survives in m.pager - the mail
// surface restores to "pager" when a thread was open.
func (m *Model) tabNext() {
	if len(m.tabs) == 0 {
		return
	}
	m.tabIdx++
	if m.tabIdx > len(m.tabs) {
		m.tabIdx = 0
	}
	m.attachTab()
}

func (m *Model) tabPrev() {
	if len(m.tabs) == 0 {
		return
	}
	m.tabIdx--
	if m.tabIdx < 0 {
		m.tabIdx = len(m.tabs)
	}
	m.attachTab()
}

func (m *Model) attachTab() {
	m.fuzzy, m.prompt = nil, nil
	if m.tabIdx > 0 {
		m.mode = "compose"
		return
	}
	if m.pager != nil {
		m.mode = "pager"
		return
	}
	m.mode = "index"
}

// closeComposeTab removes the dialogue and lands on the previous
// tab (or the mail surface when none remain).
func (m *Model) closeComposeTab(i int) {
	m.tabs = append(m.tabs[:i], m.tabs[i+1:]...)
	if m.tabIdx > i {
		m.tabIdx--
	}
	if m.tabIdx > len(m.tabs) {
		m.tabIdx = len(m.tabs)
	}
	m.attachTab()
}
```

In `Update`, add the new event cases to the `EventMsg` inner switch (next to `core.ThreadLoaded`):

```go
		case core.ComposeOpened:
			st := compose.FromEvent(e)
			m.tabs = append(m.tabs, *st)
			m.tabIdx = len(m.tabs)
			m.attachTab()
		case core.SendResult:
			for i := range m.tabs {
				if m.tabs[i].ID == e.TabID {
					if e.OK {
						m.closeComposeTab(i)
					} else {
						m.tabs[i].Phase = compose.PhaseFailed
						m.tabs[i].Output = e.Output
						if e.Err != nil && m.tabs[i].Output == "" {
							m.tabs[i].Output = e.Err.Error()
						}
					}
					break
				}
			}
```

Add the tea-level `editorDoneMsg` case (top level of `Update`, next to `tea.KeyReleaseMsg`):

```go
	case editorDoneMsg:
		if msg.err == nil {
			if st, err := applyEditorResult(m.tabs[m.tabIdx-1], msg.path); err == nil {
				m.tabs[m.tabIdx-1] = st
			}
		}
		os.Remove(msg.path)
		return m, nil
```

and the type (bottom of model.go):

```go
// editorDoneMsg reports the $EDITOR run: the buffer path is read back
// (applyEditorResult) and removed.
type editorDoneMsg struct {
	err  error
	path string
}
```

In `render()` (model.go:792), add the compose dispatch at the top:

```go
	if m.mode == "compose" {
		return m.renderCompose()
	}
```

In `statusData()` (model.go:901), add the compose arm at the top:

```go
	if m.mode == "compose" {
		st := m.tabs[m.tabIdx-1]
		return statusData{view: "compose", visible: len(m.tabs), account: st.Account}
	}
```

Create `src/tui/compose.go`:

```go
package tui

import (
	"fmt"
	"strconv"
	"strings"

	"notmutt/compose"
	"notmutt/core"
)

// composeForm is one form line with its cursor slot: 0-3 =
// From/To/Cc/Subject, 4+i = attachment i, -1 = separator (never
// highlighted).
type composeForm struct {
	slot int
	text string
}

// renderCompose builds the attached dialogue frame (spec section 5):
// the form rows, the attachment rows, the preview pane filling the
// rest, the keyhint and status rows. The frame is ALWAYS exactly
// m.height lines - the frame discipline applies to the compose
// surface like everywhere else.
func (m *Model) renderCompose() string {
	if m.fuzzy != nil {
		return m.renderFuzzy()
	}
	st := m.tabs[m.tabIdx-1]
	rows := m.height - 2
	if rows < 1 {
		rows = 1
	}
	form := m.composeForm(st)
	var b strings.Builder
	for _, f := range form {
		outer := m.styles.Normal
		if f.slot == m.formIdx {
			outer = m.styles.Indicator
		}
		b.WriteString(padRow(f.text, m.width, outer))
		b.WriteByte('\n')
	}
	previewRows := rows - len(form)
	if previewRows > 0 {
		var preview string
		switch {
		case st.Phase == compose.PhaseFailed:
			preview = "send failed:\n" + st.Output
		default:
			preview = compose.BodyWithSig(st.Body, st.SignatureBody)
		}
		lines := strings.Split(core.SanitizeControls(preview), "\n")
		for i := 0; i < previewRows; i++ {
			line := ""
			if i < len(lines) {
				line = lines[i]
			}
			b.WriteString(padRow(line, m.width, m.styles.Normal))
			b.WriteByte('\n')
		}
	}
	// the abort confirm swaps the keyhint row (the two-press q)
	if st.Phase == compose.PhaseAborting {
		b.WriteString(padRow("abort? q to confirm, any other key to cancel", m.width, m.styles.Indicator))
	} else {
		b.WriteString(keyhintRow(m.bindings["compose"], m.width))
	}
	b.WriteByte('\n')
	b.WriteString(m.statusLineWith(m.styles, m.ui))
	return b.String()
}

// composeForm renders the form rows: From/To/Cc/Subject, the
// attachment rows, separators. Address lists cap at two display rows
// (alignment never shifts; "+N more" names the overflow).
func (m *Model) composeForm(st compose.State) []composeForm {
	capList := func(addrs []string) string {
		if len(addrs) == 0 {
			return ""
		}
		if len(addrs) <= 2 {
			return strings.Join(addrs, ", ")
		}
		return strings.Join(addrs[:2], ", ") + fmt.Sprintf(", +%d more", len(addrs)-2)
	}
	rows := []composeForm{
		{slot: 0, text: fmt.Sprintf("From: %s  [%s]", st.From, st.Account)},
		{slot: 1, text: "To: " + capList(st.To)},
		{slot: 2, text: "Cc: " + capList(st.Cc)},
		{slot: 3, text: "Subject: " + st.Subject},
		{slot: -1, text: "---"},
	}
	for i, a := range st.Attachments {
		if i >= 3 {
			rows = append(rows, composeForm{slot: -1, text: fmt.Sprintf("... +%d more", len(st.Attachments)-3)})
			break
		}
		rows = append(rows, composeForm{slot: 4 + i, text: fmt.Sprintf("[ ] %s (%d bytes)", a.Name, a.Size)})
	}
	rows = append(rows, composeForm{slot: -1, text: "---"})
	return rows
}

// renderFuzzy builds the selector popup frame: the title, the ranked
// matches, the query row, the fuzzy keyhint, the status row. Exactly
// m.height lines - the popup replaces the compose frame (a clean
// diff, never an overlay). ponytail: the popup shows at most rows-2
// matches (the slice cuts the tail); large lists scroll later.
func (m *Model) renderFuzzy() string {
	rows := m.height - 2
	if rows < 1 {
		rows = 1
	}
	var b strings.Builder
	lines := []string{m.fuzzy.title}
	for i, idx := range m.fuzzy.filtered() {
		outer := m.styles.Normal
		if i == m.fuzzy.sel {
			outer = m.styles.Indicator
		}
		lines = append(lines, padRow(m.fuzzy.entries[idx], m.width, outer))
	}
	lines = append(lines, padRow(m.fuzzy.title+" "+m.fuzzy.query, m.width, m.styles.Indicator))
	for len(lines) < rows {
		lines = append(lines, padRow("", m.width, m.styles.Normal))
	}
	for _, l := range lines[:rows] {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	b.WriteString(keyhintRow(m.bindings["fuzzy"], m.width))
	b.WriteByte('\n')
	b.WriteString(m.statusLineWith(m.styles, m.ui))
	return b.String()
}
```

Notes for the implementer:
- `stripANSI` already exists in the tui package (used by padRow and the repaint test).
- The `attachTab` call after a `ComposeOpened` sets mode "compose" (tabIdx = len(tabs) > 0) - correct.
- `statusData` in compose mode: the account pill shows the dialogue's account; `legend` stays empty.
- The dispatch guard: compose actions index `m.tabs[m.tabIdx-1]` - `composeTab()` returns nil when no dialogue is attached; use it in EVERY compose action (the plan's final code does; do not keep a raw `m.tabs[m.tabIdx-1]` in the dispatch cases).
- The `edit` case returns `tea.ExecProcess(...)` - the returned cmd is the exec; the model's own `Init` cmd is not needed here (ExecProcess's Cmd handles the pause/restore).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./tui/`
Expected: PASS (all existing tests + the new compose tests).

Fix the fixture helpers' constructor calls if `New` signature changed (it did NOT - no New signature change in this plan; seams are package vars).

- [ ] **Step 5: Commit**

```bash
git add src/tui/model.go src/tui/hooks.go src/tui/compose.go src/tui/model_test.go
git commit -m "feat(tui): compose dialogue tabs, form render, picker, editor exec"
```

---

### Task 14: App wiring and end-to-end verification

**Files:**
- Modify: `src/app/app.go`
- Modify: `src/app/send.go` (import fix if needed)

- [ ] **Step 1: Write the wiring**

In `src/app/app.go`, after the `SetOpenHandler` block (app.go:71-84), add:

```go
	// the signatures root (spec section 9): ONE path, both halves of
	// the send surface read the same tree - the app (default signature
	// in buildCompose) and the tui (the picker lists the files)
	sigDir = filepath.Join(filepath.Dir(configPath()), "signatures")
	tui.SetSignaturesDir(sigDir)

	// reply: the app prefills the dialogue (account detection, parse,
	// default signature) and publishes ComposeOpened - the TUI attaches
	// the tab
	tui.SetReplyHandler(func(msg *core.Message, mode string) {
		go func() {
			st := buildCompose(cfg, view, msg, mode)
			if st == nil {
				return
			}
			st.ID = fmt.Sprintf("%d", time.Now().UnixNano())
			bus.Publish(compose.ToEvent(st))
		}()
	})

	// send: the app runs the send job on its own goroutine; SendResult
	// closes the tab or keeps it failed
	tui.SetSendHandler(func(st compose.State) {
		go sendJob(bus, worker, view, cfg, st)
	})
```

Add the imports `"notmutt/compose"` and `"time"` to app.go (time is already imported; add compose).

- [ ] **Step 2: Verify the whole build and test suite**

Run: `go build ./... && go test ./...`
Expected: PASS everywhere.

Run: `go vet ./...`
Expected: clean (ignore pre-existing gopls-idiom noise like interface{} if the vet run flags nothing new).

- [ ] **Step 3: Build the binary**

Run: `go build -o notmutt .`
Expected: the binary builds with no errors. The user runs `./notmutt` from `src/`.

- [ ] **Step 4: Sanity-check the config load**

Run: `./notmutt` (it will start; quit with q). If the user's real config has a typo'd binding, fix it as a config change, not code.
Expected: the app starts and the status bar shows the new compose keyhints when a dialogue opens (R opens one on the cursor message).

- [ ] **Step 5: Commit**

```bash
git add src/app/app.go
git commit -m "feat(app): wire compose dialogue open and send seams"
```

---

## Self-review notes (run after the last task)

- **Spec coverage:** account selection (detection chain Task 9, selection picker Task 13); async send with output kept (Task 10); fuzzy selector + signatures (Tasks 11, 13); compose dialogue in a new tab with dialogue/tab duality (Task 13); editor flow (Task 12); fcc + reindex (Task 10); In-Reply-To/References (Task 6); quoting with cap (Task 4); two-press abort (Task 13); config surface (Task 8); keybindings incl. g r chain (Tasks 1, 8, 13).
- **Placeholders:** none - every step carries complete code.
- **Type consistency:** the dispatch's direct `m.tabs[m.tabIdx-1]` indexing (Task 13) matches the `tabs []compose.State` + `tabIdx` fields; `core.ComposeOpened` field names match `compose.ToEvent/FromEvent` (Task 7); `compose.Mode.String()` output matches the event Mode strings ("compose" | "reply" | "reply-all" | "forward") and `parseMode`; `sendJob`'s `workerAPI` matches `refresh.go:13`; `tui.SetReplyHandler(func(*core.Message, string))` matches `openReply`'s `onReply(msg, mode)` call.
- **Known pinned simplifications:** the fuzzy popup shows at most rows-4 entries (no internal scroll); the compose form caps To/Cc at 2 display rows and attachments at 3; unknown editor headers are dropped; a leading blank body line is consumed by the header separator (buffer contract); `c` = account picker and `C` = signature picker (the spec's "c account/signature" split into two actions); EDITOR values are whitespace-tokenized (no quote support).

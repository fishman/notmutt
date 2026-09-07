# Draft Resume Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reopen a saved draft on a dedicated key, edit it, and retire its stored file on a successful send; an abort-to-save re-saves in place (one draft, never duplicates). The fcc/no_fcc rule is untouched (the existing delivery core already honors it).

**Architecture:** The resumed draft rides the existing compose + send pipeline. `compose.State` gains `ResumePath` (the stored draft file this dialogue edits) and `AttachDir` (a per-dialogue temp dir holding the resumed attachments). A new mail-side parse (`mail.ParseDraft`) reads the stored draft faithfully - Bcc restored, body bytes as authored (no `splitBody` tab expansion), attachments streamed to the temp dir by the app. `compose.Resume` builds the State. `deliverSend` (shared by live and scheduled delivery) retires `ResumePath` after a successful transport; `saveDraft` retires the previous path when it writes the next draft. A new `resume-draft` action (bound in the index/pager contexts) opens the dialogue through a `SetResumeHandler` seam that mirrors the reply seam.

**Tech Stack:** Go, emersion/go-message (mail), notmuch cgo worker, tcell/lipgloss TUI, TOML bindings.

## Execution status (2026-09-07)

All 9 tasks executed inline on master and committed; both `make test`
matrices green. Two deviations from the task bodies below, both approved
during execution and reflected in the committed code and the spec:

1. **Streaming pivot - AttachDir dropped.** Resumed attachments stay
   inline in the stored draft. `compose.Attachment` gains `DraftPart int`
   (0 = plain file; >0 = the stored draft's (DraftPart-1)-th attachment
   part); `compose.Resume` maps parsed atts to that shape and assembly
   streams the part from the still-present draft (retirement is
   success-only, so scheduled delivery always finds the file). No
   `resumeAttachments`, no per-dialogue temp dir, no tab-close cleanup
   (Task 1/7/8 AttachDir steps are moot). The Task 9 workflow test plus
   a new `TestAssembleStreamsDraftPart` pin the wire path.
2. **Re-save stays a close.** `saveDraft` retires the old ResumePath and
   returns; the tab always closes on save, so ResumePath is never
   advanced back to the TUI (plan self-review note applies to the code).

**Working rules:** Only edit under `src/`. `docs/`, config seeds under `src/config/`, and `.github/` are fair game only when a task names them. Test data is fabricated (alpha/atlas/example.com), never personal. Regression tests and the MCP boundary test are LOCKED - never touch them. Non-trivial logic leaves one runnable check. Commit per task with a brief lowercase imperative; code/test/CI commits carry NO trailer.

**Test command:** from the repo root, `make test TAGS=""` and `make test TAGS="lua mcp"`; per-package quick runs `cd src && go test ./compose -run <Test>` etc. `make format` before the final commit.

---

### Task 1: ResumePath + AttachDir on the compose state, event, and bus

The resume identity must survive the dialogue's bus round trip (`compose.State` -> `core.ComposeOpened` -> `compose.State`), because the TUI rebuilds the State from the event at open and passes it back to the app at send.

**Files:**
- Modify: `src/compose/state.go:100-120` (the State struct)
- Modify: `src/core/bus.go:201-218` (`ComposeOpened`)
- Modify: `src/compose/event.go:16-42` (`ToEvent`/`FromEvent`)
- Test: `src/compose/event_resume_test.go`

- [ ] **Step 1: Write the failing test**

Create `src/compose/event_resume_test.go`:

```go
// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package compose

import (
	"reflect"
	"testing"
)

// TestEventRoundTripResumeID pins the resume identity on the dialogue's
// bus round trip: ResumePath and AttachDir survive ToEvent -> FromEvent,
// because the TUI rebuilds the send State from the event.
func TestEventRoundTripResumeID(t *testing.T) {
	st := &State{
		ID: "tab9", Mode: ModeCompose, Account: "gmail", From: "bob@example.com",
		To: []string{"alice@example.com"}, Subject: "the draft",
		Body: "line one\n\n-- \nsig", ResumePath: "/root/gmail/[Gmail]/Drafts/new/1.2.host",
		AttachDir: "/tmp/notmutt-resume-123",
	}
	back := FromEvent(ToEvent(st))
	if back.ResumePath != st.ResumePath {
		t.Fatalf("ResumePath = %q, want %q", back.ResumePath, st.ResumePath)
	}
	if back.AttachDir != st.AttachDir {
		t.Fatalf("AttachDir = %q, want %q", back.AttachDir, st.AttachDir)
	}
}

// TestFromEventDefaultsResumeID: the default (a fresh compose, or a
// spool record written before this field existed) is empty, never set.
func TestFromEventDefaultsResumeID(t *testing.T) {
	back := FromEvent(ToEvent(NewCompose("gmail", "bob@example.com", "", "")))
	if back.ResumePath != "" || back.AttachDir != "" {
		t.Fatalf("fresh compose must carry no resume identity: %q %q", back.ResumePath, back.AttachDir)
	}
}

// TestFromEventKeepsResumeID covers the ToEvent field-for-field mapping
// so a later field addition cannot silently drop ResumePath.
func TestFromEventKeepsResumeID(t *testing.T) {
	st := NewCompose("gmail", "bob@example.com", "", "")
	st.Body = "b"
	st.ResumePath = "/p"
	back := FromEvent(ToEvent(st))
	if !reflect.DeepEqual(back.Body, "b") || back.ResumePath != "/p" {
		t.Fatalf("round trip dropped fields: %+v", back)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd src && go test ./compose -run TestEventRoundTripResumeID -v`
Expected: FAIL - compile error, no `ResumePath` field on State.

- [ ] **Step 3: Add the State fields**

In `src/compose/state.go`, after the `OriginalID` field (line 116) and before `Phase`:

```go
	// ResumePath is the stored draft file this dialogue edits (the
	// resume-draft key): a successful send retires it, an abort-to-save
	// replaces it. Empty for every fresh compose (reply/forward/AI/CRM).
	ResumePath string
	// AttachDir holds a resumed draft's extracted attachments (temp,
	// removed with the tab like BodyPath); empty for fresh composes.
	AttachDir string
```

- [ ] **Step 4: Add the bus + event mapping**

In `src/core/bus.go`, after `OriginalID` (line 217) inside `ComposeOpened`:

```go
	ResumePath   string
	AttachDir    string
```

In `src/compose/event.go`, add both to the `ToEvent` composite literal (after `OriginalID: s.OriginalID,`):

```go
		ResumePath: s.ResumePath, AttachDir: s.AttachDir,
```

and to the `FromEvent` struct literal (after `OriginalID: e.OriginalID,`):

```go
		ResumePath: e.ResumePath, AttachDir: e.AttachDir,
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `cd src && go test ./compose -run ResumeID`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
cd /home/timebomb/git/opencode/notmutt && git add src/compose/state.go src/core/bus.go src/compose/event.go src/compose/event_resume_test.go && git commit -m "feat(compose): carry the resume identity across the event round trip"
```

---

### Task 2: SplitSignature in the buffer

The stored draft's body carries its signature tail already attached (`BodyWithSig`). Resuming must detach it structurally - there is no saved signature name to exact-match against - so the compose buffer keeps body and signature separate and re-assembly stays byte-faithful.

**Files:**
- Modify: `src/compose/buffer.go` (add `SplitSignature`)
- Test: `src/compose/buffer_test.go`

- [ ] **Step 1: Write the failing test**

Append to `src/compose/buffer_test.go`:

```go
// TestSplitSignature: the tail detaches at the first line that is
// exactly "-- " (the SigBlock marker). The split is structural - no
// saved signature name to match - and re-assembly through BodyWithSig
// reproduces the original bytes whether or not the split is right.
func TestSplitSignature(t *testing.T) {
	orig := "line one\nline two\n\n-- \nsigned tail"
	body, sig := SplitSignature(orig)
	if body != "line one\nline two" {
		t.Fatalf("body = %q", body)
	}
	if sig != "signed tail" {
		t.Fatalf("sig = %q", sig)
	}
	if got := BodyWithSig(body, sig); got != orig {
		t.Fatalf("round trip = %q, want %q", got, orig)
	}
}

// TestSplitSignatureNone: no "-- " marker - the whole text is body.
func TestSplitSignatureNone(t *testing.T) {
	body, sig := SplitSignature("just a body\nno signature\n")
	if body != "just a body\nno signature\n" || sig != "" {
		t.Fatalf("body=%q sig=%q", body, sig)
	}
}

// TestSplitSignatureBodyContainsMarker: a body that quotes a "-- " line
// mis-splits but is byte-faithful on re-assembly - nothing is lost.
func TestSplitSignatureBodyContainsMarker(t *testing.T) {
	orig := "the reply said\n\n-- \nme too\n\n-- \nreal sig"
	body, sig := SplitSignature(orig)
	if got := BodyWithSig(body, sig); got != orig {
		t.Fatalf("round trip = %q, want %q", got, orig)
	}
}

// TestSplitSignatureSignatureOnly: a body that was only a signature
// (the SigBlock prefix, no text above) still splits cleanly.
func TestSplitSignatureSignatureOnly(t *testing.T) {
	body, sig := SplitSignature("\n\n-- \nsig")
	if body != "" || sig != "sig" {
		t.Fatalf("body=%q sig=%q", body, sig)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd src && go test ./compose -run TestSplitSignature -v`
Expected: FAIL - undefined `SplitSignature`.

- [ ] **Step 3: Add SplitSignature**

In `src/compose/buffer.go`, after `ParseBuffer`:

```go
// SplitSignature separates a body's trailing signature block at the
// first line that is exactly "-- " - the structural rule (BodyWithSig
// emits one SigBlock, so the first marker line opens it; the pager and
// the mail parse flag signatures the same way). Re-assembly through
// BodyWithSig reproduces the original bytes whether or not the split is
// semantically right, so a body that merely quotes a "-- " line
// round-trips untouched.
func SplitSignature(text string) (body, sig string) {
	const marker = "\n-- \n"
	i := strings.Index(text, marker)
	if i < 0 {
		return text, ""
	}
	return strings.TrimRight(text[:i], "\n"), text[i+len(marker):]
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd src && go test ./compose -run TestSplitSignature`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /home/timebomb/git/opencode/notmutt && git add src/compose/buffer.go src/compose/buffer_test.go && git commit -m "feat(compose): split a body's signature tail at the structural marker"
```

---

### Task 3: mail.ParseDraft + WriteDraftAttachment

The resume parser is a second path into the mail parse boundary (R7 - fuzzed in Task 4). It must be faithful where `ParseMessage` is lossy for drafts: it restores Bcc (ParseMessage reads no Bcc), reads the plain body as authored bytes (splitBody would `expandTabs` and split), and lists the real attachment parts so the app can re-extract them.

**Files:**
- Create: `src/mail/draft.go`
- Create: `src/mail/draft_test.go`

- [ ] **Step 1: Write the failing test**

Create `src/mail/draft_test.go`:

```go
// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emersion/go-message/mail"
)

// simpleDraft writes a bare single-part draft (the client's own shape
// for a draft with no attachments: one text/plain part, Bcc kept, LF).
func simpleDraft(t *testing.T) string {
	t.Helper()
	body := "line one\n\tindented with a tab\n\n-- \nsig"
	raw := "From: Bob <bob@example.com>\n" +
		"To: alice@example.com\n" +
		"Cc: carol@example.com\n" +
		"Bcc: hidden@example.net\n" +
		"Reply-To: bob@example.com\n" +
		"Subject: the draft\n" +
		"Content-Type: text/plain; charset=utf-8\n" +
		"\n" + body
	p := filepath.Join(t.TempDir(), "draft.eml")
	if err := os.WriteFile(p, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestParseDraftSimple: a bare draft restores the envelope (Bcc
// included - ParseMessage skips it) and the body as authored: the tab
// survives (splitBody would expand it) and the signature tail stays in
// the body text (SplitSignature owns the detach, at compose time).
func TestParseDraftSimple(t *testing.T) {
	d, err := ParseDraft(simpleDraft(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.To) != 1 || d.To[0] != "alice@example.com" {
		t.Fatalf("To = %v", d.To)
	}
	if len(d.Bcc) != 1 || d.Bcc[0] != "hidden@example.net" {
		t.Fatalf("Bcc must restore (ParseMessage drops it): %v", d.Bcc)
	}
	if len(d.Cc) != 1 || d.Cc[0] != "carol@example.com" {
		t.Fatalf("Cc = %v", d.Cc)
	}
	if len(d.ReplyTo) != 1 || d.ReplyTo[0] != "bob@example.com" {
		t.Fatalf("ReplyTo = %v", d.ReplyTo)
	}
	if d.Subject != "the draft" {
		t.Fatalf("Subject = %q", d.Subject)
	}
	want := "line one\n\tindented with a tab\n\n-- \nsig"
	if d.Body != want {
		t.Fatalf("Body = %q, want %q (tab must survive)", d.Body, want)
	}
	if d.BodyTruncated {
		t.Fatal("body must not be truncated")
	}
	if len(d.Atts) != 0 {
		t.Fatalf("Atts = %v, want none", d.Atts)
	}
}

// multipartDraft builds a multipart/mixed draft with one text/plain
// part and one real file attachment, using the go-message writer the
// compose Assemble path uses (emersion/go-message/mail).
func multipartDraft(t *testing.T, attachData []byte) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "draft.eml")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	mw, err := mail.CreateWriter(f, mail.Header{})
	if err != nil {
		t.Fatal(err)
	}
	hdr := mail.Header{}
	hdr.Set("To", "alice@example.com")
	hdr.Set("Bcc", "hidden@example.net")
	hdr.Set("Subject", "multi draft")
	body, err := mw.CreatePart(hdr.Header)
	if err != nil {
		t.Fatal(err)
	}
	body.Write([]byte("the body text\n"))
	body.Close()
	ah := mail.AttachmentHeader{}
	ah.Set("Content-Type", "text/plain")
	ah.SetFilename("notes.txt")
	ap, err := mw.CreatePart(ah.Header)
	if err != nil {
		t.Fatal(err)
	}
	ap.Write(attachData)
	ap.Close()
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return p
}

// TestParseDraftMultipart: the plain part is the body, the
// Content-Disposition part lists as an attachment with its own ordinal.
func TestParseDraftMultipart(t *testing.T) {
	d, err := ParseDraft(multipartDraft(t, []byte("attachment bytes\n")))
	if err != nil {
		t.Fatal(err)
	}
	if d.Body != "the body text\n" {
		t.Fatalf("Body = %q", d.Body)
	}
	if len(d.Atts) != 1 {
		t.Fatalf("Atts = %v", d.Atts)
	}
	a := d.Atts[0]
	if a.Name != "notes.txt" || a.Ordinal != 0 {
		t.Fatalf("Att = %+v", a)
	}
}

// TestWriteDraftAttachment: the demand path streams the ordinal-th
// attachment's bytes back out, matching ParseDraft's attachment-only
// walk.
func TestWriteDraftAttachment(t *testing.T) {
	p := multipartDraft(t, []byte("attachment bytes\n"))
	var buf bytes.Buffer
	if _, err := WriteDraftAttachment(p, 0, &buf); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "attachment bytes\n" {
		t.Fatalf("streamed = %q", buf.String())
	}
	var miss bytes.Buffer
	if _, err := WriteDraftAttachment(p, 1, &miss); err == nil {
		t.Fatal("an out-of-range ordinal must error")
	}
}

// TestParseDraftCRLF: a foreign (sync-tool) draft with CRLF lines
// normalizes to LF so the signature marker still splits.
func TestParseDraftCRLF(t *testing.T) {
	raw := "To: alice@example.com\r\n" +
		"Subject: crlf\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		"line one\r\n\r\n-- \r\nsig\r\n"
	p := filepath.Join(t.TempDir(), "draft.eml")
	if err := os.WriteFile(p, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	d, err := ParseDraft(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(d.Body, "\r") {
		t.Fatalf("body must normalize CRLF: %q", d.Body)
	}
	if !strings.HasSuffix(d.Body, "-- \nsig\n") {
		t.Fatalf("body must keep the signature marker after CRLF: %q", d.Body)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd src && go test ./mail -run 'ParseDraft|WriteDraftAttachment'`
Expected: FAIL - undefined `ParseDraft`.

- [ ] **Step 3: Create src/mail/draft.go**

```go
// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/emersion/go-message"
	"github.com/emersion/go-message/mail"

	"notmutt/core"
)

// Draft is the faithful surface a resume needs (the draft-resume spec,
// section 3): the envelope as authored (Bcc restored - the stored draft
// keeps it), the plain body as authored (NOT through splitBody, whose
// expandTabs would mutate the tabs a user typed), and the real
// attachment parts (bytes stay in the file; WriteDraftAttachment
// streams them on demand).
type Draft struct {
	To, Cc, Bcc, ReplyTo []string
	Subject              string
	Body                 string
	BodyTruncated        bool // the body read hit the parse cap
	Atts                 []DraftAtt
}

// DraftAtt is one resume attachment part (a Content-Disposition part).
// Ordinal indexes the attachment-only walk WriteDraftAttachment shares.
type DraftAtt struct {
	Ordinal  int
	Name     string
	MimeType string
}

// openDraft opens a draft file as a mail reader. The ParseMessage
// tolerance holds: an unknown charset/encoding degrades, never fails.
func openDraft(path string) (*mail.Reader, *os.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	mr, err := mail.CreateReader(f)
	if err != nil && !message.IsUnknownCharset(err) && !message.IsUnknownEncoding(err) {
		f.Close()
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	return mr, f, nil
}

// ParseDraft reads a stored draft back for a resume: envelope, the
// first text/plain body part read raw (CRLF normalized to LF, tabs
// kept), and the attachment parts listed in a Content-Disposition-only
// walk (the html of an alternative pair is inline, never re-attached).
// A structural part error ends the scan with the parts read so far,
// mutt-style, like ParseMessage.
func ParseDraft(path string) (*Draft, error) {
	mr, f, err := openDraft(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	defer mr.Close()
	d := &Draft{}
	hdr := mr.Header
	if addrs, err := hdr.AddressList("To"); err == nil {
		for _, a := range addrs {
			d.To = append(d.To, a.Address)
		}
	}
	if addrs, err := hdr.AddressList("Cc"); err == nil {
		for _, a := range addrs {
			d.Cc = append(d.Cc, a.Address)
		}
	}
	if addrs, err := hdr.AddressList("Bcc"); err == nil {
		for _, a := range addrs {
			d.Bcc = append(d.Bcc, a.Address)
		}
	}
	if addrs, err := hdr.AddressList("Reply-To"); err == nil {
		for _, a := range addrs {
			d.ReplyTo = append(d.ReplyTo, a.Address)
		}
	}
	d.Subject = core.DecodeSubject(hdr.Get("Subject"))
	n := 0
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil && !message.IsUnknownCharset(err) && !message.IsUnknownEncoding(err) {
			break
		}
		switch h := p.Header.(type) {
		case *mail.InlineHeader:
			if d.Body != "" {
				continue
			}
			ct, _, _ := h.ContentType()
			if ct != "text/plain" {
				continue
			}
			data, err := io.ReadAll(io.LimitReader(p.Body, maxPartBytes+1))
			if err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			if len(data) > maxPartBytes {
				d.BodyTruncated = true
				data = data[:maxPartBytes]
			}
			d.Body = strings.ReplaceAll(string(data), "\r\n", "\n")
		case *mail.AttachmentHeader:
			name, _ := h.Filename()
			if name == "" {
				name = "attachment"
			}
			ct, _, _ := h.ContentType()
			d.Atts = append(d.Atts, DraftAtt{Ordinal: n, Name: name, MimeType: refineMimeType(ct, name)})
			n++
		}
	}
	return d, nil
}

// WriteDraftAttachment streams the ordinal-th resume attachment (the
// ParseDraft attachment-only walk) to w. Bytes stream from the part -
// the draft's parts are never buffered whole.
func WriteDraftAttachment(path string, ordinal int, w io.Writer) (int64, error) {
	mr, f, err := openDraft(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	defer mr.Close()
	n := 0
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			return 0, fmt.Errorf("attachment %d not found", ordinal)
		}
		if err != nil && !message.IsUnknownCharset(err) && !message.IsUnknownEncoding(err) {
			return 0, fmt.Errorf("%s: %w", path, err)
		}
		if _, ok := p.Header.(*mail.AttachmentHeader); !ok {
			continue
		}
		if n != ordinal {
			n++
			continue
		}
		return io.Copy(w, p.Body)
	}
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd src && go test ./mail -run 'ParseDraft|WriteDraftAttachment'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /home/timebomb/git/opencode/notmutt && git add src/mail/draft.go src/mail/draft_test.go && git commit -m "feat(mail): faithful draft parse - Bcc, raw body, attachment walk"
```

---

### Task 4: Fuzz the draft parse boundary

R7: the parser is the trust boundary and must be fuzzed. `ParseDraft` is a new parser path; a fuzz target next to the html render target keeps the boundary covered.

**Files:**
- Modify: `src/mail/fuzz_test.go`

- [ ] **Step 1: Write the failing target (panic-freedom property)**

Append to `src/mail/fuzz_test.go`:

```go
// FuzzParseDraft (AGENTS.md: parser-adjacent code passes SECURITY.md's
// fuzz targets): any bytes fed to the draft parse - headers, body,
// parts - must parse tolerantly, never panic.
func FuzzParseDraft(f *testing.F) {
	f.Add("To: a@b.c\nSubject: x\n\nbody")
	f.Add("From: b@c.d\nTo: a@b.c\nBcc: h@e.f\nContent-Type: text/plain; charset=utf-8\n\nline one\n\t tab\n\n-- \nsig")
	f.Add("Subject: m\nContent-Type: multipart/mixed; boundary=z\n\n--z\nContent-Type: text/plain\n\ntext\n--z\nContent-Type: application/octet-stream\nContent-Disposition: attachment; filename=a.bin\n\nbytes\n--z--\n")
	f.Fuzz(func(t *testing.T, raw string) {
		p := pathForDraft(t, raw)
		d, err := ParseDraft(p)
		if err != nil {
			return // tolerated, never a panic
		}
		if d.BodyTruncated {
			return
		}
		for _, a := range d.Atts {
			var sink discWriter
			if _, err := WriteDraftAttachment(p, a.Ordinal, &sink); err != nil {
				t.Fatalf("listed attachment %d must stream: %v", a.Ordinal, err)
			}
		}
	})
}

// pathForDraft writes the fuzz input to a temp file (a helper the fuzz
// property needs; test-only).
func pathForDraft(t *testing.T, raw string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "draft.eml")
	if err := os.WriteFile(p, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

type discWriter struct{}

func (discWriter) Write(p []byte) (int, error) { return len(p), nil }
```

- [ ] **Step 2: Run the fuzz target briefly**

Run: `cd src && go test ./mail -run FuzzParseDraft`
Expected: PASS (seeds run once as normal tests). Also add the imports `os` and `path/filepath` to `src/mail/fuzz_test.go` if not already present.

- [ ] **Step 3: Run the full fuzz for a few seconds to smoke it**

Run: `cd src && go test ./mail -fuzz FuzzParseDraft -fuzztime 10s`
Expected: no crashes within the time budget.

- [ ] **Step 4: Commit**

```bash
cd /home/timebomb/git/opencode/notmutt && git add src/mail/fuzz_test.go && git commit -m "test(mail): fuzz the draft parse boundary"
```

---

### Task 5: compose.Resume builder

The resume builder turns the parsed draft into a compose State: envelope as saved, body with its signature tail detached (name lost - the text is authoritative), standalone (no thread continuation, no original-id tagging), and `ResumePath` set to the file being edited.

**Files:**
- Create: `src/compose/resume.go`
- Test: `src/compose/resume_test.go`

- [ ] **Step 1: Write the failing test**

Create `src/compose/resume_test.go`:

```go
// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package compose

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"notmutt/core"
	"notmutt/mail"
)

// draftFile writes a single-part draft and returns its path (the shape
// saveDraft produces for a draft without attachments).
func draftFile(t *testing.T, body string) string {
	t.Helper()
	raw := "From: Bob <bob@example.com>\n" +
		"To: alice@example.com\n" +
		"Bcc: hidden@example.net\n" +
		"Subject: the draft\n" +
		"Content-Type: text/plain; charset=utf-8\n" +
		"\n" + body
	p := filepath.Join(t.TempDir(), "draft.eml")
	if err := os.WriteFile(p, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestResumeBuilder: Resume restores the envelope (Bcc included), the
// body with the signature tail detached, the ResumePath, and no thread
// identity - a resumed draft is a standalone compose (Mode compose,
// no OriginalID/MessageID so no replied tag fires and Assemble issues a
// fresh Message-ID).
func TestResumeBuilder(t *testing.T) {
	p := draftFile(t, "line one\n\n-- \nsig text")
	d, err := mail.ParseDraft(p)
	if err != nil {
		t.Fatal(err)
	}
	orig := core.Message{ID: "id:abc", Paths: []string{p}, Tags: []string{"draft"}}
	st := Resume(orig, d, "gmail", "bob@example.com")

	if st.Mode != ModeCompose {
		t.Fatalf("Mode = %v, want compose", st.Mode)
	}
	if len(st.To) != 1 || st.To[0] != "alice@example.com" {
		t.Fatalf("To = %v", st.To)
	}
	if len(st.Bcc) != 1 || st.Bcc[0] != "hidden@example.net" {
		t.Fatalf("Bcc = %v", st.Bcc)
	}
	if st.Subject != "the draft" {
		t.Fatalf("Subject = %q", st.Subject)
	}
	if st.Body != "line one" {
		t.Fatalf("Body = %q (signature must detach)", st.Body)
	}
	if st.SignatureBody != "sig text" {
		t.Fatalf("SignatureBody = %q", st.SignatureBody)
	}
	if st.Signature != "" {
		t.Fatalf("Signature name must be empty (the text is authoritative): %q", st.Signature)
	}
	if st.ResumePath != p {
		t.Fatalf("ResumePath = %q, want %q", st.ResumePath, p)
	}
	if st.OriginalID != "" || st.MessageID != "" || len(st.References) != 0 {
		t.Fatalf("a resume must not thread or tag: %+v", st)
	}
}

// TestResumeNoSignature: a draft saved without a signature restores the
// whole body with no signature body.
func TestResumeNoSignature(t *testing.T) {
	p := draftFile(t, "no signature here")
	d, err := mail.ParseDraft(p)
	if err != nil {
		t.Fatal(err)
	}
	st := Resume(core.Message{Paths: []string{p}}, d, "gmail", "bob@example.com")
	if st.Body != "no signature here" || st.SignatureBody != "" {
		t.Fatalf("body=%q sig=%q", st.Body, st.SignatureBody)
	}
}

// TestResumeRoundTrip: resume then re-assemble reproduces the saved
// text/plain body byte-for-byte (the fresh Date/Message-ID headers
// differ, never the body).
func TestResumeRoundTrip(t *testing.T) {
	saved := "line one\n\ttabbed\nquoted\n\n-- \nsig line"
	p := draftFile(t, saved)
	d, err := mail.ParseDraft(p)
	if err != nil {
		t.Fatal(err)
	}
	st := Resume(core.Message{Paths: []string{p}}, d, "gmail", "bob@example.com")

	var buf strings.Builder
	if err := st.Assemble(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, saved) {
		t.Fatalf("the re-assembled body must carry the saved text:\n%s", out)
	}
	if strings.Contains(out, "Bcc: hidden@example.net") == false {
		t.Fatalf("the fcc/assemble shape keeps Bcc:\n%s", out)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd src && go test ./compose -run 'TestResume'`
Expected: FAIL - undefined `Resume`.

- [ ] **Step 3: Create src/compose/resume.go**

```go
// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package compose

import (
	"notmutt/core"
	"notmutt/mail"
)

// Resume prefills a resumed saved draft (spec section 3): the envelope
// as saved, the body with its signature tail detached structurally
// (name lost - the text is authoritative), no account default signature
// injected, and no thread identity - Mode stays compose, so Assemble
// issues a fresh Message-ID and no replied/forwarded tag ever fires.
// ResumePath is the stored file being edited (retired on a successful
// send). The caller extracts the draft's attachments (they are file
// paths at send time).
func Resume(orig core.Message, d *mail.Draft, account, from string) *State {
	body, sig := SplitSignature(d.Body)
	st := &State{
		Mode:          ModeCompose,
		Account:       account,
		From:          from,
		To:            d.To,
		Cc:            d.Cc,
		Bcc:           d.Bcc,
		ReplyTo:       d.ReplyTo,
		Subject:       d.Subject,
		Body:          body,
		SignatureBody: sig,
	}
	if len(orig.Paths) > 0 {
		st.ResumePath = orig.Paths[0]
	}
	return st
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd src && go test ./compose -run 'TestResume'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /home/timebomb/git/opencode/notmutt && git add src/compose/resume.go src/compose/resume_test.go && git commit -m "feat(compose): resume builder - envelope, detached signature, standalone"
```

---

### Task 6: writeFcc returns its path; retire on send; re-save in place

The retirement core: `writeFcc` must hand back the file it wrote (the re-save and the retire both need it), a successful send retires the resumed draft, and an abort-to-save retires the previous draft so exactly one remains. `no_fcc` is untouched - the fcc branch is separate.

**Files:**
- Modify: `src/app/send.go`
- Test: `src/app/send_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `src/app/send_test.go`:

```go
// sendOKStub writes a send-stub that succeeds, capturing the wire.
func sendOKStub(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "send-stub"), []byte("#!/bin/sh\ncat > "+filepath.Join(dir, "captured")+"\n"), 0755); err != nil {
		t.Fatal(err)
	}
}

// TestSendJobRetiresResumePath: a resumed draft (ResumePath set) is
// retired after a successful transport - the index link drops
// (ActRemovePaths with the exact path) and the file is gone. The fcc
// copy still lands (no_fcc off).
func TestSendJobRetiresResumePath(t *testing.T) {
	dir := t.TempDir()
	sendOKStub(t, dir)
	draft := filepath.Join(dir, "draft.eml")
	if err := os.WriteFile(draft, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Send = config.Send{Command: filepath.Join(dir, "send-stub")}
	cfg.Accounts["gmail"] = config.Account{} // sent folder resolves by fallback

	bus := core.NewBus()
	ch := bus.Subscribe()
	view := core.NewView("inbox", "tag:inbox")
	w := &stubWorker{}

	st := compose.NewCompose("gmail", "bob@example.com", "", "")
	st.ID = "tab10"
	st.To = []string{"alice@example.com"}
	st.Subject = "x"
	st.Body = "y"
	st.ResumePath = draft

	sendJob(bus, w, view, cfg, dir, *st)

	if e := (<-ch).(core.SendResult); !e.OK {
		t.Fatalf("send failed: %v %q", e.Err, e.Output)
	}
	if _, err := os.Stat(draft); !os.IsNotExist(err) {
		t.Fatal("a delivered draft must be retired")
	}
	// actions: ActNew (the sent copy) then ActRemovePaths (the draft)
	if len(w.actions) < 2 || w.actions[len(w.actions)-1].Kind != notmuch.ActRemovePaths {
		t.Fatalf("the draft must be unindexed: %+v", w.actions)
	}
	last := w.actions[len(w.actions)-1]
	if len(last.Paths) != 1 || last.Paths[0] != draft {
		t.Fatalf("RemovePaths must name the draft file: %+v", last.Paths)
	}
	// the sent copy still lands (no_fcc off): resolve the fcc dir with
	// the production helper so the assertion tracks the real resolution
	sentDir := filepath.Join(sentPath(dir, "gmail", cfg.Accounts["gmail"]), "new")
	entries, err := os.ReadDir(sentDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("the sent copy must still land in %s: %v %v", sentDir, entries, err)
	}
}

// TestSendJobFailureKeepsResumePath: a failed transport never touches
// the draft - retirement is success-only.
func TestSendJobFailureKeepsResumePath(t *testing.T) {
	dir := t.TempDir()
	stub := "#!/bin/sh\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "send-stub"), []byte(stub), 0755); err != nil {
		t.Fatal(err)
	}
	draft := filepath.Join(dir, "draft.eml")
	if err := os.WriteFile(draft, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Send = config.Send{Command: filepath.Join(dir, "send-stub")}
	cfg.Accounts["gmail"] = config.Account{Folders: map[string]string{"sent": "Sent"}}

	bus := core.NewBus()
	view := core.NewView("inbox", "tag:inbox")
	w := &stubWorker{}

	st := compose.NewCompose("gmail", "bob@example.com", "", "")
	st.ID = "tab11"
	st.To = []string{"alice@example.com"}
	st.Body = "y"
	st.ResumePath = draft

	sendJob(bus, w, view, cfg, dir, *st)

	if _, err := os.Stat(draft); err != nil {
		t.Fatal("a failed send must keep the draft")
	}
	for _, a := range w.actions {
		if a.Kind == notmuch.ActRemovePaths {
			t.Fatalf("a failed send must not unindex: %+v", w.actions)
		}
	}
}

// TestSendJobEmptyResumePath: a fresh compose (no ResumePath) retires
// nothing - only ActNew.
func TestSendJobEmptyResumePath(t *testing.T) {
	dir := t.TempDir()
	sendOKStub(t, dir)
	cfg := config.Default()
	cfg.Send = config.Send{Command: filepath.Join(dir, "send-stub")}
	cfg.Accounts["gmail"] = config.Account{Folders: map[string]string{"sent": "Sent"}}

	bus := core.NewBus()
	view := core.NewView("inbox", "tag:inbox")
	w := &stubWorker{}

	st := compose.NewCompose("gmail", "bob@example.com", "", "")
	st.ID = "tab12"
	st.To = []string{"alice@example.com"}
	st.Body = "y"

	sendJob(bus, w, view, cfg, dir, *st)

	for _, a := range w.actions {
		if a.Kind == notmuch.ActRemovePaths {
			t.Fatalf("a fresh send must not retire: %+v", w.actions)
		}
	}
}

// TestSendJobNoFccStillRetires: a no_fcc account (no client sent copy)
// retires the draft exactly like any send.
func TestSendJobNoFccStillRetires(t *testing.T) {
	dir := t.TempDir()
	sendOKStub(t, dir)
	draft := filepath.Join(dir, "draft.eml")
	if err := os.WriteFile(draft, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Send = config.Send{Command: filepath.Join(dir, "send-stub")}
	cfg.Accounts["gmail"] = config.Account{NoFcc: true}

	bus := core.NewBus()
	view := core.NewView("inbox", "tag:inbox")
	w := &stubWorker{}

	st := compose.NewCompose("gmail", "bob@example.com", "", "")
	st.ID = "tab13"
	st.To = []string{"alice@example.com"}
	st.Body = "y"
	st.ResumePath = draft

	sendJob(bus, w, view, cfg, dir, *st)

	if _, err := os.Stat(draft); !os.IsNotExist(err) {
		t.Fatal("no_fcc must not stop the draft retirement")
	}
	if len(w.actions) == 0 || w.actions[len(w.actions)-1].Kind != notmuch.ActRemovePaths {
		t.Fatalf("actions = %+v", w.actions)
	}
}

// TestSaveDraftUpdateInPlace: an abort-to-save on a resumed draft (the
// previous ResumePath set) writes the new draft AND retires the old -
// one draft, never duplicates.
func TestSaveDraftUpdateInPlace(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Accounts["gmail"] = config.Account{Preset: "gmail"}

	old := filepath.Join(dir, "old-draft.eml")
	if err := os.WriteFile(old, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}

	bus := core.NewBus()
	view := core.NewView("inbox", "tag:inbox")
	w := &stubWorker{}

	st := compose.NewCompose("gmail", "bob@example.com", "", "")
	st.ID = "tab14"
	st.To = []string{"alice@example.com"}
	st.Subject = "x"
	st.Body = "new body"
	st.ResumePath = old

	if err := saveDraft(bus, w, view, cfg, dir, *st); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("the previous draft must be retired on a re-save")
	}
	// resolve the draft folder the way production does, so the assertion
	// never hardcodes a preset's resolved name
	draftDir := filepath.Join(draftPath(dir, "gmail", cfg.Accounts["gmail"]), "new")
	entries, err := os.ReadDir(draftDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("exactly one draft must remain in %s: %v %v", draftDir, entries, err)
	}
	data, err := os.ReadFile(filepath.Join(draftDir, entries[0].Name()))
	if err != nil || !strings.Contains(string(data), "new body") {
		t.Fatalf("the new draft must carry the new body: %v", err)
	}
	// actions: ActNew (the new draft) then ActRemovePaths (the old one)
	if len(w.actions) < 2 || w.actions[len(w.actions)-1].Kind != notmuch.ActRemovePaths {
		t.Fatalf("the old draft must be unindexed: %+v", w.actions)
	}
	if p := w.actions[len(w.actions)-1].Paths; len(p) != 1 || p[0] != old {
		t.Fatalf("RemovePaths must name the old draft: %v", p)
	}
}

// TestSaveDraftFreshKeepsNoResume: a fresh abort-to-save retires
// nothing.
func TestSaveDraftFreshKeepsNoResume(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Accounts["gmail"] = config.Account{Preset: "gmail"}

	bus := core.NewBus()
	view := core.NewView("inbox", "tag:inbox")
	w := &stubWorker{}

	st := compose.NewCompose("gmail", "bob@example.com", "", "")
	st.ID = "tab15"
	st.To = []string{"alice@example.com"}
	st.Body = "y"

	if err := saveDraft(bus, w, view, cfg, dir, *st); err != nil {
		t.Fatal(err)
	}
	for _, a := range w.actions {
		if a.Kind == notmuch.ActRemovePaths {
			t.Fatalf("a fresh save must not retire: %+v", w.actions)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd src && go test ./app -run 'RetiresResumePath|KeepsResumePath|EmptyResumePath|NoFccStillRetires|UpdateInPlace|FreshKeepsNoResume'`
Expected: FAIL - no retirement happens, the draft file still exists.

- [ ] **Step 3: Change writeFcc to return its path**

In `src/app/send.go`, change the signature and return the written name (the caller needs the file for the retire):

```go
func writeFcc(dir string, data []byte) (string, error) {
	sub := filepath.Join(dir, "new")
	if err := os.MkdirAll(sub, 0700); err != nil {
		return "", err
	}
	host, _ := os.Hostname()
	name := filepath.Join(sub, fmt.Sprintf("%d.%d.%s", time.Now().Unix(), os.Getpid(), host))
	return name, os.WriteFile(name, data, 0600)
}
```

- [ ] **Step 4: Add retireDraftPath and wire the send success path**

Add `errors` to the send.go import block (it already imports `bytes`, `fmt`, `os`, `os/exec`, `path/filepath`, `strings`, `time`). Then, in `deliverSend`, replace the fcc call that ignores the return (`if err := writeFcc(...); err != nil`) with `if _, err := writeFcc(...); err != nil` and add the retirement after the OriginalID tag block (before `return note, "", nil`):

```go
	if st.ResumePath != "" {
		if err := retireDraftPath(worker, st.ResumePath); err != nil {
			if note != "" {
				note += "; "
			}
			note += "draft retire failed: " + err.Error()
		}
	}
	return note, "", nil
}
```

Add the helper at the bottom of send.go:

```go
// retireDraftPath drops a draft from the folder: the index link first
// (the mover's primitive), then the file. A file already gone is fine.
// A backend without path ops (the cli) no-ops the index silently - its
// `notmuch new` reconciles the removal next poll.
func retireDraftPath(worker workerAPI, p string) error {
	if p == "" {
		return nil
	}
	if rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActRemovePaths, Paths: []string{p}}); err != nil || rpl.Err != nil {
		if !errors.Is(err, notmuch.ErrUnsupported) && !errors.Is(rpl.Err, notmuch.ErrUnsupported) {
			return fmt.Errorf("remove %s: %v %v", p, err, rpl.Err)
		}
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
```

Note: `workerAPI` is declared in `app/refresh.go` as `Call(a notmuch.Action) (notmuch.Reply, error)` - the stub worker in the tests satisfies it.

- [ ] **Step 5: Wire the re-save to retire the previous draft**

In `saveDraft`, capture the written path and retire the previous ResumePath after the reindex (the draft-folder rule already covers the fresh file):

```go
	path, err := writeFcc(dir, buf.Bytes())
	if err != nil {
		return err
	}
	worker.Call(notmuch.Action{Kind: notmuch.ActNew})
	if st.ResumePath != "" {
		if err := retireDraftPath(worker, st.ResumePath); err != nil {
			// the new draft is written and indexed; the orphan (a rare
			// removal failure) logs, never fails the save - the compose
			// closes either way (the d key's save-and-quit)
			diag.Warn("draft", "retire", err.Error())
		}
	}
	bus.Publish(core.ViewDiff{View: view.ViewName()})
	return nil
}
```

(Replace the existing `if err := writeFcc(dir, buf.Bytes()); err != nil { return err }` and the trailing `worker.Call(ActNew)` / `ViewDiff` lines so the whole body is: assemble, resolve dir, writeFcc -> path, ActNew, retire old, ViewDiff.)

- [ ] **Step 6: Run the tests to verify they pass**

Run: `cd src && go test ./app -run 'RetiresResumePath|KeepsResumePath|EmptyResumePath|NoFccStillRetires|UpdateInPlace|FreshKeepsNoResume|SaveDraft|SendJob'`
Expected: PASS (all existing send/save tests too).

- [ ] **Step 7: Commit**

```bash
cd /home/timebomb/git/opencode/notmutt && git add src/app/send.go src/app/send_test.go && git commit -m "feat(send): retire the resumed draft on success, replace it on re-save"
```

---

### Task 7: app resumePrefill + seam wiring

The app resolves the draft message, parses it, extracts its attachments to a per-dialogue temp dir, and publishes ComposeOpened through a new seam - mirroring the reply seam. The draft-tag gate lives here, with the account rules.

**Files:**
- Modify: `src/app/compose.go` (add `isDraft`, `resumePrefill`, `resumeAttachments`)
- Modify: `src/app/app.go` (wire `tui.SetResumeHandler`)
- Test: `src/app/compose_test.go` (add a resume prefill test)

- [ ] **Step 1: Write the failing test**

Append to `src/app/compose_test.go`. It imports `compose`, `config`, `core`, `notmuch` only - add `bytes`, `os`, and `path/filepath` to its import block for these tests:

```go
// buildDraft writes a real saved draft (the client's own Assemble
// shape) with one file attachment and returns its path.
func buildDraft(t *testing.T, attachContent []byte) string {
	t.Helper()
	dir := t.TempDir()
	att := filepath.Join(dir, "data.txt")
	if err := os.WriteFile(att, attachContent, 0600); err != nil {
		t.Fatal(err)
	}
	st := compose.NewCompose("gmail", "bob@example.com", "", "")
	st.To = []string{"alice@example.com"}
	st.Bcc = []string{"hidden@example.net"}
	st.Subject = "resume me"
	st.Body = "line one\n\t tab"
	if err := st.AddAttachment(att); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := st.Assemble(&buf); err != nil {
		t.Fatal(err)
	}
	draft := filepath.Join(dir, "draft.eml")
	if err := os.WriteFile(draft, buf.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return draft
}

// TestResumePrefill: a draft-tagged message with a path parses back
// into a dialogue - envelope restored (Bcc included), body as authored,
// the attachment extracted to the per-dialogue temp dir (AttachDir),
// ResumePath set, fcc resolved, and the default signature NOT injected.
func TestResumePrefill(t *testing.T) {
	dir := t.TempDir()
	draft := buildDraft(t, []byte("attachment bytes"))
	cfg := config.Default()
	cfg.Accounts["gmail"] = config.Account{From: "bob@example.com"}
	view := core.NewView("inbox", "tag:inbox")
	msg := &core.Message{ID: "id:x", ThreadID: "thread:x", Tags: []string{"draft", "gmail"}, Paths: []string{draft}}

	// resumePrefill needs no worker when the row carries paths
	st, err := resumePrefill(cfg, view, nil, msg, dir)
	if err != nil {
		t.Fatal(err)
	}
	if st == nil {
		t.Fatal("a draft row must resume")
	}
	if st.Subject != "resume me" || len(st.To) != 1 || st.To[0] != "alice@example.com" {
		t.Fatalf("envelope: %q %v", st.Subject, st.To)
	}
	if len(st.Bcc) != 1 || st.Bcc[0] != "hidden@example.net" {
		t.Fatalf("Bcc must restore: %v", st.Bcc)
	}
	if st.Body != "line one\n\t tab" {
		t.Fatalf("Body = %q (tab must survive)", st.Body)
	}
	if st.ResumePath != draft {
		t.Fatalf("ResumePath = %q", st.ResumePath)
	}
	if len(st.Attachments) != 1 {
		t.Fatalf("attachments = %+v", st.Attachments)
	}
	if st.Attachments[0].Name != "data.txt" || st.AttachDir == "" {
		t.Fatalf("attachment = %+v attachDir=%q", st.Attachments[0], st.AttachDir)
	}
	data, err := os.ReadFile(st.Attachments[0].Path)
	if err != nil || string(data) != "attachment bytes" {
		t.Fatalf("the extracted attachment must carry the bytes: %v", err)
	}
	if st.Signature != "" || st.SignatureBody != "" {
		t.Fatalf("resume must not inject a default signature: %q %q", st.Signature, st.SignatureBody)
	}
	if want := sentPath(dir, "gmail", cfg.Accounts["gmail"]); st.Fcc != want {
		t.Fatalf("Fcc = %q, want the resolved sent path %q", st.Fcc, want)
	}
}

// TestResumePrefillGate: a non-draft row is a silent no-op (nil, nil);
// so is a nil message.
func TestResumePrefillGate(t *testing.T) {
	cfg := config.Default()
	cfg.Accounts["gmail"] = config.Account{}
	view := core.NewView("inbox", "tag:inbox")

	st, err := resumePrefill(cfg, view, nil, &core.Message{ID: "id:i", Tags: []string{"inbox"}, Paths: []string{"/x"}}, t.TempDir())
	if err != nil || st != nil {
		t.Fatalf("an inbox row must no-op: %v %+v", err, st)
	}
	st, err = resumePrefill(cfg, view, nil, nil, t.TempDir())
	if err != nil || st != nil {
		t.Fatalf("nil must no-op: %v %+v", err, st)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd src && go test ./app -run 'ResumePrefill'`
Expected: FAIL - undefined `resumePrefill`.

- [ ] **Step 3: Add the app builders to src/app/compose.go**

Add `time` to the compose.go imports (it imports `fmt`, `net/mail`, `net/url`, `os`, `path/filepath`, `sort`, `strings` already). Then append:

```go
// isDraft reports whether a message carries the draft folder tag (the
// exclusive group member; account tags are not involved). Only drafts
// resume - the gate is the account rule, not a keybinding check.
func isDraft(msg *core.Message) bool {
	if msg == nil {
		return false
	}
	for _, t := range msg.Tags {
		if t == "draft" {
			return true
		}
	}
	return false
}

// resumePrefill builds the resumed-draft dialogue (the resume-draft
// key, spec section 2): the cursor/open message must be a draft -
// anything else is a no-op (nil, nil). The stored file is parsed back
// by compose.Resume; attachments extract to a per-dialogue temp dir
// (removed with the tab, BodyPath-ownership). A path-less row (a pager
// link rehydration) resolves through a thread fetch, mirroring
// replyPrefill. The account default signature is never injected - what
// was saved is what opens.
func resumePrefill(cfg config.Config, view *core.View, worker *notmuch.Worker, msg *core.Message, root string) (*compose.State, error) {
	if !isDraft(msg) {
		return nil, nil
	}
	cand := msg
	if len(cand.Paths) == 0 && cand.ThreadID != "" {
		rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActThread, ThreadID: cand.ThreadID})
		if err != nil || rpl.Err != nil {
			return nil, fmt.Errorf("thread %s: %v %v", cand.ThreadID, err, rpl.Err)
		}
		cand = nil
		for i := range rpl.Msgs {
			m := &rpl.Msgs[i]
			if isDraft(m) && len(m.Paths) > 0 && (msg.ID == "" || m.ID == msg.ID) {
				cand = m
				break
			}
		}
		if cand == nil {
			return nil, nil
		}
	}
	if len(cand.Paths) == 0 {
		return nil, nil
	}
	account, from, _, _ := accountFrom(cfg, cand.Tags, cursorTags(view))
	d, err := mail.ParseDraft(cand.Paths[0])
	if err != nil {
		return nil, err
	}
	if d.BodyTruncated {
		return nil, fmt.Errorf("%s: draft body exceeds the parse cap", cand.Paths[0])
	}
	st := compose.Resume(*cand, d, account, from)
	st.Fcc = sentPath(root, account, cfg.Accounts[account])
	if err := resumeAttachments(cand.Paths[0], d, st); err != nil {
		return nil, err
	}
	return st, nil
}

// resumeAttachments writes a resumed draft's stored attachments out to
// a per-dialogue temp dir (0700; the compose reads attachment bytes
// from file paths at send and re-save) and sets st.AttachDir +
// st.Attachments. The dir rides the compose event and is removed with
// the tab (the BodyPath ownership), so nothing leaks after a live send
// or re-save. Attachment filenames are based (a header filename never
// carries a path component onto the wire or into the temp dir).
func resumeAttachments(path string, d *mail.Draft, st *compose.State) error {
	if len(d.Atts) == 0 {
		return nil
	}
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("notmutt-resume-%d", time.Now().UnixNano()))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for _, a := range d.Atts {
		name := filepath.Base(a.Name)
		if name == "." || name == "/" || name == "" {
			name = "attachment"
		}
		p := filepath.Join(dir, fmt.Sprintf("%d-%s", a.Ordinal, name))
		f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		size, err := mail.WriteDraftAttachment(path, a.Ordinal, f)
		cerr := f.Close()
		if err != nil {
			return err
		}
		if cerr != nil {
			return cerr
		}
		st.Attachments = append(st.Attachments, compose.Attachment{Name: name, Path: p, Size: size, MimeType: a.MimeType})
	}
	st.AttachDir = dir
	return nil
}
```

Check the imports of `src/app/compose.go` - `notmutt/notmuch` and `notmutt/mail` are already imported (replyPrefill uses `notmuch.ActThread`, `mail.ParseMessage`).

- [ ] **Step 4: Wire the seam in src/app/app.go**

Immediately after the reply-handler block (the `tui.SetReplyHandler(...)` that ends around line 356), add:

```go
	// resume-draft: the app parses the stored draft back into a dialogue
	// (account detection, faithful body, attachments to a temp dir) and
	// publishes ComposeOpened - the TUI attaches the tab. A non-draft
	// row is a silent no-op.
	tui.SetResumeHandler(func(msg *core.Message) {
		go func() {
			st, err := resumePrefill(cfg, view, worker, msg, root)
			if err != nil {
				diag.Warn("resume", "err", err.Error())
				bus.Publish(core.JobError{Job: "resume", Err: err})
				return
			}
			if st == nil {
				return
			}
			st.ID = fmt.Sprintf("%d", time.Now().UnixNano())
			bus.Publish(compose.ToEvent(st))
		}()
	})
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd src && go test ./app -run 'ResumePrefill'`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
cd /home/timebomb/git/opencode/notmutt && git add src/app/compose.go src/app/app.go src/app/compose_test.go && git commit -m "feat(app): resume prefill - parse the draft back, extract attachments"
```

---

### Task 8: TUI resume seam, action, dispatch, binding, and cleanup

The key must exist in the builtin action vocabulary, dispatch like reply, route through a new seam, and the resumed tab's temp attachments must be removed with the tab (the BodyPath close path).

**Files:**
- Modify: `src/tui/hooks.go` (add `onResume` + `SetResumeHandler`)
- Modify: `src/tui/model.go` (catalog + `openResume` + dispatch case + close-path cleanup)
- Modify: `src/config/base.toml` (bind `e` in the vim index scheme, inherit)
- Test: `src/tui/model_test.go` (dispatch test)

- [ ] **Step 1: Write the failing tests**

Append to `src/tui/model_test.go` (the `press`, `sized`, and `testBindings` helpers at the top of that file already exist; `testBindings` returns `config.Default().Bindings`, so the new base.toml `e` binding is what dispatch sees):

```go
// TestResumeDraftKeyDispatches: the resume-draft key (e, bound in the
// index scheme) hands the cursor message to the resume seam - the app
// decides draft-or-not, so any indexed row dispatches.
func TestResumeDraftKeyDispatches(t *testing.T) {
	var got *core.Message
	old := onResume
	onResume = func(msg *core.Message) { got = msg }
	defer func() { onResume = old }()

	view := core.NewView("inbox", "tag:inbox")
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "a", Timestamp: 100, Author: "Ann", Subject: "a draft", Tags: []string{"draft"}},
	})})
	m := sized(New(view, nil, testBindings(), testTagActions(), nil, config.NewStore(config.Default()), config.Default().UI))
	press(t, m, "e")
	if got == nil || got.ID != "a" {
		t.Fatalf("e must dispatch resume-draft with the cursor message, got %+v", got)
	}
}

// TestResumeDraftNoRow: an empty view (no cursor message) never calls
// the seam - dispatch is a no-op, not a nil handoff.
func TestResumeDraftNoRow(t *testing.T) {
	called := false
	old := onResume
	onResume = func(msg *core.Message) { called = true }
	defer func() { onResume = old }()

	view := core.NewView("inbox", "tag:inbox")
	m := sized(New(view, nil, testBindings(), testTagActions(), nil, config.NewStore(config.Default()), config.Default().UI))
	press(t, m, "e")
	if called {
		t.Fatal("an empty view must not dispatch resume")
	}
}

// TestResumeDraftBuiltinAndBound: resume-draft is a builtin mail action
// in index and pager (the pager inherits the index mail keys), bound to
// e in the default scheme.
func TestResumeDraftBuiltinAndBound(t *testing.T) {
	for _, ctx := range []string{"index", "pager"} {
		if !Actions[ctx]["resume-draft"] {
			t.Fatalf("Actions[%q] must carry resume-draft", ctx)
		}
	}
	km := testBindings()
	if km["index"]["e"] != "resume-draft" || km["pager"]["e"] != "resume-draft" {
		t.Fatalf("the default scheme must bind e to resume-draft (index and pager), got index=%q pager=%q",
			km["index"]["e"], km["pager"]["e"])
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd src && go test ./tui -run 'ResumeDraft'`
Expected: FAIL - undefined `SetResumeHandler` / `resume-draft` not an action / key unbound.

- [ ] **Step 3: Add the seam to src/tui/hooks.go**

After the `SetReplyHandler` block:

```go
// onResume is the resume-draft seam: the app parses the stored draft
// back into a compose dialogue and publishes ComposeOpened; a no-op
// default. msg is the cursor/open message (draft-tagged or not - the
// app gates).
var onResume = func(msg *core.Message) {}

func SetResumeHandler(fn func(*core.Message)) {
	onResume = fn
}
```

- [ ] **Step 4: Register the action + dispatch + cleanup in src/tui/model.go**

In the `Actions` catalog, add `"resume-draft": true` to the index block right after `"reply": true, "reply-all": true, "forward": true, "compose": true,` and to the pager block right after the same line there.

In the dispatch switch, add the case right after the `case "reply":` block (which calls `m.openReply("reply")`):

```go
	case "resume-draft":
		m.openResume()
```

Add `openResume` right after `openReply` (which ends around line 4750):

```go
// openResume hands the resume context to the app seam: the cursor
// row's message in the index, the open message in the pager - exactly
// the reply resolution. The app gates on the draft tag; the TUI stays
// dumb.
func (m *Model) openResume() {
	var msg *core.Message
	if m.mode == "index" {
		if row, ok := m.activeView().CursorRow(); ok {
			msg = row.Msg
			if msg == nil && row.ThreadID != "" {
				msg = &core.Message{ThreadID: row.ThreadID}
			}
		}
	} else if m.mode == "pager" && m.pager != nil {
		for _, r := range m.rows {
			if r.Msg != nil && r.Msg.ID == m.pager.msgID {
				msg = r.Msg
				break
			}
		}
		if msg == nil {
			msg = &core.Message{ThreadID: m.pager.threadID, ID: m.pager.msgID}
		}
	}
	if msg == nil {
		return
	}
	onResume(msg)
}
```

In the tab-close path (the `else { os.Remove(m.tabs[i].BodyPath) ... }` around line 5069), widen the cleanup to the resumed temp attachments:

```go
	} else {
		os.Remove(m.tabs[i].BodyPath)
		os.RemoveAll(m.tabs[i].AttachDir)
		m.tabs = append(m.tabs[:i], m.tabs[i+1:]...)
	}
```

(`os.RemoveAll` on an empty AttachDir is a no-op - fresh composes are unaffected.)

- [ ] **Step 5: Bind the key in src/config/base.toml**

In `[schemes.vim.index]`, after the compose line (`"m" = ...`) add:

```toml
"e" = { fun = "resume-draft", desc = "Edit the saved draft", show = true, inherit = true }
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `cd src && go test ./tui -run 'ResumeDraft' && go test ./app -run TestValidateBindings && go test ./config`
Expected: PASS (the config test suite validates the default binding set; the app binding validator accepts the new action).

- [ ] **Step 7: Commit**

```bash
cd /home/timebomb/git/opencode/notmutt && git add src/tui/hooks.go src/tui/model.go src/config/base.toml src/tui/model_test.go && git commit -m "feat(tui): resume-draft key, dispatch, and temp-attachment cleanup"
```

---

### Task 9: Workflow test + full gates

A workflow test proves the whole arc end to end at the app layer with the stub worker and the real filesystem: save a draft, resume it, send it - the draft file is gone and the sent copy exists; under no_fcc the sent copy is absent. Then run the project's full gates.

**Files:**
- Test: `src/app/send_test.go` (workflow test)

- [ ] **Step 1: Write the workflow tests**

Append to `src/app/send_test.go`:

```go
// TestResumeSendWorkflow: save a draft (d), then send it from the
// resumed path (ResumePath set): the transport sees the message, the
// sent copy lands (no_fcc off), and the draft file is gone.
func TestResumeSendWorkflow(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Send = config.Send{Command: filepath.Join(dir, "send-stub")}
	cfg.Accounts["gmail"] = config.Account{Preset: "gmail", Folders: map[string]string{"sent": "Sent"}}
	sendOKStub(t, dir)

	bus := core.NewBus()
	view := core.NewView("inbox", "tag:inbox")
	w := &stubWorker{}

	// save (the d key path)
	st := compose.NewCompose("gmail", "bob@example.com", "", "")
	st.ID = "tab16"
	st.To = []string{"alice@example.com"}
	st.Subject = "draft subject"
	st.Body = "draft body"
	if err := saveDraft(bus, w, view, cfg, dir, *st); err != nil {
		t.Fatal(err)
	}
	draftDir := filepath.Join(draftPath(dir, "gmail", cfg.Accounts["gmail"]), "new")
	entries, err := os.ReadDir(draftDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("one saved draft expected: %v %v", entries, err)
	}
	draft := filepath.Join(draftDir, entries[0].Name())

	// resume (the e key path) - the draft file parses back
	d, err := mail.ParseDraft(draft)
	if err != nil {
		t.Fatal(err)
	}
	res := compose.Resume(core.Message{ID: "id:d", Paths: []string{draft}}, d, "gmail", "bob@example.com")
	res.ID = "tab17"
	res.ResumePath = draft

	// send: the draft retires, the sent copy stays
	sendJob(bus, w, view, cfg, dir, *res)

	if _, err := os.Stat(draft); !os.IsNotExist(err) {
		t.Fatal("the resumed draft must be gone after a successful send")
	}
	entries, err = os.ReadDir(filepath.Join(sentPath(dir, "gmail", cfg.Accounts["gmail"]), "new"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("one sent copy expected: %v %v", entries, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "gmail", "Sent", "new", entries[0].Name()))
	if err != nil || !strings.Contains(string(data), "draft subject") {
		t.Fatalf("the sent copy must carry the message: %v", err)
	}
}
```

Add `"notmutt/mail"` to the send_test.go imports.

- [ ] **Step 2: Run the workflow test to verify it passes**

Run: `cd src && go test ./app -run TestResumeSendWorkflow`
Expected: PASS.

- [ ] **Step 3: Full test matrix + format**

Run from the repo root:
```bash
make format
make test TAGS=""
make test TAGS="lua mcp"
```
Expected: all pass, gofmt clean.

- [ ] **Step 4: Commit**

```bash
cd /home/timebomb/git/opencode/notmutt && git add src/app/send_test.go && git commit -m "test(send): workflow - save, resume, send retires the draft"
```

---

## Self-review notes (resolved during authoring)

- The spec's "draft-handler seam returns the updated state" is simplified: the abort-to-save path always closes the tab (`m.closeTab`), so the advanced ResumePath has no consumer - the re-saved draft carries no dialogue anymore, and a later resume sets ResumePath fresh from its path. saveDraft only needs to retire the previous file (Task 6), keeping the `SetDraftHandler` signature unchanged.
- Scheduling a resumed draft: the spool serializes the full State (ResumePath included), so delivery retires the draft; the temp attachment dir is removed when the tab closes at schedule time, so a scheduled resumed draft WITH attachments fails at delivery like any vanished-attachment send. Documented limitation, not in the user's requested flow (resume -> edit -> send or re-save).
- The account default signature is not injected on resume (stored tail authoritative), matching the spec.

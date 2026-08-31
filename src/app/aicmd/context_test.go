package aicmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"notmutt/core"
)

// fixture writes a synthetic message file and returns its path. No real
// mail content is used (AGENTS.md: fabricated addresses only).
func fixture(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "msg")
	if err := os.WriteFile(p, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

func msg(path, author string, ts int64) core.Message {
	return core.Message{ID: "m", ThreadID: "t", Timestamp: ts, Author: author,
		Subject: "S" + author, Paths: []string{path}}
}

func bodyCmd(data ...string) *Command {
	return &Command{Name: "x", Description: "d", Action: "view", Data: data}
}

const textBody = "From: Alpha <alpha@example.com>\nTo: beta@example.com\n" +
	"Subject: Project X\nDate: Tue, 01 Jan 2019 00:00:00 +0000\n" +
	"MIME-Version: 1.0\nContent-Type: text/plain; charset=utf-8\n\n"

// TestBuildContextFields checks every declared section and the cleaning
// rules: participants exclude the own address and dedupe, bodies drop
// quoted/signature lines, last_body is the newest message.
func TestBuildContextFields(t *testing.T) {
	clean := "line one\n> quoted a\n-- \nsig line\n"
	pAlpha := fixture(t, textBody+clean)
	pBeta := fixture(t, textBody+"beta body\n")
	pAlpha2 := fixture(t, textBody+"alpha newest\n")
	msgs := []core.Message{
		msg(pAlpha, "alpha@example.com", 100),
		msg(pBeta, "beta@example.com", 200),
		msg(pAlpha2, "alpha@example.com", 300),
	}
	cmd := bodyCmd("count", "participants", "subjects", "dates", "structure", "bodies", "last_body")
	ctx, err := BuildContext(cmd, msgs, []string{"beta@example.com"}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Message count: 3",
		"Participants: alpha@example.com",
		"Subjects:", "Salpha@example.com", "Sbeta@example.com",
		"Dates:",
		"Messages:",
		"line one",
		"beta body",
		"alpha newest",
		"Latest message:",
	} {
		if !strings.Contains(ctx, want) {
			t.Errorf("missing %q in:\n%s", want, ctx)
		}
	}
	// own address is excluded from the participants list only - it still
	// appears as a From on its own rows
	for _, line := range strings.Split(ctx, "\n") {
		if strings.HasPrefix(line, "Participants:") && strings.Contains(line, "beta@example.com") {
			t.Errorf("own address leaked into participants: %s", line)
		}
	}
	for _, not := range []string{
		"quoted a", // quoted line stripped
		"sig line", // signature stripped
	} {
		if strings.Contains(ctx, not) {
			t.Errorf("unexpected %q in:\n%s", not, ctx)
		}
	}
	// thread order: oldest first, newest message body last
	if !(strings.Index(ctx, "line one") < strings.Index(ctx, "beta body") &&
		strings.Index(ctx, "beta body") < strings.Index(ctx, "alpha newest")) {
		t.Errorf("messages not in thread order:\n%s", ctx)
	}
}

// TestBuildContextAllowlistBodies proves the declaration is enforced:
// body content is absent when bodies/last_body are not declared.
func TestBuildContextAllowlistBodies(t *testing.T) {
	secret := "confidential payload text"
	p := fixture(t, textBody+secret)
	msgs := []core.Message{msg(p, "alpha@example.com", 100)}
	ctx, err := BuildContext(bodyCmd("participants", "subjects"), msgs, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ctx, secret) {
		t.Errorf("undeclared body leaked:\n%s", ctx)
	}
}

// TestBuildContextAttachmentLeak proves attachments never reach the
// model: the name and size of an attachment part are absent even when
// bodies are declared.
func TestBuildContextAttachmentLeak(t *testing.T) {
	multipart := "From: Alpha <alpha@example.com>\nTo: beta@example.com\n" +
		"Subject: report\nDate: Tue, 01 Jan 2019 00:00:00 +0000\n" +
		"MIME-Version: 1.0\nContent-Type: multipart/mixed; boundary=\"b\"\n\n" +
		"--b\nContent-Type: text/plain; charset=utf-8\n\nbody text here\n" +
		"--b\nContent-Type: application/pdf; name=\"secret.pdf\"\n" +
		"Content-Disposition: attachment; filename=\"secret.pdf\"\n" +
		"Content-Transfer-Encoding: base64\n\nJVBERi0xLjQK\n" +
		"--b--\n"
	p := fixture(t, multipart)
	msgs := []core.Message{msg(p, "alpha@example.com", 100)}
	ctx, err := BuildContext(bodyCmd("bodies"), msgs, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ctx, "body text here") {
		t.Errorf("body part missing:\n%s", ctx)
	}
	for _, not := range []string{"secret.pdf", "application/pdf"} {
		if strings.Contains(ctx, not) {
			t.Errorf("attachment %q leaked:\n%s", not, ctx)
		}
	}
}

// TestBuildContextHTMLOnly proves an html-only message yields no body
// text - the model gets the metadata, never raw markup.
func TestBuildContextHTMLOnly(t *testing.T) {
	html := "From: Alpha <alpha@example.com>\nTo: beta@example.com\n" +
		"Subject: x\nDate: Tue, 01 Jan 2019 00:00:00 +0000\n" +
		"MIME-Version: 1.0\nContent-Type: text/html; charset=utf-8\n\n" +
		"<p>raw <b>markup</b></p>\n"
	p := fixture(t, html)
	msgs := []core.Message{msg(p, "alpha@example.com", 100)}
	ctx, err := BuildContext(bodyCmd("bodies"), msgs, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ctx, "markup") {
		t.Errorf("html body leaked:\n%s", ctx)
	}
}

// TestBuildContextCaps proves the body caps hold: one long body truncates
// at perBodyCap, and many bodies together stay under totalBodyCap.
func TestBuildContextCaps(t *testing.T) {
	long := strings.Repeat("x", perBodyCap+1000)
	p := fixture(t, textBody+long)
	msgs := []core.Message{msg(p, "alpha@example.com", 100)}
	ctx, err := BuildContext(bodyCmd("bodies"), msgs, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	// count only inside Body: sections - the metadata carries "x" in the
	// example.com addresses and must not disturb the body-cap accounting
	if n := bodyRunX(ctx); n > perBodyCap {
		t.Errorf("body exceeded per-message cap: %d", n)
	}
	// several long bodies stay within the total budget
	var many []core.Message
	for i := int64(0); i < 8; i++ {
		p := fixture(t, textBody+long)
		many = append(many, msg(p, "alpha@example.com", i))
	}
	ctx, err = BuildContext(bodyCmd("bodies"), many, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if n := bodyRunX(ctx); n > totalBodyCap {
		t.Errorf("bodies exceeded total cap: %d", n)
	}
}

// bodyRunX counts "x" characters inside the Body: sections of an assembled
// context, skipping the surrounding metadata.
func bodyRunX(ctx string) int {
	const marker = "Body:\n"
	var n int
	for {
		i := strings.Index(ctx, marker)
		if i < 0 {
			return n
		}
		ctx = ctx[i+len(marker):]
		j := strings.Index(ctx, "\n\n")
		if j < 0 {
			return n
		}
		n += strings.Count(ctx[:j], "x")
		ctx = ctx[j:]
	}
}

// TestBuildContextStripsControls proves F1 applies to the assembled
// context: terminal escape bytes from a body never reach the prompt.
func TestBuildContextStripsControls(t *testing.T) {
	evil := "evil\x1b[31mred\x07\n"
	p := fixture(t, textBody+evil)
	msgs := []core.Message{msg(p, "alpha@example.com", 100)}
	ctx, err := BuildContext(bodyCmd("bodies"), msgs, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ctx, "\x1b") || strings.Contains(ctx, "\x07") {
		t.Errorf("control bytes leaked:\n%q", ctx)
	}
}

// TestBuildContextAccountNote proves the per-account note injection and
// its gate on the account_context flag.
func TestBuildContextAccountNote(t *testing.T) {
	p := fixture(t, textBody+"body\n")
	msgs := []core.Message{msg(p, "alpha@example.com", 100)}
	note := "I am the maintainer of this account."
	with := bodyCmd("count")
	with.AccountContext = true
	ctx, err := BuildContext(with, msgs, nil, "", note)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ctx, "Account context:") || !strings.Contains(ctx, note) {
		t.Errorf("note not injected:\n%s", ctx)
	}
	without := bodyCmd("count")
	if ctx, err := BuildContext(without, msgs, nil, "", note); err != nil {
		t.Fatal(err)
	} else if strings.Contains(ctx, "Account context:") {
		t.Errorf("note injected without account_context:\n%s", ctx)
	}
	if ctx, err := BuildContext(with, msgs, nil, "", ""); err != nil {
		t.Fatal(err)
	} else if strings.Contains(ctx, "Account context:") {
		t.Errorf("empty note produced a section:\n%s", ctx)
	}
}

func TestBuildContextEmpty(t *testing.T) {
	if _, err := BuildContext(bodyCmd("count"), nil, nil, "", ""); err == nil {
		t.Fatal("expected empty-thread error")
	}
}

// TestBuildContextStyleNote proves the default style note injection: it
// lands unconditionally (no flag gate - every command runs under it) and
// an empty note produces no section.
func TestBuildContextStyleNote(t *testing.T) {
	p := fixture(t, textBody+"body\n")
	msgs := []core.Message{msg(p, "alpha@example.com", 100)}
	note := "Be brief."
	ctx, err := BuildContext(bodyCmd("count"), msgs, nil, note, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ctx, "Style:\n") || !strings.Contains(ctx, note) {
		t.Errorf("style note not injected:\n%s", ctx)
	}
	if ctx, err := BuildContext(bodyCmd("count"), msgs, nil, "", ""); err != nil {
		t.Fatal(err)
	} else if strings.Contains(ctx, "Style:") {
		t.Errorf("empty note produced a section:\n%s", ctx)
	}
}

// TestLoadDefaultContext pins the default style note read
// (<dir>/ai/context/default.md): trimmed, sanitized, missing = "".
func TestLoadDefaultContext(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "ai", "context"), 0700)
	os.WriteFile(filepath.Join(dir, "ai", "context", "default.md"), []byte("  Be \x07brief.  \n"), 0600)
	if got := LoadDefaultContext(dir); got != "Be brief." {
		t.Errorf("note = %q, want %q", got, "Be brief.")
	}
	if got := LoadDefaultContext(t.TempDir()); got != "" {
		t.Errorf("missing note = %q, want empty", got)
	}
}

func TestLoadAccountNote(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "ai", "accounts"), 0700)
	os.WriteFile(filepath.Join(dir, "ai", "accounts", "gmail.md"), []byte("  my note  \n"), 0600)
	if got := LoadAccountNote(dir, "gmail"); got != "my note" {
		t.Errorf("note = %q, want %q", got, "my note")
	}
	if got := LoadAccountNote(dir, "acme"); got != "" {
		t.Errorf("missing note = %q, want empty", got)
	}
	if got := LoadAccountNote(dir, ""); got != "" {
		t.Errorf("empty account = %q, want empty", got)
	}
}

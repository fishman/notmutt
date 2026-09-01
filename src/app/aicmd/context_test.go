package aicmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"notmutt/core"
	"notmutt/lib/testutil"
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

// oneMsg builds a one-message thread from a plain-text body - the common
// single-message fixture for the BuildContext tests.
func oneMsg(t *testing.T, body string) []core.Message {
	t.Helper()
	return []core.Message{msg(fixture(t, textBody+body), "alpha@example.com", 100)}
}

// mustContext runs BuildContext and fails on error - the caller asserts on
// the returned text.
func mustContext(t *testing.T, cmd *Command, msgs []core.Message, own, allowed []string, style, account string) string {
	t.Helper()
	ctx, err := BuildContext(cmd, msgs, own, allowed, style, account)
	if err != nil {
		t.Fatal(err)
	}
	return ctx
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
	ctx := mustContext(t, cmd, msgs, []string{"beta@example.com"}, nil, "", "")
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
	testutil.WantNot(t, ctx, "quoted a", "sig line") // quoted + signature lines stripped
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
	ctx := mustContext(t, bodyCmd("participants", "subjects"), oneMsg(t, secret), nil, nil, "", "")
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
	ctx := mustContext(t, bodyCmd("bodies"), []core.Message{msg(fixture(t, multipart), "alpha@example.com", 100)}, nil, nil, "", "")
	if !strings.Contains(ctx, "body text here") {
		t.Errorf("body part missing:\n%s", ctx)
	}
	testutil.WantNot(t, ctx, "secret.pdf", "application/pdf")
}

// TestBuildContextHTMLOnly proves an html-only message yields no body
// text - the model gets the metadata, never raw markup.
func TestBuildContextHTMLOnly(t *testing.T) {
	html := "From: Alpha <alpha@example.com>\nTo: beta@example.com\n" +
		"Subject: x\nDate: Tue, 01 Jan 2019 00:00:00 +0000\n" +
		"MIME-Version: 1.0\nContent-Type: text/html; charset=utf-8\n\n" +
		"<p>raw <b>markup</b></p>\n"
	ctx := mustContext(t, bodyCmd("bodies"), []core.Message{msg(fixture(t, html), "alpha@example.com", 100)}, nil, nil, "", "")
	if strings.Contains(ctx, "markup") {
		t.Errorf("html body leaked:\n%s", ctx)
	}
}

// TestBuildContextCaps proves the body caps hold: one long body truncates
// at perBodyCap, and many bodies together stay under totalBodyCap.
func TestBuildContextCaps(t *testing.T) {
	long := strings.Repeat("x", perBodyCap+1000)
	ctx := mustContext(t, bodyCmd("bodies"), oneMsg(t, long), nil, nil, "", "")
	// count only inside Body: sections - the metadata carries "x" in the
	// example.com addresses and must not disturb the body-cap accounting
	if n := bodyRunX(ctx); n > perBodyCap {
		t.Errorf("body exceeded per-message cap: %d", n)
	}
	// several long bodies stay within the total budget
	var many []core.Message
	for i := int64(0); i < 8; i++ {
		many = append(many, msg(fixture(t, textBody+long), "alpha@example.com", i))
	}
	ctx = mustContext(t, bodyCmd("bodies"), many, nil, nil, "", "")
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
	ctx := mustContext(t, bodyCmd("bodies"), oneMsg(t, evil), nil, nil, "", "")
	if strings.Contains(ctx, "\x1b") || strings.Contains(ctx, "\x07") {
		t.Errorf("control bytes leaked:\n%q", ctx)
	}
}

// TestBuildContextAccountNote proves the per-account note injection and
// its gate on the account_context flag.
func TestBuildContextAccountNote(t *testing.T) {
	note := "I am the maintainer of this account."
	with := bodyCmd("count")
	with.AccountContext = true
	msgs := oneMsg(t, "body\n")
	ctx := mustContext(t, with, msgs, nil, nil, "", note)
	if !strings.Contains(ctx, "Account context:") || !strings.Contains(ctx, note) {
		t.Errorf("note not injected:\n%s", ctx)
	}
	without := bodyCmd("count")
	if ctx := mustContext(t, without, msgs, nil, nil, "", note); strings.Contains(ctx, "Account context:") {
		t.Errorf("note injected without account_context:\n%s", ctx)
	}
	if ctx := mustContext(t, with, msgs, nil, nil, "", ""); strings.Contains(ctx, "Account context:") {
		t.Errorf("empty note produced a section:\n%s", ctx)
	}
}

func TestBuildContextEmpty(t *testing.T) {
	if _, err := BuildContext(bodyCmd("count"), nil, nil, nil, "", ""); err == nil {
		t.Fatal("expected empty-thread error")
	}
}

// TestBuildContextStyleNote proves the default style note injection: it
// lands unconditionally (no flag gate - every command runs under it) and
// an empty note produces no section.
func TestBuildContextStyleNote(t *testing.T) {
	note := "Be brief."
	msgs := oneMsg(t, "body\n")
	ctx := mustContext(t, bodyCmd("count"), msgs, nil, nil, note, "")
	if !strings.Contains(ctx, "Style:\n") || !strings.Contains(ctx, note) {
		t.Errorf("style note not injected:\n%s", ctx)
	}
	if ctx := mustContext(t, bodyCmd("count"), msgs, nil, nil, "", ""); strings.Contains(ctx, "Style:") {
		t.Errorf("empty note produced a section:\n%s", ctx)
	}
}

// TestLoadDefaultContext pins the default style note read
// (<dir>/ai/context/default.md): trimmed, sanitized, missing = "".
func TestLoadDefaultContext(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "ai/context/default.md", "  Be \x07brief.  \n")
	if got := LoadDefaultContext(dir); got != "Be brief." {
		t.Errorf("note = %q, want %q", got, "Be brief.")
	}
	if got := LoadDefaultContext(t.TempDir()); got != "" {
		t.Errorf("missing note = %q, want empty", got)
	}
}

// TestLoadAccountContext pins the per-account context read
// (<dir>/ai/accounts/<account>/default.md): trimmed, sanitized, missing
// account or file = "".
func TestLoadAccountContext(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "ai/accounts/gmail/default.md", "  my note  \n")
	if got := LoadAccountContext(dir, "gmail"); got != "my note" {
		t.Errorf("note = %q, want %q", got, "my note")
	}
	if got := LoadAccountContext(dir, "acme"); got != "" {
		t.Errorf("missing note = %q, want empty", got)
	}
	if got := LoadAccountContext(dir, ""); got != "" {
		t.Errorf("empty account = %q, want empty", got)
	}
}

// TestBuildContextContextOrder pins the context ordering: the default
// context (style) comes before the account context, so the account reads
// as the override on top of the shared style.
func TestBuildContextContextOrder(t *testing.T) {
	cmd := bodyCmd("count")
	cmd.AccountContext = true
	ctx := mustContext(t, cmd, oneMsg(t, "body\n"), nil, nil, "Be brief.", "Handle support tickets.")
	styleAt := strings.Index(ctx, "Style:")
	accountAt := strings.Index(ctx, "Account context:")
	if styleAt < 0 || accountAt < 0 || accountAt < styleAt {
		t.Errorf("account context must follow the style block:\n%s", ctx)
	}
}

// TestBuildContextGrantIntersection pins the per-account [ai-data] gate:
// a declared field outside the account's grant renders no section, a
// granted one renders, and nil (no gate) lets every declared field pass.
func TestBuildContextGrantIntersection(t *testing.T) {
	cmd := bodyCmd("count", "bodies", "subjects")
	msgs := oneMsg(t, "granted body\n")

	ctx := mustContext(t, cmd, msgs, nil, []string{"count", "bodies"}, "", "")
	if !strings.Contains(ctx, "Message count: 1") || !strings.Contains(ctx, "granted body") {
		t.Errorf("granted fields must render:\n%s", ctx)
	}
	if strings.Contains(ctx, "Subjects:") {
		t.Errorf("ungranted subject field must not render:\n%s", ctx)
	}

	// nil grant = no per-account gate, everything declared passes.
	ctx = mustContext(t, cmd, msgs, nil, nil, "", "")
	if !strings.Contains(ctx, "Subjects:") {
		t.Errorf("nil grant must not filter declared fields:\n%s", ctx)
	}

	// empty grant = deny every declared field.
	ctx = mustContext(t, cmd, msgs, nil, []string{}, "", "")
	if strings.Contains(ctx, "Message count:") || strings.Contains(ctx, "Subjects:") {
		t.Errorf("empty grant must render no data section:\n%s", ctx)
	}
}

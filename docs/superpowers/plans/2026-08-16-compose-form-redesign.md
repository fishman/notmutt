# Compose form redesign - implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restructure the compose form around hotkey editing: a two-column
table settings block with blue `compose.label` labels, j/k navigation
restricted to the attachment list, field hotkeys x/b/r/S, and the prompt
dialogue styled like the form (blue label, normal entry, no background).

**Architecture:** One atomic change (the binding keys, the action
vocabulary and the dispatch must land together or validateBindings
rejects the build), committed as a single commit. The theme plumbing
adds the `compose.label` style id through the existing StyleTable /
Resolved machinery; the form rendering keeps the viewport windowing
and splits the settings rows into label/value cells.

**Tech Stack:** Go, bubbletea v2, lipgloss v0.13 (vendored, no new
dependencies - the two-column layout uses per-cell Width+Align, the
same primitive the lipgloss table package uses internally).

Reference: the spec at
`docs/superpowers/specs/2026-08-16-compose-form-redesign.md`, the
current form at `src/tui/compose.go:127-165`, the dispatch at
`src/tui/model.go:661-745`, the theme machinery at
`src/config/config.go` (`StyleTable` struct ~line 220, `rawStyleTable`
~line 326, `Resolved` ~line 520, builtin theme ~line 780), the style
set at `src/tui/styles.go` (`Styles` struct ~line 24, `DefaultStyles`
~line 154, `ResolveStyles` ~line 202), the store clone at
`src/config/store.go:58-82`, the dialogue box at
`src/tui/model.go:1647-1690`.

---

### Task 1: restructure the compose form around hotkey editing

**Files:**
- Modify: `src/config/base.toml` (vim + emacs compose schemes)
- Modify: `src/config/config_test.go` (wantCompose pin, theme tests)
- Modify: `src/config/config.go` (StyleTable, rawStyleTable, Resolved, builtin theme)
- Modify: `src/config/store.go` (cloneStyleTable)
- Modify: `src/tui/styles.go` (Styles struct, DefaultStyles, ResolveStyles)
- Modify: `src/tui/model.go` (Actions, form-down/up, edit, field editors, security, detach, onComposeOpened, overlayDialogue)
- Modify: `src/tui/compose.go` (composeForm, renderCompose, composeLabel)
- Test: `src/tui/model_test.go`

- [ ] **Step 1: update the binding data (base.toml) and its pins**

In `src/config/base.toml`, replace the vim compose scheme's j/k entries
and the field editor block:

```toml
[schemes.vim.compose]
"j" = { fun = "form-down", desc = "Move to the next attachment" }
"k" = { fun = "form-up", desc = "Move to the previous attachment" }
"ctrl+d" = "half-page-down"
"ctrl+u" = "half-page-up"
"ctrl+f" = "page-down"
"ctrl+b" = "page-up"
"t" = { fun = "edit-to", desc = "Edit the To list" }
"s" = { fun = "edit-subject", desc = "Edit the subject" }
"f" = { fun = "edit-from", desc = "Edit the From address" }
"x" = { fun = "edit-cc", desc = "Edit the Cc list" }
"b" = { fun = "edit-bcc", desc = "Edit the Bcc list" }
"r" = { fun = "edit-replyto", desc = "Edit the Reply-To list" }
"S" = { fun = "security", desc = "Cycle the security setting" }
"e" = { fun = "edit", desc = "Edit the message body" }
"a" = { fun = "attach", desc = "Attach a file" }
"d" = { fun = "detach", desc = "Detach the attachment under the cursor" }
"c" = { fun = "account", desc = "Choose the sender account" }
"C" = { fun = "signature", desc = "Choose the signature" }
"y" = { fun = "send", desc = "Send the message" }
"q" = { fun = "abort", desc = "Abort the composition" }
"[" = "tab-prev"
"]" = "tab-next"
"?" = "help"
```

In the emacs compose scheme, add the four new actions as bare strings
(descriptions inherit by action from the vim scheme):

```toml
[schemes.emacs.compose]
"ctrl+n" = "form-down"
"ctrl+p" = "form-up"
"ctrl+v" = "page-down"
"alt+v" = "page-up"
"t" = "edit-to"
"s" = "edit-subject"
"f" = "edit-from"
"x" = "edit-cc"
"b" = "edit-bcc"
"r" = "edit-replyto"
"S" = "security"
"e" = "edit"
"a" = "attach"
"d" = "detach"
"c" = "account"
"C" = "signature"
"y" = "send"
"q" = "abort"
"[" = "tab-prev"
"]" = "tab-next"
"?" = "help"
```

In `src/config/config_test.go`, the `wantCompose` map (around line 236)
gains the four keys:

```go
	"t": "edit-to", "s": "edit-subject", "f": "edit-from",
	"x": "edit-cc", "b": "edit-bcc", "r": "edit-replyto", "S": "security",
	"e": "edit", "a": "attach", "d": "detach",
```

Run: `cd /home/user/git/opencode/notmutt/src && go test ./config/`
Expected: PASS (the wantCompose pin passes with the new keys).

- [ ] **Step 2: extend the action vocabulary and the dispatch (model.go)**

In `src/tui/model.go`, the Actions map's compose context (line 61-73)
gains the four new actions:

```go
	"compose": {
		"form-down": true, "form-up": true,
		"scroll-down": true, "scroll-up": true,
		"page-down": true, "page-up": true,
		"half-page-down": true, "half-page-up": true,
		"scroll-top": true, "scroll-bottom": true,
		"edit": true, "attach": true, "detach": true,
		"edit-from": true, "edit-to": true, "edit-subject": true,
		"edit-cc": true, "edit-bcc": true, "edit-replyto": true,
		"security": true,
		"account": true, "signature": true,
		"send": true, "abort": true,
		"tab-prev": true, "tab-next": true,
		"help": true,
	},
```

Replace the form-down/form-up cases (currently model.go:661-673) with
the attachment-only navigation:

```go
	case "form-down":
		// navigation lives in the attachment list only: the settings
		// rows are edited by hotkey, never focused
		if n := len(m.composeTab().Attachments); n > 0 && m.formIdx < 8+n-1 {
			m.formIdx++
		}
		deferPaint()
		deferred = true
	case "form-up":
		if m.formIdx > 8 {
			m.formIdx--
		}
		deferPaint()
		deferred = true
```

Replace the "edit" case (model.go:674-714) with the unconditional body
editor (the slot switch is deleted):

```go
	case "edit":
		// the body editor is unconditional: every field edits by its
		// own hotkey (t/s/f/x/b/r), the account by c, the security by S
		if m.composeTab().Phase == compose.PhaseSending {
			break
		}
		if m.composeTab().Phase == compose.PhaseFailed {
			m.composeTab().Phase = compose.PhaseEditing
		}
		st := *m.composeTab()
		tabID := st.ID
		path, err := writeEditorBuffer(st)
		if err != nil {
			return m, nil
		}
		return m, tea.ExecProcess(editorCmd(path), func(err error) tea.Msg {
			return editorDoneMsg{err: err, path: path, tabID: tabID}
		})
```

Extend the field-editor case (model.go:726-745) with the three new
fields:

```go
	case "edit-to", "edit-subject", "edit-from", "edit-cc", "edit-bcc", "edit-replyto":
		// the mutt field editors: t/s/f/x/b/r open an inline prompt
		// prefilled with the field's current value (the compose
		// body stays on e and the $EDITOR buffer)
		if m.composeTab().Phase != compose.PhaseSending {
			if m.composeTab().Phase == compose.PhaseFailed {
				m.composeTab().Phase = compose.PhaseEditing
			}
			st := m.composeTab()
			d := &dialogue{kind: dialogueInput, field: strings.TrimPrefix(action, "edit-")}
			switch d.field {
			case "from":
				d.label, d.input = "From: ", st.From
			case "subject":
				d.label, d.input = "Subject: ", st.Subject
			case "to":
				d.label, d.input = "To: ", strings.Join(st.To, ", ")
			case "cc":
				d.label, d.input = "Cc: ", strings.Join(st.Cc, ", ")
			case "bcc":
				d.label, d.input = "Bcc: ", strings.Join(st.Bcc, ", ")
			case "replyto":
				d.label, d.input = "Reply-To: ", strings.Join(st.ReplyTo, ", ")
			}
			m.dialogue = d
		}
```

Add the security case right after the "signature" case:

```go
	case "security":
		if m.composeTab().Phase != compose.PhaseSending {
			m.composeTab().Security = m.composeTab().Security.Next()
		}
```

Replace the detach case (model.go:719-725) so the cursor clamps back
into the attachment range after a removal:

```go
	case "detach":
		t := m.composeTab()
		if t.Phase != compose.PhaseSending {
			if i := m.formIdx - 8; i >= 0 && i < len(t.Attachments) {
				t.Attachments = slices.Delete(t.Attachments, i, i+1)
				if n := len(t.Attachments); m.formIdx > 8+n-1 {
					m.formIdx = 8 + n - 1
				}
				if m.formIdx < 8 {
					m.formIdx = 8
				}
			}
		}
```

In `onComposeOpened` (model.go:917-925) the formIdx reset becomes:

```go
	m.formIdx = 8 // first attachment slot; a phantom with no attachments
```

And the formIdx doc comment (model.go:165-167) becomes:

```go
	// formIdx is the compose form cursor slot: 8+i attachment i. The
	// settings rows are never focused - every field edits by hotkey.
```

Run: `cd /home/user/git/opencode/notmutt/src && go build ./... && go test ./tui/ ./config/`
Expected: PASS. Two existing tests to check:
- `TestEditorEditArmsExec` (sets formIdx = 1 before 'e'): still passes
  (the editor exec is now unconditional).
- `TestSendGatesDetachAttachDuringSending` (sets formIdx = 8): still
  passes (detach still indexes formIdx - 8).
- Grep the test file for tests pressing 'e' at formIdx 0 expecting the
  account picker (`grep -n 'formIdx = 0' tui/model_test.go`); with the
  redesign 'e' no longer opens the picker - if any test relies on the
  old behavior, update it to press 'c' instead.

- [ ] **Step 3: the compose.label theme plumbing**

In `src/config/config.go`:

The StyleTable struct (line 229-232) gains the compose section:

```go
	Tabbar    TabbarStyleTable
	Compose   ComposeStyleTable
	Index     IndexStyleTable
	Pager     PagerStyleTable
```

New type after TabbarStyleTable (line 237-240):

```go
// ComposeStyleTable: the compose form's style surface; label is the
// two-column settings label, shared with the prompt dialogue's label.
type ComposeStyleTable struct {
	Label Style
}
```

In `rawStyleTable` (the switch around line 333), add the compose case
after the "tabbar" case:

```go
		case "compose":
			cm, ok := val.(map[string]interface{})
			if !ok {
				return StyleTable{}, fmt.Errorf("compose: expected a table")
			}
			if l, ok := cm["label"]; ok {
				s, err := rawStyle(l)
				if err != nil {
					return StyleTable{}, err
				}
				t.Compose.Label = s
				delete(cm, "label")
			}
			for k := range cm {
				return StyleTable{}, fmt.Errorf("compose.%s: unknown key", k)
			}
```

In the Theme Resolved mapping (after `out["tabbar.active"]` at line
542):

```go
	out["compose.label"] = apply("compose.label", table.Compose.Label)
```

In the builtin defaultTheme dark variant (after the Tabbar block,
line 791-794):

```go
				Compose: ComposeStyleTable{
					Label: Style{Fg: "base0D"}, // the form's settings labels: onedark author blue
				},
```

In `src/config/store.go` cloneStyleTable (line 59-64), add the compose
label to the per-style Attrs clone list:

```go
	for _, s := range []*Style{
		&t.Normal, &t.Indicator, &t.Status, &t.Progress, &t.Error,
		&t.Compose.Label,
		&t.Index.Number, &t.Index.Date, &t.Index.Author, &t.Index.Subject,
		&t.Index.Flags, &t.Index.Staged, &t.Index.Ghost, &t.Index.Tag.Default,
		&t.Pager.Header, &t.Pager.HdrDefault, &t.Pager.Signature, &t.Pager.Attachment,
	} {
```

(Add only `&t.Compose.Label` - the Tabbar styles are pre-existing
entries outside the clone list and stay untouched in this commit.)

In `src/tui/styles.go`:

The Styles struct (line 24-35) gains the field after TabActive:

```go
	TabActive    lipgloss.Style // tab strip active-tab pill
	ComposeLabel lipgloss.Style // compose settings label (the two-column form + the dialogue box)
```

DefaultStyles (line 154-196) gains:

```go
		Tabbar:    lipgloss.NewStyle().Foreground(c("#abb2bf")).Background(c("#3e4451")),
		TabActive: lipgloss.NewStyle().Foreground(c("#21252b")).Background(c("#61afef")),
		// the background must be set - the label cell's width padding
		// fills with it (colorWhitespace), so the column seam never
		// leaks the terminal default background
		ComposeLabel: lipgloss.NewStyle().Foreground(c("#61afef")).Background(c("#21252b")),
```

ResolveStyles (line 242-243) gains:

```go
		Tabbar:    to("tabbar", normal),
		TabActive: to("tabbar.active", normal),
		ComposeLabel: to("compose.label", normal),
```

Add config tests for the theme section in `src/config/config_test.go`
(next to the existing theme tests around line 508): a TOML string with
a compose section resolves the id, and an unknown compose key is a
load error:

```go
func TestThemeComposeLabel(t *testing.T) {
	cfg, err := Load("", strings.NewReader("[theme.dark]\n[theme.dark.compose]\nlabel = { fg = \"base0D\" }"))
	if err != nil {
		t.Fatal(err)
	}
	res := cfg.Theme.Resolved(cfg.Palette, "dark")
	if res["compose.label"].Fg != "base0D" {
		t.Fatalf("compose.label fg = %q, want base0D", res["compose.label"].Fg)
	}
}

func TestThemeComposeUnknownKey(t *testing.T) {
	_, err := Load("", strings.NewReader("[theme.dark]\n[theme.dark.compose]\nnonesuch = { fg = \"base0D\" }"))
	if err == nil || !strings.Contains(err.Error(), "compose.nonesuch") {
		t.Fatalf("unknown compose key must be a load error, got %v", err)
	}
}
```

(Check the exact Load signature used by the neighboring theme tests
and match it - it may take a config-dir path and a reader; the
`[theme.dark]` table needs `default` too if the neighboring tests set
it - mirror them.)

Run: `cd /home/user/git/opencode/notmutt/src && gofmt -l . && go vet ./config/ ./tui/ && go test ./config/`
Expected: PASS (gofmt prints nothing).

- [ ] **Step 4: the two-column form and the dialogue restyle**

In `src/tui/compose.go`:

The composeForm struct (line 15-18) becomes:

```go
// composeForm is one form line: the settings rows carry a label +
// value (rendered as a two-column table, never highlighted), the
// attachment rows carry a cursor slot (8+i), the static rows
// (dividers, content-type) carry plain text.
type composeForm struct {
	slot  int
	label string // settings row label, rendered right-aligned in compose.label
	value string // settings row value
	text  string // plain rows (dividers, content-type, attachments)
}
```

The composeForm builder (line 127-165) splits the settings rows into
label/value and drops the settings slots:

```go
	rows := []composeForm{
		{label: "Account", value: st.Account},
		{label: "From", value: st.From},
		{label: "To", value: capList(st.To)},
		{label: "Cc", value: capList(st.Cc)},
		{label: "Bcc", value: capList(st.Bcc)},
		{label: "Subject", value: st.Subject},
		{label: "Reply-To", value: capList(st.ReplyTo)},
		{label: "Fcc", value: st.Fcc},
		{label: "Security", value: st.Security.String()},
		{text: "---"},
		{text: "[ ] " + compose.ContentTypeOf(st.Body)},
	}
```

The sanitize loop at the end of composeForm becomes:

```go
	// the form rows render mail-derived text (Subject/To/Cc from the
	// replied-to message's headers) - same sanitizer as the index rows
	// and the preview pane (F1). The labels are constants, never
	// sanitized.
	for i := range rows {
		rows[i].text = core.SanitizeControls(rows[i].text)
		rows[i].value = core.SanitizeControls(rows[i].value)
	}
```

The renderCompose row loop (line 63-70) becomes:

```go
	labelW := 0
	for _, f := range form {
		if f.label != "" && len(f.label)+1 > labelW {
			labelW = len(f.label) + 1
		}
	}
	vis := m.formView.window()
	for i, f := range vis {
		outer := m.styles.Normal
		if f.slot == m.formIdx {
			outer = m.styles.Indicator
		}
		line := f.text
		if f.label != "" {
			line = composeLabel(f.label, f.value, labelW, m.width, m.styles)
		}
		b.WriteString(padRow(line, m.width, outer))
		b.WriteByte('\n')
	}
```

New helper next to composeForm:

```go
// composeLabel renders one settings row as a two-column table: the
// label right-aligned in a fixed column (the colons align at the
// seam), the value truncated to the remaining row width. The label
// carries the compose.label style (theme blue); the value cell keeps
// the normal style, so the caller's row padding never restyles it.
// NOTE: the value truncates with truncCells, never lipgloss Width() -
// Width() word-wraps at the cell width and would embed newlines in
// the row, displacing the frame height (spec review 2026-08-16).
func composeLabel(label, value string, labelW, w int, st Styles) string {
	lbl := st.ComposeLabel.Width(labelW).Align(lipgloss.Right).Render(label + ":")
	val := st.Normal.Render(truncCells(value, w-labelW-1))
	return lbl + " " + val
}
```

Add the lipgloss import to compose.go's import block (line 3-9).

In `src/tui/model.go` overlayDialogue (line 1666-1679), the content
row splits into a blue label and a normal entry, no background fill:

```go
	label := core.SanitizeControls(m.dialogue.label)
	entry := core.SanitizeControls(m.dialogue.input)
	if m.dialogue.kind == dialogueConfirm {
		entry = "(enter = confirm, esc = cancel)"
	}
	lbl := m.styles.ComposeLabel.Render(label)
	// labels are ASCII constants, so byte length is cell width; the
	// entry truncates to the remaining inner width - the line never
	// exceeds it and the box never word-wraps to a different height
	budget := inner - len(label)
	if budget < 0 {
		budget = 0
	}
	box := m.styles.Normal.
		Border(boxBorder(g)).
		BorderForeground(m.styles.Indicator.GetBackground()).
		BorderBackground(m.styles.Normal.GetBackground()).
		Width(inner).
		Render(lbl + m.styles.Normal.Render(truncCells(entry, budget)))
```

Run: `cd /home/user/git/opencode/notmutt/src && gofmt -l . && go vet ./tui/ && go test ./tui/`
Expected: PASS. Check `TestComposeFrameMuttLayout` - it asserts the
mutt-layout rows by text; the table rows keep the same visible text
("Account: gmail" etc.), so text assertions pass, but if it pins row
positions or the keyhint row's exact content, update it for the new
keys.

- [ ] **Step 5: pin the new behavior (model_test.go)**

Add these tests (place them near the other compose tests):

```go
// TestFormNavRestrictedToAttachments pins the redesign's navigation:
// j/k move only within the attachment list; with no attachments they
// are no-ops, and the cursor never enters the settings rows.
func TestFormNavRestrictedToAttachments(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	m.tabs[0].Attachments = []compose.Attachment{
		{Name: "a.txt", Size: 3}, {Name: "b.txt", Size: 3},
	}
	if m.formIdx != 8 {
		t.Fatalf("a fresh dialogue must land on the first attachment, formIdx = %d", m.formIdx)
	}
	m = press(t, m, "j")
	if m.formIdx != 9 {
		t.Fatalf("j must move into the second attachment, formIdx = %d", m.formIdx)
	}
	m = press(t, m, "j")
	if m.formIdx != 9 {
		t.Fatalf("j must clamp at the last attachment, formIdx = %d", m.formIdx)
	}
	m = press(t, m, "k")
	if m.formIdx != 8 {
		t.Fatalf("k must move back to the first attachment, formIdx = %d", m.formIdx)
	}
	m = press(t, m, "k")
	if m.formIdx != 8 {
		t.Fatalf("k must stop at the first attachment, formIdx = %d", m.formIdx)
	}
	m.tabs[0].Attachments = nil
	m = press(t, m, "j")
	if m.formIdx != 8 {
		t.Fatalf("j with no attachments must no-op, formIdx = %d", m.formIdx)
	}
}

// TestFieldHotkeysArmDialogues pins the new field hotkeys: x/b/r arm
// the prompt dialogue prefilled with the field's current values, and
// enter splits the addresses back into the tab state.
func TestFieldHotkeysArmDialogues(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	m = press(t, m, "x")
	if m.dialogue == nil || m.dialogue.field != "cc" || m.dialogue.label != "Cc: " {
		t.Fatalf("x must arm the Cc dialogue: %+v", m.dialogue)
	}
	m = press(t, m, "esc")
	m = press(t, m, "b")
	if m.dialogue == nil || m.dialogue.field != "bcc" || m.dialogue.label != "Bcc: " {
		t.Fatalf("b must arm the Bcc dialogue: %+v", m.dialogue)
	}
	m = press(t, m, "esc")
	m = press(t, m, "r")
	if m.dialogue == nil || m.dialogue.field != "replyto" || m.dialogue.label != "Reply-To: " {
		t.Fatalf("r must arm the Reply-To dialogue: %+v", m.dialogue)
	}
	for _, ch := range "a@b, c@d" {
		m = press(t, m, string(ch))
	}
	m = pressType(t, m, '\r')
	if got := m.tabs[0].ReplyTo; len(got) != 2 || got[0] != "a@b" || got[1] != "c@d" {
		t.Fatalf("enter must split the addresses into ReplyTo, got %v", got)
	}
}

// TestSecurityCycleAction pins the S action: none -> sign -> encrypt
// -> sign+encrypt -> none.
func TestSecurityCycleAction(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	if m.tabs[0].Security != compose.SecurityNone {
		t.Fatalf("a fresh dialogue must be unsigned, got %v", m.tabs[0].Security)
	}
	m = press(t, m, "S")
	if m.tabs[0].Security != compose.SecuritySign {
		t.Fatalf("S must cycle into sign, got %v", m.tabs[0].Security)
	}
	m = press(t, m, "S")
	m = press(t, m, "S")
	if m.tabs[0].Security != compose.SecuritySignEncrypt {
		t.Fatalf("three cycles must land on sign+encrypt, got %v", m.tabs[0].Security)
	}
	m = press(t, m, "S")
	if m.tabs[0].Security != compose.SecurityNone {
		t.Fatalf("the fourth cycle must wrap to none, got %v", m.tabs[0].Security)
	}
}

// TestEditUnconditionalAtAnySlot pins the redesign's 'e': the body
// editor arms on every slot, including the former account slot.
func TestEditUnconditionalAtAnySlot(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	m.formIdx = 0
	next, cmd := m.Update(tea.KeyPressMsg{Text: "e", Code: 'e'})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("e at slot 0 must arm the body editor")
	}
	if m.fuzzy != nil {
		t.Fatal("e must not open the account picker")
	}
}

// TestComposeTableColonAlign pins the two-column table: every settings
// row's label colon sits at the same cell (labelW = 9 at the default
// label set: "Security:" / "Reply-To:" are the widest).
func TestComposeTableColonAlign(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(Model)
	frame := stripANSI(m.render())
	lines := strings.Split(frame, "\n")
	seam := -1
	for i, l := range lines {
		if !strings.Contains(l, "Account:") {
			continue
		}
		seam = strings.Index(l, ":")
		break
	}
	if seam != 8 {
		t.Fatalf("the label column seam must sit at cell 8, got %d:\n%s", seam, frame)
	}
	seen := 0
	for _, l := range lines {
		if c := strings.Index(l, ":"); c == seam {
			seen++
		}
	}
	if seen < 9 {
		t.Fatalf("all nine settings rows must align at the seam, aligned = %d:\n%s", seen, frame)
	}
}

// TestDialogueLabelStyledBlue pins the dialogue restyle: the content
// row renders the label in compose.label (blue) and the entry in the
// normal style, with no indicator background on the text.
func TestDialogueLabelStyledBlue(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(Model)
	m = press(t, m, "f")
	for _, ch := range "bob@x.io" {
		m = press(t, m, string(ch))
	}
	frame := m.render()
	lines := strings.Split(frame, "\n")
	row := lines[len(lines)-3] // the box content row
	if !strings.Contains(row, m.styles.ComposeLabel.Render("From: ")) {
		t.Fatalf("the label must render in compose.label:\n%s", frame)
	}
	if !strings.Contains(row, m.styles.Normal.Render("bob@x.io")) {
		t.Fatalf("the entry must render in the normal style:\n%s", frame)
	}
	if strings.Contains(row, m.styles.sgr.indicator.open) {
		t.Fatalf("the content row must carry no background fill:\n%s", frame)
	}
}
```

Check the helpers used above exist and match: `openDialogue`,
`press`, `pressType` (used by the existing tests), `compose.Security*
` constants (compose/state.go), `m.styles.sgr.indicator.open` (the sgr
set in styles.go - in-package tests can touch it; if the field is
named differently, use the actual name). `TestFieldHotkeysArmDialogues`
relies on the existing dialogueKey enter path for the replyto field
consumer - verify it exists (`grep -n 'case "replyto"' tui/model.go`).

Run: `cd /home/user/git/opencode/notmutt/src && go test -count=1 ./tui/`
Expected: PASS.

- [ ] **Step 6: full gate and commit**

```bash
cd /home/user/git/opencode/notmutt/src
gofmt -l .            # empty (pre-existing vendor/ and notmuch/cli_test.go hits are not yours)
go vet ./...
go test -count=1 ./...
```

Then commit (code commit - Co-Authored-By only, no AI-assisted
trailer):

```bash
git add config/base.toml config/config.go config/config_test.go config/store.go tui/model.go tui/compose.go tui/styles.go tui/model_test.go
git commit -m "feat(tui): restructure the compose form around hotkey editing

Co-Authored-By: Claude <noreply@anthropic.com>"
```

Also verify the build with the lua tag (the lua plugin does not touch
the form, but the tag must stay green):

```bash
go build -tags lua ./...
```

Acceptance (manual - never run ./notmutt interactively per the
privacy rule; the user does the interactive pass):
1. The form shows the blue label column, colons aligned at the seam,
   values in the second column.
2. j/k highlight only the attachment rows; with no attachments they
   do nothing.
3. x/b/r open the dialogue prefilled; S cycles the security; e opens
   the body editor on any slot.
4. The prompt dialogue shows the label in blue with plain entry text
   and no background fill.

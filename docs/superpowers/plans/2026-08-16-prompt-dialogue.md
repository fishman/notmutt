# Prompt dialogue implementation plan

> **For agentic workers:** implement task-by-task; each task ends with a
> runnable check and a commit. The spec is
> `docs/superpowers/specs/2026-08-16-prompt-dialogue-design.md` (normative).

**Goal:** one mode-independent prompt dialogue - a lipgloss-bordered box
above the status line in every frame - that confirms (enter/esc) or
collects text input, replacing `formPrompt` and the inline abort confirm.

**Architecture:** `formPrompt` becomes `dialogue` (confirm | input with a
field consumer); `promptKey` becomes `dialogueKey` returning
`(tea.Model, tea.Cmd)` so the confirm path can dispatch through the single
action switch. Rendering: `overlayDialogue` splices a 3-row lipgloss box
over the three frame rows above the status line - the `overlayPreview`
splice pattern, config border glyphs, indicator-colored border.

**Tech Stack:** Go, bubbletea v2, lipgloss. No new dependencies.

The mailbox privacy rule applies: never run ./notmutt interactively; the
user does the interactive pass.

---

## Task 1: the dialogue widget (rename + confirm support)

**Files:**
- Modify: `src/tui/model.go` - type, key handler, Update gate, arming
  sites, abort action, attach-command re-arm, fuzzySelect prefill
- Modify: `src/tui/compose.go:43-49` - the top-row switch loses the
  PhaseAborting case; the dialogue case renders first
- Modify: `src/tui/model_test.go` - prompt tests renamed to dialogue,
  TestAbortTwoPress rewritten for enter/esc

**Context:** current `m.prompt *formPrompt` (model.go:1758-1767),
`promptKey` (model.go:1768-1816), Update gate (model.go:283-287:
`if m.prompt != nil { return m, m.promptKey(msg) }`), arming sites in
dispatchAction: "edit" slots 3/4/6 (model.go:703-714), "attach"
(model.go:728-731), "edit-to"/"edit-subject"/"edit-from" (model.go:
733-749), "abort" state machine (model.go:767-775), runAttachCommand
sets `m.prompt = nil` (model.go:1827), attachCmdDoneMsg failure branch
re-arms the prompt (model.go:435-440), fuzzySelect attachcmd branch
(model.go:1894-1900).

- [ ] **Step 1: replace formPrompt with dialogue**

Delete the formPrompt struct and its comment; add (same file position,
~model.go:1758):

```go
// dialogue is the modal prompt box: confirm (enter to confirm, esc to
// cancel) or input (typed text delivered to the field consumer). The
// label is the rendered prefix; the input the current text. The field
// names the input consumer (attach | from | to | subject | cc | bcc |
// replyto); the action the confirm's landing action (the binding
// vocabulary, dispatched through dispatchAction).
type dialogueKind int

const (
	dialogueConfirm dialogueKind = iota
	dialogueInput
)

type dialogue struct {
	kind   dialogueKind
	field  string
	label  string
	input  string
	action string
}
```

- [ ] **Step 2: replace promptKey with dialogueKey**

Delete promptKey; add dialogueKey. The signature changes to
`(tea.Model, tea.Cmd)`: the confirm branch dispatches through
dispatchAction (value receiver), whose mutations only survive when the
returned model is forwarded - so Update must return `m.dialogueKey(msg)`,
and every non-confirm branch returns `m, nil`. Note runAttachCommand
returns tea.Cmd - its branch must be `return m, m.runAttachCommand(...)`:

```go
// dialogueKey captures the dialogue keys: printable text appends,
// backspace pops, enter resolves (attach: invalid paths keep the
// prompt open; field: the value replaces the dialogue field; confirm:
// dispatches the action), esc cancels. The prompt only exists while a
// dialogue is attached, so the direct tab index is safe.
// dialogueKey returns the model and the tea.Cmd to run when enter arms
// one (an attach command exec - a path enter has no side command).
// Update forwards both, so the prompt can hand back a subprocess run
// without escaping the message loop.
func (m *Model) dialogueKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	d := m.dialogue
	switch {
	case msg.String() == "enter":
		input := strings.TrimSpace(d.input)
		if d.kind == dialogueConfirm {
			m.dialogue = nil
			return m.dispatchAction(d.action, 1)
		}
		if d.field == "attach" {
			if strings.HasPrefix(input, "@") {
				return m, m.runAttachCommand(strings.TrimPrefix(input, "@"))
			}
			path := compose.ExpandHome(input)
			if st := &m.tabs[m.tabIdx-1]; st.AddAttachment(path) == nil {
				m.dialogue = nil
			}
			return m, nil
		}
		st := &m.tabs[m.tabIdx-1]
		switch d.field {
		case "from":
			st.From = input
		case "subject":
			st.Subject = input
		case "to":
			st.To = compose.SplitAddrs(input)
		case "cc":
			st.Cc = compose.SplitAddrs(input)
		case "bcc":
			st.Bcc = compose.SplitAddrs(input)
		case "replyto":
			st.ReplyTo = compose.SplitAddrs(input)
		}
		m.dialogue = nil
	case msg.String() == "esc":
		m.dialogue = nil
		if m.mode == "compose" && m.composeTab().Phase == compose.PhaseAborting {
			m.composeTab().Phase = compose.PhaseEditing
		}
	case msg.String() == "backspace":
		if d.input != "" {
			d.input = d.input[:len(d.input)-1]
		}
	case msg.Text == "?" && d.field == "attach" && d.input == "":
		// a path can legally contain '?' - the list key is only the
		// empty-prompt '?'; anything else appends
		if names := attachCommandNames(); len(names) > 0 {
			m.fuzzy = newFuzzy("attachcmd", "attach command:", names)
		}
	case msg.Text != "":
		if d.kind == dialogueInput {
			d.input += msg.Text
		}
	}
	return m, nil
}
```

- [ ] **Step 3: the Update gate**

model.go:283-287 - the field and the gate:

```go
	if m.dialogue != nil {
		return m.dialogueKey(msg)
	}
```

Rename the struct field at model.go:201 (`prompt *formPrompt` ->
`dialogue *dialogue`, comment: "dialogue is the modal prompt box; non-nil
captures the dialogue keys in every mode"). The picker-outranks-prompt
comment above the gate stays - the attach '?' picker still arms with the
dialogue open.

- [ ] **Step 4: the arming sites** (all in dispatchAction)

"edit" slots 3/4/6 (model.go:703-714) becomes:

```go
		case 3, 4, 6:
			f := map[int]string{3: "cc", 4: "bcc", 6: "replyto"}[m.formIdx]
			d := &dialogue{kind: dialogueInput, field: f}
			switch f {
			case "cc":
				d.label, d.input = "Cc: ", strings.Join(m.composeTab().Cc, ", ")
			case "bcc":
				d.label, d.input = "Bcc: ", strings.Join(m.composeTab().Bcc, ", ")
			case "replyto":
				d.label, d.input = "Reply-To: ", strings.Join(m.composeTab().ReplyTo, ", ")
			}
			m.dialogue = d
```

"attach" (model.go:728-731):

```go
	case "attach":
		if m.composeTab().Phase != compose.PhaseSending {
			m.dialogue = &dialogue{kind: dialogueInput, field: "attach", label: "attach path: "}
		}
```

"edit-to"/"edit-subject"/"edit-from" (model.go:733-749):

```go
	case "edit-to", "edit-subject", "edit-from":
		// the mutt field editors: t/s/f open an inline prompt
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
			}
			m.dialogue = d
		}
```

"abort" (model.go:767-775) - the state machine stays, the default branch
arms the confirm dialogue (PhaseAborting stays as the marker that makes
the confirmed dispatch land on the close branch instead of re-arming):

```go
	case "abort":
		st := m.composeTab()
		switch st.Phase {
		case compose.PhaseSending:
			// never cancel an in-flight delivery; the tab closes when
			// the send result lands
		case compose.PhaseAborting:
			m.closeComposeTab(m.tabIdx - 1)
		default:
			st.Phase = compose.PhaseAborting
			m.dialogue = &dialogue{kind: dialogueConfirm, label: "Abort composition?", action: "abort"}
		}
```

Delete the form-down PhaseAborting reset (model.go:663-665) - the
dialogue gate captures every key, so the branch is unreachable; esc's
phase reset (Step 2) replaces it.

- [ ] **Step 5: the attach-command paths**

runAttachCommand (model.go:1827): `m.prompt = nil` -> `m.dialogue = nil`.
attachCmdDoneMsg failure branch (model.go:435-440):

```go
		} else {
			for i := range m.tabs { // the tab may have closed meanwhile
				if m.tabs[i].ID == msg.tabID {
					m.dialogue = &dialogue{kind: dialogueInput, field: "attach", label: "attach path: ", input: "@" + msg.name}
					break
				}
			}
		}
```

fuzzySelect attachcmd branch (model.go:1894-1900):

```go
	if m.fuzzy.kind == "attachcmd" {
		// the selection arms the attach prompt: enter runs it
		if m.dialogue != nil && m.dialogue.kind == dialogueInput && m.dialogue.field == "attach" {
			m.dialogue.input = "@" + entry
		}
		m.fuzzy = nil
		return
	}
```

- [ ] **Step 6: the compose top-row switch** (compose.go:43-49)

Replace the whole switch with the dialogue case first (the PhaseAborting
case is now unreachable - the abort always arms the dialogue - and dies
here):

```go
	switch {
	case m.dialogue != nil:
		b.WriteString(padRow(core.SanitizeControls(m.dialogue.label+m.dialogue.input), m.width, m.styles.Indicator))
	default:
		b.WriteString(m.keyhint())
	}
```

- [ ] **Step 7: adapt the tests** (model_test.go)

The compiler finds every `m.prompt` / `formPrompt` reference; the test
mechanical renames:
- `m.prompt` -> `m.dialogue`; `m.prompt.field` -> `m.dialogue.field`;
  `m.prompt.input` -> `m.dialogue.input` (model_test.go:234, 1735-1744,
  1760-1792, 1804-1830).
- model_test.go:1714 (TestAbortTwoPress) - rewrite for the dialogue
  semantics:

```go
func TestAbortConfirmDialogue(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	m = press(t, m, "q")
	if m.tabs[0].Phase != compose.PhaseAborting {
		t.Fatalf("q arms aborting: %v", m.tabs[0].Phase)
	}
	if m.dialogue == nil || m.dialogue.kind != dialogueConfirm || m.dialogue.action != "abort" {
		t.Fatalf("q must arm the abort confirm dialogue: %+v", m.dialogue)
	}
	m = press(t, m, "j") // text keys are ignored while the confirm is open
	if m.dialogue == nil || m.tabs[0].Phase != compose.PhaseAborting {
		t.Fatalf("the confirm dialogue must capture keys: %v %v", m.dialogue, m.tabs[0].Phase)
	}
	m = press(t, m, "esc")
	if m.dialogue != nil || m.tabs[0].Phase != compose.PhaseEditing {
		t.Fatalf("esc cancels the abort: %v %v", m.dialogue, m.tabs[0].Phase)
	}
	m = press(t, m, "q")
	m = press(t, m, "enter")
	if len(m.tabs) != 0 || m.mode != "index" {
		t.Fatalf("enter confirms the abort: %d %q", len(m.tabs), m.mode)
	}
}
```

Add a test that the enter-confirm dispatch runs through the action
switch (the value-receiver model forward): feed "q" then "enter", assert
the tab closed and the model returned by Update is the closed one
(covered by the last block above - keep it).

- [ ] **Step 8: run the checks**

```bash
cd /home/user/git/opencode/notmutt/src
gofmt -l tui/ config/ compose/ app/ core/ references/notmuch/ cache/   # empty (vendor hits are pre-existing)
go build ./...
go test ./tui/ ./compose/ ./config/
```

- [ ] **Step 9: commit**

```bash
cd /home/user/git/opencode/notmutt
git add src/tui/model.go src/tui/compose.go src/tui/model_test.go
git commit -m "feat(tui): generalize the compose prompt into a dialogue widget

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## Task 2: the boxed overlay

**Files:**
- Modify: `src/tui/model.go` - overlayDialogue, index/pager wiring
- Modify: `src/tui/compose.go` - renderCompose top row always the
  keyhint; renderCompose/renderFuzzy wrap their frames
- Modify: `src/tui/model_test.go` - frame tests

**Context:** the splice pattern is overlayPreview (model.go:1641-1710):
split the frame, build one bordered lipgloss style with pre-styled
interior lines, pad each box line to the frame width with the normal sgr,
copy over whole lines. Border color derivation: the preview popup uses
the indicator's BACKGROUND as the border color - follow that (the spec's
"indicator fg" wording was wrong: indicator fg is the dark text color and
would be invisible on the onedark frame). The confirm hint travels in the
content row: `label + " (enter = confirm, esc = cancel)"`.

- [ ] **Step 1: overlayDialogue** (model.go, next to overlayPreview)

```go
// overlayDialogue splices the prompt dialogue box over the frame: a
// lipgloss-bordered box (border, content row, border) whose rows
// replace whole frame lines above the status line, so the splice
// never cuts an SGR sequence. The derivation is the preview popup's:
// config border glyphs (R11), the indicator's background as the
// border color, the content row indicator-styled (the compose prompt
// row's style). A terminal too small (height < 5, width < 3) leaves
// the frame untouched.
func (m Model) overlayDialogue(frame string) string {
	if m.dialogue == nil {
		return frame
	}
	lines := strings.Split(frame, "\n")
	if m.height < 5 || m.width < 3 || len(lines) < 4 {
		return frame
	}
	// short frames (an empty index list) get padded before the
	// keyhint/status tail, like overlayPreview
	pad := m.height - len(lines)
	if pad > 0 {
		tail := append([]string{}, lines[len(lines)-2:]...)
		lines = append(lines[:len(lines)-2], make([]string, pad)...)
		lines = append(lines, tail...)
	}
	g := m.ui.Glyphs
	sg := m.styles.sgr
	inner := m.width - 2
	text := core.SanitizeControls(m.dialogue.label + m.dialogue.input)
	if m.dialogue.kind == dialogueConfirm {
		text += " (enter = confirm, esc = cancel)"
	}
	box := m.styles.Normal.
		Border(lipgloss.Border{
			TopLeft: g.BorderTL, Top: g.BorderH, TopRight: g.BorderTR,
			Left: g.BorderV, Right: g.BorderV,
			BottomLeft: g.BorderBL, Bottom: g.BorderH, BottomRight: g.BorderBR,
		}).
		BorderForeground(m.styles.Indicator.GetBackground()).
		BorderBackground(m.styles.Normal.GetBackground()).
		Width(inner).
		Render(sg.indicator.render(truncCells(text, inner)))
	rows := make([]string, 0, 3)
	for i, line := range strings.Split(box, "\n") {
		if i == 3 {
			break
		}
		rows = append(rows, padRowSGR(line, m.width, sg.normal))
	}
	top := len(lines) - 4 // three rows above the status line (last)
	copy(lines[top:top+3], rows)
	return strings.Join(lines, "\n")
}
```

- [ ] **Step 2: wire the frames** (model.go)

Index - both render paths end with `return m.overlayPreview(b.String())`
(model.go:1552 and 1638); dialogue splices last so it wins any overlap:

```go
		return m.overlayDialogue(m.overlayPreview(b.String()))
```

Pager frame (model.go:1516-1523) - the inline builder, after the status
line:

```go
		return m.overlayDialogue(b.String())
```

- [ ] **Step 3: compose frames** (compose.go)

renderCompose: the top-row switch (Task 1 Step 6) collapses to the
keyhint always (the dialogue renders at the bottom now); the comment
above the switch dies with it:

```go
	b.WriteString(m.keyhint())
	b.WriteByte('\n')
```

and the tail (`b.WriteString(m.statusLineWith(m.styles, m.ui))`) wraps:

```go
	b.WriteString(m.statusLineWith(m.styles, m.ui))
	return m.overlayDialogue(b.String())
```

renderFuzzy (compose.go:194) - the dialogue stays visible under the
picker ('?' arming, fuzzySelect prefills it): same tail wrap. Update the
renderCompose doc comment: the frame is tabbar, keyhint, form rows,
preview, status; the dialogue splices above the status when open.

- [ ] **Step 4: frame tests** (model_test.go)

Rewrite TestAttachPromptRendersRow (model_test.go:1804-1830) - the
keyhint is the top row again, the dialogue is the bottom box; the
sanitize and typing asserts move to the box content row (third line from
the end):

```go
func TestAttachPromptRendersBox(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(Model)
	m = press(t, m, "a")
	frame := m.render()
	if got := strings.Count(frame, "\n") + 1; got != 24 {
		t.Fatalf("the dialogue frame must be exactly 24 lines, got %d", got)
	}
	lines := strings.Split(stripANSI(frame), "\n")
	if !strings.Contains(lines[0], "form-down") {
		t.Fatalf("the keyhint must stay on the top row:\n%s", frame)
	}
	if !strings.Contains(lines[21], "attach path:") {
		t.Fatalf("the dialogue box must render above the status line:\n%s", frame)
	}
	if !strings.Contains(lines[23], "compose") { // box is 3 rows: 20/21/22; status stays last
		t.Fatalf("the status line must stay the last row:\n%s", frame)
	}
	m = press(t, m, "h")
	m = press(t, m, "i")
	if !strings.Contains(stripANSI(m.render()), "attach path: hi") {
		t.Fatalf("typed input must render in the box:\n%s", m.render())
	}
	m.dialogue.input = "x\x1b[31m"
	if out := m.render(); strings.Contains(out, "\x1b[31m") {
		t.Fatalf("control chars leaked into the dialogue box:\n%q", out)
	}
}
```

Add TestConfirmBoxRendersHint - open the abort confirm (q) in compose,
assert the box content row contains "Abort composition?" and
"(enter = confirm, esc = cancel)".

Add TestDialogueBoxRendersInIndex - arm the dialogue directly on an
index model (`m.dialogue = &dialogue{kind: dialogueInput, label: "go: "}`),
render, assert the box content row shows and the frame is m.height lines
and the status line is the last row (the dialogue is modal and cannot be
armed in index by actions today - the direct arm pins the render path).

- [ ] **Step 5: run the checks**

```bash
cd /home/user/git/opencode/notmutt/src
gofmt -l tui/ config/ compose/ app/ core/ references/notmuch/ cache/   # empty (vendor hits pre-existing)
go vet ./...
go build ./...
go test ./...
```

- [ ] **Step 6: commit**

```bash
cd /home/user/git/opencode/notmutt
git add src/tui/model.go src/tui/compose.go src/tui/model_test.go
git commit -m "feat(tui): render the prompt dialogue as a boxed overlay

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## Verification

Final gate:

```bash
cd /home/user/git/opencode/notmutt/src
gofmt -l .                    # only pre-existing vendor/ + references/notmuch/cli_test.go hits
go vet ./...
go test ./...
```

Acceptance (manual - the user does the interactive pass, never run
./notmutt with real mail interactively):
1. Index: a dialogue box with border rows renders above the status line;
   the status line stays last; the frame is exactly terminal height;
   keyhint is covered while open, back when closed.
2. Compose: q opens "Abort composition? (enter = confirm, esc = cancel)";
   enter aborts, esc returns to editing; j/k form keys stay dead while
   the confirm is open; a opens the attach prompt in the box; typing
   renders, esc cancels; '?' still lists attach commands with the prompt
   visible underneath; "@yazi" + enter still runs the picker.
3. The compose keyhint stays on the top row the whole time.

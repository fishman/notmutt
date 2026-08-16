# Prompt dialogue - design spec

A mode-independent prompt dialogue: one lipgloss-bordered box, rendered
above the status line in every frame, used to confirm or cancel and to
enter text requests. Generalizes the compose prompt machinery (attach
path, field prompts) and the abort confirm into a single widget.

## 1. Goal and acceptance

One dialogue widget replaces `formPrompt` and the `PhaseAborting`
confirm: any mode can open a confirm (enter to confirm, esc to cancel)
or an input prompt (typed text delivered to a consumer). It renders as
a 3-row lipgloss box (border top, content row, border bottom) spliced
over the three rows directly above the status line. Frame invariant
holds: every frame is still exactly m.height lines, status line last.

Acceptance (scripted tests; item 6 is manual):

1. Frame shape in index, pager, and compose: with a dialogue open, the
   three rows above the status line are the box (border row, content
   row, border row); total frame height is unchanged; the status line
   is untouched.
2. Confirm: enter dispatches the confirm action; esc closes the
   dialogue without dispatching; printable keys do nothing.
3. Input: printable keys append to the input; backspace trims; enter
   delivers to the consumer; esc discards.
4. The compose consumers work through the dialogue: attach path
   (plain path attaches, `@cmd` arms the exec, `?` opens the command
   picker with the dialogue open underneath), from/to/subject/cc/bcc/
   replyto split-addr fields.
5. Abort in compose arms `dialogue{confirm, "Abort composition?",
   action: "abort"}`; enter aborts, esc returns to editing.
6. Manual: the box renders with the config border glyphs (indicator
   fg on the frame background), content row indicator-styled, and the
   compose keyhint stays at its top position while the box sits above
   the status line.

## 2. The dialogue widget

`src/tui/dialogue.go` (new):

```go
type dialogueKind int

const (
	dialogueConfirm dialogueKind = iota
	dialogueInput
)

type dialogue struct {
	kind   dialogueKind
	field  string // input consumer: attach | from | to | subject | cc | bcc | replyto
	label  string
	input  string
	action string // confirm kind: action to dispatch on confirm
}
```

- `m.dialogue *dialogue` replaces `m.prompt *formPrompt`; formPrompt
  is deleted (DRY - one prompt system).
- The `field` consumers are the model-side branches promptKey already
  has: attach path (enter -> @cmd exec / ExpandHome + AddAttachment),
  split-addr fields into the compose tab state. The consumers do not
  change, only the struct that carries them.
- Confirm's `action` dispatches through `dispatchAction` - the binding
  action vocabulary, already pinned by validateBindings.

## 3. Key handling

`dialogueKey(msg tea.KeyPressMsg) tea.Cmd` replaces `promptKey`. The
Update gate keeps its position (before mode dispatch) so the dialogue
captures keys in every mode:

- printable -> append to input (confirm kind ignores text keys)
- backspace -> trim a rune (input kind)
- enter -> confirm: dispatchAction(action), dialogue = nil; input:
  the field consumer, dialogue = nil on success, stays open on
  consumer failure (unknown @cmd keeps the prompt open, today's
  behavior)
- esc -> dialogue = nil, no side effect

The attach `?` picker arming (kind attach, input empty) opens the
fuzzy picker with the dialogue still open; fuzzySelect prefills
`dialogue.input = "@" + entry` (the existing prefill moves from
prompt to dialogue).

## 4. Rendering

`overlayDialogue(frame string) string` splices the box over the three
rows immediately above the status line (index/pager: the keyhint row
and the two rows above it; compose: three preview rows). The
derivation is the preview popup's: config border glyphs (Glyphs.
BorderTL/TR/BL/BR/H/V), BorderForeground = indicator fg,
BorderBackground = normal bg, content width = frame width minus the
borders. The content row is indicator-styled, sanitized (F1), padded
to full width, truncated to the inner width. Confirm content row:
`label + " (enter = confirm, esc = cancel)"`. Box height 3 exactly;
a terminal too small (height < 5) leaves the frame untouched.

- renderIndex: `overlayDialogue` after `overlayPreview` (the preview
  popup and the dialogue cannot be open together in practice - the
  dialogue is opened by keys, the popup by the P preview action - but
  the splice order makes the dialogue win).
- renderPager: overlay on the pager frame.
- renderCompose: the prompt/abort-confirm switch at the top row is
  deleted (compose.go:45-49) - the keyhint row is always the top row;
  the dialogue splices above the status line like every other mode.

## 5. Compose abort

The abort action arms `dialogue{confirm, "Abort composition?",
action: "abort"}` instead of a dedicated confirm row; enter dispatches
abort, esc returns to editing. `PhaseAborting` survives as a dispatch
marker only (render state, not a UI branch): confirm-enter's abort
action lands on the PhaseAborting branch which closes the tab - without
the marker the same action would re-arm the dialogue forever. Esc from
the confirm resets PhaseAborting to PhaseEditing. The renderCompose
PhaseAborting branch and the "q to confirm" semantics are gone (one
explicit enter, no accidentally-quit-on-any-key).

## 6. Out of scope

- No new binding triggers (quit-confirm, delete-confirm, search
  prompts): the API exists, consumers come when wanted. YAGNI.
- The fuzzy picker stays a separate full-frame surface.
- No dialogue persistence (session-local, like the staged buffer).

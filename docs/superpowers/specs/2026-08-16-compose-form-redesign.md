# Compose form redesign - design spec

The compose form restructures around hotkey editing: the sender-info
settings block renders as a two-column table (labels right-aligned at
the colon, in theme blue), navigation (j/k) moves only within the
attachment list, every field edits through its own hotkey, and the
prompt dialogue shares the form's styling (blue label, normal entry,
no background fill on the text).

## 1. Goal and acceptance

Acceptance (scripted tests; the last item is manual):

1. The settings rows (Account, From, To, Cc, Bcc, Subject, Reply-To,
   Fcc, Security) render as a two-column table: each label right-
   aligned in a fixed column (the colons align at the seam), the value
   in the second column. Labels carry the `compose.label` style
   (theme blue, onedark #61afef); values render in the normal style.
2. j/k (form-down/form-up) move only within the attachment list,
   clamping to [8, 8+n-1]; with no attachments they are no-ops. The
   settings rows never take focus; no row highlights outside the
   attachment list.
3. Field hotkeys: c account (unchanged), f From, t To, x Cc, b Bcc,
   s Subject, r Reply-To, S Security cycle (all arm the prompt
   dialogue prefilled with the current value except c/S), e the body
   editor unconditionally (every slot). a attach and d detach (the
   attachment under the cursor) unchanged, y send, q abort unchanged.
4. form-down/form-up descriptions read "Move to the next/previous
   attachment".
5. The prompt dialogue content row uses the same styling: the label in
   `compose.label` blue, the entry text in the normal style, no
   background fill on the text (the indicator-styled content row is
   gone). The box border keeps its colors.
6. `compose.label` resolves through the theme machinery (style hex >
   variant palette > base palette > defaults), is present in the
   builtin onedark theme and in the DefaultStyles fallback, and is
   honored by the store's clone.
7. Manual: the form shows the blue label column, the colons align, and
   the attachment rows highlight as the only navigable rows; the
   prompt dialogue shows "From:" in blue with plain entry text.

## 2. Layout

The form rows are still one list the viewport windows, in the same
order as today (settings, divider, content-type, attachments, divider,
preview pager). What changes:

- The settings rows split into label + value cells. The label column
  width is the widest label including its colon (9 cells: "Security:",
  "Reply-To:"); every label right-aligns in it, so all colons sit at
  the seam. The value cell is truncated to the remaining row width
  (the row never word-wraps; the caller's row padding completes the
  width). Labels are constants, so byte length is cell width.
- The cursor slots shrink to the attachment list: slot 8+i for
  attachment i; every settings row and the static rows (dividers,
  content-type) are slot -1. formIdx starts at 8 (the first attachment
  slot; a phantom with no attachments - no row to highlight).
- The content-type row, the dividers and the attachment rows render
  exactly as today.

## 3. Navigation and hotkeys

- form-down: `formIdx++` clamped to 8+n-1; form-up: `formIdx--`
  floored at 8. Both no-op when the attachment list is empty.
- detach clamps the cursor back into [8, 8+n-1] after the removal
  (floor 8 when the last attachment went).
- "edit" (e) is the body editor unconditionally: the slot switch is
  deleted. The PhaseSending gate and the PhaseFailed reset stay.
- The field editors extend the existing t/s/f case: x/b/r arm the
  dialogue with field cc/bcc/replyto, prefilled with the joined
  current values, delivered through the existing SplitAddrs consumers.
- "security" (S) cycles st.Security (none -> sign -> encrypt ->
  sign+encrypt -> none), gated against PhaseSending like the field
  editors.
- The account picker keeps its c binding; the slot-aware 'e' branches
  it replaced are gone.

## 4. Styling

`compose.label` is a new style id following the dotted-id pattern
(tabbar.active is the shape): the StyleTable gains a nested "compose"
section with a "label" style, unmarshalled strictly (any other key in
the compose section is a load error), resolved to the id
"compose.label", present in the builtin onedark theme (base0D - the
author blue) and in the DefaultStyles fallback (#61afef), and cloned
by the store. The style inherits fg/bg from normal when either is
empty (R11), and the resolved style carries the normal background so
the label cell's width padding fills the frame.

## 5. Prompt dialogue styling

The dialogue content row (overlayDialogue) splits label and entry:
the label renders with `compose.label` (blue), the entry with the
normal style, both sanitized (F1) and truncated so the line never
exceeds the box's inner width. The indicator-styled content row is
gone - no background fill on the text. The confirm hint keeps its
text, in the normal style. The box border is untouched.

## 6. Out of scope

- No new navigation keys, no changes to the preview pane, the fuzzy
  picker, the keyhint row layout (new keys truncate like any long
  row), or the emacs scheme beyond binding the new actions.
- Attachment size display stays "(N bytes)"; the content-type row
  stays text-only.

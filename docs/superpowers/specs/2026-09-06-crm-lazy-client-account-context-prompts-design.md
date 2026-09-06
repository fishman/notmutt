# CRM workflow revisions: lazy client, account context, AI-prompt drafts

## Status

Design approved 2026-09-06. Replaces three behaviors of the HubSpot
follow-up workflow (spec 2026-09-03-hubspot-workflow-design.md):

1. The client and its token resolve eagerly at startup and live for the
   process. New: per-operation construction, key wiped after each op.
2. The workflow has no mail-account notion. New: `[crm.hubspot] account`
   selects the account whose context file and `[ai-data]` grant the draft
   leg uses.
3. `d` runs a fixed built-in draft. New: `d` opens the standard AI prompt
   picker over CRM-flagged prompts (built-in defaults ship), and the
   chosen prompt drafts the mail.

Everything else (pull paging, analyze/briefing, write-back on send and
dismiss, queue surface, provider-neutral core) is unchanged.

## 1. Token lifecycle: on demand, wiped when done

`crmWire` stops resolving anything at startup. It records the dormant
config and subscribes to the bus; no token_cmd runs, no key exists, until
the first operation.

The pull trigger moves from `RefreshRequested` (which fires on the `=`
mail refresh too) to a new `core.CrmOpened` event the TUI publishes on
first Q-open (where it publishes RefreshRequested today, guarded by the
existing crmPulled flag). Result: the token resolves only when the user
opens the queue, and `=` no longer side-pulls CRM.

Per-operation client factory. The adapter holds a factory, not a client:

- `newCrmClient(ctx) (crm.Client, func())` - runs the token_cmd argv
  (`ai.FetchKey`), builds the hubspot client, and returns it plus a wipe
  func. The key buffer is owned by the client (NewClient stores the
  slice by reference, no copy) - one buffer, one owner, no
  post-construction zero at the factory.
- `hubspot.Client` gains `Wipe()`: zeroes the stored key bytes. The
  wipe func calls it after the operation; the client reference then
  drops.
- Every operation path (pull run, analyze, dismiss mark, prompt draft,
  send write-back mark) builds a fresh client, runs, then wipes. The
  key's lifetime is one operation.

Expired-token refresh. The token_cmd output carries no TTL metadata,
so expiry is only observable as a 401. The client handles it there:
`do()` gains a token-resolver seam (nil = the static key, the test
posture). A 401 zeroes the old key buffer, runs the resolver (the
token_cmd argv), swaps in the fresh key, and retries the request once;
a second 401 surfaces as the error. Refresh-on-401 IS the expiry
handling - any API access that meets an expired token pulls a new one
on demand. The wipe covers whichever buffer is live at operation end.

Honest limitation, documented in code: Go cannot guarantee zeroing (GC
copies buffers; wipe is best-effort over the stored copy). The posture
is shortest practical lifetime plus best-effort zeroing; the escalation
path for a stronger guarantee is a token_cmd that fronts an external
secret holder (gpg-agent), config-side.

`crmPullSource` and the queue open guard now key on configured provider,
not on a built client - the surface opens when configured; the first
pull happens on open.

## 2. `[crm.hubspot] account`

New config key: `account = "gmail"`. Validated at load: must name a
configured `[accounts.<name>]` entry (load error otherwise). Empty =
today's fallback.

Draft leg only (analyze/briefing prompts stay neutral):

- Context file: account set -> `ai/accounts/<account>/default.md`
  (`aicmd.LoadAccountContext`); unset -> `ai/context/default.md`
  (`aicmd.LoadDefaultContext`). The note rides the draft-generation
  chat as the style override.
- Mail grounding: the grant resolves from the configured account's
  `[ai-data]` entry (no more thread-tag derivation). The from: query
  stays global (email-scoped) - scoping to the account's folder space
  would silently kill grounding for accounts whose mail is not under a
  matching folder prefix. No grant = no grounding (today's no-op).

## 3. CRM-flagged prompts and the picker

Prompt flagging. `aicmd.Command` gains `CRM bool`, parsed from a
`crm: true` frontmatter line (the prompt file format already carries
name/description/action/data/account_context/summary_context; unknown
keys are loader errors today, so the loader key set grows with the
field).

Built-in defaults. Two CRM-flagged prompts join the embedded seed set
`src/app/aicommands/prompts/` (seeded by `seedAICommands` next to the
existing built-ins):

- `follow-up.md` - "Follow-up": drafts a follow-up email from the
  contact, company, and briefing below.
- `reconnect.md` - "Reconnect": drafts a re-engagement email to a cold
  contact from the same context.

Both `action = "compose"`, `account_context = true`, no mail `data`
(CRM commands take the CRM context block, not a mail allowlist).

Picker flow. `d` on a briefed row opens the standard fuzzy picker (the
A-key machinery) listing only CRM-flagged prompts, resolved for the
configured account (account prompts plus global prompts, mirroring the
A-key narrowing). New tui seams: `SetCrmAIPromptSource(fn func()
[]AICommand)` (the app resolves the account internally) and
`onCrmAICommand(name, contact, extra)`.

Running a chosen prompt: one chat call on the resolved `[ai]` provider.
System = the prompt body plus a CRM context block (contact, company,
briefing) plus the account/default context note (section 2). User =
the picker's e-key extra, prefilled with the contact line and the
follow-up intent, editable. No streaming view (the thread-scoped
summary view has no thread here); the picker closes, the draft opens
the prefilled compose through the existing `crmOpenDraftCompose`
path, so send write-back (`crmComposeRefs` -> `RunMark`) is retained
unchanged. `d` without a briefing stays guarded, as today.

Replacement, not addition. The lib's `RunDraft` and its tests are
removed - the prompt run needs the app-side prompt loader, context
files, and chat plumbing, so the draft leg moves into the adapter.
`workflow.go` keeps `RunPull`/`RunAnalyze`/`RunMark` unchanged.

## 4. Errors

- Factory failure (token_cmd dies, bad key): the operation publishes
  `CrmRowError` naming the step; the queue stays usable. Errors never
  carry the token or its output.
- A chosen CRM prompt whose body fails to load: status-line error, no
  compose.
- `[crm.hubspot] account` naming an unknown account: load error.

## 5. Testing

- Factory: token_cmd runs once per operation (counting fake), not at
  startup; wipe called after each op; key bytes zeroed (assert on the
  returned buffer).
- Refresh: a 401 mid-operation re-runs token_cmd and retries the
  request once (httptest: 401 then 200 - two token_cmd runs, retry
  succeeds, live buffer zeroed at end); a second 401 surfaces the
  error, no retry loop.
- Trigger: first Q-open publishes `CrmOpened` and pulls; `=`
  (RefreshRequested) no longer pulls; no factory call before first
  open.
- Account: account set -> draft call carries the account context file
  text and the configured grant; unset -> default context text;
  grounding grant comes from the configured account, not thread tags.
- Picker: only CRM-flagged prompts listed; both built-in defaults
  parse with `crm = true`; choosing one opens the prefilled compose and
  send write-back still marks; `d` with no briefing stays guarded.
- `TestCrmHubspotWorkflow` reworked: CrmOpened trigger, per-op factory,
  picker-based draft leg; the RunDraft unit tests go with the code
  they covered.

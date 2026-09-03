# HubSpot follow-up workflow - design spec

A HubSpot integration: pull new contacts the user scanned (created in HubSpot
outside the client), review them in a queue, run a company analysis grounded
in the CRM record plus their website and web/news research, review that
briefing, then draft a personalized follow-up into a prefilled compose.
After a send (or a dismiss) the contact is marked in HubSpot so the pull
skips it. Trigger-to-draft answers came from a brainstorm handoff
(docs/hubspot-workflow-handoff.md); this spec pins the decisions.

Decisions locked in brainstorm:
- Trigger: review queue of unprocessed HubSpot contacts (newest first).
- Analysis input: HubSpot CRM record + company website and web/news research,
  run over the referenced `[ai]` provider connection (no arbitrary-domain
  fetches).
- Privacy: company/contact/web data reaches the LLM freely through the `[ai]`
  provider; inbound mail content joins ONLY through the aicmd gate (BuildContext
  + `[ai-data.<account>]` grant) when a thread with the contact's address exists.
- Draft flow: analysis briefing first, then a separate "draft from this" LLM
  call opens a prefilled compose. Nothing is ever sent automatically.
- Write-back: yes - marking the contact processed is written to HubSpot, which
  is the source of truth (no local dedup DB).
- Client architecture: purpose-built stdlib Go clients (option B). No OpenAPI
  codegen - none exists in the tree and ~6 endpoints do not justify one. A
  shared client core is extracted only when a second vendor appears (YAGNI).

The whole feature is `//go:build lua`: it calls the AI provider path
(`src/app/ai`, `src/app/ai_stream.go`) which is lua-gated today. The tag is a
feature gate, not a licensing carve-out - the new clients are stdlib-only and
Apache-clean, but default builds carry no AI/CRM/search code, mirroring the ai
package precedent.

## 1. Goal and acceptance

Goal: from a HubSpot contact created by a business-card scan to a
reviewable, personalized follow-up draft, with processed state written back.

Acceptance (scripted tests; item 9 manual):

1. Config: `[hubspot]` loads strictly (unknown keys = load errors, the R8
   rule). `[hubspot] provider` references one `[ai]` entry (empty = first
   configured); HubSpot's own token comes from `[hubspot] token_cmd`; no
   literal secret and no second AI-side token surface exists.
2. Pull (`ListUnprocessed`) lists contacts lacking the marker, newest first,
   paginated; refresh diff-and-inserts new rows into the queue view (the R3
   pattern, no full rebuild). A `created_after` knob (empty default) skips a
   pre-feature backlog.
3. Analyze on a row runs a cancellable background job (contact + company from
   HubSpot, research queries on the company domain/name over the `[hubspot]
   provider` connection, snippets -> non-mail context -> `ai.Chat`) and
   streams a briefing into the queue view's detail region. Row status
   advances only on success or explicit error.
4. Draft is enabled only when a briefing exists for the row. It composes a
   follow-up grounded in the briefing plus (gated) mail history and opens a
   prefilled compose (`To` from the contact address, subject, body) via the
   normal compose-open seam. Send is manual, unchanged (R4).
5. Mail-context grounding: only when an inbound thread with the contact's
   address exists AND `[ai-data.<account>]` grants it, the draft context is
   extended through `aicmd.BuildContext`. The briefing path structurally
   excludes mail content (asserted by test, section 8 item 3).
6. Write-back fires once per contact on `SendResult{OK}` for a compose tagged
   with that contact, and on a dismiss action: `MarkFollowedUp(id)` sets the
   marker property. Failure keeps the row visible with a retry action (the
   marker is eventually consistent; the sent mail is the durable artifact).
7. HubSpot and research clients are exercised against `httptest` servers that
   pin method, path, auth header, and request JSON; no live API in CI.
8. External failures surface as `JobError` on the bus; the row keeps its last
   status and remains retryable. No message content, token, or query in any
   log (F6).
9. Manual: end-to-end against a real portal with a private-app token and a
   fabricated company - scan-created contact appears, briefing reads, draft
   opens, send writes the marker back, next pull omits the contact.

## 2. Config

`src/config/config.go`, a new section parsed like `[ai]`:

```toml
[hubspot]
provider         = "deepseek"       # name of an [ai] provider driving the workflow; empty = first configured
token_cmd        = "cmd printing the private-app token"   # HubSpot's own secret, never literal
marker_property  = "notmutt_followed_up"                  # the processed marker
created_after    = ""                                     # RFC3339; empty = all unprocessed
```

- `provider` selects which `[ai]` entry drives the workflow. Multiple
  `[ai.<name>]` providers may be configured (the existing registry); the
  workflow references one by name. Analysis, research, and draft all ride that
  connection: its `base-url` is the host and its `pass_cmd` (the `[ai]` secret
  field) is the auth - there is no second provider or token surface. Empty =
  the resolveAIProvider rule (first configured). A distinct research model is a
  future per-step override, not today's shape.
- `marker_property` is the property the pull filters on and write-back sets.
  It must exist in the portal (one-time setup, typically a checkbox property);
  an API error naming the missing property is surfaced verbatim so the config
  fix is obvious (the R14 refusal-style message). HubSpot private apps may not
  create custom properties - if the portal cannot, the operator pre-creates it.
- The HubSpot host is a compile-time constant (`api.hubspot.com`); no
  `[lua.network]` allowlist is involved - the Go client is stricter than a
  config allowlist by construction. The research/analysis host is the
  referenced `[ai]` entry's `base-url`, which already exists as config; the
  workflow adds no new host surface. A config-driven HubSpot base URL is
  deliberately not offered (deny-by-default; no new knob without a need).
- `[hubspot]` is portal-scoped, not per mail account; the workflow does not
  depend on any account's folder/tag state except the opt-in `[ai-data]` grant
  for mail grounding.

## 3. Clients

Two small stdlib packages (`net/http` + `encoding/json`, the
`src/app/ai/ai.go` precedent), both `//go:build lua`:

`src/hubspot/client.go` - the CRM client. Bearer auth from `token_cmd`, fixed
base URL, per-request timeout, paging loop, rate-limit mapped to a retryable
error. Data types (Contact, Company) carry only the fields the workflow uses.

- `ListUnprocessed(createdAfter string) ([]Contact, error)` - the search
  endpoint, filter "marker property is unset", sort by createdAt desc, page
  until exhausted.
- `Contact(id) (Contact, error)` + associated company id.
- `Company(id) (Company, error)` - name, domain, industry, size, description.
- `MarkFollowedUp(id) error` - set `marker_property`.

`src/search/client.go` - the research call: queries on the company domain and
name over the referenced `[ai]` provider connection (base-url + `pass_cmd`
from that `[ai]` entry; no own token). Returns `[]Result{Title, URL, Snippet}`
with a cap on count and per-result length (bounded, like the aicmd body caps).
Whether research is a distinct search/extract endpoint on that connection or
is served by the model itself is a per-provider detail - a provider that
fronts neither limits research to model knowledge, an accepted constraint of
riding one connection (the hubspot client below is unaffected).

Endpoint paths are illustrative; the exact HubSpot and research request shapes
are pinned in the plan and locked by the httptest tests.

## 4. The workflow driver

`src/app/hubspot_engine.go` (new, lua-gated) mirrors `src/app/ai_engine.go`
and `src/app/send.go`: package-private job funcs launched on fresh goroutines,
publishing to `core.Bus`, cancellable via the existing Task machinery.

- `runHubspotPull` - calls `ListUnprocessed`, publishes queue rows.
- `runHubspotAnalyze(contact)` - gathers contact + company, runs research
  queries over the `[hubspot] provider` connection, assembles a non-mail
  context, streams the briefing via `ai.Chat` on that connection. Publishes
  progress; cancellable mid-stream.
- `runHubspotDraft(contact, briefing)` - resolves the contact's email address
  and searches for an inbound thread with it across accounts; grounding is
  applied only from accounts that hold mail from that address AND whose
  `[ai-data.<account>]` grant permits it (each account contributes through
  `BuildContext` independently; no grant, no contribution). Composes the
  follow-up, prefills a `compose.State` (`To`, subject, body), publishes
  `compose.ToEvent`.
- `runHubspotMark(id)` - `MarkFollowedUp`, called on send-OK and on dismiss.

Context assembly for the briefing is a sibling of `BuildContext`, not a
reuse: `BuildContext` is structured around mail messages (body caps, quoted
stripping, attachment dropping). The briefing assembler takes Contact +
Company + research Results into a bounded prompt string; it accepts no mail
input, so mail content cannot reach it by construction.

## 5. Review-queue view

A new list buffer (row model + transitions out of the UI, R5). Rows:
`name | company | title | created | status`. Statuses: `new`, `briefing`,
`drafted`, `sent`, `dismissed` (row leaves the queue once write-back lands).
Selection shows the briefing in a detail region of the view.

The row model and this surface are provider-neutral: the core type is
`CrmContact` carrying a `provider` id - the routing key (`hubspot` today; a
future CRM is a new value, no core/TUI rename). Core and TUI never name a
vendor; only the vendor clients and the engine do. The engine fills rows with
`provider = "hubspot"` and the surface dispatches every action with the full
row so the owning engine filters on its provider.

Row actions (vim scheme, R9 declarative bindings, configurable):
- `a` analyze - runs the briefing job; guard: not while a job is running on
  this row.
- `d` draft - enabled only with a briefing present; opens prefilled compose.
- `x` dismiss - write-back marker; row leaves on next refresh.

A sent or dismissed row stays visible (status `sent`/`dismissed`) until its
write-back lands, then leaves on refresh; a failed marker keeps it visible and
retryable (section 7).

Pull runs on queue open and on refresh (R3 diff-and-insert). Refresh never
clobbers a briefing or an in-flight status (the reconcile-then-replay spirit
of R14).

## 6. Privacy and security boundaries

- Mail content reaches the LLM only via `aicmd.BuildContext` at the draft
  step, under the existing per-command allowlist + `[ai-data]` grant. The
  briefing path accepts no mail input (section 4).
- HubSpot JSON and research snippets are foreign input. All rendered or
  prompt-bound strings pass `core.SanitizeText`; research/briefing content is
  length-capped before the prompt; JSON is decoded with `encoding/json` and
  never trusted as safe text (F1).
- Secrets: the HubSpot token comes from `[hubspot] token_cmd`, argv exec (F4),
  never logged (F6). AI-side auth is the referenced `[ai]` entry's `pass_cmd`,
  reused rather than duplicated - there is no second token surface to leak.
- Rows/briefings are session-local like `lastAIOutput`; no new persistence.
  HubSpot is the processed-state store; compose drafts persist through the
  existing saveDraft/draft seams.

## 7. Error handling and resilience

- Any external call failure -> `JobError`; the row retains its last status and
  stays in the queue, retryable. No silent drops.
- Timeouts on every call (the ai-package posture); analyze is a cancellable
  Task so a mid-stream abort leaves the row at its prior status.
- Write-back is retryable: send-OK/dismiss mark; a failed marker leaves the
  row with its state so `x`/`a` retries. Mail is the durable artifact.
- Paging failures stop the pull at the last good batch and surface the error;
  the queue keeps the rows already received.

## 8. Testing

1. Client tests: `httptest` servers pin method/path/auth-header/request JSON
   and exercise paging, rate-limit retry, and error mapping. A
   provider-resolution test pins that `[hubspot] provider` selects that `[ai]`
   entry (its base-url + pass_cmd) and that an empty value falls back to the
   first configured provider.
2. Pull test: fabricated contact set (generated names, never personal) -> the
   unprocessed filter and newest-first order, `created_after` honored.
3. Context-assembler test: fabricated research data in -> bounded non-mail
   context out; asserts the briefing builder cannot receive mail content.
4. Write-back rule test: send-OK and dismiss each call `MarkFollowedUp` once
   with the right id; a marker failure keeps the row.
5. Regression-test rule: locked tests stay untouched; new tests sit beside.

## 9. Out of scope

- OAuth - private-app `token_cmd` only.
- Scanning/OCR of a physical card inside notmutt (the scan happens in HubSpot's
  app and lands as a contact; notmutt consumes the contact).
- Contact create/edit/delete, company enrichment back to HubSpot.
- Scheduled send, multi-portal, contact images/attachments from the workflow.
- Non-HubSpot vendors - the trigger to extract a shared client core.

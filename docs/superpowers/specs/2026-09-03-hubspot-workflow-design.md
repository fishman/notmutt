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
- Client architecture: one `lua && crm`-gated subsystem `src/lib/crm` (package `crm`,
  the `src/lib/html` / `src/lib/crypto` precedent) holds the CRM core and stays
  vendor-neutral: `client.go` declares a neutral `Client` interface (including
  `Provider()`, the routing id) plus the `Contact`/`Company` domain types - no
  wire code, no vendor name. HubSpot is a concrete implementation under it:
  `src/lib/crm/hubspot` (package `hubspot`, the sibling-of-`crm` layering)
  implements `crm.Client` with stdlib `net/http` only - no OpenAPI codegen,
  none exists in the tree and ~6 endpoints do not justify one. Research, the
  briefing builder, and the workflow job bodies live in `crm`, take the
  `Client` interface, and publish rows/briefings/drafts carrying
  `client.Provider()`, so `crm` names no vendor. The lib injects the app-only
  capabilities it must not import: the bearer key is resolved upstream and
  passed to `hubspot.NewClient`, the `[ai]` chat call arrives as a ChatFn,
  gated mail grounding as a MailGroundFn. Code above the lib is a THIN adapter
  (`src/app/crm_engine.go` + `!lua || !crm` stub): it routes on `cfg.Crm.Provider`
  (today `case "hubspot"` builds `hubspot.NewClient`), resolves the key via
  `ai.FetchKey` and the AI entry via the existing resolveAIProvider rule,
  opens compose, and feeds tui hooks. The only vendor string above the vendor
  impls is `cfg.Crm.Provider` itself; a second CRM is a new subpackage + a new
  switch case, no core/TUI/lib rename.

The whole feature is `//go:build lua && crm`: it calls the AI provider path
(`src/app/ai`, `src/app/ai_stream.go`) which is lua-gated today. With crm off,
nothing from the feature compiles; a crm build without lua compiles nothing
either - both tags are required. The tag pair is a feature gate, not a
licensing carve-out - the new clients are stdlib-only and Apache-clean, but
default builds and crm-less lua builds carry no AI/CRM/search code, mirroring
the ai package precedent.

## 1. Goal and acceptance

Goal: from a HubSpot contact created by a business-card scan to a
reviewable, personalized follow-up draft, with processed state written back.

Acceptance (scripted tests; item 9 manual):

1. Config: the `[crm]` section loads strictly (unknown keys = load errors,
   the R8 rule). `[crm] provider` names the CRM engine; the per-vendor
   `[crm.hubspot]` table's `ai` references one `[ai]` entry (empty = first
   configured); HubSpot's own token comes from `[crm.hubspot] token_cmd`; no
   literal secret and no second AI-side token surface exists.
2. Pull (`ListUnprocessed`) lists contacts lacking the marker, newest first,
   paginated; refresh diff-and-inserts new rows into the queue view (the R3
   pattern, no full rebuild). A `created_after` knob (empty default) skips a
   pre-feature backlog.
3. Analyze on a row runs a cancellable background job (contact + company from
   HubSpot, research queries on the company domain/name over the `[crm.hubspot]
   ai` entry's connection, snippets -> non-mail context -> `ai.Chat`) and
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

`src/config/config.go`, a vendor-neutral `[crm]` section with nested
per-vendor tables, parsed strictly like `[ai]`:

```toml
[crm]
provider = "hubspot"                    # which CRM engine; "" = dormant, "hubspot" today

[crm.hubspot]
ai              = "deepseek"             # an [ai] entry driving the workflow; empty = first configured
token_cmd       = "cmd printing the private-app token"   # HubSpot's own secret, never literal
marker_property = "notmutt_followed_up"  # the processed marker
created_after   = ""                     # RFC3339; empty = all unprocessed
```

- `[crm] provider` names the CRM engine the client runs - the same routing
  idea as the `provider` id on the neutral row model (section 5). Each
  engine's parameters live in its own sub-table (`[crm.hubspot]` today); a
  future CRM adds a `provider` value and a sibling sub-table. Config may name
  vendors (it feeds the vendor engine, not core/tui), but the section it sits
  under stays vendor-neutral: `crm`, not `hubspot`. Empty `provider` = the
  feature is dormant; a non-empty `provider` must name a vendor whose table is
  configured, else a load error.
- `[crm.hubspot] ai` selects which `[ai]` entry drives the workflow. Multiple
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
- The `[crm]` section is portal-scoped, not per mail account; the workflow
  does not depend on any account's folder/tag state except the opt-in
  `[ai-data]` grant for mail grounding.

## 3. The subsystem: `src/lib/crm` (neutral) + `src/lib/crm/hubspot`

The neutral package `src/lib/crm` holds the vendor-independent core (`net/http`
is confined to vendor subpackages; `crm` itself imports only context/config/
core/stdlib - the lib layering rule: nothing from app/tui/notmuch/compose):

- `client.go` - the neutral seam. `Client` is an interface: `Provider() string`
  plus `ListUnprocessed`, `Contact`, `Company`, `MarkFollowedUp`. `Contact`/
  `Company` are the domain types, carrying only the fields the workflow uses.
  No wire code, no HTTP, no vendor name lives here.
- `research.go` - `Research(ctx, p config.AIProvider, company Company, chat
  ChatFn) ([]Result, error)`: queries on the company domain and name over the
  referenced `[ai]` entry's connection. The chat call is INJECTED (`ChatFn`,
  defaulted to `ai.Chat` by the adapter), so the lib rides the `[ai]` config
  without importing the lua-gated ai package. Returns `[]Result{Title, URL,
  Snippet}` with a cap on count and per-result length (bounded, like the aicmd
  body caps). Whether research is a distinct search/extract endpoint on that
  connection or is served by the model itself is a per-provider detail - a
  provider that fronts neither limits research to model knowledge, an accepted
  constraint of riding one connection.
- `briefing.go` - the non-mail context assembler: `briefing(contact Contact,
  company Company, rs []Result) (string, error)`. It accepts no mail input, so
  mail content cannot reach it by construction (section 8 item 3).
- `workflow.go` + `crm.go` - the job core: `runPull`, `runAnalyze`,
  `runDraft`, `runMark` publish core events over an injected `*core.Bus`
  (queue snapshot, briefing, draft, row error) and carry the shared types.
  Every job takes `client Client` and stamps rows/briefings/drafts with
  `client.Provider()` - the routing id comes from the client, never a literal.
  `runDraft` takes the injected ChatFn and MailGroundFn; grounding is "an
  inbound thread with the address exists AND its `[ai-data.<account>]` grant
  permits it, else empty (no mail context)".

The vendor implementation is `src/lib/crm/hubspot` (package `hubspot`, the
parent-imports-interface shape reversed: the impl imports the parent for
`crm.Client`). It holds `NewClient(ctx, key []byte) *Client`, the wire structs,
the paging loop, and the HTTP error mapping (429/5xx -> a retryable sentinel
surfaced through the interface). Fixed base URL, per-request timeout (the
ai-package posture). `Provider()` returns `"hubspot"`. Its methods:

- `ListUnprocessed(createdAfter string) ([]Contact, error)` - the search
  endpoint, filter "marker property is unset", sort by createdAt desc, page
  until exhausted. Search rows carry no association, so returned contacts have
  an empty `CompanyID`; the per-row company comes from an analyze-time
  `Contact(id)` refetch (never an N+1 in the pull).
- `Contact(id) (Contact, error)` + associated company id.
- `Company(id) (Company, error)` - name, domain, industry, size, description.
- `MarkFollowedUp(id, marker string) error` - set `marker_property`.

Endpoint paths are illustrative; the exact HubSpot and research request shapes
are pinned in the plan and locked by the httptest tests.

## 4. Workflow core + app adapter

The job core lives in the lib (`src/lib/crm/workflow.go`): `runPull`,
`runAnalyze`, `runDraft`, `runMark` are launched on fresh goroutines from the
adapter, publish to `*core.Bus`, cancellable via the existing Task machinery.
Each takes its capabilities as injected args - the `crm.Client` interface, the
resolved `config.AIProvider`, the ChatFn, the MailGroundFn - so none of them
needs an app import, and none names a vendor.

`src/app/crm_engine.go` (new, `lua && crm`-gated) is a THIN adapter mirroring
`src/app/ai_engine.go` and `src/app/send.go`. It no-ops unless
`cfg.Crm.Provider != ""`, and builds the vendor client by switching on that
value (`case "hubspot": hubspot.NewClient(ctx, key)` from
`src/lib/crm/hubspot`), then:

- Resolves the bearer key via `ai.FetchKey(hs.TokenCmd)` and the AI entry via
  the existing resolveAIProvider rule (`hs.AI`, empty = first configured),
  supplies the ChatFn (default `ai.Chat`), and builds the MailGroundFn that
  returns "" unless an inbound thread with the contact's address exists AND
  its account's `[ai-data.<account>]` grant permits it (each account
  contributes through `BuildContext` independently; no grant, no
  contribution).
- On a queue-ready event launches `runPull`; on `core.CrmDraft` opens the
  compose - prefills a `compose.State` (`To`, subject, body), publishes
  `compose.ToEvent` - and tracks `composeID -> {Provider, ContactID}` for the
  send hook (Provider from the draft; the send hook matches it against the
  client it built, i.e. `cfg.Crm.Provider`); on send-OK/dismiss launches
  `runMark`; on row errors drives write-back. Supplies the queue surface into
  tui hooks (the SetAICommandSource/Handler shape); the `!lua || !crm` build
  carries a stub.

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
vendor; only the vendor subpackage and the adapter (via `cfg.Crm.Provider`) do.
The engine fills rows with `client.Provider()` and the surface dispatches every
action with the full row so the owning engine filters on its provider.

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
- Secrets: the HubSpot token comes from `[crm.hubspot] token_cmd`, argv exec (F4),
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
   provider-resolution test pins that `[crm] provider` routes to the hubspot
   engine and that `[crm.hubspot] ai` selects that `[ai]` entry (its base-url
   + pass_cmd), an empty `ai` falling back to the first configured provider.
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

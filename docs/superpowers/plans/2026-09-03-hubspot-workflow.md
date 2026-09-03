# HubSpot follow-up workflow implementation plan

> **For agentic workers:** implement task-by-task; each task ends with a
> runnable check. The spec is
> `docs/superpowers/specs/2026-09-03-hubspot-workflow-design.md` (normative).
> Steps use checkbox syntax for tracking.

**Goal:** from a HubSpot contact created by a business-card scan, a
reviewable company briefing and a personalized follow-up draft, with
processed state written back to HubSpot.

**Architecture:** purpose-built stdlib Go clients (`src/hubspot`, `src/search`)
behind the `lua` build tag (they call the lua-gated `ai` package), a lua-gated
`src/app/hubspot_engine.go` + `!lua` stub (the ai_engine split), and a new
non-mail queue surface in `tui`. Core and tui stay vendor-neutral: rows are
`core.CrmContact` carrying a `provider` id that routes actions to the owning
engine; nothing in `core`/`tui` names HubSpot. The surface stays inert in
default builds via hook setters. Analysis/research/draft all ride one `[ai]`
entry named by `[hubspot] provider`; only the HubSpot portal token is
`[hubspot] token_cmd`.

**Tech Stack:** Go stdlib `net/http` + `encoding/json`; `//go:build lua`;
existing `ai` (Chat/FetchKey/aiStream), `aicmd`, `compose`, `core.Bus`,
notmuch worker, tui search-tab/index machinery. No new dependencies.

**Docs:** this plan + the spec are committed first (Co-Authored-By: Deepseek).
Code commits carry no co-author. The mailbox privacy rule applies: never run
./notmutt interactively; the user does the interactive pass.

---

## Task 1: docs (this plan + spec committed)

Then code.

## Task 2: `[hubspot]` config

**Files:** `src/config/config.go`, `src/config/config_test.go`

Add `Hubspot HubspotConfig` to `Config` (`toml:"hubspot"`, near the AI fields,
config.go:136-243) and validate in the existing strict-load walk (find where
`[ai]`/`pass_cmd` are validated, config.go:1817-1819, and add beside):

```go
type HubspotConfig struct {
    Provider       string   `toml:"provider"`         // an [ai] entry name; empty = first configured
    TokenCmd       []string `toml:"token_cmd"`        // argv printing the portal token
    MarkerProperty string   `toml:"marker_property"`  // default "notmutt_followed_up"
    CreatedAfter   string   `toml:"created_after"`    // RFC3339; empty = all unprocessed
}
```

Validation rules:
- `token_cmd` must be non-empty with no blank argv elements (mirror the
  ai pass_cmd rule at config.go:1817).
- empty `provider` is allowed (resolveAIProvider falls back); a non-empty
  `provider` must name a configured `[ai]` entry, else a load error naming it.
- `marker_property` defaults to `notmutt_followed_up` when blank.
- `created_after`, when set, must parse as RFC3339, else a load error.
- unknown `hubspot.*` keys = load errors (the strict rule already enforced).

**Check:** `cd src && go test ./config/` (new strict-load tests green; existing
`TestConfigUnknownKeys`-style tests still green). Empty-hubspot config loads.

## Task 3: queue bus events

**Files:** `src/core/bus.go`

The tui surface and the lua engine are in different build tags; both must speak
core types. Add plain data event structs beside `AiChunk`/`AiResult`
(bus.go:389-417) - inert, default build, produced only under lua (the Ai*
precedent):

```go
type CrmContact struct {        // one queue row; provider-neutral - core never names a vendor
    Provider                       string // CRM source, the routing id: "hubspot" (a future CRM adds a value)
    ID, Email, First, Last, Title  string // ID is provider-local
    Company                        string
    CreatedAt                      time.Time
    Status                         string // new|briefing|drafted|sent|dismissed
}
type CrmQueue struct{ Contacts []CrmContact }
type CrmBriefing struct{ Provider, ContactID, Text string }
type CrmRowError struct{ Provider, ContactID string; Err error } // JobError-style, F6-safe
```

`CrmQueue` gets a last-value snapshot in `newBus` (bus.go:35-49, the
list at 38-49). `CrmRowError` needs no snapshot. The HubSpot engine
(provider value `"hubspot"`) produces and consumes these; nothing in `core` or
`tui` names HubSpot.

**Check:** `cd src && go build ./core/`.

## Task 4: HubSpot client

**Files:** `src/hubspot/client.go`, `src/hubspot/client_test.go` (both
`//go:build lua`; the package imports `notmutt/app/ai` for `FetchKey`)

Constants/API:

```go
const apiBase = "https://api.hubspot.com"

type Client struct{ key []byte; hc *http.Client }

func New(ctx context.Context, tokenCmd []string) (*Client, error) // key = ai.FetchKey(ctx, tokenCmd)
func (c *Client) ListUnprocessed(ctx context.Context, marker, createdAfter string) ([]Contact, error)
func (c *Client) Contact(ctx context.Context, id string) (Contact, error)          // + associated company id
func (c *Client) Company(ctx context.Context, id string) (Company, error)
func (c *Client) MarkFollowedUp(ctx context.Context, id, marker string) error

type Contact struct{ ID, Email, FirstName, LastName, JobTitle string; CompanyID string; CreatedAt time.Time }
type Company struct{ ID, Name, Domain, Industry, Description string }
```

Wire: bearer header `Authorization: Bearer <key>`; timeout = the ai-package
posture (per-request 30s, mirror ai.go:93); POST/PATCH bodies JSON.
`ListUnprocessed` hits `POST /crm/v3/objects/contacts/search` with
`filterGroups:[{filters:[{propertyName:marker, operator:"NOT_HAS_PROPERTY"}]}]`,
sort `createdate DESC`, page `limit:100` + `after` until the `paging.next.after`
link is absent; when `createdAfter` is set, add a `createdate > createdAfter`
filter. `Contact`/`Company` GET `/crm/v3/objects/contacts/{id}` (with
`associations` for company) and `/crm/v3/objects/companies/{id}`.
`MarkFollowedUp` PATCH `/crm/v3/objects/contacts/{id}` setting
`{properties:{marker:"true"}}`. Map HTTP 429 and 5xx to a retryable sentinel
error (`ErrRetry`); the company-missing association yields an empty `CompanyID`
(no error).

**Check:** httptest tests pin method/path/Authorization/request JSON for all
four calls, plus paging (two pages), 429 -> ErrRetry, and `Contact` without a
company association. `cd src && go test -count=1 -tags lua ./hubspot/`.

## Task 5: research over the `[ai]` connection

**Files:** `src/search/search.go`, `src/search/search_test.go` (both
`//go:build lua`)

Research rides the referenced `[ai]` entry (spec section 2/3): one model call
returns concrete current facts about the company; there is no separate token or
host. Inject the chat call so the parse/cap logic is testable without network:

```go
type ChatFn func(ctx context.Context, p config.AIProvider, model, system, text string, emit func(string)) (string, error)

func Research(ctx context.Context, p config.AIProvider, company hubspot.Company, chat ChatFn) ([]Result, error)
type Result struct{ Title, URL, Snippet string }
```

`Research` sends a system prompt ("you are a researcher; reply only as a
numbered list, one fact per line: <fact> | <source-url-or-UNKNOWN> | <2-line
snippet>") asking for concrete, current company facts (what they sell, size,
recent news). Parse each line into a `Result`; a line that fails to parse wraps
as one `Result{Snippet: line}`. Cap: 8 results, each snippet <= 600 chars
(truncate). Default `chat` = `ai.Chat`; `Research` never touches mail.

**Check:** unit test with a fake `ChatFn`: structured output parses into
capped `[]Result`; garbage output degrades to a single wrapping Result; caps
enforced. `cd src && go test -count=1 -tags lua ./search/`.

## Task 6: briefing context builder

**Files:** `src/app/hubspot_ctx.go`, `src/app/hubspot_ctx_test.go` (both
`//go:build lua`)

A sibling of `aicmd.BuildContext` (spec section 4) - non-mail, bounded, no mail
params in the signature:

```go
// briefing assembles the analysis prompt from CRM + research. It takes no
// mail input by construction: mail grounding is a separate gated call (Task 10).
func briefing(contact hubspot.Contact, company hubspot.Company, rs []search.Result) (string, error)
```

Layout: contact (name, title), company (name/domain/industry/description),
then research results. Caps mirror aicmd's: total <= 8000 chars, research
snippets already capped by `search.Research`. Empty contact/company render as
"unknown". Sanitize every field with `core.SanitizeText` before joining.

**Check:** `hubspot_ctx_test.go` with fabricated data: output bounded, fields
present, no mail fields can even be passed (the signature is the guarantee);
empty fields render "unknown". `cd src && go test -count=1 -tags lua ./app/ -run HubspotCtx`.

## Task 7: engine skeleton + build split

**Files:** `src/app/hubspot_engine.go` (`//go:build lua`),
`src/app/hubspot_engine_stub.go` (`//go:build !lua`), `src/app/app.go`

Mirror ai_engine.go:23-58 + ai_engine_stub.go exactly. Default `app.go` calls
two setters (place next to app.go:288-290); the symbols exist in both builds:

```go
// hubspot_engine.go (lua):
func hubspotPullSource() []tui.CrmCommand                  // stub: nil
func hubspotWire(ctx context.Context, bus *core.Bus, worker workerAPI, cfg config.Config, root string) // stub: no-op
// hubspot_engine_stub.go (!lua): matching no-op symbols, no lua imports
```

`app.go` (after the AI-command wiring, ~app.go:290):

```go
hubspotWire(ctx, bus, worker, cfg, root) // no-op in !lua builds
```

`hubspotWire` subscribes to `core.Bus` (the subscriber pattern at app.go:393
or ai wiring): on `RefreshRequested` it re-runs the pull; on `core.SendResult`
and `core.CrmRowError` it drives write-back (Tasks 11). Keep it a thin
subscriber; the job bodies are separate.

**Check:** `cd src && go build .` and `go build -tags "lua mcp" ./...`.

## Task 8: the pull

**Files:** `src/app/hubspot_engine.go`

`runHubspotPull(bus, worker, cfg, root)` mirrors `sendJob`'s fresh-goroutine
shape (app.go:343-345): on a `run` mutex guard, build the client
(`hubspot.New`), `ListUnprocessed`, publish `core.CrmQueue{Contacts}` as
the rows arrive (one publish per page, so refresh diff-and-inserts - the R3
shape). Map each `hubspot.Contact` to a `core.CrmContact` row here with
`Provider: "hubspot"` (client `FirstName`/`LastName`/`CompanyID` -> row
`First`/`Last`/`Company` via the company name lookup when loaded; `Status`
starts `"new"`). Any error
publishes `core.CrmRowError{Provider: "hubspot", Err}` and stops;
rows already received stay. Guard with the `running` mutex precedent
(refresh.go:39-47) so overlapping refreshes no-op.

**Check:** `go build -tags lua ./app/`; the pull path is exercised in Task 13's
httptest-driven integration test.

## Task 9: analyze -> briefing

**Files:** `src/app/hubspot_engine.go`

`runHubspotAnalyze(contact hubspot.Contact, ...)` - a cancellable job on the
row: fetch company (`hubspot.Company` via `CompanyID`; empty CompanyID skips to
research with company fields empty), `search.Research` on the company
name/domain over the `[hubspot] provider` entry, assemble with `briefing`
(Task 6), then stream the model's briefing text. Stream via `aiStream` on that
provider (ai_stream.go:54, the shape runAICommand uses) and publish
`core.CrmBriefing{Provider: "hubspot", ContactID: contact.ID, Text}`.
Cancellable through the existing
Task machinery (task.go registerTask/completeTask); abort leaves the row at its
prior status. Resolve the provider with `resolveAIProvider(cfg,
cfg.Hubspot.Provider)` (ai_draft.go:22).

**Check:** `go build -tags lua ./app/`. Behavior verified in Task 13 with a
fake provider (httptest OpenAI-compatible endpoint via the provider's
`base-url`).

## Task 10: draft -> prefilled compose

**Files:** `src/app/hubspot_engine.go`, `src/app/hubspot_draft.go` (lua)

`runHubspotDraft(contact hubspot.Contact, briefingText string, ...)`:
1. Ask the model (same provider) for the follow-up: system = "write a concise
   follow-up email to a person I met and whose business card I scanned; reply
   `Subject: <line>` on the first line, then the body". Prompt = the briefing
   plus any gated mail grounding (step 2). Parse subject/body; fall back to a
   default subject if the parse fails. Reuse `wrapEmail`/`wrapLines`
   (ai_draft.go:136-150) to wrap the body.
2. Mail grounding, gated: if an inbound thread from `contact.Email` exists, it
   joins only through `aicmd.BuildContext` under a per-account `[ai-data]`
   grant (the rule at spec section 4). Find the thread with a worker query
   `from:"<contact.Email>"` (ActQuery/ActThread, the ai_engine.go:75 flow);
   resolve the owning account from the thread tags; build context with a
   synthetic `aicmd.Command` whose `Data` equals that account's granted fields
   (command.go:26-41; the grant resolution is `AIDataGrant`). No grant = no
   mail context. The briefing path (Task 6) is untouched by this.
3. Open the compose: a `hubspotDraftCompose(cfg, root, contact, subject, body)
   *compose.State` sibling of `aiDraftCompose` (ai_draft.go:46-67) that sets
   `To` from `contact.Email`, subject/body, and reuses the newCompose/prefill
   fields (compose.go:79-90); publish `compose.ToEvent`. Track
   `composeID -> {Provider: "hubspot", ContactID}` in a session-local map (a
   package var like `lastAIOutput`, ai_engine.go:44-49) for the send hook
   (Task 11). Nothing is sent automatically.

**Check:** `go build -tags lua ./app/`; the map + subject/body parse get a
unit test in Task 13 (`hubspot_draft_test.go`, fake chat).

## Task 11: write-back on send and dismiss

**Files:** `src/app/hubspot_engine.go`

From `hubspotWire`'s subscribers (Task 7): on `core.SendResult{OK:true,
TabID}` where `TabID` is in the compose map, look up the entry and, when its
`Provider` is `"hubspot"`, call `runHubspotMark(ContactID)`:
`hubspot.MarkFollowedUp(id, marker)`. On success drop the map entry / publish
a queue update so the row leaves; on failure publish
`core.CrmRowError{Provider: "hubspot", ContactID: id, Err}` and keep the row
retryable (the `x`/`a` re-run covers retry). The marker is eventually
consistent; the sent mail is the durable artifact. Dismiss publishes
`core.CrmQueue` minus that contact only after the mark succeeds; the dismiss
handler filters on the row's `Provider` before dispatching (a neutral surface
must not assume HubSpot).

**Check:** `go build -tags lua ./app/`; the once-per-contact rule is pinned in
Task 13's test (fake client records calls).

## Task 12: TUI hooks + queue surface

**Files:** `src/tui/hooks.go`, `src/tui/crm.go`, `src/tui/crm_test.go` (all
default build)

`hooks.go` (inert unless set - the ai hook pattern at hooks.go:246-272):
`type CrmCommand struct{ Name, Desc string }`; `SetCrmPullSource(fn func()
[]CrmCommand)` defaulting to nil; `SetCrmActionHandler(fn func(action string,
c core.CrmContact))` defaulting to a no-op (the onAICommand shape,
hooks.go:265-272). The handler receives the full row, so dispatch is
provider-aware.

The queue surface is a new non-mail list buffer. Model its skeleton on the
search tab + index: `openSearchTab`/`runSearchQuery`/`ViewDiff`
(model.go:4507, refresh.go:386, core bus ViewDiff at core/bus.go:172) give the
list-view precedent, but rows are `core.CrmContact`, not a notmuch view. Add a
`crm.go` (default build - `core.CrmContact` and the Crm* events are default):
a small model + render holding `[]core.CrmContact`, selection, and the selected
row's `core.CrmBriefing` text rendered in a detail region under the list; row
actions map to the handler hook with the selected row: `a` -> analyze,
`d` -> draft (enabled only when a briefing is present for that row), `x` ->
dismiss. Rows come from subscribing to `core.CrmQueue`/`CrmBriefing` on the
bus (the subscriber wiring at model.go). Keep it to the existing renderer
primitives and theme objects (`normal`, `index_*`, `prompt`/`message`, R11
machinery) - no new dependencies. When no source is set (default build), the
surface never opens and the hooks are inert.

**Check:** `go build ./tui/` in default and `-tags lua`; `crm_test.go` for the
row model transitions (new -> briefing -> drafted/sent/dismissed) and the
`d`-requires-briefing guard, no screen needed.

## Task 13: wire the surface + integration tests

**Files:** `src/app/app.go`, `src/app/hubspot_engine.go`,
`src/app/hubspot_test.go` (lua), `src/tui/crm.go`

- Open path: register the queue surface behind a new index-mode keybinding
  (the binding map at key.go; mirror how the AI picker key opens). `hubspotWire`
  supplies the pull source and the action handler into tui hooks (the
  SetAICommandSource/Handler call shape at app.go:288-290), filling rows with
  `Provider: "hubspot"`. The surface dispatches every action with the full row;
  the handler filters `c.Provider == "hubspot"` before acting (a neutral
  surface must not assume a vendor). First open triggers `runHubspotPull`.
- `hubspot_test.go` (lua): drive `runHubspotPull`/`Analyze`/`Draft`/`Mark`
  end-to-end against an `httptest` OpenAI-compatible provider + an `httptest`
  HubSpot server: pull publishes `core.CrmQueue` rows with
  `Provider: "hubspot"`; analyze publishes a `CrmBriefing`; draft opens a
  `compose.State` with the contact address; send-OK and dismiss each call
  `MarkFollowedUp` once with the right id; a failing mark keeps the row (Task
  11 rule). Fabricated data only.

**Check:** `cd src && go test -count=1 -tags "lua mcp" ./app/ -run 'Hubspot'`
and `go test -count=1 -tags "lua mcp" ./tui/ -run Hubspot`.

## Task 14: full verification + docs sweep

**Files:** `docs/features.md` (if a feature list exists), nothing else

`cd src && go test -count=1 -tags "lua mcp" ./...` (locked tests green) and
`go test ./...` default. Add the HubSpot workflow line to any feature/roadmap
list the Lua-IPC work touched (match its wording). The user does the one
interactive pass against a real portal.

**Check:** both test runs green; docs updated.

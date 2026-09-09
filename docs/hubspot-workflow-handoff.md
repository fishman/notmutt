# HubSpot workflow - brainstorm handoff (parked thread)

NOT COMMITTED. Handoff for a fresh agent to continue the HubSpot design
brainstorm in notmutt. Mirror this task list in the new session:

- [#97] Explore project context for HubSpot design (in_progress here)
- [#98] Ask clarifying questions on the workflow (open)
- [#99] Propose 2-3 client architecture approaches (open)
- [#100] Present workflow design (open)
- [#101] Write design doc and transition to plan (open)

## Original ask (verbatim, the thread's goal)

> i want a hubspot integration some sort of workflow, pull in new contacts
> from hubspot (i scanned their business card) analyze company and send a
> follow up email, i'm not sure how the workflow should go. I'm not sure if
> i should implement generic api clients based on the http openapi tooling
> we built or if we should add a hubspot client hidden behind a build flag,
> but then every client we add will be a mess

Working shape (hypothesis, confirm with the user): from a scanned business
card -> a HubSpot contact; the workflow pulls that contact's CRM record /
company, runs an AI analysis of the company, and drafts a follow-up email.
Not settled: trigger shape, data landing, analysis scope, draft shape.

## What "http openapi tooling we built" is (established during #97)

The HTTP surface is a Lua tool, not a Go client: docs/lua.md:103 shows a
raw REST call from a Lua plugin:

    http.request("GET", "https://api.hubspot.com/crm/v3/objects/contacts", {...})

gated per-host by `[lua.network.<name>]` allowlists (docs/usage.md:281):

    [lua.network.hubspot]
    targets = ["*.hubspot.com"]

hostname-matched, never the bare domain (lua_http_test.go:324-329), and
strict config keys (config_test.go:294-327). This is the deny-by-default
network boundary the MCP layer mirrors. Whether an OpenAPI-driven client
layer exists on top is unverified - the new agent must confirm before
building any proposal on it.

## Seams the workflow would touch (finish this map in #97)

- Lua http tool + network allowlists: src/app/lua_http.go area,
  docs/lua.md (http.request example), config `[lua.network.*]`.
- AI command architecture: src/app/aicmd/context.go (the ONE controlled
  path mail content takes to an LLM - per-command field allowlist +
  explicit picker + configured `[ai]` provider; docs/usage.md "AI
  commands"). Recent commits: chain AI commands on the last summary with
  the e-key prompt amendment (03c4b17), streamed summary (2a46905),
  provider registry (cec0d8c/d477384).
- Jobs/bus action surface: core.Bus, worker actions (the async layer every
  UI action rides, R3/R4).
- Compose/send machinery: dialogue state machines + send_command (R4);
  follow-up draft = new compose prefilled with the mail context.
- Contacts/address corpus: whatever the client knows about senders
  (address book source, if any) - where a scanned business card's contact
  would match an inbound mail.

## Open questions to ask in #98 (one at a time, per the brainstorming skill)

- Trigger shape: per-mail action? global? tied to a contact/thread?
- Where does HubSpot data land: pager view, sidebar, a buffer?
- What does "analyze company" mean and what is its output?
- What should the follow-up draft look like and what context does it carry?
- Privacy boundary: can business-card/company data reach the LLM, and does
  the company analysis need the aicmd/context.go gated path or a new one?
- Success criteria: what makes this workflow feel done?

## Architecture question to weigh in #99 (the user's own framing)

- (A) Generic API client generated/derived from OpenAPI specs, reused for
  every future vendor (HubSpot is the first).
- (B) One purpose-built HubSpot client behind a build flag - which the user
  fears becomes "every client we add will be a mess".
A third option (e.g. thin per-vendor Lua scripts on the existing
http.request tool, config-as-client) belongs on the table. Trade-offs must
cite notmutt constraints: supply-chain policy (R7), build-tag-gated
extensions as the precedent (R12 dbus, R8 lua), deny-by-default network
boundary, no UI in core (R5).

## Standing constraints (hard, carry into the new session)

- Privacy: never submit mail content (bodies/headers) to an LLM except the
  aicmd/context.go path. Business-card/company data policy is exactly what
  #98 must pin.
- Scope: code only under src/. references/ is read-only source material.
- Commits: Conventional Commits, code commits carry no AI marker; doc/spec
  commits carry Co-Authored-By: Deepseek. Only commit when asked.
- Brainstorm flow: skill superpowers:brainstorming - explore, then one
  clarifying question at a time, propose 2-3 approaches, present design for
  approval, write spec to docs/superpowers/specs/YYYY-MM-DD-<topic>-design.md
  and commit (doc commit), then transition to writing-plans. No code until
  the user approves the design (HARD GATE).
- ASCII only in all output.

# CRM queue persistence: briefing cache + cross-client refresh

## Status

Design approved 2026-09-06. The CRM queue becomes restart-surviving and
cross-client-fresh: briefings persist on disk keyed by the contact's
revision, and held rows adopt a fresh pull's contact fields.

## Context

The queue's rows already survive restarts: a pull rebuilds them from
HubSpot (the source of truth), and a contact marked in another client
drops via the existing pull-driven reconciliation. What does NOT
survive:

- Briefings (the expensive AI calls) live in the adapter's memory cache
  and die on exit - every start re-analyzes every row.
- Held rows keep their OLD contact snapshot across a refresh: onQueue
  discards the fresh page's fields for held rows, so an edit made in
  another client (HubSpot UI, another instance) renders stale until the
  app restarts.
- The wire never fetches the contact's revision, so there is no cheap
  signal that a row changed.

## 1. Contact revision on the wire

- HubSpot search results carry a top-level `updatedAt` (the object
  revision timestamp, same shape as the already-parsed `createdAt`).
- `hubspot.contactWire` gains `UpdatedAt string json:"updatedAt"`;
  `contact()` parses it into `crm.Contact.UpdatedAt time.Time`.
- The neutral `crm.Contact` and the bus `core.CrmContact` gain
  `UpdatedAt time.Time`; `RunPull`'s mapping carries it.

No property-set change: `updatedAt` is a standard top-level field.

## 2. Briefing cache on disk (R13 discipline, second derived store)

A dedicated bbolt store, `crm-cache.db` in the XDG cache dir next to
`mime-cache.db` (0600, the cachePath precedent). Deliberate R1
exception: a second derived store, justified by the briefing cost -
pure cache, never authoritative, everything rebuilds from HubSpot.

- Shape: text keyed by string, a small dedicated store (NOT the
  attachment-shaped Cache interface - a second file, the same
  discipline): `Get(key string) (string, bool, error)`,
  `Put(key, value string) error`, `Close()`. Open failure degrades to
  cacheless (the cachejob pattern), never a startup error.
- Key: `provider + "\x00" + contactID + "\x00" + updatedAt.UnixNano()`
  - a contact edited in another client changes its revision, orphaning
  the stale briefing naturally (cached strings are attacker-influenced
  input; they pass the same SanitizeText path as fresh briefings, never
  trusted by virtue of being cached).
- Adapter wiring: the briefing cache (`crmBriefText`) consults disk on
  a miss and writes through on every `CrmBriefing`. The key now needs
  the row's UpdatedAt - `crmBriefingText(provider, id, updatedAt)` and
  `crmCacheBriefing` gain the revision parameter (the CrmBriefing event
  does not carry it; the analyze action does, and the prompt run has the
  row). Hydration is lazy per lookup, no startup walk.

## 3. Snapshot adoption in onQueue

`crmQueue.onQueue` keeps a held row's briefing/status/in-flight by key
(unchanged) but ADOPTS the page's fresh contact fields (identity,
company, title, timestamps, UpdatedAt) into the held row's contact - an
external edit renders on the next pull, and the row's briefing cache
key moves with the new revision (a stale briefing drops from the cache
naturally on the next write-through).

## 4. Tests

- Wire: the search mapping parses `updatedAt` (hubspot client_test).
- Cache: round-trip put/get, revision-keyed miss after an UpdatedAt
  change, open-failure degrades cacheless.
- onQueue: a held row adopts the fresh page's fields while keeping its
  briefing/status (new tui test beside the existing merge pins).
- Integration: two crmWires over the same cache file - the second reads
  the briefing without a second analyze (the fake AI server counts
  calls); a contact edit between pulls refreshes the row.

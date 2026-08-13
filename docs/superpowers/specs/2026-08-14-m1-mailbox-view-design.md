# M1: Mailbox view + foundation - design spec

Date: 2026-08-14. Status: approved for implementation planning.

Normative requirements: AGENTS.md (R1-R13). Security: SECURITY.md
(F1-F13). This spec is the M1 slice: the mailbox view (R3) with the
foundation underneath it (config store, event bus, notmuch worker,
MIME cache). Reference repos are advisory - they prove the mail
concept and the failure modes, never the implementation.

## 1. Goal and acceptance

M1's deliverable is the index/mailbox view working against the real
mailbox. Foundation exists to serve it. Acceptance:

1. Config load is strict: unknown key in config.toml = load error with
   file:line.
2. Real mailbox: `notmutt` opens, renders the inbox view async; the
   first page appears before the query finishes (budgeted batches).
3. Tag op from the index: optimistic apply, worker confirms, other
   views converge on the next refresh cycle.
4. Benchmark report: cgo vs CLI-per-query on the real 129k DB
   (first-page latency, full thread query, tag-op latency,
   lock-timeout behavior at lock_timeout=10). CLI-per-query is the
   default unless cgo demonstrably wins (SECURITY.md F10).
5. Diff property test + real-DB soak (section 9): the view converges.

## 2. Module layout

```
src/                  # Go module, this repo, top-level dir
  config/             # TOML schema, strict load, defaults, observers
  core/               # event bus, mailbox view model (no terminal imports)
  notmuch/            # worker: actions in, results out; backend behind interface
  cache/              # MIME cache: Cache interface + bbolt backend
  tui/                # BubbleTea views (minimal index for M1)
  app/                # main: wires store + bus + worker + view
```

No empty packages; send/, crypto/, filter/, lua/ appear when their
slices land. core/ is terminal-free (R5): the view model lives in
core, tui renders it.

## 3. Config store

TOML is the config language and the file shape IS the schema shape
(R8). No flat registry, no dotted-key store.

- Files: `~/.config/notmutt/config.toml`; accounts as `accounts/*.toml`
  later. M1 schema is minimal: `[ui] keymap` (enum vim|emacs), one or
  more `[view.<name>]` (query string, threads bool). The rest of R8's
  surface (tag-groups, filters, themes, bindings) lands with its slice.
- Load: BurntSushi/toml with metadata - unknown keys are load errors
  with file:line. Defaults struct merges under file values.
- Validators per section at load: enums; view query strings checked
  with a `notmuch count` dry run (DB is open anyway).
- Observers: typed per section. `SetKeymap`, `SetThemeVariant`,
  `SetView` are the single write path; each notifies its section's
  observers, which publish a ConfigChanged event on the bus. No ad-hoc
  disk reads anywhere.
- Changing a view's query triggers that view's full reload (section 7).

## 4. Event bus

One channel, typed message structs, dispatch on type (aerc worker
pattern). Publish/Subscribe; dispatcher uses a type switch, no
reflection. Messages for M1: QueryBatch, WorkerDone, CacheResult,
ConfigChanged, ViewDiff. Workers and UI both publish and subscribe -
the bus is the same spine R4 dialogue events and R12
ColorSchemeChanged ride. The BubbleTea program receives bus messages
via a relay bridge; the bridge is the only tui<->core seam.

## 5. Notmuch worker

- One worker owns DB access; actions in, results out: Open, QueryMsg,
  QueryThreads, QueryThread (by thread_id), Tag, Revision, Close.
- Results stream as QueryBatch, time-sliced (budgeted fill): first
  page renders before the query completes. Batches are emitted in
  canonical sort order (the worker sorts before slicing).
- Backend behind an interface: in-tree cgo bindings, and CLI-per-query
  (exec `notmuch`, parse output - aerc worker/notmuch/lib pattern).
  M1 runs the benchmark (section 1.4) and keeps the loser behind the
  interface, unused.
- Lock rule: every op carries a lock budget (lock_timeout=10, muttrc
  precedent); exceeding it errors to the bus as WorkerLockTimeout,
  never blocks the UI.
- Revision action: `notmuch count --lastmod` on the CLI path
  (prints uuid + revision, notmuch/notmuch-count.c:122-124); 
  `notmuch_database_get_revision` on the cgo path.

## 6. Mailbox view model

Rows are MESSAGES, not thread summaries (the muttrc index_format is
per-message; use_threads = 'threads'). The view is a forest of thread
trees:

```
View { threads: []*Thread }              // ordered by last-date, deterministic tiebreak
Thread { id, collapsed: bool,            // collapse state survives refreshes
         root: *Node }
Node { message, children: []*Node,       // children via References links
       depth }                           // ghosts for missing parents (mutt "[...]" rows)
```

- Row order = depth-first flatten of the thread list. Tree glyph
  column (mutt `tree` color object) draws the branches.
- Canonical sort, client-side: never trust notmuch output order.
  Thread level by last-date, message level by sort_aux (reverse-date),
  tiebreak by id bytes - a total order, or equal keys churn the diff.
- Row template (R11): fixed slots (number, flags, attachment, date,
  author, subject, count, tag glyphs); optional slots reserve width,
  blank when absent; widths in terminal cells (go-runewidth). Tag
  glyphs from the transform table, max-2 slots, priority order.
- Flags are tags (R1): unread/replied/forwarded/deleted render from
  notmuch tags; no local flag state.
- Selection identity: cursor tracks message-id (fallback thread-id),
  never a row index; scroll follows index.
- Tag ops from the index: Tag() to the worker; the view applies the
  optimistic local diff, worker confirms; cross-view consistency is
  notmuch's job (each view re-queries its own filter).
- Collapse: insert under a collapsed parent updates the row's
  count/unread indicator without expanding the row set.

## 7. Refresh policy - two paths

### Incremental cycle (the continuous path)

Verified against notmuch source in this workspace: every DB change -
new mail AND tag changes - stamps NOTMUCH_VALUE_LAST_MOD with a fresh
revision (`_notmuch_message_sync`, notmuch/lib/message.cc:1351-1371;
`_notmuch_database_new_revision`, lib/database.cc:491-505). afew tags
through the notmuch2 bindings (afew/Database.py:8), so its changes
land in the same cycle. The `lastmod:NN..MM` query prefix
(lib/lastmod-fp.cc) matches exactly the changed messages.

```
cycle:  read R_cur + uuid (Revision action)
        if uuid != stored uuid: full reload (below), return
        query lastmod:R_prev..R_cur     -> changed messages
        map to affected thread_ids      -> fetch those threads only
        per-thread merge-walk diff -> apply (section 8)
        R_prev = R_cur                  # revision queried through, not newest read
```

- Cost proportional to changed mail, not mailbox size.
- R_prev records the revision the query covered; a change landing
  mid-cycle falls into the next cycle: one-cycle lag, deterministic,
  no lost updates.
- Triggers: WorkerDone (notmuch new, tag ops, filter job incl. afew),
  debounced/coalesced into one cycle.

### Full reload

| trigger | detection |
|---|---|
| DB rebuild / compact | uuid mismatch on any cycle (count --lastmod uuid; the DB's own guard: notmuch/notmuch.c:313) |
| Manual | `:refresh` command |
| View config change | view query edited in config store |
| Reconcile soak | slow periodic timer (15-30 min) - firmware net; lastmod is complete for DB state, this catches client bugs and path drift |
| First load | budgeted progressive fill |

Full reload reuses the same merge-walk diff against the current
model - cursor/scroll survive. After reload: store uuid, R_prev =
current revision, resume cycles.

### Pipeline property (from afew)

MailMover renames files without touching the DB: no revision bump, and
the DB's stored file paths go stale until the next `notmuch new`
(which updates file lists, itself a DB modification). View state is
current immediately; OPENING a message may hit a stale path - the
client tolerates it: path refresh on open failure, error to the view
otherwise. File reads always follow the DB's path, never local globs.

## 8. Diff mechanics (the firmware piece)

Old and new are sorted by the same canonical comparator, so the diff
is a sorted-list merge - no LCS, O(n+m):

```
i, j := 0, 0
for i < len(old) || j < len(new):
    if i >= len(old):                 emit Insert(new[j:]); break
    if j >= len(new):                 emit Remove(old[i:]); break
    o, n := old[i], new[j]
    if o.id == n.id:                  i++; j++            // unchanged
    else if less(o.key, n.key):       emit Remove(o); i++ // only in old
    else:                             emit Insert(n); j++ // only in new
```

- Recurses into each unchanged thread over its message list: a new
  reply sorts to its date position, one Insert at that index -
  "insert between entries" literally.
- Move detection, second pass (still O(n+m)): ids present in both sets
  at different merge positions become Move{id, from, to} instead of
  remove+insert churn. The cursor's stability depends on this when
  the cursor's own row is the churned one.
- Thread merge (References joining two threads): Remove(T1),
  Remove(T2), Insert(T12). Cursor transfer by message-id membership
  in the new tree; else index clamping.
- Conflict rule: a tag op landing between snapshot fetch and diff
  apply - apply the diff, then replay pending local ops. Deterministic
  convergence, worst cost one frame.
- Renderer applies events by index; the tree structure is never
  rebuilt. (BubbleTea redraws the terminal diff itself; our diff keeps
  the view model correct and cursor/scroll stable - the two problems
  the terminal diff can't solve.)

## 9. Testing

- Property test: apply(diff(old,new), old) == new over generated
  snapshots with random inserts/removes/moves/thread-merges.
- Real-DB soak: two snapshots of the real mailbox with a tag op in
  between; diff applies and converges exactly.
- Incremental-cycle integration check: real DB, tag a message, verify
  the next cycle's lastmod changeset includes it and the view
  converges; uuid flip forces a full reload.
- Cursor invariants: cursor identity present in new => cursor points
  at it.
- Config strictness: unknown key = error with location.
- MIME cache invalidation: (path, size, mtime) key - rename and edit
  cases (section 10).

## 10. MIME cache (R13)

- Cache interface: Get/Put/Delete, keyed by (path, size, mtime) -
  renames and edits invalidate naturally. bbolt backend (pure Go,
  embedded), selectable per config.
- Payload: attachment list (name/type/size) - feeds the row slot.
  Nothing else: notmuch serves the rest (R1).
- The worker asks the cache on row fill; a miss triggers a MIME scan
  as a background job, result stored, row repainted when it lands.
- Cache file 0600; cached strings (attachment filenames,
  attacker-influenced) pass the same sanitize/render/mailcap paths as
  fresh data (F5/F2). Corrupt cache payloads must not crash - parse
  defensively, discard entry.

## 11. Minimal UI

- BubbleTea index: renders the view model via the R11 row template;
  j/k/q hardcoded for M1 (R9's binding map is its own slice).
- The bridge relay (section 4) forwards bus messages into BubbleTea's
  Update loop.

## 12. Knobs (not M1)

- Event-driven partial thread fetch driven by notmuch-new output
  (reduces the changeset query itself); lastmod already covers it.
- Cache compression (bbolt + zstd) - measure first.
- Multiple account stores.

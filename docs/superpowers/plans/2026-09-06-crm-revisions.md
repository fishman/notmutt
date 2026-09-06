# CRM Workflow Revisions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rework the HubSpot follow-up workflow: per-operation token lifecycle (fetch on demand, refresh on 401, wipe after), an `[crm.hubspot] account` context for the draft leg, and `d` opening the standard AI prompt picker over CRM-flagged prompts instead of a fixed built-in draft.

**Architecture:** The app adapter (`src/app/crm_engine.go`, `//go:build lua && crm`) stops holding a client; it holds a per-operation client factory (token_cmd argv + hubspot construction + wipe). The pull trigger moves from `RefreshRequested` to a new `core.CrmOpened` event published by the TUI on first Q-open. The hubspot client gains a token-resolver seam (refresh-on-401, retry once) and `Wipe()`. `[crm.hubspot] account` resolves the draft leg's `[ai-data]` grant and context note (`ai/accounts/<account>/default.md`, falling back to `ai/context/default.md`). `aicmd.Command` gains `CRM bool`; two built-in CRM prompts ship; `d` opens the A-key fuzzy picker over CRM-flagged prompts, and the chosen prompt drafts through the existing compose + write-back path. The lib's `RunDraft` (and its tests) are removed.

**Tech Stack:** Go, notmuch, tcell+lipgloss TUI, build tags `lua && crm` (feature) / `!lua || !crm` (stub), TDD with the stdlib test package only.

**Normative spec:** `docs/superpowers/specs/2026-09-06-crm-lazy-client-account-context-prompts-design.md`

**Hard rules:** code only under `src/`; code commits carry NO AI marker and NO Co-Authored-By trailer (doc commits carry `Co-Authored-By: Deepseek`); ASCII only; never submit mail content to an LLM (test data fabricated: alpha/atlas/acme/sender@example.com); tag-blind LSP diagnostics on `lua && crm` files are false positives - trust `go build -tags "lua crm mcp"`; regression tests are LOCKED (never edit/weaken/remove existing tests without user approval - the removals below are the ONLY sanctioned removals, and only of the RunDraft tests this plan names). The working tree carries uncommitted sibling work (crm.toml seed, app.go embed wiring, the marker-preflight fix in `src/lib/crm/hubspot/`, usage.md edits) - leave it untouched except where a task names the file.

---

### Task 1: hubspot client - token resolver, Wipe, 401 refresh

**Files:**
- Modify: `src/lib/crm/hubspot/client.go`
- Test: `src/lib/crm/hubspot/client_test.go`

The client gains: a `token` resolver seam (nil = static key), `Wipe()`, and a 401-retry in `do()`.

- [ ] **Step 1: Write the failing tests**

Append to `src/lib/crm/hubspot/client_test.go` (after `TestListUnprocessedMarkerMissing`):

```go
// TestWipe zeroes the stored key buffer (best-effort erasure; the caller
// drops the client reference right after).
func TestWipe(t *testing.T) {
	key := []byte("sekret")
	c := NewClient(context.Background(), key)
	c.Wipe()
	for _, b := range key {
		if b != 0 {
			t.Fatalf("key not zeroed after Wipe: %q", key)
		}
	}
}

// TestTokenRefresh retries a 401 once with a freshly resolved token: the
// resolver runs, the old buffer zeroes, and the retried request succeeds.
func TestTokenRefresh(t *testing.T) {
	fresh := []byte("fresh-key")
	resolves := 0
	c, calls := start(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fresh-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		io.WriteString(w, `{"results":[`+alphaContactJSON+`]}`)
	})
	old := []byte("stale-key")
	c.key = old
	c.SetTokenResolver(func(ctx context.Context) ([]byte, error) {
		resolves++
		return fresh, nil
	})
	got, err := c.ListUnprocessed(context.Background(), "notmutt_followed_up", "")
	if err != nil {
		t.Fatalf("ListUnprocessed: %v", err)
	}
	if len(got) != 1 || got[0].ID != "201" {
		t.Errorf("contacts = %+v, want [201]", got)
	}
	if resolves != 1 {
		t.Errorf("resolver ran %d times, want 1", resolves)
	}
	for _, b := range old {
		if b != 0 {
			t.Errorf("old key buffer not zeroed on refresh: %q", old)
		}
	}
	_ = calls
}

// TestTokenRefreshTwiceFails: a second 401 surfaces the error - one
// resolver run, one retry, no loop.
func TestTokenRefreshTwiceFails(t *testing.T) {
	resolves := 0
	c, _ := start(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	c.SetTokenResolver(func(ctx context.Context) ([]byte, error) {
		resolves++
		return []byte("still-stale"), nil
	})
	_, err := c.ListUnprocessed(context.Background(), "notmutt_followed_up", "")
	if err == nil {
		t.Fatal("ListUnprocessed = nil, want the 401 error")
	}
	if resolves != 1 {
		t.Errorf("resolver ran %d times, want 1 (single retry)", resolves)
	}
}
```

Note: `TestTokenRefresh`/`TestTokenRefreshTwiceFails` call the search directly - their first request is the marker pre-flight (existing behavior), so the handlers above must answer it: add the same properties-path guard as the existing ListUnprocessed tests:

```go
	if r.URL.Path == "/crm/v3/properties/contacts/notmutt_followed_up" {
		w.WriteHeader(http.StatusOK)
		return
	}
```

- [ ] **Step 2: Run them, expect FAIL**

```bash
cd src && go test -count=1 -tags "lua crm mcp" ./lib/crm/hubspot/ -run 'TestWipe|TestTokenRefresh'
```

Expected: FAIL - `Wipe`/`SetTokenResolver` undefined.

- [ ] **Step 3: Implement**

In `src/lib/crm/hubspot/client.go`:

1. Add the resolver field to `Client` (after `key`):

```go
	// token is the on-demand resolver a 401 calls for a fresh key (nil =
	// static key, the test posture). One buffer owner: do() swaps it under
	// the same rule as construction - the old buffer zeroes first.
	token func(ctx context.Context) ([]byte, error)
```

2. Add the methods after `NewClientURL`:

```go
// SetTokenResolver wires the 401 refresh path: a nil resolver keeps the
// static key (tests). The resolver output becomes the stored key - the
// caller must not reuse the buffer after.
func (c *Client) SetTokenResolver(fn func(ctx context.Context) ([]byte, error)) {
	c.token = fn
}

// Wipe zeroes the stored bearer key. Best effort: Go cannot guarantee
// erasure (GC copies buffers), so wipe plus the caller's short client
// lifetime is the posture.
func (c *Client) Wipe() {
	for i := range c.key {
		c.key[i] = 0
	}
}
```

3. Restructure `do()` into `do` + `doOnce`:

```go
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	return c.doOnce(ctx, method, path, body, out, true)
}

// doOnce performs one request; refresh permits one 401 retry with a
// freshly resolved token before the error surfaces. The wire body reuses
// the same reader-safe shape: body is re-marshaled per attempt.
func (c *Client) doOnce(ctx context.Context, method, path string, body, out any, refresh bool) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+string(c.key))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusUnauthorized && refresh && c.token != nil {
		resp.Body.Close()
		fresh, err := c.token(ctx)
		if err != nil {
			return fmt.Errorf("hubspot: token refresh: %w", err)
		}
		for i := range c.key {
			c.key[i] = 0
		}
		c.key = fresh
		return c.doOnce(ctx, method, path, body, out, false)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return c.apiErr(resp)
	}
	if out == nil {
		io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(out); err != nil {
		return fmt.Errorf("hubspot: decode response: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run, expect PASS**

```bash
cd src && go test -count=1 -tags "lua crm mcp" ./lib/crm/hubspot/
```

Expected: ok - all existing tests plus the three new ones pass (the 401 branch never fires on the static-key tests, so behavior is unchanged there).

- [ ] **Step 5: Commit**

```bash
git add src/lib/crm/hubspot/client.go src/lib/crm/hubspot/client_test.go
git commit -m "feat(crm): refresh the hubspot token on 401 and wipe the key"
```

---

### Task 2: core - CrmOpened event

**Files:**
- Modify: `src/core/bus.go`

- [ ] **Step 1: Add the event type**

In `src/core/bus.go`, after the `RefreshRequested` type:

```go
// CrmOpened is the CRM queue's first-open signal (the Q key): the app
// adapter pulls on it - the pull happens when the user opens the queue,
// never on startup or the mail refresh.
type CrmOpened struct{}
```

- [ ] **Step 2: Build**

```bash
cd src && go build ./core/ && go build -tags "lua crm mcp" ./core/
```

Expected: BUILD_OK both.

- [ ] **Step 3: Commit**

```bash
git add src/core/bus.go
git commit -m "feat(core): add the CrmOpened bus event"
```

---

### Task 3: config - `[crm.hubspot] account`

**Files:**
- Modify: `src/config/config.go`
- Test: `src/config/config_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `src/config/config_test.go`:

```go
// TestLoadCrmHubspotAccount pins the [crm.hubspot] account key: it names
// the mail account whose context file and [ai-data] grant the draft leg
// uses; a name that does not match a configured [accounts] entry is a
// load error, not a silent empty grant.
func TestLoadCrmHubspotAccount(t *testing.T) {
	cfg, err := Load(write(t, `
[accounts.gmail]
from = "You <you@gmail.com>"

[crm]
provider = "hubspot"

[crm.hubspot]
ai = "deepseek"
token_cmd = ["gpg", "-q", "-d", "/home/alpha/.hubspot/token.gpg"]
account = "gmail"
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Crm.Hubspot.Account != "gmail" {
		t.Fatalf("crm.hubspot.account = %q, want gmail", cfg.Crm.Hubspot.Account)
	}
}

func TestLoadCrmHubspotUnknownAccountErrors(t *testing.T) {
	_, err := Load(write(t, `
[crm]
provider = "hubspot"

[crm.hubspot]
ai = "deepseek"
token_cmd = ["gpg", "-q", "-d", "/home/alpha/.hubspot/token.gpg"]
account = "nonesuch"
`))
	if err == nil {
		t.Fatal("Load = nil, want the unknown-account error")
	}
	if !strings.Contains(err.Error(), `"nonesuch"`) {
		t.Errorf("error = %q, want it to name the account", err)
	}
}
```

- [ ] **Step 2: Run, expect FAIL**

```bash
cd src && go test ./config/ -run 'TestLoadCrmHubspotAccount|TestLoadCrmHubspotUnknownAccountErrors'
```

Expected: FAIL - unknown field `account` (strict TOML decoding).

- [ ] **Step 3: Implement**

1. In `src/config/config.go`, `HubspotConfig` gains the field (after `CreatedAfter`):

```go
	// Account is the mail account whose context file and [ai-data] grant
	// the draft leg uses; empty = the thread-derived fallback.
	Account string `toml:"account"`
```

2. In the validation block inside `if h := cfg.Crm.Hubspot; h != nil {` (after the `CreatedAfter` check):

```go
		if a := h.Account; a != "" {
			if _, ok := cfg.Accounts[a]; !ok {
				return fmt.Errorf("crm.hubspot: account %q does not name a configured [accounts] entry", a)
			}
		}
```

- [ ] **Step 4: Run, expect PASS**

```bash
cd src && go test ./config/
```

Expected: ok.

- [ ] **Step 5: Commit**

```bash
git add src/config/config.go src/config/config_test.go
git commit -m "feat(config): add the crm.hubspot account context key"
```

---

### Task 4: aicmd - CRM flag on commands

**Files:**
- Modify: `src/app/aicmd/command.go`
- Test: `src/app/aicmd/command_test.go` (create if absent - check; if it exists, append)

- [ ] **Step 1: Write the failing test**

Append (or create in `src/app/aicmd/command_test.go`):

```go
// TestLoadCommandCRMFflag pins the crm frontmatter key: true/false parse,
// anything else is a load error, and the default stays false.
func TestLoadCommandCRMFflag(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p.md")
	if err := os.WriteFile(path, []byte(`---
name: Follow-up
description: Draft a follow-up from the CRM context
action: compose
crm: true
account_context: true
---
Write a follow-up email from the contact context below.
`), 0600); err != nil {
		t.Fatal(err)
	}
	cmd, err := LoadCommand(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cmd.CRM {
		t.Error("crm = false, want true")
	}
	if err := os.WriteFile(path, []byte(`---
name: Follow-up
description: Draft a follow-up
crm: maybe
---
x
`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCommand(path); err == nil {
		t.Error("crm: maybe accepted, want a load error")
	}
}
```

- [ ] **Step 2: Run, expect FAIL**

```bash
cd src && go test ./app/aicmd/ -run TestLoadCommandCRMFflag
```

Expected: FAIL - unknown key `crm` (the strict loader rejects it).

- [ ] **Step 3: Implement**

1. In `src/app/aicmd/command.go`, `Command` gains (after `SummaryContext`):

```go
	// CRM marks a queue-surface prompt (the d key picker): it drafts from
	// the CRM context block, never a mail thread.
	CRM bool
```

2. In `LoadCommand`'s key switch, before `default:` (mirroring `account_context`):

```go
		case "crm":
			switch value {
			case "true":
				cmd.CRM = true
			case "false":
				cmd.CRM = false
			default:
				return nil, fmt.Errorf("%s:%d: crm must be true or false", path, i+1)
			}
```

- [ ] **Step 4: Run, expect PASS**

```bash
cd src && go test ./app/aicmd/
```

Expected: ok.

- [ ] **Step 5: Commit**

```bash
git add src/app/aicmd/command.go src/app/aicmd/command_test.go
git commit -m "feat(aicmd): flag CRM prompts in the command frontmatter"
```

---

### Task 5: built-in CRM prompt seeds

**Files:**
- Create: `src/app/aicommands/prompts/follow-up.md`
- Create: `src/app/aicommands/prompts/reconnect.md`
- Test: `src/app/app_test.go` (append; create if absent)

The seeds embed via the existing `//go:embed aicommands/prompts/*.md` glob (app.go) and seed into `<configdir>/ai` via `seedAICommands` - no wiring change.

- [ ] **Step 1: Write the two prompt files**

`src/app/aicommands/prompts/follow-up.md`:

```markdown
---
name: Follow-up
description: Draft a follow-up email from the CRM contact and briefing
action: compose
crm: true
account_context: true
---
You are drafting a follow-up email to the contact in the context below:
their identity, company, and a briefing assembled from the CRM record and
recent research. Address the points the briefing calls out.

Write a short, professional follow-up in the user's voice - the style
note, when present, is the voice to match. Write in short paragraphs
separated by blank lines - the client wraps the text to the email line
width itself, so never hard-wrap lines, use tables, or count columns.
The text becomes the body of a new compose dialogue - the recipient and
subject are already filled in, and the user reviews before sending. Do
not include a salutation or signature, and do not start with "Subject:".
```

`src/app/aicommands/prompts/reconnect.md`:

```markdown
---
name: Reconnect
description: Draft a re-engagement email to a cold contact
action: compose
crm: true
account_context: true
---
You are drafting a re-engagement email to the contact in the context
below: their identity, company, and a briefing assembled from the CRM
record and recent research. The relationship is cold - reopen it
concretely (a specific hook from the briefing, never flattery).

Write a short, professional email in the user's voice - the style note,
when present, is the voice to match. Write in short paragraphs separated
by blank lines - the client wraps the text to the email line width
itself, so never hard-wrap lines, use tables, or count columns. The text
becomes the body of a new compose dialogue - the recipient and subject
are already filled in, and the user reviews before sending. Do not
include a salutation or signature, and do not start with "Subject:".
```

- [ ] **Step 2: Write the failing test**

Append to `src/app/app_test.go` (the package is `app`, so it can read the embedded `aiSeedFS`):

```go
// TestCrmSeedPromptsParse pins the built-in CRM prompt seeds: both parse
// strictly and carry the crm flag + compose action the queue picker
// filters on.
func TestCrmSeedPromptsParse(t *testing.T) {
	for _, name := range []string{"prompts/follow-up.md", "prompts/reconnect.md"} {
		data, err := aiSeedFS.ReadFile("aicommands/" + name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		dir := t.TempDir()
		path := filepath.Join(dir, "seed.md")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		cmd, err := aicmd.LoadCommand(path)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !cmd.CRM || cmd.Action != "compose" {
			t.Errorf("%s: crm=%v action=%q, want true/compose", name, cmd.CRM, cmd.Action)
		}
	}
}
```

- [ ] **Step 3: Run, expect FAIL** (the seeds do not parse until Task 4's loader key lands - if Task 4 is done, they pass immediately; then the run's purpose is the embed check)

```bash
cd src && go test ./app/ -run TestCrmSeedPromptsParse
```

Expected: FAIL on the `crm` key if run before Task 4; PASS after.

- [ ] **Step 4: Verify the full package still passes**

```bash
cd src && go test ./app/
```

Expected: ok.

- [ ] **Step 5: Commit**

```bash
git add src/app/aicommands/prompts/follow-up.md src/app/aicommands/prompts/reconnect.md src/app/app_test.go
git commit -m "feat(crm): ship the built-in follow-up and reconnect prompts"
```

---

### Task 6: tui - CrmOpened, queue draft guard split, CRM prompt picker

**Files:**
- Modify: `src/tui/model.go` (first-open publish ~1646-1651; crm keypress branch ~496-506; picker cases)
- Modify: `src/tui/hooks.go`
- Modify: `src/tui/crm.go`
- Test: `src/tui/crm_test.go` (rewrite the draft-action test), `src/tui/model_test.go` (append)

The TUI publishes `CrmOpened` instead of `RefreshRequested` on first Q-open; the queue's draft case leaves `crmQueue.action` (the model opens the picker); two new seams arrive; the picker and its e-key extra run through them.

- [ ] **Step 1: Write the failing test for the event**

In `src/tui/model_test.go`, append:

```go
// TestCrmOpenPublishesCrmOpened pins the queue's first open: it publishes
// CrmOpened (the pull trigger), not RefreshRequested - the mail refresh
// must never side-pull CRM.
func TestCrmOpenPublishesCrmOpened(t *testing.T) {
	bus := core.NewBus()
	ch := bus.Subscribe()
	SetCrmPullSource(func() []CrmCommand { return []CrmCommand{{Name: "hubspot", Desc: "pull"}} })
	t.Cleanup(func() { SetCrmPullSource(nil) })
	m := model()
	m.bus = bus
	m = press(t, m, "Q")
	var gotCrm bool
loop:
	for {
		select {
		case e := <-ch:
			if _, ok := e.(core.CrmOpened); ok {
				gotCrm = true
				break loop
			}
			if _, ok := e.(core.RefreshRequested); ok {
				t.Fatal("first Q-open published RefreshRequested, want CrmOpened")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("no CrmOpened published on first Q-open")
		}
	}
	if !gotCrm {
		t.Fatal("no CrmOpened published on first Q-open")
	}
}
```

(The harness is the file's existing `model()` helper - `press(t, m, key)` delivers keys; `model()` builds without a bus, so the test assigns one before pressing. The Q key opens the queue only when the pull source is wired - hence the `SetCrmPullSource`.)

- [ ] **Step 2: Run, expect FAIL**

```bash
cd src && go test ./tui/ -run TestCrmOpenPublishesCrmOpened
```

Expected: FAIL - RefreshRequested published (or no CrmOpened).

- [ ] **Step 3: Implement the event swap**

In `src/tui/model.go`, in the crm-queue open case, replace the `RefreshRequested` publish:

```go
		if !m.crmPulled {
			if m.bus != nil {
				m.bus.Publish(core.CrmOpened{})
			}
			m.crmPulled = true
		}
```

- [ ] **Step 4: Split the draft guard out of the queue model**

In `src/tui/crm.go`:

1. Replace the `crmActionDraft` case in `action()` with a guard method (the picker opens in the model, which needs the guard without the dispatch):

```go
		switch name {
		case crmActionAnalyze:
			if r.inFlight {
				return false
			}
			r.inFlight = true
		case crmActionDismiss:
			r.contact.Status = crmStatusDismissed
		default:
			return false
		}
```

2. Add after `action()`:

```go
// draftable is the d-key guard: a row drafts only with a briefing. The
// model consults it before opening the prompt picker (the picker replaces
// the action dispatch for draft - a draft is a prompt run now).
func (q *crmQueue) draftable() bool {
	r := q.cursor()
	return r != nil && r.briefing != ""
}
```

3. The model needs the selected row for the picker's enter: add a field to `crmQueue`:

```go
type crmQueue struct {
	rows  []*crmRow
	byKey map[crmKey]*crmRow
	cur   int
	// draftSel is the key the d picker opened on (the enter/e handlers
	// resolve the row through it, never a position).
	draftSel crmKey
}
```

- [ ] **Step 5: Rewrite the draft test in crm_test.go**

Replace `TestCrmQueueDraftRequiresBriefing` (it pinned the removed dispatch) with the guard pin:

```go
// TestCrmQueueDraftable pins the d guard: a row drafts only with a
// briefing. The draft dispatch itself is the model's (the prompt picker);
// action() no longer carries a draft case.
func TestCrmQueueDraftable(t *testing.T) {
	var gotAction string
	SetCrmActionHandler(func(action string, c core.CrmContact) { gotAction = action })
	t.Cleanup(func() { SetCrmActionHandler(func(string, core.CrmContact) {}) })
	q := newCrmQueue()
	q.onQueue(core.CrmQueue{Contacts: []core.CrmContact{{Provider: "hubspot", ID: "201", Email: "alpha@example.com"}}})
	if q.draftable() {
		t.Error("draftable on a row with no briefing")
	}
	q.onBriefing(core.CrmBriefing{Provider: "hubspot", ContactID: "201", Text: "alpha sells widgets"})
	if !q.draftable() {
		t.Error("not draftable on a row with a briefing")
	}
	if gotAction != "" {
		t.Errorf("action() dispatched %q for a guard check", gotAction)
	}
}
```

(Remove the old test function wholesale - the sanctioned removal of the old draft-dispatch pin.)

- [ ] **Step 6: Add the two seams to hooks.go**

After the CrmCommand block in `src/tui/hooks.go`:

```go
// crmAIPrompts is the CRM prompt source seam (the queue's d key): the app
// returns the CRM-flagged prompts for the configured account; nil = none -
// d reports unavailable.
var crmAIPrompts = func() []AICommand { return nil }

func SetCrmAIPromptSource(fn func() []AICommand) {
	if fn != nil {
		crmAIPrompts = fn
	}
}

// onCrmAICommand runs a chosen CRM prompt on a queue row (the picker's
// enter): the app drafts the follow-up into a prefilled compose. extra is
// the picker's e-key text (empty = the default follow-up extra).
var onCrmAICommand = func(name string, c core.CrmContact, extra string) {}

func SetCrmAICommandHandler(fn func(string, core.CrmContact, string)) {
	if fn != nil {
		onCrmAICommand = fn
	}
}
```

- [ ] **Step 7: Open the picker from the d key**

In `src/tui/model.go`'s crm keypress branch, replace the `"d"` case:

```go
				case msg.Typed() && msg.Text == "d":
					if !m.crm.draftable() {
						break
					}
					cmds := crmAIPrompts()
					if len(cmds) == 0 {
						m.logEntry(i18n.T("no CRM prompts configured"), true)
						break
					}
					names := make([]string, 0, len(cmds))
					payload := make([]string, 0, len(cmds))
					for _, c := range cmds {
						name := c.Name
						payload = append(payload, name)
						if c.Desc != "" {
							name += " - " + c.Desc
						}
						names = append(names, name)
					}
					m.crm.draftSel = m.crm.cursor().key()
					m.dialogue = &listDialogue{f: newFuzzyPayload("crmcmd", i18n.T("CRM prompt:"), names, payload)}
				case msg.Typed() && msg.Text == "x":
					m.crm.action(crmActionDismiss)
```

(Only the `d` case changes; `a` and `x` keep their dispatch through `action()`.)

- [ ] **Step 8: Handle the picker enter and the e key**

1. In the listDialogue commit switch (`src/tui/model.go`, after the `"aicmd"` case ~4002-4010), add:

```go
		case "crmcmd":
			// the CRM prompt picker (the queue's d key): enter runs the
			// chosen prompt on the draft-selected row with the default
			// follow-up extra
			name, ok := d.f.selectedPayload()
			if ok {
				if c := m.crm.rowByKey(m.crm.draftSel); c != nil {
					c.contact.Status = crmStatusDrafted
					m.cancelDialogue()
					onCrmAICommand(name, c.contact, "")
				}
			}
			return nil, nil
```

2. Add the `rowByKey` helper to `src/tui/crm.go`:

```go
// rowByKey resolves a held row by key (the d picker's selected row,
// resolved at enter time, never a stored position).
func (q *crmQueue) rowByKey(k crmKey) *crmRow {
	return q.byKey[k]
}
```

3. The e key: where the `"aicmd"` listDialogue opens its extra input (the handler that builds `&textDialogue{field: "aiextra", ...}`, ~line 3910), add the CRM branch. Find the existing code shape (it returns the textDialogue for kind "aicmd") and extend:

```go
	if d.f.kind == "aicmd" {
		return &textDialogue{field: "aiextra", label: i18n.T("extra prompt: "), aicmdName: name}, nil
	}
	if d.f.kind == "crmcmd" {
		return &textDialogue{field: "crmextra", label: i18n.T("extra prompt: "), crmCmdName: name}, nil
	}
```

4. Add `crmCmdName` to `textDialogue` (after `aicmdName`):

```go
	// crmCmdName is the selected CRM prompt (field "crmextra"): the queue
	// picker's e key.
	crmCmdName string
```

5. In the textDialogue commit switch (after the `"aiextra"` case ~4273-4279), add:

```go
	case "crmextra":
		// the CRM picker's e key: run the chosen prompt with the extra text
		// (empty input still runs - the adapter supplies the default extra)
		if c := m.crm.rowByKey(m.crm.draftSel); c != nil {
			c.contact.Status = crmStatusDrafted
			m.cancelDialogue()
			onCrmAICommand(d.crmCmdName, c.contact, input)
		}
		return nil, nil
```

- [ ] **Step 9: Run the tui tests**

```bash
cd src && go test ./tui/ -run 'TestCrm|TestCrmOpenPublishes'
```

Expected: ok (the rewritten guard test + the event test + existing crm tests).

- [ ] **Step 10: Commit**

```bash
git add src/tui/model.go src/tui/hooks.go src/tui/crm.go src/tui/crm_test.go src/tui/model_test.go
git commit -m "feat(tui): open the CRM prompt picker from the queue, publish CrmOpened"
```

---

### Task 7: app adapter - lazy per-op client factory + CrmOpened trigger + account grant

**Files:**
- Modify: `src/app/crm_engine.go`
- Modify: `src/app/crm_engine_stub.go` (no-op twin only if it references changed symbols - check)
- Test: `src/app/crm_engine_test.go` (compile-level changes only here; the deep rework is Task 9)

The adapter stops building a client at startup. It builds a per-operation factory; the subscriber triggers on `CrmOpened`; row actions and write-back build/wipe per op; the ground closure honors the configured account.

- [ ] **Step 1: Restructure crmAdapterState and the factory**

Replace the `crmAdapterState` struct and `crmHubspotNew` seam in `src/app/crm_engine.go`:

```go
// crmAdapterState is the crm wire's resolved state: the per-operation
// client factory (token on demand, wiped after each op), the [ai] entry
// the chat calls run on, the marker property the write-back records, the
// configured mail account ("" = thread-derived fallback), the bus jobs
// publish on, and the gated mail-grounding closure. provider is the
// routing id - the row filter the action handler applies.
type crmAdapterState struct {
	provider string
	newClient func(ctx context.Context) (crm.Client, func(), error)
	aiCfg    config.AIProvider
	marker   string
	account  string
	bus      *core.Bus
	ground   crm.MailGroundFn
}

// crmAdapter is the resolved wire state; nil while the CRM is dormant or
// its setup failed. Written once in crmWire before the subscriber starts;
// the workflow jobs build a fresh client per operation and wipe it after.
// Session-local.
var crmAdapter *crmAdapterState

// crmHubspotNew is the hubspot client factory seam the per-op factory
// builds through: the production value constructs against the live API,
// and the integration test swaps it to point a real client at an httptest
// server via hubspot.NewClientURL.
var crmHubspotNew = func(ctx context.Context, key []byte) *hubspot.Client {
	return hubspot.NewClient(ctx, key)
}

// crmHubspotWipe is the wipe seam (crmHubspotNew's pair): the per-op
// factory returns it as the cleanup func; the integration test overrides
// it to observe wipe calls.
var crmHubspotWipe = func(c *hubspot.Client) { c.Wipe() }
```

- [ ] **Step 2: Rewrite crmWire**

Replace the body from the `switch cfg.Crm.Provider` block through the adapter construction:

```go
func crmWire(ctx context.Context, bus *core.Bus, worker workerAPI, cfg config.Config, root string) {
	if cfg.Crm.Provider == "" {
		return
	}
	hs := cfg.Crm.Hubspot
	var aiCfg config.AIProvider
	switch cfg.Crm.Provider {
	case "hubspot":
		// Resolve the [ai] entry without touching the token subprocess: the
		// common misconfig - a valid token_cmd but no [ai] section - fails
		// cheap here. The token itself resolves per operation.
		var err error
		aiCfg, err = resolveAIProvider(cfg, hs.AI)
		if err != nil {
			diag.Warn("crm: disabled", "err", err.Error())
			return
		}
	default:
		return // a provider with no client case: config load already rejects it
	}
	newClient := func(ctx context.Context) (crm.Client, func(), error) {
		key, err := ai.FetchKey(ctx, hs.TokenCmd)
		if err != nil {
			return nil, nil, fmt.Errorf("crm: token: %w", err)
		}
		hc := crmHubspotNew(ctx, key)
		hc.SetTokenResolver(func(ctx context.Context) ([]byte, error) {
			return ai.FetchKey(ctx, hs.TokenCmd)
		})
		return hc, func() { crmHubspotWipe(hc) }, nil
	}
	crmAdapter = &crmAdapterState{
		provider:  "hubspot",
		newClient: newClient,
		aiCfg:     aiCfg,
		marker:    hs.MarkerProperty,
		account:   hs.Account,
		bus:       bus,
		ground:    crmMailGround(cfg, worker, hs.Account),
	}
	// the subscriber: the queue's first open (CrmOpened) launches a pull on
	// its own goroutine with a per-op client (RunPull's mutex absorbs
	// overlap); a generated CrmBriefing caches the text the prompt run
	// needs; a SendResult for a CRM-origin compose marks the contact
	// followed up. The mail refresh (RefreshRequested) never pulls CRM.
	ch := bus.Subscribe()
	go func() {
		for e := range ch {
			switch e := e.(type) {
			case core.CrmOpened:
				go func() {
					client, wipe, err := newClient(context.Background())
					if err != nil {
						bus.Publish(core.CrmRowError{Provider: "hubspot", Err: err})
						return
					}
					defer wipe()
					crm.RunPull(bus, client, hs.MarkerProperty, hs.CreatedAfter)
				}()
			case core.CrmBriefing:
				crmCacheBriefing(e)
			case core.SendResult:
				crmWriteBackOnSend(bus, newClient, "hubspot", hs.MarkerProperty, e)
			}
		}
	}()
}
```

(The `core.CrmDraft` case disappears: the prompt run builds the compose directly - Task 8.)

- [ ] **Step 3: Per-op row actions**

Replace `crmRowAction`'s body (keep the provider filter and contact conversion):

```go
func crmRowAction(action string, c core.CrmContact) {
	a := crmAdapter
	if a == nil || c.Provider != a.provider {
		return
	}
	contact := crmWireContact(c)
	switch action {
	case "analyze":
		go func() {
			client, wipe, err := a.newClient(context.Background())
			if err != nil {
				a.bus.Publish(core.CrmRowError{Provider: a.provider, ContactID: contact.ID, Err: err})
				return
			}
			defer wipe()
			crm.RunAnalyze(a.bus, client, a.aiCfg, contact, ai.Chat)
		}()
	case "dismiss":
		// Marking writes the follow-up property; double-marking is accepted -
		// the write is idempotent, and a re-x after a slow mark re-writes it.
		go func() {
			client, wipe, err := a.newClient(context.Background())
			if err != nil {
				a.bus.Publish(core.CrmRowError{Provider: a.provider, ContactID: contact.ID, Err: err})
				return
			}
			defer wipe()
			crm.RunMark(a.bus, client, contact.ID, a.marker)
		}()
	}
}
```

(The `draft` case is gone - the picker path replaces it.)

- [ ] **Step 4: Write-back with the factory**

Replace `crmWriteBackOnSend`:

```go
// crmWriteBackOnSend reacts to a send result for a CRM-origin compose: an OK
// result consumes the compose map entry (the row leaves on the next pull
// page, which omits a contact whose marker landed) and, when the entry's
// provider matches the wired one, runs MarkFollowedUp through a per-op
// client. A failed send leaves both the compose and the entry alone - the
// dialogue retries, and a retried OK still marks. The mark's own outcome is
// unobservable here (RunMark is void), so the entry drops at consumption
// time; a failed mark keeps the row retryable via a fresh pull/action.
func crmWriteBackOnSend(bus *core.Bus, newClient func(ctx context.Context) (crm.Client, func(), error), provider, marker string, e core.SendResult) {
	if !e.OK {
		return
	}
	crmComposeMu.Lock()
	ref, ok := crmComposeRefs[e.TabID]
	if ok {
		delete(crmComposeRefs, e.TabID)
	}
	crmComposeMu.Unlock()
	if !ok || ref.Provider != provider {
		return
	}
	go func() {
		client, wipe, err := newClient(context.Background())
		if err != nil {
			bus.Publish(core.CrmRowError{Provider: provider, ContactID: ref.ContactID, Err: err})
			return
		}
		defer wipe()
		crm.RunMark(bus, client, ref.ContactID, marker)
	}()
}
```

- [ ] **Step 5: Account-aware ground**

Change `crmMailGround`'s signature and grant resolution:

```go
// crmMailGround builds the adapter's gated mail-grounding closure for the
// prompt run: a worker query from:"<email>" finds the newest inbound thread,
// the grant resolves from the configured [crm.hubspot] account (empty =
// the thread's tag-derived account, the old fallback), and - only when that
// account's [ai-data] grant permits - aicmd.BuildContext runs over the
// thread with a synthetic command whose Data is the grant. No thread or no
// grant returns "" - mail content never reaches a prompt except through
// BuildContext's Data allowlist.
func crmMailGround(cfg config.Config, worker workerAPI, account string) crm.MailGroundFn {
	...
		account := account // the configured one; empty falls through to derivation
		if account == "" {
			if m := newestOf(rpl.Msgs); m != nil {
				account = resolveAccount(cfg, tagsOf(m), nil)
			}
		}
```

(The rest of the closure - the charset gate, the query, the thread fetch, the grant check, the BuildContext call - is unchanged.)

- [ ] **Step 6: crmPullSource on configuration, not client**

Replace the nil-guard in `crmPullSource` (the command text stays as-is):

```go
func crmPullSource() []tui.CrmCommand {
	a := crmAdapter
	if a == nil {
		return nil
	}
	return []tui.CrmCommand{{Name: a.provider, Desc: "review the " + a.provider + " follow-up queue"}}
}
```

- [ ] **Step 7: Build both ways**

```bash
cd src && go build ./... && go build -tags "lua crm mcp" ./...
```

Expected: BUILD_OK both. (The integration test in `crm_engine_test.go` will NOT compile yet - it drives the removed draft path; it is a test file, so `go build` passes but `go test` fails. That is Task 9's job.)

- [ ] **Step 8: Commit**

```bash
git add src/app/crm_engine.go
git commit -m "feat(crm): build the client per operation and pull on CrmOpened"
```

---

### Task 8: app adapter - the CRM prompt run path; remove lib RunDraft

**Files:**
- Modify: `src/app/crm_engine.go` (prompt list source + run path)
- Modify: `src/app/app.go` (wire the two new seams)
- Modify: `src/lib/crm/workflow.go` (remove RunDraft + helpers; export DefaultDraftSubject)
- Modify: `src/lib/crm/workflow_test.go` (remove the 5 RunDraft tests)
- Modify: `src/app/crm_engine_stub.go` (if the stub mirrors crmPullSource only, no change)

- [ ] **Step 1: The prompt list source and the run path**

Append to `src/app/crm_engine.go`:

```go
// crmPromptList is the CRM prompt picker source (SetCrmAIPromptSource):
// the CRM-flagged commands for the configured account - the account's own
// flagged prompts (already first from LoadCommands) plus the flagged
// defaults; "" account = flagged defaults only.
func crmPromptList() []tui.AICommand {
	cmds, err := aicmd.LoadCommands(filepath.Join(configDir(), "ai"))
	if err != nil {
		diag.Warn("aicmd", "err", err.Error())
		return nil
	}
	a := crmAdapter
	account := ""
	if a != nil {
		account = a.account
	}
	out := make([]tui.AICommand, 0, len(cmds))
	for _, c := range cmds {
		if !c.CRM {
			continue
		}
		if c.Account != "" && c.Account != account {
			continue
		}
		out = append(out, tui.AICommand{Name: c.Name, Desc: c.Description})
	}
	return out
}

// crmPromptBlock is a queue row's identity for the prompt context: foreign
// CRM input, sanitized before it reaches a prompt (F1).
func crmPromptBlock(c core.CrmContact) string {
	name := strings.TrimSpace(c.First + " " + c.Last)
	if name == "" {
		name = c.Email
	}
	var parts []string
	parts = append(parts, "Name: "+name, "Email: "+c.Email)
	if c.Title != "" {
		parts = append(parts, "Title: "+c.Title)
	}
	if c.Company != "" {
		parts = append(parts, "Company: "+c.Company)
	}
	return core.SanitizeControls(strings.Join(parts, "\n"))
}

// runCrmPrompt runs a chosen CRM-flagged prompt on a queue row: the cached
// briefing, the gated mail ground, and the account/default context note
// assemble the prompt context; one chat call on the resolved [ai] entry
// produces the body, which opens the prefilled compose through the same
// path the old draft used (so send write-back is retained). extra is the
// picker's e-key text; empty falls back to a default follow-up instruction.
// No HubSpot call and no token - the row data is already local.
func runCrmPrompt(bus *core.Bus, cfg config.Config, root string, name string, c core.CrmContact, extra string) {
	a := crmAdapter
	if a == nil || c.Provider != a.provider {
		return
	}
	fail := func(err error) {
		bus.Publish(core.CrmRowError{Provider: c.Provider, ContactID: c.ID, Err: err})
	}
	text := crmBriefingText(c.Provider, c.ID)
	if text == "" {
		fail(errors.New("crm: prompt: no briefing"))
		return
	}
	cmds, err := aicmd.LoadCommands(filepath.Join(root, "ai"))
	if err != nil {
		fail(err)
		return
	}
	var cmd *aicmd.Command
	for i := range cmds {
		if cmds[i].Name == name && cmds[i].CRM {
			cmd = &cmds[i]
			break
		}
	}
	if cmd == nil {
		fail(fmt.Errorf("crm: prompt: %q is not a CRM prompt", name))
		return
	}
	note := aicmd.LoadDefaultContext(root)
	if a.account != "" {
		note = aicmd.LoadAccountContext(root, a.account)
	}
	ground, err := a.ground(context.Background(), c.Email)
	if err != nil {
		fail(err)
		return
	}
	system := cmd.Body + "\n\nContact context:\n" + crmPromptBlock(c) + "\nBriefing:\n" + text
	if ground != "" {
		system += "\nMail context:\n" + ground
	}
	if note != "" {
		system += "\nStyle:\n" + note
	}
	user := strings.TrimSpace(extra)
	if user == "" {
		user = "Write a follow-up email to this contact."
	}
	out, err := ai.Chat(context.Background(), a.aiCfg, a.aiCfg.Model, system, user, func(string) {})
	if err != nil {
		fail(fmt.Errorf("crm: prompt: %w", err))
		return
	}
	crmOpenDraftCompose(bus, cfg, root, core.CrmDraft{
		Provider:  c.Provider,
		ContactID: c.ID,
		Email:     c.Email,
		Subject:   crm.DefaultDraftSubject,
		Body:      out,
	})
}
```

- [ ] **Step 2: Wire the seams in app.go**

In `src/app/app.go`, next to the existing `SetCrmPullSource(crmPullSource)` / `SetCrmActionHandler` wiring:

```go
	SetCrmAIPromptSource(crmPromptList)
	SetCrmAICommandHandler(func(name string, c core.CrmContact, extra string) {
		go runCrmPrompt(bus, cfg, root, name, c, extra)
	})
```

(App.go imports core already; crm_engine.go's symbols are package-local.)

- [ ] **Step 3: Remove lib RunDraft, export the subject**

In `src/lib/crm/workflow.go`:

1. Delete the whole `RunDraft` function and its helpers (`splitDraftReply` and anything used only by it). KEEP `MailGroundFn`'s declaration (the adapter still types its closure with it).
2. Where `defaultDraftSubject` was declared, replace with:

```go
// DefaultDraftSubject is the compose subject the CRM prompt run prefills
// (the contact and briefing supply the body; the user edits both).
const DefaultDraftSubject = "Following up"
```

- [ ] **Step 4: Remove the RunDraft tests**

In `src/lib/crm/workflow_test.go`, delete the five RunDraft test functions wholesale (sanctioned removal - they pin the replaced path): `TestRunDraftPublishesDraft`, `TestRunDraftDefaultSubject`, `TestRunDraftNilChatErrors`, `TestRunDraftGroundingErrorPublishesRowError`, `TestRunDraftChatErrorPublishesRowError`, plus any helper only they used (check `analyzeProvider`/`chatError` - keep them if other tests use them). Do NOT touch any RunPull/RunAnalyze/RunMark test.

- [ ] **Step 5: Run the lib and app builds**

```bash
cd src && go test -count=1 -tags "lua crm mcp" ./lib/crm/...
```

Expected: ok (RunPull/RunAnalyze/RunMark tests untouched and green).

- [ ] **Step 6: Commit**

```bash
git add src/app/crm_engine.go src/app/app.go src/lib/crm/workflow.go src/lib/crm/workflow_test.go
git commit -m "feat(crm): run CRM-flagged prompts from the picker instead of a fixed draft"
```

---

### Task 9: integration test rework + full suites

**Files:**
- Modify: `src/app/crm_engine_test.go`

- [ ] **Step 1: Rework TestCrmHubspotWorkflow**

Change the test's drive loop to the new contract (read the existing function first - the edits are targeted):

1. The pull trigger: every `bus.Publish(core.RefreshRequested{})` becomes `bus.Publish(core.CrmOpened{})`.
2. The draft leg: `crmRowAction("draft", q.Contacts[0])` becomes the prompt path - after the briefing cache poll (keep the existing `crmEventually` on `crmBriefingText`), call:

```go
	runCrmPrompt(bus, cfg, root, "Follow-up", q.Contacts[0], "")
```

then poll for the opened compose (the existing ComposeOpened snapshot logic - keep it).
3. Assert the compose Subject equals `crm.DefaultDraftSubject` and To/Body carry the contact email and the fake AI output (the fake AI server's reply - keep the existing assertions' shape, adjusting the expected body if the prompt's context changed it; the fake server is deterministic, so use whatever it returns).
4. Token hygiene asserts (the new lazy contract): add a `tokenRuns int` counter to the fake `token_cmd` (the test's cfg token_cmd is a fake command - increment in it), then assert: 0 runs before the first CrmOpened, >=1 after the pull, and `wipes` recorded via overriding `crmHubspotWipe` (set it in the test, restore in cleanup) - one wipe per successful op.
5. Keep the send-OK mark and the failing-dismiss legs (they now go through the factory - the assertions still hold: one PATCH, row omitted from the next pull).

- [ ] **Step 2: Run the integration test**

```bash
cd src && go test -count=1 -tags "lua crm mcp" ./app/ -run TestCrmHubspotWorkflow -v
```

Expected: RUN + PASS.

- [ ] **Step 3: Both full suites**

```bash
cd src && go test -count=1 -tags "lua crm mcp" ./...
go test ./...
```

Expected: exit 0 for both (locked tests green; the default build compiles the stub).

- [ ] **Step 4: Commit**

```bash
git add src/app/crm_engine_test.go
git commit -m "test(crm): drive the reworked workflow through CrmOpened and the picker"
```

---

### Task 10: docs

**Files:**
- Modify: `docs/usage.md`
- Modify: `src/app/crm.toml`

- [ ] **Step 1: usage.md - the account key and the picker**

1. In the CRM follow-up workflow subsection's toml block, add the account line after `ai`:

```toml
account = "gmail"            # the mail account whose context/grant the draft uses; blank = default context
```

2. Update the flow prose: replace the `d` sentence ("drafts a personalized follow-up into a prefilled compose (`d`)") with the picker wording - `d` opens the AI prompt picker over CRM-flagged prompts (built-ins: Follow-up, Reconnect); enter runs the chosen prompt with the CRM context.

- [ ] **Step 2: crm.toml seed - the account line**

Add to `src/app/crm.toml` (aligned comment column, single-line comments):

```toml
# account = "gmail"                       # the mail account whose context/grant the draft uses
```

- [ ] **Step 3: Commit (doc rules - Deepseek trailer)**

```bash
git add docs/usage.md src/app/crm.toml
git commit -m "docs(crm): document the account key and the prompt picker draft

Co-Authored-By: Deepseek"
```

---

## Self-review notes (run by the controller, not the implementers)

- Task 1's `doOnce` recursion: `refresh` false on the retry, so exactly one refresh attempt; the wire body re-marshals per attempt (the first attempt's reader is consumed).
- Task 6's d-key branch: `m.crm.cursor()` is non-nil when `draftable()` returned true (same cursor); `draftSel` survives the dialogue by key, not position.
- Task 7: `crmWriteBackOnSend`'s new signature is called only from crmWire's subscriber - update every call site together (both are in the same file, both shown).
- Task 8: `crmOpenDraftCompose` is unchanged and still registers `crmComposeRefs` - write-back rides the existing path.
- Locked tests untouched: `TestMCPScopeEnforcement`, the RunPull/RunAnalyze tests, and the marker-preflight test from the prior fix are all outside the removed set. The five RunDraft tests are the sanctioned removals.
- Stub file: `crm_engine_stub.go` only carries the no-op `crmPullSource`/`crmWire` twins - confirm it still compiles in the default build after Task 7/8 (Task 7 Step 7 builds it).

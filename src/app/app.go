package app

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"notmutt/compose"
	"notmutt/config"
	"notmutt/core"
	"notmutt/mail"
	"notmutt/notmuch"
	"notmutt/setup"
	"notmutt/tui"
)

const lockBudget = 10 * time.Second

//go:embed lua/templates/*.lua
var templateFS embed.FS

func Run() error {
	if len(os.Args) > 1 && os.Args[1] == "setup" {
		return setupAccounts()
	}
	cfg, err := config.Load(configPath())
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if err := validateBindings(&cfg); err != nil {
		return err
	}
	bus := core.NewBus()
	st := config.NewStore(cfg)
	st.Subscribe("ui", func() { bus.Publish(core.ConfigChanged{Section: "ui"}) })
	st.Subscribe("view", func() { bus.Publish(core.ConfigChanged{Section: "view"}) })
	st.Subscribe("theme", func() { bus.Publish(core.ConfigChanged{Section: "theme"}) })
	st.Subscribe("refresh", func() { bus.Publish(core.ConfigChanged{Section: "refresh"}) })
	openDiagLog()
	go runDiagBus(bus)
	worker := notmuch.NewWorker(bus, notmuch.New(), lockBudget)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go worker.Start(ctx)

	// DB open check plus per-view query validation (spec section 3:
	// notmuch dry run for every view query). The empty path resolves
	// inside the backend via `notmuch config get database.path`
	// (argv-only, F4); the handle stays open for the process lifetime.
	if rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActOpen, Query: ""}); err != nil || rpl.Err != nil {
		return fmt.Errorf("notmuch open: %v %v", err, rpl.Err)
	}
	for name, v := range cfg.Views {
		rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActQuery, Query: v.Query, Limit: 1})
		if err != nil || rpl.Err != nil {
			return fmt.Errorf("view %q: query %q: %v %v", name, v.Query, err, rpl.Err)
		}
	}

	name := firstView(cfg)
	view := core.NewView(name, cfg.Views[name].Query)
	refresher := newRefresher(bus, worker, view, 0)
	go refresher.cycle() // initial load streams in via ViewDiff; the TUI starts immediately

	cjob := newCacheJob(bus, worker, view, cachePath())
	go cjob.Run(ctx)

	groups := st.Config().TagGroupList()
	view.SetGroups(groups)

	tui.SetApplyHandler(func() {
		go func() {
			if err := applyStaged(view, groups, worker); err != nil {
				bus.Publish(core.JobError{Job: "apply", Err: err})
			}
			// the view changed either way (applied drops and baselines);
			// a partial failure still renders the succeeded entries
			bus.Publish(core.ViewDiff{View: view.Name})
		}()
	})

	// open: the worker loads the thread's messages (headers + paths,
	// ActThread), the open job renders the files and runs the render
	// transforms, and the TUI attaches the lines on ThreadLoaded (R13
	// two-step - content loads on open only). The preview variant (the
	// p key) skips the read-marking; the TUI keeps the index surface
	// for the popup.
	tui.SetOpenHandler(func(threadID string, preview bool) {
		go openThread(worker, bus, threadID, preview)
	})

	// the signatures root (spec section 9): ONE path, both halves of
	// the send surface read the same tree - the app (default signature
	// in buildCompose) and the tui (the picker lists the files)
	sigDir = filepath.Join(filepath.Dir(configPath()), "signatures")
	tui.SetSignaturesDir(sigDir)

	// the Lua layer (R8): plugin files from <configdir>/lua, each
	// registering its body_render as a render transform. The adapter
	// compiles only under the lua build tag (the R12 pattern); default
	// builds run the no-op stub. Loaded before any open can fire.
	loadLuaPlugins(filepath.Join(filepath.Dir(configPath()), "lua"))

	// attach commands: config tables register first, then Lua plugin
	// registrations (later, per-plugin load order) - both land in the
	// registry; the TUI reads it through the seam
	loadConfigAttachCommands(cfg)
	tui.SetAttachCommandSource(func() map[string][]string { return attachCommandSnapshot() })

	// reply: the app prefills the dialogue (account detection, parse,
	// default signature) and publishes ComposeOpened - the TUI attaches
	// the tab
	tui.SetReplyHandler(func(msg *core.Message, mode string) {
		go func() {
			st := replyPrefill(cfg, view, worker, msg, mode)
			if st == nil {
				return
			}
			st.ID = fmt.Sprintf("%d", time.Now().UnixNano())
			bus.Publish(compose.ToEvent(st))
		}()
	})

	// send: the app runs the send job on its own goroutine; SendResult
	// closes the tab or keeps it failed
	tui.SetSendHandler(func(st compose.State) {
		go sendJob(bus, worker, view, cfg, st)
	})

	// address completion: the compose Tab trigger (lazy, debounced in
	// the TUI) harvests the sender corpus once; the result lands as
	// AddressIndex on the bus
	tui.SetAddressRequestHandler(func() {
		go func() {
			rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActAddresses, Query: "*"})
			if err != nil || rpl.Err != nil {
				return
			}
			bus.Publish(core.AddressIndex{Addrs: rpl.Addrs})
		}()
	})

	// the filter job (R2): the poll's classification pipeline. The mail
	// root resolves once (argv-only, F4 - the setupAccounts pattern); a
	// failure disables the job - the client still works, the poll
	// degrades to the refresher's plain new.
	var fj *filterJob
	if root, err := mailRoot(); err != nil {
		diag.Warn("filter: disabled", "err", err.Error())
	} else {
		fj = newFilterJob(bus, worker, st, root, view.Name)
	}

	go runRefresher(ctx, bus, worker, refresher, st, fj)

	// the new-mail notification (R2 side effect): the filter job's
	// completion event, live runs only - a dry-run report is review
	// noise, not a delivery. The backend resolves once at startup
	// (auto-detected by default); the payload is the count plus the
	// priority subjects (F6: subjects only).
	go func() {
		backend := resolveNotifyBackend(st.Config(), notifyDaemonReachable)
		ch := bus.Subscribe()
		for e := range ch {
			if d, ok := e.(core.FilterDone); ok && !d.DryRun {
				go notifyNewMail(st.Config(), backend, d.Entries, d.Priority)
			}
		}
	}()

	busCh := bus.Subscribe()
	// WithFPS(120) aligns the renderer's write tick with the model's 8ms
	// paint cadence (ShouldRender gate): at the 60fps default a paint waits
	// up to 16.6ms for the next write tick, and the release-settle paint
	// lands a frame late. Idle ticks are free (the renderer skips unchanged
	// frames), so the higher rate costs nothing when nothing moves.
	prog := tea.NewProgram(tui.New(view, busCh, cfg.Bindings, cfg.TagActions, bus, st, cfg.UI), tea.WithFPS(120))
	go func() {
		<-ctx.Done()
		prog.Quit()
	}()
	_, err = prog.Run()
	return err
}

// BodyRenderHook is the render-transform boundary (R8's Lua layer
// registers adapters here, decision record 20): a transform on the
// thread's rendered lines, run on the async open job under the chain
// deadline. It sees F1-clean plain text (pre-styling), never raw mail.
type BodyRenderHook func(ctx context.Context, lines []core.Line) ([]core.Line, error)

var renderHooks []BodyRenderHook

// renderHookBudget bounds one transform chain. A hook that exceeds it
// drops its output and the render falls back to the un-hooked lines -
// the pager never blocks on a plugin (the matcha freeze gap this
// fixes). The context carries the deadline: a gopher-lua adapter can
// wire it to SetContext as the kill switch.
var renderHookBudget = time.Second

// RegisterBodyRenderHook registers a thread-render transform. The
// round trip is free with no hooks registered: applyBodyRenderHooks is
// a no-op, and openThread's hot path never touches a context.
func RegisterBodyRenderHook(fn BodyRenderHook) {
	renderHooks = append(renderHooks, fn)
}

// applyBodyRenderHooks runs the registered transforms in order under
// one chain deadline. A hook that errors or overruns the budget stops
// the chain and falls back to the lines it received - the last good
// render wins (F6: only the error is logged, never content).
func applyBodyRenderHooks(lines []core.Line) []core.Line {
	if len(renderHooks) == 0 {
		return lines
	}
	ctx, cancel := context.WithTimeout(context.Background(), renderHookBudget)
	defer cancel()
	for _, h := range renderHooks {
		out, err := h(ctx, lines)
		if err != nil {
			log.Printf("body render hook: %v", err)
			return lines
		}
		lines = out
	}
	return lines
}

// openThread loads a thread through the worker, renders it, and
// publishes ThreadLoaded with the render lines (R13 two-step: content
// loads on open only; the render + transforms run here on the async
// job, never on the TUI's event path). A full open (preview=false)
// marks the thread read with an ActTag -unread (R1 - read is a tag;
// the refresh cycle reconciles it into the view). The tag failure
// keeps the thread open - the fetch already succeeded - and surfaces
// as a JobError.
func openThread(worker workerAPI, bus *core.Bus, threadID string, preview bool) {
	rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActThread, ThreadID: threadID})
	if err != nil {
		bus.Publish(core.ThreadLoaded{ThreadID: threadID, Preview: preview, Err: err})
		return
	}
	if rpl.Err != nil {
		bus.Publish(core.ThreadLoaded{ThreadID: threadID, Preview: preview, Err: rpl.Err})
		return
	}
	lines, err := mail.RenderThread(rpl.Msgs)
	if err != nil {
		bus.Publish(core.ThreadLoaded{ThreadID: threadID, Preview: preview, Err: err})
		return
	}
	lines = applyBodyRenderHooks(lines)
	bus.Publish(core.ThreadLoaded{ThreadID: threadID, Preview: preview, Lines: lines})
	if !preview {
		rpl, err := worker.Call(notmuch.Action{
			Kind:   notmuch.ActTag,
			Query:  "thread:" + threadID,
			TagOps: []core.TagOp{{Tag: "unread", Add: false}},
		})
		if err != nil || rpl.Err != nil {
			bus.Publish(core.JobError{Job: "open", Err: fmt.Errorf("mark read %s: %v %v", threadID, err, rpl.Err)})
		}
	}
}

// runRefresher is the refresh loop: the poll ticker refreshes the view
// at the [refresh] interval (R2/R3 - the poll is the trigger for the
// user's classification pipeline, so the cadence is configurable,
// default 20 min). The manual refresh key publishes RefreshRequested,
// which runs the same poll body. With the filter job enabled it owns
// `notmuch new` (the revision bracket + delta classification); its
// ActNew's WorkerDone cycles the view, and a post-ActNew cycle catches
// the lastmod bumps of the classification tags. Disabled, the poll
// runs a plain `notmuch new` here. A [refresh] config change re-arms
// the ticker; other sections reach onConfig.
func runRefresher(ctx context.Context, bus *core.Bus, worker workerAPI, r *refresher, st *config.Store, fj *filterJob) {
	ch := bus.Subscribe()
	// interval 0 = the automatic poll is off (a nil channel never
	// fires in the select); the refresh key still runs the poll body
	// on demand.
	var tickCh <-chan time.Time
	var ticker *time.Ticker
	if st.Config().Refresh.Interval > 0 {
		ticker = time.NewTicker(refreshInterval(st))
		defer ticker.Stop()
		tickCh = ticker.C
	}
	poll := func() {
		if fj != nil && st.Config().Filter.Enabled {
			// the filter job owns `notmuch new`; the view cycle comes
			// from its ActNew's WorkerDone (the refresher's own
			// WorkerDone case below)
			go fj.run()
			return
		}
		if rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActNew}); err != nil || rpl.Err != nil {
			// a backend without a New path (ErrUnsupported) is expected -
			// the poll then degrades to the revision refresh, which picks
			// up external new runs
			if !errors.Is(err, notmuch.ErrUnsupported) && !errors.Is(rpl.Err, notmuch.ErrUnsupported) {
				diag.Warn("notmuch new", "err", fmt.Sprintf("%v %v", err, rpl.Err))
			}
		}
		r.cycle()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-tickCh:
			poll()
		case e := <-ch:
			switch e := e.(type) {
			case core.RefreshRequested:
				poll()
			case core.WorkerDone:
				r.cycle()
			case core.ConfigChanged:
				if e.Section == "refresh" {
					if ticker != nil {
						ticker.Reset(refreshInterval(st))
					} else if st.Config().Refresh.Interval > 0 {
						ticker = time.NewTicker(refreshInterval(st))
						defer ticker.Stop()
						tickCh = ticker.C
					}
				}
				r.onConfig(st, e)
			}
		}
	}
}

// refreshInterval is the [refresh] poll cadence as a duration
// (validated >= 1s at load).
func refreshInterval(st *config.Store) time.Duration {
	return time.Duration(st.Config().Refresh.Interval) * time.Second
}

// setupAccounts is the `notmutt setup` subcommand: resolve the
// notmuch mail root from the CLI config (argv-only, F4), detect the
// accounts by their folder structure (the merged template set), and
// write the generated config next to config.toml as accounts.toml
// (0600, F5). Detection reads directory names only, never mail
// content.
func setupAccounts() error {
	root, err := mailRoot()
	if err != nil {
		return fmt.Errorf("setup: resolve database.path: %w", err)
	}
	cfg, err := config.Load(configPath())
	if err != nil {
		log.Printf("setup: config %s: %v", configPath(), err)
	}
	accs, err := setup.Detect(root, mergedTemplates(cfg.Setup.Templates))
	if err != nil {
		return fmt.Errorf("setup: %w", err)
	}
	var matched, unmatched []string
	for _, a := range accs {
		if a.Template == "" {
			unmatched = append(unmatched, a.Name)
		} else {
			matched = append(matched, fmt.Sprintf("%s (%s)", a.Name, a.Template))
		}
	}
	dir := filepath.Dir(configPath())
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("setup: %w", err)
	}
	seedTemplates(dir)
	path := filepath.Join(dir, "accounts.toml")
	if err := os.WriteFile(path, []byte(setup.Generate(accs)), 0600); err != nil {
		return fmt.Errorf("setup: write %s: %w", path, err)
	}
	fmt.Printf("setup: accounts: %s\n", strings.Join(matched, ", "))
	fmt.Printf("setup: no template match: %s\n", strings.Join(unmatched, ", "))
	fmt.Printf("setup: wrote %s\n", path)
	return nil
}

// seedTemplates copies the shipped example templates to
// <configdir>/lua/templates on the first setup run: the place users
// copy from to add a provider shape (a contributed template overrides
// the built-in of the same name, R2). Never overwrites an existing
// file - a customized layout stays untouched, missing files fill in.
func seedTemplates(dir string) {
	dst := filepath.Join(dir, "lua", "templates")
	if err := os.MkdirAll(dst, 0700); err != nil {
		log.Printf("setup: seed templates: %v", err)
		return
	}
	entries, err := fs.ReadDir(templateFS, "lua/templates")
	if err != nil {
		log.Printf("setup: seed templates: %v", err)
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(dst, e.Name())
		if _, err := os.Stat(path); err == nil {
			continue
		}
		src, err := fs.ReadFile(templateFS, "lua/templates/"+e.Name())
		if err != nil {
			continue
		}
		if err := os.WriteFile(path, src, 0600); err != nil {
			log.Printf("setup: seed template %s: %v", e.Name(), err)
		}
	}
}

// mergedTemplates is the detection set: the built-ins (the lua build
// evaluates the embedded template files, default builds the Go
// fallback), then the OPT-IN contributed Lua templates from
// <configdir>/lua/templates - only the names in active load (not all
// templates are autoloaded; the seeded examples stay inert until
// [setup] templates names them), sorted by name. A loaded Lua
// template replaces the built-in of the same name (the R2 preset
// override rule).
func mergedTemplates(active []string) []setup.Template {
	base := builtinTemplates()
	if len(base) == 0 {
		base = setup.Templates
	}
	out := make([]setup.Template, 0, len(base)+1)
	seen := map[string]bool{}
	for _, t := range base {
		out = append(out, t)
		seen[t.Name] = true
	}
	var add []setup.Template
	for _, t := range luaTemplates(filepath.Dir(configPath()), active) {
		if !seen[t.Name] {
			add = append(add, t)
			continue
		}
		for i := range out {
			if out[i].Name == t.Name {
				out[i] = t
				break
			}
		}
	}
	sort.Slice(add, func(i, j int) bool { return add[i].Name < add[j].Name })
	return append(out, add...)
}

func configPath() string {
	if p := os.Getenv("NOTMUTT_CONFIG"); p != "" {
		return p
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "config.toml"
	}
	return filepath.Join(base, "notmutt", "config.toml")
}

func cachePath() string {
	base, err := os.UserCacheDir()
	if err != nil {
		return "mime-cache.db"
	}
	return filepath.Join(base, "notmutt", "mime-cache.db")
}

func firstView(cfg config.Config) string {
	for name := range cfg.Views {
		return name
	}
	return ""
}

// validateBindings checks the loaded bindings against the per-context
// action vocabulary (R9): every value must be a builtin action of its
// context or a declared tag action (index context only), no tag action
// may shadow an index builtin name, and every tag action must be
// referenced by at least one binding.
func validateBindings(cfg *config.Config) error {
	used := map[string]bool{}
	for context, km := range cfg.Bindings {
		for key, action := range km {
			if tui.Actions[context][action] {
				continue
			}
			if context != "index" {
				return fmt.Errorf("bindings.%s: key %q: unknown action %q", context, key, action)
			}
			if _, ok := cfg.TagActions[action]; !ok {
				return fmt.Errorf("bindings.%s: key %q: unknown action %q", context, key, action)
			}
			used[action] = true
		}
	}
	for name := range cfg.TagActions {
		if tui.Actions["index"][name] {
			return fmt.Errorf("tag action %q collides with a builtin action", name)
		}
		if !used[name] {
			return fmt.Errorf("tag action %q is not bound", name)
		}
	}
	return nil
}

// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	netmail "net/mail"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"notmutt/compose"
	"notmutt/config"
	"notmutt/core"
	"notmutt/filter"
	"notmutt/i18n"
	"notmutt/lib/xdg"
	"notmutt/mail"
	"notmutt/notmuch"
	"notmutt/setup"
	"notmutt/tui"
)

const lockBudget = 10 * time.Second

// pollWatchInterval is the external-poll stamp watch tick: how fast a
// headless `notmutt poll` reaches running clients. A stat per tick is
// cheap; the poll body itself is idempotent, so a stamp change across
// N instances costs N no-op revision brackets at most.
const pollWatchInterval = 5 * time.Second

//go:embed lua/templates/*.lua
var templateFS embed.FS

func Run() error {
	if len(os.Args) > 1 && os.Args[1] == "setup" {
		return setupAccounts()
	}
	if len(os.Args) > 1 && os.Args[1] == "poll" {
		return pollOnce()
	}
	if len(os.Args) > 1 && os.Args[1] == "attachments" {
		return attachmentsOnce()
	}
	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		// the MCP stdio server (mcp.go): disabled by default - builds
		// without the mcp+lua tags answer with the stub's not-built-in
		// error
		return serveMCP()
	}
	cfg, err := config.Load(configDir())
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	i18n.SetLanguage(cfg.UI.Language)
	me := cfg.MyAddrs()
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

	view := core.NewView(cfg.ActiveView, cfg.Views[cfg.ActiveView].Query)
	refresher := newRefresher(bus, worker, view, 0)

	// views is the view registry for the hydrator: name -> view. The
	// search tabs (the ctrl+f seam) register under their query; a closed
	// tab's entry lingers until exit (bounded by the search-tab count,
	// harmless).
	views := map[string]*core.View{view.ViewName(): view}

	cjob := newCacheJob(bus, worker, view, cachePath())
	go cjob.Run(ctx)

	// the hydrator fills stub rows into real thread trees (R3); the
	// threaded closure reads the store, so a view-config change picks up
	// on the next scan. The registry resolves the triggering event's
	// view - a search tab hydrates like the main one.
	tjob := newThreadJob(bus, worker, view, func() bool {
		return st.Config().Views[st.Config().ActiveView].Threads
	}, func(name string) *core.View { return views[name] })
	go tjob.Run(ctx)

	groups := st.Config().TagGroupList()
	view.SetGroups(groups)
	b := st.Config().Index.Thread
	view.SetWindowBudget(b.MaxRows)
	// the thread-tail marks derive from the view rows (never an open);
	// the identity set is startup-captured like the seam closures above
	view.SetMe(me)

	// the search tab (the ctrl+f key): the app configures the fresh
	// view like the main one (window budget, identity) and loads the
	// raw notmuch query into it; the query is the view's name, so its
	// events key per tab. A repeated query renames the new tab (a #N
	// suffix) so its events route to it, not the earlier tab.
	tui.SetSearchHandler(func(v *core.View) {
		if views[v.ViewName()] != nil {
			v.SetIdentity(v.ViewName()+" #"+strconv.Itoa(len(views)), v.ViewQuery())
		}
		v.SetGroups(groups)
		v.SetWindowBudget(b.MaxRows)
		v.SetMe(me)
		views[v.ViewName()] = v
		go runSearchQuery(worker, bus, v)
	})

	// the notmuch mail root (argv-only, F4 - the setupAccounts pattern):
	// ONE resolution for the filter job, the fcc derivation, and the
	// apply's move-after-tag. A failure disables the filter job and
	// leaves the fcc empty (skipped on send) - the client still works.
	root, rootErr := mailRoot()
	if rootErr != nil {
		diag.Warn("filter: disabled", "err", rootErr.Error())
	}

	tui.SetApplyHandler(func() {
		go func() {
			if err := applyStaged(view, groups, worker, cfg, root); err != nil {
				bus.Publish(core.JobError{Job: "apply", Err: err})
			}
			// the view changed either way (applied drops and baselines);
			// a partial failure still renders the succeeded entries
			bus.Publish(core.ViewDiff{View: view.ViewName()})
		}()
	})

	// open: the worker loads the thread's messages (headers + paths,
	// ActThread), the open job renders the files and runs the render
	// transforms, and the TUI attaches the lines on ThreadLoaded (R13
	// two-step - content loads on open only). The preview variant (the
	// p key) skips the read-marking; the TUI keeps the index surface
	// for the popup. The headers flag is the h key: the render includes
	// the full header block.
	tui.SetOpenHandler(func(threadID, msgID string, preview, headers bool, width int) {
		// RenderAuto: the open default resolves per sender domain
		// ([pager] default-views) once the message is in hand - the
		// domain is message data, only the fetch has it
		go openThread(worker, bus, threadID, msgID, preview, core.RenderAuto, headers, width, false, cfg.Pager.DefaultViews, me)
	})

	// the render toggle (the v key in the pager), the source view
	// (ctrl+u), and the link labels (the F key): the same open path
	// with the other view - the worker refetches the thread (the open
	// job is the render owner, R13; the TUI never renders). labelLinks
	// is the F key: the renderer prefixes every link with its "[N]"
	// label and the target list rides the reply. The explicit modes
	// never resolve against the domain map.
	tui.SetRenderHandler(func(threadID, msgID string, mode core.RenderMode, headers bool, width int, labelLinks bool) {
		go openThread(worker, bus, threadID, msgID, false, mode, headers, width, labelLinks, nil, me)
	})

	// the attachment view (the v dialog's enter) and save (the s key
	// in an attachment view): both re-extract the chosen attachment
	// from the thread's message on demand - ParseMessage never keeps
	// non-image part bytes, the demand path reads one part
	tui.SetAttachmentViewHandler(func(threadID, msgID string, ordinal int) {
		go viewAttachment(worker, bus, threadID, msgID, ordinal)
	})
	tui.SetAttachmentSaveHandler(func(threadID, msgID string, ordinal int, path string) {
		go saveAttachment(worker, bus, threadID, msgID, ordinal, path)
	})

	// the categorize hotkey (the index c key): the app runs the
	// attachment-category pass over the cursor thread's messages and
	// publishes CategorizeResult (the save/skip lines for the log)
	tui.SetCategorizeHandler(func(threadID string) {
		go categorizeThread(worker, bus, threadID, &cfg)
	})

	// the remote image fetch (the load-remote-images mode): http(s)
	// srcs fetch ONLY on the key, capped and off the render path
	// (imgfetch.go); the TUI publishes the url through this seam. The
	// tracking-pixel block travels with the config
	tui.SetImageFetchHandler(func(url string) {
		go fetchImage(bus, url, cfg.Pager.AllowTrackingImages)
	})

	// the signatures root (spec section 9): ONE path, both halves of
	// the send surface read the same tree - the app (default signature
	// in buildCompose) and the tui (the picker lists the files)
	sigDir = filepath.Join(configDir(), "signatures")
	tui.SetSignaturesDir(sigDir)

	// the Lua layer (R8): plugin files from <configdir>/lua, each
	// registering its body_render as a render transform. The adapter
	// compiles only under the lua build tag (the R12 pattern); default
	// builds run the no-op stub. Loaded before any open can fire.
	loadLuaPlugins(filepath.Join(configDir(), "lua"), cfg.Lua.Network)

	// binding validation AFTER the plugin load: a binding may name a
	// plugin-registered action (the lua build only - the stub registry
	// is empty, so default builds reject them as unknown actions)
	if err := validateBindings(&cfg); err != nil {
		return err
	}

	// the Lua action seams (R8): the dispatch fallthrough invokes
	// actions by name, the plugin-key fallback by key + area; both run
	// on their own goroutine and publish LuaResult
	tui.SetPluginActionSource(func() map[string]bool { return pluginActionNames() })
	tui.SetPluginKeyBoundSource(func(key, area string) bool { return luaKeyBound(key, area) })
	tui.SetLuaActionHandler(func(action, threadID string) {
		go runLuaAction(action, threadID, bus, &cfg, worker)
	})
	tui.SetLuaKeyHandler(func(key, area, threadID string) {
		go runLuaBind(key, area, threadID, bus, &cfg, worker)
	})
	tui.SetLuaCommandHandler(func(command, threadID string) {
		go runLuaCommand(command, threadID, bus, &cfg, worker)
	})

	// attach commands: config tables register first, then Lua plugin
	// registrations (later, per-plugin load order) - both land in the
	// registry; the TUI reads it through the seam
	loadConfigAttachCommands(cfg)
	tui.SetAttachCommandSource(func() []tui.AttachCommand { return attachCommandSnapshot() })

	// the link opener (the pager F key): the config's opener argv with
	// the url appended (F4 - argv only, never shell-interpolated), a
	// missing config opens with xdg-open. Fire-and-forget: the opener
	// detaches its viewer and returns.
	tui.SetOpenLinkHandler(func(url string) {
		argv := cfg.Opener
		if len(argv) == 0 {
			argv = []string{"xdg-open"}
		}
		exec.Command(argv[0], append(argv[1:], url)...).Start()
	})

	// reply: the app prefills the dialogue (account detection, parse,
	// default signature) and publishes ComposeOpened - the TUI attaches
	// the tab
	tui.SetReplyHandler(func(msg *core.Message, mode string) {
		go func() {
			st, err := replyPrefill(cfg, view, worker, msg, mode, root)
			if err != nil {
				diag.Warn("reply", "err", err.Error())
				bus.Publish(core.JobError{Job: "reply", Err: err})
				return
			}
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
		go sendJob(bus, worker, view, cfg, root, st)
	})

	// draft: the abort confirm's d key - a local write, runs inline;
	// the error keeps the composition open
	tui.SetDraftHandler(func(st compose.State) error {
		return saveDraft(bus, worker, view, cfg, root, st)
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

	// the filter job (R2): the poll's classification pipeline; a root
	// resolution failure disables it - the poll degrades to the
	// refresher's plain new.
	var fj *filterJob
	if rootErr == nil {
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

	// the picker and prompt round trips (R8): the TUI publishes the
	// results on the bus, this subscriber resumes the Lua action blocked
	// on its waiter
	go func() {
		ch := bus.Subscribe()
		for e := range ch {
			if p, ok := e.(core.PickerResult); ok {
				deliverPickerResult(p)
			}
			if p, ok := e.(core.PromptResult); ok {
				deliverPromptResult(p)
			}
		}
	}()

	busCh := bus.Subscribe()
	// The loop owns the screen (tcell, record 23): keys and resizes
	// come from its event channel, the model's EventCmd re-arms the
	// bus. The loop paints on ShouldRender, so the model's 8ms frame
	// tick IS the paint cadence - no renderer tick to align (the
	// WithFPS machinery died with tea).
	quitCh := make(chan struct{})
	go func() {
		<-ctx.Done()
		close(quitCh)
	}()
	tui.SetFileDirState(statePath())
	return tui.Run(tui.New(view, busCh, cfg.Bindings, cfg.TagActions, bus, st, cfg.UI), quitCh)
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

// openThread loads the opened message through the worker, renders it,
// and publishes ThreadLoaded with the render lines (R13 two-step:
// content loads on open only; the render + transforms run here on the
// async job, never on the TUI's event path). The thread fetch narrows
// to the message (msgID): the pager shows one message, never the
// whole thread - a bare open (empty msgID) renders the thread's
// first. The mark is computed against the FULL fetch before the
// narrowing: the pager tints the message by its position in the
// conversation (the recent-5 and other-side highlight, me = the
// account from addresses). A full open (preview=false) marks the
// opened message read with an ActTag -unread (R1 - read is a tag; the
// refresh cycle reconciles it into the view). The tag failure keeps
// the thread open - the fetch already succeeded - and surfaces as a
// JobError.
func openThread(worker workerAPI, bus *core.Bus, threadID, msgID string, preview bool, mode core.RenderMode, headers bool, width int, labelLinks bool, defViews map[string]string, me []string) {
	rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActThread, ThreadID: threadID})
	if err != nil {
		bus.Publish(core.ThreadLoaded{ThreadID: threadID, MsgID: msgID, Preview: preview, Err: err})
		return
	}
	if rpl.Err != nil {
		bus.Publish(core.ThreadLoaded{ThreadID: threadID, MsgID: msgID, Preview: preview, Err: rpl.Err})
		return
	}
	idx := 0
	if msgID != "" {
		for i, m := range rpl.Msgs {
			if m.ID == msgID {
				idx = i
				break
			}
		}
	}
	if len(rpl.Msgs) > 0 {
		rpl.Msgs = []core.Message{rpl.Msgs[idx]}
	}
	msgID = ""
	if len(rpl.Msgs) > 0 {
		msgID = rpl.Msgs[0].ID
	}
	if mode == core.RenderAuto {
		mode = openViewMode(defViews, rpl.Msgs)
		// an html-only message has no plain content: its plain render is
		// the html structure with the colors stripped, so the open
		// default upgrades it to the html view (an explicit default-views
		// plain mapping cannot prefer content that does not exist)
		if mode == core.RenderPlain && len(rpl.Msgs) > 0 && len(rpl.Msgs[0].Paths) > 0 {
			if parsed, err := mail.ParseMessage(rpl.Msgs[0].Paths[0]); err == nil && mail.ViewMime(parsed, core.RenderPlain) == "text/html" {
				mode = core.RenderHTML
			}
		}
	}
	lines, mime, links, err := mail.RenderThread(rpl.Msgs, mode, headers, width, labelLinks)
	if err != nil {
		bus.Publish(core.ThreadLoaded{ThreadID: threadID, MsgID: msgID, Preview: preview, Err: err})
		return
	}
	lines = applyBodyRenderHooks(lines)
	bus.Publish(core.ThreadLoaded{ThreadID: threadID, MsgID: msgID, Preview: preview, RenderMode: mode, Headers: headers, LinkLabels: labelLinks, Links: links, Mime: mime, Lines: lines})
	// the read mark names the opened message, never the whole thread:
	// the other messages in the thread keep their unread state
	if !preview && msgID != "" {
		rpl, err := worker.Call(notmuch.Action{
			Kind:   notmuch.ActTag,
			Query:  "id:" + msgID,
			TagOps: []core.TagOp{{Tag: "unread", Add: false}},
		})
		if err != nil || rpl.Err != nil {
			bus.Publish(core.JobError{Job: "open", Err: fmt.Errorf("mark read %s: %v %v", msgID, err, rpl.Err)})
		}
	}
}

// findMsg locates the opened message in the thread fetch by id; the
// thread's first when no id rode the request (a bare open). The
// request's id can be stale (the message moved between the index
// fetch and the open): the fallback keeps the pager functional on the
// first message.
func findMsg(msgs []core.Message, msgID string) (core.Message, bool) {
	if msgID != "" {
		for _, msg := range msgs {
			if msg.ID == msgID {
				return msg, true
			}
		}
	}
	if len(msgs) == 0 {
		return core.Message{}, false
	}
	return msgs[0], true
}

// extractAttachment fetches the opened message and reads the
// ordinal-th attachment's bytes and type - the shared demand path of
// the view and save seams (one file open per demand, never held in
// memory). foundID is the message actually used: the reply identity
// must match the pager's.
func extractAttachment(worker workerAPI, threadID, msgID string, ordinal int) (name, typ string, data []byte, foundID string, err error) {
	rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActThread, ThreadID: threadID})
	if err != nil {
		return "", "", nil, msgID, err
	}
	if rpl.Err != nil {
		return "", "", nil, msgID, rpl.Err
	}
	msg, ok := findMsg(rpl.Msgs, msgID)
	if !ok || len(msg.Paths) == 0 {
		return "", "", nil, msgID, fmt.Errorf("no message path")
	}
	name, typ, data, err = mail.ExtractAttachment(msg.Paths[0], ordinal)
	return name, typ, data, msg.ID, err
}

// viewAttachment serves the v dialog's enter: extract + render the
// chosen attachment to pager lines and publish AttachmentLoaded - the
// TUI swaps the pager content until back re-opens the message. A
// matching mailcap copiousoutput entry replaces the bytes with its
// preview text (a pdf renders as pdftotext output, never as bytes);
// a preview failure is an error, not a fallback to the raw dump.
func viewAttachment(worker workerAPI, bus *core.Bus, threadID, msgID string, ordinal int) {
	name, typ, data, foundID, err := extractAttachment(worker, threadID, msgID, ordinal)
	if err != nil {
		bus.Publish(core.AttachmentLoaded{ThreadID: threadID, MsgID: foundID, Err: err})
		return
	}
	if argv, ok := previewMailcap(typ); ok {
		if out, err := mail.RunPreview(argv, data); err != nil {
			bus.Publish(core.AttachmentLoaded{ThreadID: threadID, MsgID: foundID, Err: err})
			return
		} else {
			data = out
		}
	}
	bus.Publish(core.AttachmentLoaded{ThreadID: threadID, MsgID: foundID, Ordinal: ordinal, Name: name, Lines: mail.RenderAttachment(data)})
}

// previewMailcap resolves an attachment type to its mailcap preview
// command: the built-in defaults (a pdf previews as pdftotext text)
// plus the user's <configdir>/mailcap overrides by type.
func previewMailcap(typ string) ([]string, bool) {
	mc := mail.DefaultMailcap()
	if b, err := os.ReadFile(filepath.Join(configDir(), "mailcap")); err == nil {
		mc.Parse(b)
	}
	return mc.PreviewCommand(typ)
}

// saveAttachment serves the s key in an attachment view: extract the
// attachment and write it to the path (0600, F5); the result rides
// AttachmentSaved for the status line.
func saveAttachment(worker workerAPI, bus *core.Bus, threadID, msgID string, ordinal int, path string) {
	_, _, data, _, err := extractAttachment(worker, threadID, msgID, ordinal)
	if err != nil {
		bus.Publish(core.AttachmentSaved{Path: path, Err: err})
		return
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		bus.Publish(core.AttachmentSaved{Path: path, Err: err})
		return
	}
	bus.Publish(core.AttachmentSaved{Path: path})
}

// openViewMode resolves the open key's default view for a thread: the
// sender domain's configured default ([pager] default-views), plain
// otherwise. The thread's first message is the thread identity - a
// mapped domain's thread opens in that domain's view.
func openViewMode(defViews map[string]string, msgs []core.Message) core.RenderMode {
	if len(msgs) > 0 && defViews != nil {
		if d := senderDomain(msgs[0].Author); d != "" {
			if v, ok := defViews[d]; ok && v == "html" {
				return core.RenderHTML
			}
		}
	}
	return core.RenderPlain
}

// senderDomain extracts the address part's domain, lowercased (the
// lookup is case-insensitive); an unparseable From has no domain and
// keeps the plain default.
func senderDomain(from string) string {
	a, err := netmail.ParseAddress(from)
	if err != nil || a.Address == "" {
		return ""
	}
	if _, d, ok := strings.Cut(a.Address, "@"); ok {
		return strings.ToLower(d)
	}
	return ""
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
	// the external-poll stamp: `notmutt poll` from another process
	// touches it after a successful run; this watcher runs the poll
	// body on change, so every open instance displays the update
	// regardless of how many there are. A stat per tick is the
	// cheapest wake-up signal - the revision work happens inside the
	// poll body, and an unchanged stamp costs nothing.
	stamp := pollStampPath()
	last, err := os.Stat(stamp)
	var stampMtime time.Time
	if err == nil {
		stampMtime = last.ModTime()
	}
	watch := time.NewTicker(pollWatchInterval)
	defer watch.Stop()
	// the initial load runs in this event-loop goroutine: cycle and
	// onConfig are serialized by construction - the store's first view
	// change cannot race the startup walk (the startup caller used to
	// launch cycle() unsynchronized; goto keys made the store a live
	// writer, so the initial load moved in here)
	r.cycle()
	for {
		select {
		case <-ctx.Done():
			return
		case <-watch.C:
			fi, err := os.Stat(stamp)
			if err == nil && !fi.ModTime().Equal(stampMtime) {
				stampMtime = fi.ModTime()
				poll()
			}
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

// refreshInterval is the [refresh] poll cadence as a duration (the
// config value is minutes - mbsync syncs in minutes, never seconds).
func refreshInterval(st *config.Store) time.Duration {
	return time.Duration(st.Config().Refresh.Interval) * time.Minute
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
	cfg, err := config.Load(configDir())
	if err != nil {
		log.Printf("setup: config %s: %v", configDir(), err)
	}
	accs, err := setup.Detect(root, mergedTemplates(cfg.Setup.Templates))
	if err != nil {
		return fmt.Errorf("setup: %w", err)
	}
	// the physical resolution (the mover's own machinery): each matched
	// account's hard-tag folders resolve against the real tree BEFORE
	// anything is written - an account that resolves nothing fails
	// setup, the generated config would promise moves that cannot
	// happen.
	resolved, err := resolveSetupFolders(root, accs)
	if err != nil {
		return err
	}
	var matched, unmatched []string
	for _, a := range accs {
		if a.Template == "" {
			unmatched = append(unmatched, a.Name)
		} else {
			matched = append(matched, fmt.Sprintf("%s (%s)", a.Name, a.Template))
		}
	}
	dir := configDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("setup: %w", err)
	}
	seedTemplates(dir)
	path := filepath.Join(dir, "accounts.toml")
	if err := os.WriteFile(path, []byte(setup.Generate(accs)), 0600); err != nil {
		return fmt.Errorf("setup: write %s: %w", path, err)
	}
	fmt.Printf("setup: accounts: %s\n", strings.Join(matched, ", "))
	for _, a := range accs {
		if lines, ok := resolved[a.Name]; ok {
			fmt.Printf("setup: %s: %s\n", a.Name, strings.Join(lines, " "))
		}
	}
	fmt.Printf("setup: no template match: %s\n", strings.Join(unmatched, ", "))
	fmt.Printf("setup: wrote %s\n", path)
	return nil
}

// resolveSetupFolders resolves each matched account's hard-tag folders
// against the physical tree through the mover's own machinery
// (Candidates + ResolveFolder - the account's folder space plus its
// detected folder map; first existing wins, else the first candidate,
// the sync tool creates the folder). An account whose folders resolve
// to nothing fails setup. Returns per-account sorted "tag=path" lines
// with absolute paths.
func resolveSetupFolders(root string, accs []setup.Account) (map[string][]string, error) {
	out := map[string][]string{}
	for _, a := range accs {
		if a.Template == "" {
			continue
		}
		acc := config.Account{Folder: &a.Name, Folders: a.Folders}
		tags := make([]string, 0, len(a.Folders))
		for tag := range a.Folders {
			tags = append(tags, tag)
		}
		sort.Strings(tags)
		var lines []string
		for _, tag := range tags {
			cs := filter.Candidates(acc, tag)
			if len(cs) == 0 {
				continue
			}
			resolved := filter.ResolveFolder(root, acc.Tag(a.Name), cs)
			lines = append(lines, tag+"="+filepath.Join(root, resolved))
		}
		if len(lines) == 0 {
			return nil, fmt.Errorf("setup: %s: no folder resolves - its tag folders are missing", a.Name)
		}
		out[a.Name] = lines
	}
	return out, nil
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
	for _, t := range luaTemplates(configDir(), active) {
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

func configDir() string {
	if p := os.Getenv("NOTMUTT_CONFIG"); p != "" {
		return p
	}
	base := xdg.ConfigHome()
	if base == "" {
		return "notmutt"
	}
	return filepath.Join(base, "notmutt")
}

// statePath is the client-state file (XDG state): non-derived,
// survives cache clears - the file chooser's last directory.
func statePath() string {
	return filepath.Join(xdg.StateHome(), "notmutt", "state.toml")
}

func cachePath() string {
	if base := xdg.CacheHome(); base == "" {
		return "mime-cache.db"
	}
	return filepath.Join(xdg.CacheHome(), "notmutt", "mime-cache.db")
}

// pollSpec is the poll command's run mode: a fixed (from, to] window
// replays a stored diff (the reproducibility harness - the revision
// moves with every new run, so reruns pin the window instead of
// recapturing it), and apply overrides the dry-run config for this
// run only, the config file untouched.
type pollSpec struct {
	apply    bool
	from, to uint64
	windowed bool
}

// parsePollSpec reads the poll flags: --apply (one-shot live override
// of the dry-run config) and --from/--to (replay a fixed lastmod
// window without a new run or a revision capture).
func parsePollSpec(args []string) (pollSpec, error) {
	var spec pollSpec
	seen := map[string]bool{}
	fs := flag.NewFlagSet("poll", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // the parse error surfaces once, via the caller
	fs.BoolVar(&spec.apply, "apply", false, "apply the diff, overriding the dry-run config for this run")
	fs.Uint64Var(&spec.from, "from", 0, "replay a fixed lastmod window (with -to)")
	fs.Uint64Var(&spec.to, "to", 0, "replay a fixed lastmod window (with -from)")
	if err := fs.Parse(args); err != nil {
		return spec, err
	}
	fs.Visit(func(f *flag.Flag) { seen[f.Name] = true })
	if seen["from"] != seen["to"] {
		return spec, errors.New("poll: --from and --to must be given together")
	}
	spec.windowed = seen["from"]
	if spec.windowed && spec.from > spec.to {
		return spec, errors.New("poll: --from must not exceed --to")
	}
	return spec, nil
}

// pollOnce is the headless poll (`notmutt poll [--apply] [--from N
// --to N]`): the same poll body as the client's interval and the
// in-TUI refresh key, run outside the client for scripts and cron.
// The filter owns `notmuch new` when enabled; a disabled filter
// degrades to a plain new. Dry-run config is honored - the first runs
// against a real mailbox are always dry; --apply overrides it for one
// run. --from/--to replay a stored lastmod window without touching
// notmuch (see the reproducibility harness, scripts/poll-repro.sh). A
// successful run touches the poll stamp, waking every running client's
// refresher so they display the update.
func pollOnce() error {
	spec, err := parsePollSpec(os.Args[2:])
	if err != nil {
		return err
	}
	cfg, err := config.Load(configDir())
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	bus := core.NewBus()
	worker := notmuch.NewWorker(bus, notmuch.New(), lockBudget)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go worker.Start(ctx)
	if rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActOpen, Query: ""}); err != nil || rpl.Err != nil {
		return fmt.Errorf("notmuch open: %v %v", err, rpl.Err)
	}
	root := ""
	if cfg.Filter.Enabled {
		root, err = mailRoot()
		if err != nil {
			return fmt.Errorf("poll: %w", err)
		}
	}
	line, diff, err := runPoll(worker, cfg, root, spec)
	if err != nil {
		return err
	}
	fmt.Println(line)
	for _, l := range diff {
		fmt.Println(l)
	}
	return nil
}

// runPoll is the poll body behind `notmutt poll`: the summary line,
// the reviewable per-entry diff, and the stamp that wakes running
// clients. Shared with the tests; pollOnce resolves the worker and the
// mail root, runPoll decides. A fixed-window spec replays a stored
// lastmod bracket without touching notmuch - no new run, no revision
// capture - so the output must reproduce the stored diff exactly.
func runPoll(worker workerAPI, cfg config.Config, root string, spec pollSpec) (string, []string, error) {
	if !cfg.Filter.Enabled {
		if spec.windowed {
			return "", nil, errors.New("poll: the filter is disabled, no window to replay")
		}
		if rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActNew}); err != nil || rpl.Err != nil {
			return "", nil, fmt.Errorf("notmuch new: %v %v", err, rpl.Err)
		}
		if err := touchPollStamp(); err != nil {
			return "", nil, err
		}
		return "poll: notmuch new (filter disabled)", nil, nil
	}
	if spec.apply {
		cfg.Filter.DryRun = false // one-shot: the config file stays untouched
	}
	rep, mr, win, err := pollDiff(worker, cfg, root, spec, nil)
	if err != nil {
		return "", nil, fmt.Errorf("poll: %w", err)
	}
	if win == "" {
		return "poll: no new mail", nil, nil
	}
	// the stamp means "state changed, wake the clients": a fresh
	// capture saw the revision move; a windowed replay only writes
	// when applying.
	if !spec.windowed || !rep.DryRun {
		if err := touchPollStamp(); err != nil {
			return "", nil, err
		}
	}
	mode := "applied"
	if rep.DryRun {
		mode = "dry-run"
	}
	moved, skipped := moveCounts(mr)
	summary := fmt.Sprintf("poll: %s: %s: %d entries, %d moved, %d skipped", mode, win, len(rep.Entries), moved, skipped)
	return summary, pollDiffLines(rep, mr), nil
}

// pollDiff classifies the poll's window: a fresh capture (ActNew -
// the backend's new wrapper returns the (pre, cur] bracket, the
// revision moving proves new mail) or the fixed (from, to] bracket of
// a replay spec. Returns the window as the summary reports it; a
// fresh capture on a quiet mailbox returns an empty window and no
// classification pass.
func pollDiff(worker workerAPI, cfg config.Config, root string, spec pollSpec, progress func(done, total int)) (*filter.Report, *filter.MoveReport, string, error) {
	pre, cur := spec.from, spec.to
	if !spec.windowed {
		rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActNew})
		if err != nil || rpl.Err != nil {
			if !errors.Is(err, notmuch.ErrUnsupported) && !errors.Is(rpl.Err, notmuch.ErrUnsupported) {
				return nil, nil, "", fmt.Errorf("new: %v %v", err, rpl.Err)
			}
			// a backend without a New path (ErrUnsupported): no
			// bracket, no classification - the poll reports no new mail
			return nil, nil, "", nil
		}
		pre, cur = rpl.Pre, rpl.Rev
		if cur == pre {
			return nil, nil, "", nil // nothing new; no classification pass
		}
	}
	rep, mr, err := classifyDelta(worker, cfg, root, pre, cur, progress)
	if err != nil {
		return rep, mr, "", err
	}
	return rep, mr, fmt.Sprintf("%d..%d", pre, cur), nil
}

// pollDiffLines renders the reviewable diff: per entry, the resolved
// tag ops and the first path's move decision - the dry-run report's
// review surface (what-would-happen; a live run reports the same).
// Paths only, never message ids or subjects (F6). Capped like the
// diag lines: a backfill must not drown the review, and the cap is
// deterministic, so replayed outputs still compare byte-identical.
const pollDiffLineCap = 100

func pollDiffLines(rep *filter.Report, mr *filter.MoveReport) []string {
	byFrom := map[string]filter.MoveEntry{}
	for _, m := range mr.Moves {
		byFrom[m.From] = m
	}
	var lines []string
	n := 0
	for _, e := range rep.Entries {
		ops := make([]string, 0, len(e.Ops))
		for _, op := range e.Ops {
			if op.Add {
				ops = append(ops, "+"+op.Tag)
			} else {
				ops = append(ops, "-"+op.Tag)
			}
		}
		path := ""
		if len(e.Paths) > 0 {
			path = e.Paths[0]
		}
		line := strings.TrimSpace(strings.Join(ops, " ") + "  " + path)
		if m, ok := byFrom[path]; ok {
			if m.To != "" {
				line += "  ->  " + m.To
			} else if m.Skip != "" {
				line += "  (skip: " + m.Skip + ")"
			}
		}
		lines = append(lines, line)
		if n++; n >= pollDiffLineCap {
			lines = append(lines, fmt.Sprintf("# %d more entries", len(rep.Entries)-n))
			break
		}
	}
	return lines
}

// pollStampPath is the cross-process poll signal: `notmutt poll`
// touches the stamp after a successful run; every running client's
// refresher watches its mtime and runs the poll body on change
// (the delivery-gate mark pattern - a cache mtime as the signal).
// Any number of instances pick the update up within one watch tick.
func pollStampPath() string {
	base, err := os.UserCacheDir()
	if err != nil {
		return "poll-stamp"
	}
	return filepath.Join(base, "notmutt", "poll-stamp")
}

func touchPollStamp() error {
	p := pollStampPath()
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return fmt.Errorf("poll stamp: %w", err)
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("poll stamp: %w", err)
	}
	return f.Close()
}

// validateBindings checks the loaded bindings against the per-context
// action vocabulary (R9): every value must be a builtin action of its
// context, a plugin-registered action (the Lua registry, any context),
// or a declared tag action (index context only), no tag action may
// shadow an index builtin name, and every tag action must be
// referenced by at least one binding. Runs after loadLuaPlugins - the
// plugin action set must be in hand.
func validateBindings(cfg *config.Config) error {
	used := map[string]bool{}
	for context, km := range cfg.Bindings {
		for key, action := range km {
			if tui.Actions[context][action] {
				continue
			}
			if strings.HasPrefix(action, "goto-") {
				// a view switch: the view name rides in the action
				// (goto-<view>; the per-account keys derive at load)
				if _, ok := cfg.Views[strings.TrimPrefix(action, "goto-")]; !ok {
					return fmt.Errorf("bindings.%s: key %q: %q: no such view", context, key, action)
				}
				continue
			}
			if pluginActionNames()[action] {
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

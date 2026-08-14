package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"notmutt/config"
	"notmutt/core"
	"notmutt/notmuch"
	"notmutt/tui"
)

const lockBudget = 10 * time.Second

func Run() error {
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
	worker := notmuch.NewWorker(bus, notmuch.NewCLI(), lockBudget)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go worker.Start(ctx)

	// DB open check plus per-view query validation (spec section 3:
	// notmuch dry run for every view query)
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
				return
			}
			bus.Publish(core.ViewDiff{View: view.Name})
		}()
	})

	// open: the worker loads the thread's messages (headers + paths,
	// ActThread), the TUI parses the files into the pager on the
	// ThreadLoaded event (R13 two-step - content loads on open only)
	tui.SetOpenHandler(func(threadID string) {
		go func() {
			rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActThread, ThreadID: threadID})
			if err != nil {
				bus.Publish(core.ThreadLoaded{ThreadID: threadID, Err: err})
				return
			}
			if rpl.Err != nil {
				bus.Publish(core.ThreadLoaded{ThreadID: threadID, Err: rpl.Err})
				return
			}
			bus.Publish(core.ThreadLoaded{ThreadID: threadID, Msgs: rpl.Msgs})
		}()
	})

	go runRefresher(ctx, bus, worker, refresher, st)

	busCh := bus.Subscribe()
	prog := tea.NewProgram(tui.New(view, busCh, cfg.Bindings, cfg.TagActions, bus, st, cfg.UI), tea.WithAltScreen())
	go func() {
		<-ctx.Done()
		prog.Quit()
	}()
	_, err = prog.Run()
	return err
}

func runRefresher(ctx context.Context, bus *core.Bus, worker workerAPI, r *refresher, st *config.Store) {
	ch := bus.Subscribe()
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			worker.Call(notmuch.Action{Kind: notmuch.ActNew})
			r.cycle()
		case e := <-ch:
			switch e := e.(type) {
			case core.WorkerDone:
				r.cycle()
			case core.ConfigChanged:
				r.onConfig(st, e)
			}
		}
	}
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

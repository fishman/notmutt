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
	// the action vocabulary lives in tui, so the app is the boundary that
	// checks the config data against it (strict load, spec section 7)
	for key, action := range cfg.Bindings["index"] {
		if !tui.Actions[action] {
			return fmt.Errorf("bindings.index: unknown action %q for key %q", action, key)
		}
	}
	bus := core.NewBus()
	st := config.NewStore(cfg)
	st.Subscribe("ui", func() { bus.Publish(core.ConfigChanged{Section: "ui"}) })
	st.Subscribe("view", func() { bus.Publish(core.ConfigChanged{Section: "view"}) })
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

	go runRefresher(ctx, bus, worker, view, refresher, st)

	busCh := bus.Subscribe()
	prog := tea.NewProgram(tui.New(view, busCh, cfg.Bindings["index"]), tea.WithAltScreen())
	go func() {
		<-ctx.Done()
		prog.Quit()
	}()
	_, err = prog.Run()
	return err
}

func runRefresher(ctx context.Context, bus *core.Bus, worker workerAPI, view *core.View, r *refresher, st *config.Store) {
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

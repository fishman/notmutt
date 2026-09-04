// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build lua && crm

package app

import (
	"context"

	"notmutt/app/ai"
	"notmutt/config"
	"notmutt/core"
	"notmutt/lib/crm"
	"notmutt/lib/crm/hubspot"
	"notmutt/tui"
)

// crmAdapterState is the crm wire's resolved state: the neutral client
// the workflow drives and the [ai] entry its chat calls run on.
type crmAdapterState struct {
	client crm.Client
	aiCfg  config.AIProvider
}

// crmAdapter is the resolved wire state; nil while the CRM is dormant or
// its setup failed. Written once in crmWire before the subscriber starts;
// the workflow jobs read it, never rebuild the client. Session-local.
var crmAdapter *crmAdapterState

// crmPullSource is the CRM pull-command source seam (SetCrmPullSource).
// The surface-wiring task fills the command list; a nil list keeps the
// CRM queue surface closed.
func crmPullSource() []tui.CrmCommand { return nil }

// crmWire is the CRM follow-up workflow's app adapter: dormant unless
// [crm] names a provider, then it builds that provider's neutral client
// (the switch on cfg.Crm.Provider - the only vendor string on the app
// surface), resolves the [ai] entry the workflow runs on, and reacts to
// the refresh key by launching a pull. The job bodies live in
// lib/crm/workflow.go; the compose-open and write-back reactions attach
// here in later tasks.
func crmWire(ctx context.Context, bus *core.Bus, worker workerAPI, cfg config.Config, root string) {
	if cfg.Crm.Provider == "" {
		return
	}
	hs := cfg.Crm.Hubspot
	var client crm.Client
	switch cfg.Crm.Provider {
	case "hubspot":
		key, err := ai.FetchKey(ctx, hs.TokenCmd)
		if err != nil {
			diag.Warn("crm: disabled", "err", err.Error())
			return
		}
		// NewClient retains the key for the client's lifetime, so the wire
		// does not clear it (the ai-caller-clears rule yields here).
		client = hubspot.NewClient(ctx, key)
	default:
		return // a provider with no client case: config load already rejects it
	}
	aiCfg, err := resolveAIProvider(cfg, hs.AI)
	if err != nil {
		diag.Warn("crm: disabled", "err", err.Error())
		return
	}
	crmAdapter = &crmAdapterState{client: client, aiCfg: aiCfg}
	// the pull trigger: each manual refresh launches a pull on its own
	// goroutine; RunPull's mutex absorbs overlap, so no gate here.
	go func() {
		ch := bus.Subscribe()
		for e := range ch {
			if _, ok := e.(core.RefreshRequested); ok {
				go crm.RunPull(bus, client, hs.MarkerProperty, hs.CreatedAfter)
			}
		}
	}()
}

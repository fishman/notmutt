// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build !lua || !crm

package app

import (
	"context"

	"notmutt/config"
	"notmutt/core"
	"notmutt/tui"
)

// crmPullSource without the lua && crm build: no CRM, no pull commands.
func crmPullSource() []tui.CrmCommand { return nil }

// crmRowAction without the lua && crm build: no CRM, no row actions.
func crmRowAction(action string, c core.CrmContact) {}

// crmWire without the lua && crm build: no-op.
func crmWire(ctx context.Context, bus *core.Bus, worker workerAPI, cfg config.Config, root string) {
}

// crmPromptList without the lua && crm build: no CRM prompts.
func crmPromptList() []tui.AICommand { return nil }

// runCrmPrompt without the lua && crm build: no-op.
func runCrmPrompt(bus *core.Bus, cfg config.Config, root string, name string, c core.CrmContact, extra string) {
}

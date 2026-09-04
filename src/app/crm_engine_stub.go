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

// crmWire without the lua && crm build: no-op.
func crmWire(ctx context.Context, bus *core.Bus, worker workerAPI, cfg config.Config, root string) {
}

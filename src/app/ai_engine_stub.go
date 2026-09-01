// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build !lua

package app

import (
	"notmutt/config"
	"notmutt/core"
	"notmutt/tui"
)

// aiCommandList without the lua build: no AI backend, no commands.
func aiCommandList(account string) []tui.AICommand { return nil }

// runAICommand without the lua build: no-op.
func runAICommand(name, threadID, extra string, bus *core.Bus, cfg config.Config, worker workerAPI, root string) {
}

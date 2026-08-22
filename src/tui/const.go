// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

import "time"

// const.go: the tui package's tuning constants - timing and layout knobs.

// Timing.
const (
	// legendDebounce: cursor rest before the legend resolves on
	// terminals without release reporting (the release path resolves
	// immediately). The settle guard resolves only a tick armed by a
	// still cursor - never what it flashed past. 100ms caps re-arm
	// churn at 10 ticks/sec.
	legendDebounce = 100 * time.Millisecond
	// progressTickInterval: the progress bar's repaint cadence while a job is on (R15).
	progressTickInterval = 200 * time.Millisecond
	// sendTickInterval: the send dialogue spinner's frame cadence while a send is in flight (R4).
	sendTickInterval = 100 * time.Millisecond
	// addrDebounce: a Tab storm coalesces into one address harvest after
	// the last trigger (the legendDebounce pattern).
	addrDebounce = 40 * time.Millisecond
)

// Layout.
const (
	// progressWidth: the progress region's cell budget in the status row.
	progressWidth = 40
	// defaultStatusWidth: the width the width-less statusLine variant
	// renders at (tests, callers without a window size).
	defaultStatusWidth = 80
	// pagerStyleMargin: the styled band around the pager's visible
	// window; small scrolls stay inside it.
	pagerStyleMargin = 20
	// logCap: the session log ring cap (the status line shows the last
	// entry, the ~ overlay scrolls the rest).
	logCap = 200
)

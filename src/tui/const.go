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
	// statusSpinTickInterval: the status-line spinner's frame cadence
	// while the client is busy (R15).
	statusSpinTickInterval = 120 * time.Millisecond
	// statusClearInterval: the status auto-clear's expiry-check cadence
	// while a clearable message is up (the loop's gated arm).
	statusClearInterval = 250 * time.Millisecond
	// addrDebounce: a Tab storm coalesces into one address harvest after
	// the last trigger (the legendDebounce pattern).
	addrDebounce = 40 * time.Millisecond
	// statusTimeout: a non-error status message clears after this long;
	// an error persists for investigation. The status line is transient
	// (mutt's message window); the ~ overlay keeps the full ring.
	statusTimeout = 5 * time.Second
	// imgSettleInterval: the scroll-burst settle check's cadence while
	// image suppression holds (the loop's gated arm).
	imgSettleInterval = 33 * time.Millisecond
	// imgSettleDebounce: how long the pager must rest before a scroll
	// burst's held images decode and paint once. Longer than the tick so
	// a mid-burst tick can never exit between keypresses.
	imgSettleDebounce = 120 * time.Millisecond
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

package tui

import "time"

// const.go: the tui package's tuning constants - timing and layout
// knobs in one place.

// Timing.
const (
	// legendDebounce is how long the cursor must rest before the status
	// legend resolves on terminals without release reporting (the
	// KeyReleaseMsg path resolves immediately, no debounce). tea.Tick
	// is a one-shot with no cancellation, so key auto-repeat piles
	// ticks up; the settle guard in the legendTick handler makes a tick
	// resolve only when no move happened since it was armed - a
	// too-young tick re-arms itself. The legend shows what the cursor
	// rested on, never what it flashed past. 100ms caps the hold-time
	// re-arm churn at 10 ticks/sec (the old 20ms tick doubled the
	// render rate during a hold).
	legendDebounce = 100 * time.Millisecond
	// progressTickInterval is the progress bar's repaint cadence while
	// a job is on (R15).
	progressTickInterval = 200 * time.Millisecond
)

// Layout.
const (
	// progressWidth is the progress region's cell budget in the status
	// row.
	progressWidth = 40
	// defaultStatusWidth is the width the width-less statusLine variant
	// renders at (tests and callers without a window size).
	defaultStatusWidth = 80
	// pagerStyleMargin is the styled band the pager keeps around the
	// visible window; small scroll steps stay inside it and never touch
	// the styling pass.
	pagerStyleMargin = 20
)

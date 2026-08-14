package tui

import "time"

// const.go: the tui package's tuning constants - timing and layout
// knobs in one place.

// Timing.
const (
	// legendDebounce is how long the cursor must rest before the status
	// legend resolves (the debounced part of the status line). tea.Tick
	// is a one-shot with no cancellation, so key auto-repeat piles
	// ticks up; the settle guard in the legendTick handler makes a tick
	// resolve only when no move happened since it was armed - a
	// too-young tick re-arms itself. The legend shows what the cursor
	// rested on, never what it flashed past.
	legendDebounce = 20 * time.Millisecond
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
)

// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build lua

package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"notmutt/app/ai"
	"notmutt/config"
	"notmutt/core"
)

// chunkBatcher rate-limits streamed AI deltas to one publish per
// minInterval: a fast provider outruns the TUI repaint and the bus drops
// overflow events. Deltas buffer and flush whole (nothing lost, the
// final flush sends the last partial frame); the callback runs on the
// single stream-read goroutine, so no lock is needed.
type chunkBatcher struct {
	minInterval time.Duration
	last        time.Time
	buf         strings.Builder
	publish     func(string)
}

// add buffers one delta and flushes when the interval has elapsed; flush
// is called once more after the stream ends.
func (b *chunkBatcher) add(d string) {
	b.buf.WriteString(d)
	if time.Since(b.last) < b.minInterval {
		return
	}
	b.flush()
}

func (b *chunkBatcher) flush() {
	s := b.buf.String()
	b.buf.Reset()
	if s == "" {
		return
	}
	b.last = time.Now()
	b.publish(s)
}

// aiStream runs one completion with the shared job plumbing: jobID,
// AiStarted, batched + sanitized AiChunk, and the ai.Chat call. The
// caller owns the settle (post-processing + AiResult).
func aiStream(bus *core.Bus, ctx context.Context, tid string,
	p config.AIProvider, model, system, text string) (jobID, out string, err error) {
	jobID = fmt.Sprintf("%d", time.Now().UnixNano())
	bus.Publish(core.AiStarted{JobID: jobID, ThreadID: tid})
	batcher := &chunkBatcher{minInterval: 25 * time.Millisecond, publish: func(d string) {
		bus.Publish(core.AiChunk{JobID: jobID, Text: core.SanitizeText(d)})
	}}
	out, err = ai.Chat(ctx, p, model, system, text, batcher.add)
	batcher.flush()
	return jobID, out, err
}

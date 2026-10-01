// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"bytes"
	"image"
	"strings"
	"time"

	"github.com/gdamore/tcell/v3"
)

const kittyProbeTimeout = 50 * time.Millisecond
const kittyProbeID = "2147483647"

// kittyProbeTty probes after raw mode starts, before tcell starts its reader.
// Unrelated input is replayed to tcell; no second reader races the event loop.
type kittyProbeTty struct {
	tcell.Tty
	probed    bool
	supported bool
	pending   []byte
}

func (t *kittyProbeTty) Start() error {
	if err := t.Tty.Start(); err != nil {
		return err
	}
	if !t.probed {
		t.supported, t.pending = probeKittyGraphics()
		t.probed = true
	}
	return nil
}

func (t *kittyProbeTty) Read(p []byte) (int, error) {
	if len(t.pending) > 0 {
		n := copy(p, t.pending)
		t.pending = t.pending[n:]
		return n, nil
	}
	return t.Tty.Read(p)
}

// Query the actual transport/format we paint, without storing or showing an
// image. The reserved query ID is independent of the paint ID allocator.
func kittyGraphicsQuery() []byte {
	var buf bytes.Buffer
	kittySend(&buf, "a=q,i="+kittyProbeID+",f=100,t=d", image.NewNRGBA(image.Rect(0, 0, 1, 1)))
	return buf.Bytes()
}

// kittyGraphicsReply consumes only a complete response to our own query.
// Arbitrary header order is legal; partial replies and unrelated input remain
// buffered. An error is a completed but unsuccessful query.
func kittyGraphicsReply(data []byte) (supported, done bool, pending []byte) {
	for off := 0; off < len(data); {
		start := bytes.Index(data[off:], []byte("\x1b_G"))
		if start < 0 {
			break
		}
		start += off
		end := bytes.Index(data[start+3:], []byte("\x1b\\"))
		if end < 0 {
			break
		}
		end += start + 3
		header, status, ok := strings.Cut(string(data[start+3:end]), ";")
		if ok {
			for _, key := range strings.Split(header, ",") {
				if key == "i="+kittyProbeID {
					pending = append(pending, data[:start]...)
					pending = append(pending, data[end+2:]...)
					return status == "OK", true, pending
				}
			}
		}
		off = end + 2
	}
	return false, false, data
}

// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"bytes"
	"encoding/base64"
	"image/png"
	"os"
	"strings"
	"testing"

	"notmutt/core"
)

const probeAck = "\x1b_Gi=" + kittyProbeID + ";OK\x1b\\"

// Run inside a graphics-capable terminal with NOTMUTT_TEST_KITTY=1.
// This checks the real raw-mode/startup integration, without opening mail.
func TestLiveKittyGraphicsProbe(t *testing.T) {
	if os.Getenv("NOTMUTT_TEST_KITTY") != "1" {
		t.Skip("requires a live Kitty-graphics terminal")
	}
	s, protocol, err := newScreen()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Fini()
	if protocol != "kitty" {
		t.Fatalf("live graphics query failed: selected %q", protocol)
	}
	t.Log("live PNG graphics query and tcell startup succeeded")
}

func TestKittyGraphicsQuery(t *testing.T) {
	query := string(kittyGraphicsQuery())
	if !strings.HasPrefix(query, "\x1b_Ga=q,i="+kittyProbeID+",f=100,t=d,m=0;") || !strings.HasSuffix(query, "\x1b\\") {
		t.Fatalf("expected a non-displaying direct PNG query, got %q", query)
	}
	_, payload, _ := strings.Cut(query, ";")
	data, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(payload, "\x1b\\"))
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil || img.Bounds().Dx() != 1 || img.Bounds().Dy() != 1 {
		t.Fatalf("query must contain a valid one-pixel PNG: %v", err)
	}
}

func TestKittyGraphicsReply(t *testing.T) {
	ack := probeAck
	wrong := "\x1b_Gi=7;OK\x1b\\"
	malformed := "\x1b_Gi=" + kittyProbeID + "\x1b\\"
	for _, tc := range []struct {
		name, input, pending string
		supported, done      bool
	}{
		{"success", ack, "", true, true},
		{"header-order", "\x1b_Gp=1,i=" + kittyProbeID + ";OK\x1b\\", "", true, true},
		{"negative", "\x1b_Gi=" + kittyProbeID + ";ENOTSUP: unsupported\x1b\\", "", false, true},
		{"input-preserved", "a\x1b[A" + ack + "z", "a\x1b[Az", true, true},
		{"wrong-id", wrong, wrong, false, false},
		{"unrelated-then-matching", wrong + ack, wrong, true, true},
		{"malformed", malformed, malformed, false, false},
		{"plain-OK", "OK", "OK", false, false},
		{"no-reply", "", "", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			supported, done, pending := kittyGraphicsReply([]byte(tc.input))
			if supported != tc.supported || done != tc.done || string(pending) != tc.pending {
				t.Fatalf("got (%t, %t, %q), want (%t, %t, %q)", supported, done, pending, tc.supported, tc.done, tc.pending)
			}
		})
	}
	for n := range len(ack) {
		data := []byte(ack[:n])
		if supported, done, pending := kittyGraphicsReply(data); supported || done || !bytes.Equal(pending, data) {
			t.Fatalf("partial reply at byte %d must not complete or consume input", n)
		}
	}
}

func TestKittyProbeTtyReplaysInput(t *testing.T) {
	tty := &kittyProbeTty{pending: []byte("keys")}
	var out bytes.Buffer
	for len(tty.pending) > 0 {
		var buf [1]byte
		n, err := tty.Read(buf[:])
		if err != nil {
			t.Fatal(err)
		}
		out.Write(buf[:n])
	}
	if out.String() != "keys" {
		t.Fatalf("input was lost or reordered: %q", out.String())
	}
}

func TestKittyImageIDsStayPositiveAfterClear(t *testing.T) {
	m := Model{imgProto: "kitty", kimg: map[*core.Image]int{}, painted: map[*core.Image]cellRect{}}
	img := &core.Image{Cols: 1, Rows: 1}
	next := map[*core.Image]imgPaint{img: {rect: cellRect{w: 1, h: 1}, img: testImg(10, 20), h: 1}}
	var out bytes.Buffer
	old := imageWriter
	imageWriter = &out
	defer func() { imageWriter = old }()
	for frame := 0; frame < 2; frame++ {
		out.Reset()
		m.paintKitty(next)
		if m.kimg[img] != 1 || !strings.Contains(out.String(), "a=t,i=1,") || !strings.Contains(out.String(), "a=p,i=1,") {
			t.Fatalf("first paint after reset must transmit and place positive ID 1: %q", out.String())
		}
		out.Reset()
		m.paintKitty(next)
		if out.Len() != 0 {
			t.Fatal("an unchanged frame must not retransmit or replace")
		}
		m.clearImageRects()
	}
}

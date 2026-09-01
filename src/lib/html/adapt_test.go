// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

import (
	"math"
	"testing"
)

func TestAdaptBG(t *testing.T) {
	if got := AdaptBG("#ffffff", "#282c34"); got != "#282c34" {
		t.Errorf("AdaptBG(white, theme) = %s, want the theme bg", got)
	}
	// isometry: per-channel distances survive the reflection
	a, b := mustRGB(AdaptBG("#808080", "#282c34")), mustRGB(AdaptBG("#c0c0c0", "#282c34"))
	c0, c1 := mustRGB("#808080"), mustRGB("#c0c0c0")
	for ch := 0; ch < 3; ch++ {
		if d := b[ch] - a[ch]; d != -(c1[ch] - c0[ch]) {
			t.Errorf("channel %d distance changed: %d before, %d after", ch, c1[ch]-c0[ch], d)
		}
	}
	if got := AdaptBG("#000000", "#282c34"); got != "#ffffff" {
		t.Errorf("AdaptBG(black) = %s, want clamped to white", got)
	}
	for c, in := range map[string]string{"": "", "nope": "nope"} {
		if got := AdaptBG(c, "#282c34"); got != in {
			t.Errorf("AdaptBG(%q) = %q, want unchanged", c, got)
		}
	}
}

func TestAdaptFG(t *testing.T) {
	// dark text must read on the dark bg: Rec.709 luma > 0.7
	if got := AdaptFG("#333333", "#282c34"); luma(got) <= 0.7 {
		t.Errorf("AdaptFG(#333333) = %s, luma %f <= 0.7", got, luma(got))
	}
	// blue link stays blue (hue ~210) and clears the original ratio
	blue := AdaptFG("#0066cc", "#282c34")
	if h, _, _ := rgbToHSL(mustRGB(blue)); math.Abs(h-210) > 2 {
		t.Errorf("AdaptFG(#0066cc) = %s, hue %f, want ~210", blue, h)
	}
	if r := wcagRatio(mustRGB(blue), mustRGB("#282c34")); r < wcagRatio(mustRGB("#0066cc"), white) {
		t.Errorf("AdaptFG(#0066cc) ratio %f < original's %f", r, wcagRatio(mustRGB("#0066cc"), white))
	}
	// near-white fallback when the hue cannot clear at any lightness
	if got := AdaptFG("#000000", "#282c34"); got != "#f5f5f5" {
		t.Errorf("AdaptFG(#000000) = %s, want near-white fallback", got)
	}
	if got := AdaptFG("", "#282c34"); got != "" {
		t.Errorf("AdaptFG(\"\") = %q, want unchanged", got)
	}
}

func luma(c string) float64 {
	n := mustRGB(c)
	return (0.299*float64(n[0]) + 0.587*float64(n[1]) + 0.114*float64(n[2])) / 255
}

func mustRGB(c string) [3]int {
	out, err := parseRGB(c)
	if err != nil {
		panic(err)
	}
	return out
}

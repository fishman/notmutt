package chrome

import (
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
)

func TestStatusDropsProgressBeforeTitle(t *testing.T) {
	left := []Segment{{Runs: []Run{{Text: "inbox", Style: "status.view"}}, Priority: 10}}
	right := []Segment{{Runs: []Run{{Text: "downloading", Style: "progress"}}, Priority: 0}}
	runs := Status(9, "status", left, right)
	text := tabText(runs)
	if !strings.Contains(text, "inbox") || strings.Contains(text, "downloading") || runewidth.StringWidth(text) != 9 {
		t.Fatalf("status = %q", text)
	}
	if len(right) != 1 || right[0].Runs[0].Text != "downloading" {
		t.Fatal("caller segments mutated")
	}
}

func TestStatusKeepsRightGroupAtEdge(t *testing.T) {
	left := []Segment{{Runs: []Run{{Text: "view", Style: "view"}}, Priority: 10}}
	right := []Segment{{Runs: []Run{{Text: "3/5", Style: "status"}, {Text: "#", Style: "progress"}}, Priority: 5}}
	runs := Status(30, "status", left, right)
	text := tabText(runs)
	if runewidth.StringWidth(text) != 30 || !strings.HasSuffix(text, "3/5# ") {
		t.Fatalf("status = %q", text)
	}
	var highlighted bool
	for _, run := range runs {
		if run.Text == "#" && run.Style == "progress" {
			highlighted = true
		}
	}
	if !highlighted {
		t.Fatalf("progress style lost: %#v", runs)
	}
}

func TestStatusClipsWideText(t *testing.T) {
	left := []Segment{{Runs: []Run{{Text: strings.Repeat("\u754c", 3), Style: "view"}}, Priority: 10}}
	for _, width := range []int{0, 1, 4, 7} {
		text := tabText(Status(width, "status", left, nil))
		if runewidth.StringWidth(text) != width || strings.Contains(text, "\n") {
			t.Fatalf("width %d: %q", width, text)
		}
	}
}

func TestStatusSegmentWidth(t *testing.T) {
	segs := []Segment{
		{Runs: []Run{{Text: "\u754c", Style: "view"}}},
		{Runs: []Run{{Text: "done", Style: "progress"}}},
	}
	if got := Width(segs); got != 11 {
		t.Fatalf("two padded segments with a gap occupy %d cells, want 11", got)
	}
}

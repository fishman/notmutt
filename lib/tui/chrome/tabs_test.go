package chrome

import (
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
)

func tabText(runs []Run) string {
	var b strings.Builder
	for _, run := range runs {
		b.WriteString(run.Text)
	}
	return b.String()
}

func TestTabsRetainsActiveTail(t *testing.T) {
	runs := Tabs([]string{"Inbox", "Draft", "Important"}, 2, 18, "tabbar", "tabbar.active")
	text := tabText(runs)
	if !strings.Contains(text, "Important") || runewidth.StringWidth(text) != 18 || strings.Contains(text, "Draft") {
		t.Fatalf("tabs = %q", text)
	}
	if runs[0].Style != "tabbar.active" {
		t.Fatalf("active tab style = %q", runs[0].Style)
	}
}

func TestTabsClipsWideLabelsByCell(t *testing.T) {
	for _, width := range []int{1, 2, 4, 8} {
		runs := Tabs([]string{strings.Repeat("\u754c", 3)}, 0, width, "tabbar", "tabbar.active")
		text := tabText(runs)
		if runewidth.StringWidth(text) != width || strings.Contains(text, "\n") {
			t.Fatalf("width %d tabs = %q (%d cells)", width, text, runewidth.StringWidth(text))
		}
	}
}

func TestTabsDoesNotMutateLabels(t *testing.T) {
	labels := []string{"Inbox", "Alpha", "Active"}
	_ = Tabs(labels, 2, 6, "bar", "active")
	if labels[1] != "Alpha" || labels[2] != "Active" {
		t.Fatalf("labels mutated: %v", labels)
	}
}

package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"notmutt/config"
	"notmutt/core"
)

// frame renders the model at a fixed window size - the render path
// that the row cache and the region layers serve. It arms the paint:
// the gate (View returns the last painted frame while paint is false)
// would otherwise keep the stale frame for these direct renders - a
// real message arms it at Update entry.
func frame(m Model) string {
	m.width, m.height = 80, 24
	m.paint = true
	return m.View().Content
}

// TestRowCacheReflectsSetTags pins the invalidation half of the row
// cache: SetTags marks the view dirty, the next Rows() reflattens and
// churns row addresses, and the cache (keyed by address) must miss.
// The unread flag glyph is the only "N" in the frame.
func TestRowCacheReflectsSetTags(t *testing.T) {
	m := model()
	if out := frame(m); !strings.Contains(out, "N") {
		t.Fatalf("unread flag missing:\n%s", out)
	}
	m.view.SetTags("a", []string{"inbox"})
	m.rows = m.view.Rows() // the bus-event refresh
	if out := frame(m); strings.Contains(out, "N") {
		t.Fatalf("row cache must invalidate on a tag change:\n%s", out)
	}
}

// TestRowCacheReflectsSetAtts pins the key half of the cache: SetAtts
// mutates the shared message WITHOUT a reflatten (addresses stable),
// so only the atts bool in the key covers it - a stale cached line
// would miss the attachment marker.
func TestRowCacheReflectsSetAtts(t *testing.T) {
	m := model()
	if out := frame(m); strings.Contains(out, "📎") {
		t.Fatalf("attachment marker must start absent:\n%s", out)
	}
	m.view.SetAtts("a", []core.Attachment{{Name: "f", Size: 1}})
	m.rows = m.view.Rows() // same slice - the memoized flatten is not dirty
	if out := frame(m); !strings.Contains(out, "📎") {
		t.Fatalf("row cache must reflect SetAtts without a reflatten:\n%s", out)
	}
}

// TestRowCacheEqualsFresh drops the warm cache and re-renders: the
// miss path must rebuild byte-identical lines.
func TestRowCacheEqualsFresh(t *testing.T) {
	m := model()
	warm := frame(m)
	m.rowCache = map[rowKey]string{}
	if fresh := frame(m); fresh != warm {
		t.Fatalf("fresh row render differs from cached:\n%s", diffLine(warm, fresh))
	}
}

// TestLayerCacheEqualsFresh resets the region layers and re-renders:
// each layer's rebuild must equal its cached string.
func TestLayerCacheEqualsFresh(t *testing.T) {
	m := model()
	warm := frame(m)
	m.hintLayer, m.statusLayer, m.helpLayer = &layer{}, &layer{}, &layer{}
	if fresh := frame(m); fresh != warm {
		t.Fatalf("fresh layer render differs from cached:\n%s", diffLine(warm, fresh))
	}
}

// diffLine is a t.Fatalf-style first-difference locator for frame
// equality failures: the index of the first differing byte and the
// surrounding lines.
func diffLine(a, b string) string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			start := i - 40
			if start < 0 {
				start = 0
			}
			end := i + 40
			if end > n {
				end = n
			}
			return fmt.Sprintf("first diff at byte %d:\n  a: %q\n  b: %q", i, a[start:end], b[start:end])
		}
	}
	return fmt.Sprintf("lengths differ: %d vs %d", len(a), len(b))
}

// BenchmarkIndexRender times the steady-state frame build on a large
// list: every visible row and both region layers hit their caches, so
// the loop measures the concatenation path a cursor move pays between
// paint windows.
func BenchmarkIndexRender(b *testing.B) {
	view := core.NewView("inbox", "tag:inbox")
	view.SetGroups([]core.TagGroup{{Tags: []string{"inbox", "archive", "deleted", "sent", "draft", "pending", "spam"}}})
	ts := make([]*core.Thread, 0, 5000)
	for i := 0; i < 5000; i++ {
		id := fmt.Sprintf("t%d", i)
		ts = append(ts, core.NewThread(id, []*core.Message{
			{ID: id, Timestamp: int64(i), Author: "Ann", Subject: "hello", Tags: []string{"inbox"}},
		}))
	}
	view.MergeThreads(ts)
	m := New(view, nil, testBindings(), testTagActions(), nil, config.NewStore(config.Default()), config.Default().UI)
	m.width, m.height = 120, 40
	// one real move arms the cursor-id scan (the never-moved fallback
	// resolves via the view's flattening CursorRow - the documented
	// page-key stall, not the steady-state path this benchmark times)
	next, _ := m.Update(tea.KeyPressMsg{Text: "j", Code: 'j'})
	m = next.(Model)
	m.View() // warm the row cache and the layers
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.View()
	}
}

// BenchmarkIndexRenderMiss times the uncached frame build on the same
// workload (the cache cleared per iteration) - the README's before/
// after pairing for the row cache.
func BenchmarkIndexRenderMiss(b *testing.B) {
	view := core.NewView("inbox", "tag:inbox")
	view.SetGroups([]core.TagGroup{{Tags: []string{"inbox", "archive", "deleted", "sent", "draft", "pending", "spam"}}})
	ts := make([]*core.Thread, 0, 5000)
	for i := 0; i < 5000; i++ {
		id := fmt.Sprintf("t%d", i)
		ts = append(ts, core.NewThread(id, []*core.Message{
			{ID: id, Timestamp: int64(i), Author: "Ann", Subject: "hello", Tags: []string{"inbox"}},
		}))
	}
	view.MergeThreads(ts)
	m := New(view, nil, testBindings(), testTagActions(), nil, config.NewStore(config.Default()), config.Default().UI)
	m.width, m.height = 120, 40
	next, _ := m.Update(tea.KeyPressMsg{Text: "j", Code: 'j'})
	m = next.(Model)
	m.View()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.rowCache = map[rowKey]string{}
		m.View()
	}
}

// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

import (
	"strings"
	"testing"

	xhtml "golang.org/x/net/html"
)

// buildBody parses a full document around body and returns the body's
// top-level boxes (nil body -> nil).
func buildBody(body string) []*Box {
	doc, err := xhtml.Parse(strings.NewReader("<html><body>" + body + "</body></html>"))
	if err != nil {
		panic(err)
	}
	return Build(doc, ParseStyleSheets(doc))
}

func tags(boxes []*Box) []string {
	var out []string
	for _, b := range boxes {
		out = append(out, b.Tag)
	}
	return out
}

func TestBuildRolesAndSkips(t *testing.T) {
	bs := buildBody(`<p>one</p><span>two</span><br><img src="x"><script>var x</script><div style="display:none">gone</div>`)
	got := tags(bs)
	want := []string{"p", "span", "br", "img"}
	if len(got) != len(want) {
		t.Fatalf("body boxes = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("box %d tag = %q, want %q (all=%v)", i, got[i], want[i], got)
		}
	}
	if bs[0].Role != RoleBlock {
		t.Fatalf("p role = %d, want RoleBlock", bs[0].Role)
	}
	if bs[1].Role != RoleInline {
		t.Fatalf("span role = %d, want RoleInline", bs[1].Role)
	}
	if bs[2].Role != RoleBR {
		t.Fatalf("br role = %d, want RoleBR", bs[2].Role)
	}
	if bs[3].Role != RoleImg {
		t.Fatalf("img role = %d, want RoleImg", bs[3].Role)
	}
}

func TestBuildDisplayNoneAndHeadSkipped(t *testing.T) {
	bs := buildBody(`<div style="display:none">x</div><p>kept</p>`)
	if len(bs) != 1 || bs[0].Tag != "p" {
		t.Fatalf("display:none div must drop, got %v", tags(bs))
	}
}

func TestBlockTextAndPText(t *testing.T) {
	bs := buildBody(`<p>one two</p>`)
	p := bs[0]
	if len(p.Child) != 1 || p.Child[0].Role != RoleText || p.Child[0].Text != "one two" {
		t.Fatalf("p children = %+v, want one RoleText with the raw text", p.Child)
	}
}

func TestAuthorDisplayBeatsTag(t *testing.T) {
	bs := buildBody(`<span style="display:block">x</span>`)
	if len(bs) != 1 || bs[0].Role != RoleBlock {
		t.Fatalf("span display:block role = %v, want RoleBlock", bs[0].Role)
	}
}

func TestBlockInInlineSplitsRuns(t *testing.T) {
	bs := buildBody(`<div>outer<span>a<div>inner</div>b</span></div>`)
	span := bs[0].Child[1]
	if span.Role != RoleBlock {
		t.Fatalf("inline span containing a block must become RoleBlock, got %d", span.Role)
	}
	if len(span.Child) != 3 {
		t.Fatalf("span children = %d, want 3 runs/block", len(span.Child))
	}
	if span.Child[0].Tag != "" || span.Child[0].Role != RoleBlock {
		t.Fatalf("first run must be an anonymous block, got tag=%q role=%d", span.Child[0].Tag, span.Child[0].Role)
	}
	if span.Child[1].Tag != "div" {
		t.Fatalf("middle child must be the inner div, got %q", span.Child[1].Tag)
	}
	if span.Child[2].Tag != "" || span.Child[2].Role != RoleBlock {
		t.Fatalf("last run must be an anonymous block, got tag=%q role=%d", span.Child[2].Tag, span.Child[2].Role)
	}
}

func TestPreGetsPreWhiteSpace(t *testing.T) {
	bs := buildBody(`<pre>keep  spaces</pre>`)
	if len(bs) != 1 || bs[0].WS != WSPre {
		t.Fatalf("pre WS = %d, want WSPre", bs[0].WS)
	}
}

func TestPreClassReachesNestedInline(t *testing.T) {
	bs := buildBody(`<pre><b>bold  text</b></pre>`)
	b := bs[0].Child[0]
	if b.Role != RoleInline || b.WS != WSPre {
		t.Fatalf("nested b under bare pre: role=%d WS=%d, want RoleInline WSPre", b.Role, b.WS)
	}
}

func TestListMarkerTagging(t *testing.T) {
	bs := buildBody(`<ul><li>one<ul><li>nested</li></ul></li><li>two</li></ul>`)
	ul := bs[0]
	if len(ul.Child) != 2 {
		t.Fatalf("ul children = %d, want 2 li", len(ul.Child))
	}
	if ul.Child[0].Marker != "disc" {
		t.Fatalf("outer li marker = %q, want disc", ul.Child[0].Marker)
	}
	if ul.Child[1].Marker != "disc" {
		t.Fatalf("second li marker = %q, want disc", ul.Child[1].Marker)
	}
	nested := ul.Child[0].Child[1] // li(one)'s second child is the nested ul (its text precedes it)
	if nested.Tag != "ul" || len(nested.Child) != 1 {
		t.Fatalf("nested = tag %q with %d children, want ul with 1 li", nested.Tag, len(nested.Child))
	}
	if nested.Child[0].Marker != "circle" {
		t.Fatalf("nested li marker = %q, want circle", nested.Child[0].Marker)
	}
}

func TestTableIsLeaf(t *testing.T) {
	bs := buildBody(`<table><tr><td>c</td></tr></table>`)
	if len(bs) != 1 || bs[0].Role != RoleTable {
		t.Fatalf("table role = %v, want RoleTable leaf", bs[0].Role)
	}
	if bs[0].Node == nil {
		t.Fatal("table leaf must carry its element node")
	}
}

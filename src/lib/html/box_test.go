// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

import (
	"strings"
	"testing"

	xhtml "golang.org/x/net/html"
)

// buildBody parses a full document around body and returns the body's
// top-level flow boxes.
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
	// the block p, then the inline run (span/br/img) grouped into an
	// anonymous block; script and display:none drop
	if len(bs) != 2 || bs[0].Tag != "p" || bs[0].Role != RoleBlock {
		t.Fatalf("top = %v (%d boxes), want p block then anon run", tags(bs), len(bs))
	}
	run := bs[1]
	if run.Tag != "" || run.Role != RoleBlock || len(run.Children) != 3 {
		t.Fatalf("anon run = tag %q role %d with %d children, want 3", run.Tag, run.Role, len(run.Children))
	}
	if run.Children[0].Role != RoleInline {
		t.Fatalf("span role = %d, want RoleInline", run.Children[0].Role)
	}
	if run.Children[1].Role != RoleBR {
		t.Fatalf("br role = %d, want RoleBR", run.Children[1].Role)
	}
	if run.Children[2].Role != RoleImg {
		t.Fatalf("img role = %d, want RoleImg", run.Children[2].Role)
	}
}

func TestBuildDisplayNoneAndHeadSkipped(t *testing.T) {
	bs := buildBody(`<div style="display:none">x</div><p>kept</p>`)
	if len(bs) != 1 || bs[0].Tag != "p" {
		t.Fatalf("display:none div must drop, got %v", tags(bs))
	}
}

func TestBodyStyleReachesContent(t *testing.T) {
	doc, err := xhtml.Parse(strings.NewReader(`<html><body style="color:#ff0000"><p>text</p></body></html>`))
	if err != nil {
		panic(err)
	}
	bs := Build(doc, ParseStyleSheets(doc))
	p := bs[0]
	if p.St.Fg != "#ff0000" {
		t.Fatalf("p under author-styled body must inherit Fg, got %q", p.St.Fg)
	}
	if p.Children[0].St == nil || p.Children[0].St.Fg != "#ff0000" {
		t.Fatal("p's text leaf must carry the inherited Fg style")
	}
}

func TestHTMLStyleReachesContent(t *testing.T) {
	doc, err := xhtml.Parse(strings.NewReader(`<html style="color:#00ff00"><body><p>text</p></body></html>`))
	if err != nil {
		panic(err)
	}
	bs := Build(doc, ParseStyleSheets(doc))
	if bs[0].St.Fg != "#00ff00" {
		t.Fatalf("p under an author-styled html element must inherit Fg, got %q", bs[0].St.Fg)
	}
}

func TestBlockTextAndPText(t *testing.T) {
	bs := buildBody(`<p>one two</p>`)
	p := bs[0]
	if len(p.Children) != 1 || p.Children[0].Role != RoleText || p.Children[0].Text != "one two" {
		t.Fatalf("p children = %+v, want one RoleText with the raw text", p.Children)
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
	span := bs[0].Children[1]
	if span.Role != RoleBlock {
		t.Fatalf("inline span containing a block must become RoleBlock, got %d", span.Role)
	}
	if len(span.Children) != 3 {
		t.Fatalf("span children = %d, want 3 runs/block", len(span.Children))
	}
	if span.Children[0].Tag != "" || span.Children[0].Role != RoleBlock {
		t.Fatalf("first run must be an anonymous block, got tag=%q role=%d", span.Children[0].Tag, span.Children[0].Role)
	}
	if span.Children[1].Tag != "div" {
		t.Fatalf("middle child must be the inner div, got %q", span.Children[1].Tag)
	}
	if span.Children[2].Tag != "" || span.Children[2].Role != RoleBlock {
		t.Fatalf("last run must be an anonymous block, got tag=%q role=%d", span.Children[2].Tag, span.Children[2].Role)
	}
}

func TestBlockOriginMixedSplitsRuns(t *testing.T) {
	bs := buildBody(`<div>text<div>inner</div>more</div>`)
	div := bs[0]
	if len(div.Children) != 3 {
		t.Fatalf("mixed block-origin div children = %d, want 3 (run, div, run)", len(div.Children))
	}
	run0 := div.Children[0]
	if run0.Role != RoleBlock || run0.Tag != "" || len(run0.Children) != 1 || run0.Children[0].Text != "text" {
		t.Fatalf("first run = %+v, want anon block wrapping 'text'", run0)
	}
	if run0.St != div.St || run0.WS != div.WS {
		t.Fatal("anon run must share the container's style and white-space class")
	}
	if div.Children[1].Tag != "div" {
		t.Fatalf("middle child = %q, want the inner div", div.Children[1].Tag)
	}
	run2 := div.Children[2]
	if run2.Role != RoleBlock || len(run2.Children) != 1 || run2.Children[0].Text != "more" {
		t.Fatalf("last run = %+v, want anon block wrapping 'more'", run2)
	}
}

func TestDescendantBlockification(t *testing.T) {
	bs := buildBody(`<span>a<i>b<div>c</div>d</i>e</span>`)
	span := bs[0]
	if span.Role != RoleBlock {
		t.Fatalf("top span must become RoleBlock, got %d", span.Role)
	}
	if len(span.Children) != 3 {
		t.Fatalf("span children = %d, want 3 (run, i, run)", len(span.Children))
	}
	i := span.Children[1]
	if i.Tag != "i" || i.Role != RoleBlock {
		t.Fatalf("i = tag %q role %d, want RoleBlock", i.Tag, i.Role)
	}
	if len(i.Children) != 3 || i.Children[1].Tag != "div" {
		t.Fatalf("i children must be anon(b), div, anon(d), got %d children", len(i.Children))
	}
}

func TestTopLevelMixedSplitsRuns(t *testing.T) {
	bs := buildBody(`hello<p>para</p>tail`)
	if len(bs) != 3 {
		t.Fatalf("top-level boxes = %d, want 3 (run, p, run)", len(bs))
	}
	if bs[1].Tag != "p" || bs[1].Role != RoleBlock {
		t.Fatalf("middle = tag %q, want p block", bs[1].Tag)
	}
	if bs[0].Role != RoleBlock || len(bs[0].Children) != 1 || bs[0].Children[0].Text != "hello" {
		t.Fatalf("first top run = %+v, want anon wrapping hello", bs[0])
	}
	if bs[2].Role != RoleBlock || len(bs[2].Children) != 1 || bs[2].Children[0].Text != "tail" {
		t.Fatalf("last top run = %+v, want anon wrapping tail", bs[2])
	}
}

func TestPreGetsPreWhiteSpace(t *testing.T) {
	bs := buildBody(`<pre>keep  spaces</pre>`)
	pre := bs[0]
	if pre.WS != WSPre {
		t.Fatalf("pre WS = %d, want WSPre", pre.WS)
	}
	if len(pre.Children) != 1 || pre.Children[0].Role != RoleText || pre.Children[0].WS != WSPre {
		t.Fatalf("pre text must carry WSPre, got %+v", pre.Children)
	}
}

func TestPreClassReachesNestedInline(t *testing.T) {
	bs := buildBody(`<pre><b>bold  text</b></pre>`)
	b := bs[0].Children[0]
	if b.Role != RoleInline || b.WS != WSPre {
		t.Fatalf("nested b under bare pre: role=%d WS=%d, want RoleInline WSPre", b.Role, b.WS)
	}
}

func TestListMarkerTagging(t *testing.T) {
	bs := buildBody(`<ul><li>one<ul><li>nested</li></ul></li><li>two</li></ul>`)
	ul := bs[0]
	if len(ul.Children) != 2 {
		t.Fatalf("ul children = %d, want 2 li", len(ul.Children))
	}
	if ul.Children[0].Marker != "disc" || ul.Children[1].Marker != "disc" {
		t.Fatalf("outer li markers = %q and %q, want disc", ul.Children[0].Marker, ul.Children[1].Marker)
	}
	nested := ul.Children[0].Children[1] // li(one)'s second child: its text run precedes the nested ul
	if nested.Tag != "ul" || len(nested.Children) != 1 {
		t.Fatalf("nested = tag %q with %d children, want ul with 1 li", nested.Tag, len(nested.Children))
	}
	if nested.Children[0].Marker != "circle" {
		t.Fatalf("nested li marker = %q, want circle", nested.Children[0].Marker)
	}
}

func TestStrayBlockInListGetsNoMarker(t *testing.T) {
	bs := buildBody(`<ul><div>stray</div><li>real</li></ul>`)
	ul := bs[0]
	if ul.Children[0].Marker != "" {
		t.Fatalf("stray div must not get a marker, got %q", ul.Children[0].Marker)
	}
	if ul.Children[1].Marker != "disc" {
		t.Fatalf("real li marker = %q, want disc", ul.Children[1].Marker)
	}
}

func TestAuthorListItemGate(t *testing.T) {
	bs := buildBody(`<ul><div style="display:list-item">a</div><li style="display:block">b</li></ul>`)
	ul := bs[0]
	if ul.Children[0].Marker != "disc" {
		t.Fatalf("div display:list-item must get a marker, got %q", ul.Children[0].Marker)
	}
	if ul.Children[1].Marker != "" {
		t.Fatalf("li display:block must lose its marker, got %q", ul.Children[1].Marker)
	}
}

func TestTableExpandsToGridTree(t *testing.T) {
	// <table><tr><td>a</td></tr></table> (rawTable: the HTML5 parser wraps a
	// literal <tr> under <table> in an implied tbody, so the anonymous
	// row-group repair needs the author's raw structure)
	bs := rawTable(tnode("tr", tnode("td", tnodeText("a"))))
	tbl := bs[0]
	if tbl.Role != RoleTable || tbl.Tbl != "table" {
		t.Fatalf("table role/tbl = %v/%q, want RoleTable/table", tbl.Role, tbl.Tbl)
	}
	if len(tbl.Children) != 1 || tbl.Children[0].Tbl != "row-group" || tbl.Children[0].Tag != "" {
		t.Fatalf("table child = %+v, want an anonymous row-group wrapping the tr", tbl.Children)
	}
	row := tbl.Children[0].Children[0]
	if len(tbl.Children[0].Children) != 1 || row.Tbl != "row" || row.Tag != "tr" {
		t.Fatalf("row-group child = %+v, want the tr as a row", tbl.Children[0].Children)
	}
	cell := row.Children[0]
	if len(row.Children) != 1 || cell.Tbl != "cell" || cell.Tag != "td" {
		t.Fatalf("row child = %+v, want a td cell", row.Children)
	}
	if len(cell.Children) != 1 || cell.Children[0].Role != RoleText || cell.Children[0].Text != "a" {
		t.Fatalf("cell content = %+v, want one RoleText 'a'", cell.Children)
	}
}

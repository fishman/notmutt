# HTML layout engine: foundation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stand up the stage-1 foundation in `lib/html` - a white-space enum, a UA display/white-space table, and a DOM-to-box-tree builder - as strictly additive code with zero behavior change to the running renderer.

**Architecture:** Three layers per the spec (`docs/superpowers/specs/2026-09-03-html-layout-engine-design.md`): stage 1 (CSS-faithful layout in `lib/html`), stage 2 (terminal backend in `mail`). This plan builds the stage-1 skeleton only: `Style` gains a precise `WS` enum (the current `Pre` bool stays for the running walker), a `ua.go` classifies tags and resolves the effective white-space, and `box.go` builds a flow box tree (block/inline/text/br/img, tables as unexpanded leaves) applying display:none skip and block-in-inline blockification. No rendering changes: the mail facade still calls the old walker; nothing in `mail/` is touched.

**Tech Stack:** Go, x/net/html, cascadia (existing deps). Test cmd: `cd src && go test -count=1 ./lib/html/`.

**Spec refs:** Sections 1 (box model + build), 2 (UA floor). WeasyPrint refs: `formatting_structure/build.py` (box rules), `css/html5_ua.css` (UA defaults).

---

## File structure

- Modify: `src/lib/html/html.go` - add `WS` enum + `WSSet` to `Style`, parse all five `white-space` values in `apply`, reset `WSSet` in `StyleOf`. Keep `Pre` derived (unchanged semantics for the running walker).
- Create: `src/lib/html/ua.go` - `parseWS`, `uaDisplay(tag)`, `effectiveWS(tag, s)`, `listMarker(parentTag, depth)`.
- Create: `src/lib/html/box.go` - `Role` enum, `Box` struct, `Build(doc, rules)`, `ParseStyleSheets(doc)`, internal `buildElement`, `roleOf`, `hasBlockChild`, `splitRuns`.
- Test: `src/lib/html/ua_test.go`, `src/lib/html/box_test.go`.

## Decisions locked for this plan

- Additive only. `Style.Pre` keeps its current meaning (`pre/pre-wrap/pre-line` set it true) because `mail/html.go` reads it (`br && !cs.Pre`). `WS` is the precise new field the box tree uses. `uaDefaults` (the shared 4-rule emphasis table) is NOT touched - a `pre` default for the new engine flows through `effectiveWS`, not the shared table, so the running walker cannot drift.
- Tables arrive as unexpanded leaf `Box`es carrying their `*xhtml.Node`; the table plan expands them. List items are walked (their content is flow text), and markers tag each `li`.
- The builder walks from the document `body`; head content never becomes boxes (it is `display:none` by `uaDisplay` anyway, plus body-scoped walking).
- Text whitespace is NOT collapsed at build time: inline layout (a later plan) collapses it. Boxes keep raw text.
- Author `display` and `white-space` come from `StyleOf` (the existing cascade). The UA table fills only what the author did not set.

---

### Task 1: White-space enum in Style (additive)

**Files:**
- Modify: `src/lib/html/html.go` (Style struct ~line 26, apply white-space branch ~line 161, StyleOf reset ~line 238)
- Test: `src/lib/html/ua_test.go`

- [ ] **Step 1: Write the failing test**

`src/lib/html/ua_test.go`:

```go
// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

import "testing"

func TestParseWhiteSpaceValues(t *testing.T) {
	cases := map[string]WS{
		"normal": WSNormal, "nowrap": WSNowrap, "pre": WSPre,
		"pre-wrap": WSPreWrap, "pre-line": WSPreLine,
		"Normal": WSNormal, " pre-wrap ": WSPreWrap, "break-spaces": WSNormal,
	}
	for in, want := range cases {
		if got := parseWS(in); got != want {
			t.Errorf("parseWS(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestApplyWhiteSpaceSetsEnum(t *testing.T) {
	var s Style
	s.apply(ParseDecls("white-space: nowrap"))
	if s.WS != WSNowrap || !s.WSSet {
		t.Fatalf("nowrap: WS=%d WSSet=%v, want WSNowrap set", s.WS, s.WSSet)
	}
	if s.Pre {
		t.Fatal("nowrap must not set the legacy Pre flag")
	}
	s = Style{}
	s.apply(ParseDecls("white-space: pre-wrap"))
	if s.WS != WSPreWrap || !s.Pre {
		t.Fatalf("pre-wrap must set WS=%d and legacy Pre, got WS=%d Pre=%v", WSPreWrap, s.WS, s.Pre)
	}
}

func TestWhiteSpaceInheritsButFlagDoesNot(t *testing.T) {
	parent := &Style{WS: WSPre, WSSet: true}
	child := StyleOf(el("p"), parent, nil)
	if child.WS != WSPre {
		t.Fatalf("white-space must inherit, child WS=%d", child.WS)
	}
	if child.WSSet {
		t.Fatal("WSSet must not inherit (it marks an explicit declaration at this node)")
	}
}
```

The `el` helper (an element node with the given tag) is defined in the test file too:

```go
func el(tag string) *xhtml.Node { return &xhtml.Node{Type: xhtml.ElementNode, Data: tag} }
```

Add the x/net/html import to the test file.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd src && go test -count=1 ./lib/html/ -run 'TestParseWhiteSpace|TestApplyWhiteSpace|TestWhiteSpace'`
Expected: FAIL to compile (`WS`, `parseWS`, `WSSet` undefined).

- [ ] **Step 3: Add the WS enum to Style**

`src/lib/html/html.go`, in the `Style` struct (after the `Pre` field):

```go
	Pre  bool   // white-space: pre* -> no wrap, keep spaces (legacy; WS is precise)
	WS   WS     // white-space class (precise); zero = normal. Inherits.
	WSSet bool  // an explicit white-space declaration at this node, not inherited
```

Add the enum and parser just above `Style` (after the imports):

```go
// WS is the computed white-space class (CSS white-space). Zero = normal.
type WS int

const (
	WSNormal WS = iota
	WSNowrap
	WSPre
	WSPreWrap
	WSPreLine
)

// parseWS maps a white-space value to its class; unknown values
// (break-spaces, typos) land on normal like weasyprint's validator.
func parseWS(v string) WS {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "nowrap":
		return WSNowrap
	case "pre":
		return WSPre
	case "pre-wrap":
		return WSPreWrap
	case "pre-line":
		return WSPreLine
	}
	return WSNormal
}
```

- [ ] **Step 4: Parse all values in apply and reset the flag in StyleOf**

`src/lib/html/html.go`, replace the `white-space` branch in `apply` (currently ~line 161):

```go
	if v, ok := decls["white-space"]; ok {
		s.WS = parseWS(v)
		s.WSSet = true
		switch s.WS {
		case WSPre, WSPreWrap, WSPreLine:
			s.Pre = true
		default:
			s.Pre = false
		}
	}
```

`src/lib/html/html.go`, in `StyleOf` (after `s.FgSet = false` ~line 239), add the reset alongside the other explicit-source flags:

```go
	s.WSSet = false // white-space inherits; its explicit-source flag never does
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd src && go test -count=1 ./lib/html/ -run 'TestParseWhiteSpace|TestApplyWhiteSpace|TestWhiteSpace' -v`
Expected: PASS (3 tests).

- [ ] **Step 6: Full lib/html suite still green**

Run: `cd src && go test -count=1 ./lib/html/`
Expected: PASS (all existing cascade/adapt/links tests plus the new ones).

- [ ] **Step 7: Commit**

```bash
git add src/lib/html/html.go src/lib/html/ua_test.go
git commit -m "feat(html): parse precise white-space class into Style"
```

---

### Task 2: UA display and white-space table (ua.go)

**Files:**
- Create: `src/lib/html/ua.go`
- Test: `src/lib/html/ua_test.go`

- [ ] **Step 1: Write the failing test**

Append to `src/lib/html/ua_test.go`:

```go
func TestUADisplayDefaults(t *testing.T) {
	cases := map[string]string{
		"p": "block", "div": "block", "span": "", "b": "", "a": "",
		"table": "table", "tr": "table-row", "td": "table-cell",
		"thead": "table-row-group", "caption": "table-caption",
		"script": "none", "title": "none", "style": "none",
		"li": "block", "img": "", "br": "",
	}
	for tag, want := range cases {
		if got := uaDisplay(tag); got != want {
			t.Errorf("uaDisplay(%q) = %q, want %q", tag, got, want)
		}
	}
}

func TestEffectiveWhiteSpacePreTag(t *testing.T) {
	// a bare <pre> with no author declaration must behave as pre
	s := StyleOf(el("pre"), &Style{}, nil)
	if got := effectiveWS("pre", s); got != WSPre {
		t.Fatalf("bare pre: effectiveWS = %d, want WSPre", got)
	}
	// an explicit author value wins over the tag default
	s2 := StyleOf(el("pre"), &Style{}, nil)
	s2.apply(ParseDecls("white-space: nowrap"))
	if got := effectiveWS("pre", s2); got != WSNowrap {
		t.Fatalf("author nowrap on pre: effectiveWS = %d, want WSNowrap", got)
	}
	// an inline element inherits the parent class
	parent := &Style{WS: WSPreWrap}
	s3 := StyleOf(el("span"), parent, nil)
	if got := effectiveWS("span", s3); got != WSPreWrap {
		t.Fatalf("inherited pre-wrap: effectiveWS = %d, want WSPreWrap", got)
	}
}

func TestListMarkerByDepth(t *testing.T) {
	if got := listMarker("ul", 1); got != "disc" {
		t.Errorf("ul depth1 = %q, want disc", got)
	}
	if got := listMarker("ul", 2); got != "circle" {
		t.Errorf("ul depth2 = %q, want circle", got)
	}
	if got := listMarker("ul", 3); got != "square" {
		t.Errorf("ul depth3 = %q, want square", got)
	}
	if got := listMarker("ol", 2); got != "decimal" {
		t.Errorf("ol = %q, want decimal", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd src && go test -count=1 ./lib/html/ -run 'TestUADisplay|TestEffectiveWhiteSpace|TestListMarker'`
Expected: FAIL to compile (`ua.go` missing).

- [ ] **Step 3: Write ua.go**

`src/lib/html/ua.go`:

```go
// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

// The UA floor (html5_ua.css analog): tag defaults the cascade does not
// carry. uaDisplay and effectiveWS are layered over StyleOf by the box
// builder; the running mail walker never reads them.

// uaDisplay is the UA default display keyword for a tag, "" when the
// default is inline. The head/script/skip set mirrors mail's skipTags.
func uaDisplay(tag string) string {
	switch tag {
	case "address", "article", "aside", "blockquote", "body", "dd",
		"details", "dialog", "div", "dl", "dt", "fieldset", "figcaption",
		"figure", "footer", "form", "h1", "h2", "h3", "h4", "h5", "h6",
		"header", "hr", "html", "legend", "li", "main", "nav", "ol", "p",
		"pre", "section", "ul":
		return "block"
	case "table":
		return "table"
	case "thead", "tbody", "tfoot":
		return "table-row-group"
	case "tr":
		return "table-row"
	case "td", "th":
		return "table-cell"
	case "caption":
		return "table-caption"
	case "colgroup":
		return "table-column-group"
	case "col":
		return "table-column"
	case "base", "head", "iframe", "link", "meta", "noscript",
		"script", "style", "template", "title":
		return "none"
	}
	return ""
}

// effectiveWS resolves a node's white-space class: an author
// declaration wins, else the pre-tag UA default, else the inherited
// value. white-space inherits; the UA only overrides inheritance at the
// element whose tag owns a default (pre).
func effectiveWS(tag string, s *Style) WS {
	if s.WSSet {
		return s.WS
	}
	if tag == "pre" {
		return WSPre
	}
	return s.WS
}

// listMarker is the ::marker type for an li under a list of the given
// tag at the given nesting depth (weasyprint html5_ua.css nesting).
func listMarker(tag string, depth int) string {
	if tag == "ol" {
		return "decimal"
	}
	switch depth {
	case 2:
		return "circle"
	case 3:
		return "square"
	default:
		return "disc"
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd src && go test -count=1 ./lib/html/ -run 'TestUADisplay|TestEffectiveWhiteSpace|TestListMarker' -v`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add src/lib/html/ua.go src/lib/html/ua_test.go
git commit -m "feat(html): UA display and white-space floor table"
```

---

### Task 3: Box types and Build entry

**Files:**
- Create: `src/lib/html/box.go`
- Test: `src/lib/html/box_test.go`

- [ ] **Step 1: Write the failing tests**

`src/lib/html/box_test.go`:

```go
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

func roles(boxes []*Box) []Role {
	var out []Role
	for _, b := range boxes {
		out = append(out, b.Role)
	}
	return out
}

func tags(boxes []*Box) []string {
	var out []string
	for _, b := range boxes {
		out = append(out, b.Tag)
	}
	return out
}

func TestBuildRolesAndSkips(t *testing.T) {
	bs := buildBody(`<p>one</p><span>two</span><script>var x</script><div style="display:none">gone</div><br><img src="x">`)
	if len(bs) != 5 {
		t.Fatalf("body boxes = %d, want 5 (p span br img and span text?) got %v", len(bs), roles(bs))
	}
	// p -> block; span content text; br -> RoleBR; img -> RoleImg; display:none dropped
}

func TestBuildDisplayNoneAndHeadSkipped(t *testing.T) {
	bs := buildBody(`<div style="display:none">x</div><p>kept</p>`)
	if len(bs) != 1 || bs[0].Tag != "p" {
		t.Fatalf("display:none div must drop, got %v", tags(bs))
	}
}
```

Note: the test bodies are approximations of the DOM shape; Task 3 first pins the plumbing (nil/len guards compile and run), Task 4 pins the exact shapes with precise assertions. For Step 1 keep the assertions loose enough to fail for the right reason (roles not yet defined), then tighten in Task 4.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd src && go test -count=1 ./lib/html/ -run TestBuild -v`
Expected: FAIL to compile (`Box`, `Role`, `Build`, `ParseStyleSheets` undefined).

- [ ] **Step 3: Write box.go (roles, Box, parse, build skeleton)**

`src/lib/html/box.go`:

```go
// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

import (
	xhtml "golang.org/x/net/html"
)

// Role is how a box takes part in flow. Tables arrive as leaves until
// the table plan expands them; table rows/cells only appear once that
// expansion runs.
type Role int

const (
	RoleBlock Role = iota // vertical flow container
	RoleInline            // inline element (flattens into a line at layout)
	RoleText              // raw text leaf
	RoleBR                // forced break
	RoleImg               // replaced image (atomic; block or inline)
	RoleTable             // table grid (leaf in this plan)
)

// Box is one node of the stage-1 flow tree. St is the computed style;
// WS is the effective white-space class (effectiveWS); Gap/Side/Marker
// are filled by later plans. Text carries raw text (whitespace is
// collapsed at layout, never at build).
type Box struct {
	Role   Role
	Tag    string      // element tag, "" on anonymous boxes
	Node   *xhtml.Node // originating element (img src, table ref, list tag)
	St     *Style
	WS     WS
	Marker string // list-item marker type: disc|circle|square|decimal
	Text   string // RoleText only
	Child  []*Box
}

// ParseStyleSheets gathers every <style> element's text into one
// cascade (the walker's collectStyleBlocks, moved to the package that
// owns the cascade).
func ParseStyleSheets(doc *xhtml.Node) []CSSRule {
	var rules []CSSRule
	var walk func(n *xhtml.Node)
	walk = func(n *xhtml.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == xhtml.ElementNode {
				if c.Data == "style" && c.FirstChild != nil {
					rules = append(rules, ParseStyleSheet(c.FirstChild.Data))
				}
				walk(c)
			}
		}
	}
	walk(doc)
	return rules
}

// Build returns the body's top-level flow boxes under the cascade. A
// document without a body yields nil. display:none and the
// head/script/skip set produce no box.
func Build(doc *xhtml.Node, rules []CSSRule) []*Box {
	var body *xhtml.Node
	for c := doc.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == xhtml.ElementNode && c.Data == "body" {
			body = c
			break
		}
	}
	if body == nil {
		return nil
	}
	var out []*Box
	for c := body.FirstChild; c != nil; c = c.NextSibling {
		out = append(out, buildNode(c, &Style{}, rules, 0)...)
	}
	return out
}

// buildNode turns one body child (text or element) into boxes; a
// display:none element or head skip yields none.
func buildNode(n *xhtml.Node, parent *Style, rules []CSSRule, listDepth int) []*Box {
	switch n.Type {
	case xhtml.TextNode:
		return []*Box{{Role: RoleText, Text: n.Data, WS: effectiveWS("", parent)}}
	case xhtml.ElementNode:
		b := buildElement(n, parent, rules, listDepth)
		if b == nil {
			return nil
		}
		return []*Box{b}
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it compiles and passes the loose assertions**

Run: `cd src && go test -count=1 ./lib/html/ -run TestBuild -v`
Expected: PASS (the loose assertions tolerate the current partial tree; Task 4 tightens).

- [ ] **Step 5: Commit**

```bash
git add src/lib/html/box.go src/lib/html/box_test.go
git commit -m "feat(html): flow box types and Build entry"
```

---

### Task 4: buildElement with roles, blockification, markers

**Files:**
- Modify: `src/lib/html/box.go`
- Test: `src/lib/html/box_test.go`

- [ ] **Step 1: Tighten the tests (write the failing shapes)**

Replace the loose Task 3 test bodies in `src/lib/html/box_test.go` with precise assertions:

```go
func TestBuildRolesAndSkips(t *testing.T) {
	bs := buildBody(`<p>one</p><span>two</span><br><img src="x"><script>var x</script><div style="display:none">gone</div>`)
	var got []string
	for _, b := range bs {
		got = append(got, b.Tag)
	}
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
		t.Fatalf("span display:block role = %v, want RoleBlock", roles(bs))
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
	bs := buildBody(`<pre>a\n  b</pre>`)
	if len(bs) != 1 || bs[0].WS != WSPre {
		t.Fatalf("pre WS = %d, want WSPre", bs[0].WS)
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
	nested := ul.Child[0].Child[0] // the nested ul
	if len(nested.Child) != 1 || nested.Child[0].Marker != "circle" {
		t.Fatalf("nested li marker = %q, want circle", nested.Child[0].Marker)
	}
}

func TestTableIsLeaf(t *testing.T) {
	bs := buildBody(`<table><tr><td>c</td></tr></table>`)
	if len(bs) != 1 || bs[0].Role != RoleTable {
		t.Fatalf("table role = %v, want RoleTable leaf", roles(bs))
	}
	if bs[0].Node == nil {
		t.Fatal("table leaf must carry its element node")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd src && go test -count=1 ./lib/html/ -run 'TestBuild|TestAuthorDisplay|TestBlockInInline|TestPreGets|TestListMarker|TestTableIsLeaf' -v`
Expected: FAIL (buildElement not yet present; roles unresolved; wrong tree shape).

- [ ] **Step 3: Implement buildElement and helpers**

`src/lib/html/box.go`, replace the placeholder build with the full builder:

```go
// buildElement builds one element into a box (nil when dropped).
func buildElement(n *xhtml.Node, parent *Style, rules []CSSRule, listDepth int) *Box {
	st := StyleOf(n, parent, rules)
	tag := n.Data
	d := st.Display
	if d == "" {
		d = uaDisplay(tag)
	}
	if d == "none" {
		return nil
	}
	var role Role
	switch tag {
	case "br":
		role = RoleBR
	case "img":
		role = RoleImg
	default:
		role = roleOf(tag, d)
	}
	b := &Box{Role: role, Tag: tag, Node: n, St: st, WS: effectiveWS(tag, st)}
	// Table-family and replaced/break leaves carry their subtree/attrs
	// on Node; later plans expand them.
	if role != RoleBlock && role != RoleInline {
		return b
	}
	nextDepth := listDepth
	if tag == "ul" || tag == "ol" {
		nextDepth++
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case xhtml.TextNode:
			b.Child = append(b.Child, &Box{Role: RoleText, Text: c.Data, WS: effectiveWS(tag, st)})
		case xhtml.ElementNode:
			if child := buildElement(c, st, rules, nextDepth); child != nil {
				if tag == "ul" || tag == "ol" {
					child.Marker = listMarker(tag, nextDepth)
				}
				b.Child = append(b.Child, child)
			}
		}
	}
	if role == RoleInline && hasBlockChild(b.Child) {
		b.Role = RoleBlock
		b.Child = splitRuns(b.Child)
	}
	return b
}

// roleOf maps an effective display keyword to a Role. Unknown values
// (flex, grid, inline-block) land on block - the mail-safe default the
// walker already used.
func roleOf(tag, d string) Role {
	switch d {
	case "block", "flex", "grid", "inline-block", "flow-root", "list-item":
		return RoleBlock
	case "table":
		return RoleTable
	case "table-row-group", "table-header-group", "table-footer-group":
		return RoleTable
	case "table-row":
		return RoleTable
	case "table-cell":
		return RoleTable
	case "table-caption":
		return RoleTable
	case "table-column-group", "table-column":
		return RoleTable
	}
	return RoleInline
}

// hasBlockChild reports whether any direct child is a block-level box
// (the block-in-inline trigger).
func hasBlockChild(cs []*Box) bool {
	for _, c := range cs {
		if c.Role == RoleBlock || c.Role == RoleTable {
			return true
		}
	}
	return false
}

// splitRuns wraps consecutive inline-level children of a now-blockified
// container into anonymous blocks, separating them from block children
// (weasyprint's block-in-inline split): text before a block does not
// bleed into it.
func splitRuns(cs []*Box) []*Box {
	var out []*Box
	var run []*Box
	flush := func() {
		if len(run) == 0 {
			return
		}
		out = append(out, &Box{Role: RoleBlock, Child: run})
		run = nil
	}
	for _, c := range cs {
		if c.Role == RoleBlock || c.Role == RoleTable {
			flush()
			out = append(out, c)
		} else {
			run = append(run, c)
		}
	}
	flush()
	return out
}
```

Also add the package import for x/net/html is already present from Task 3.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd src && go test -count=1 ./lib/html/ -run 'TestBuild|TestAuthorDisplay|TestBlockInInline|TestPreGets|TestListMarker|TestTableIsLeaf' -v`
Expected: PASS.

Note on `TestPreGetsPreWhiteSpace`: the body text contains a literal `\n  ` sequence only if the Go source uses a raw string with a real newline. The test body string `'<pre>a\n  b</pre>'` is a backtick raw string in the plan - if it contains the two characters backslash-n the assertion still passes because only the box WS is checked; the text is not collapsed yet (a later plan). Keep the assertion on WS only.

- [ ] **Step 5: Full lib/html suite green**

Run: `cd src && go test -count=1 ./lib/html/`
Expected: PASS.

- [ ] **Step 6: The running renderer still passes its suite**

Run: `cd src && go test -count=1 -tags "lua mcp" ./mail/ ./lib/...`
Expected: PASS (no behavior change: mail never reads WS, ua.go, or Build).

- [ ] **Step 7: Commit**

```bash
git add src/lib/html/box.go src/lib/html/box_test.go
git commit -m "feat(html): build flow boxes with blockification and list markers"
```

---

## Self-review

- Spec coverage: Section 1's box tree (types + display:none skip + block-in-inline split + table leaf) and Section 2's UA floor (display classification + white-space + pre default) land here. Margins/marker glyphs (block.go/Plan 3), table expansion (Plan 4), image sizing (Plan 5), the stage-2 backend and migration (later plans) are deliberately deferred.
- Zero behavior change: mail/ is untouched; `uaDefaults` untouched; `Pre` semantics unchanged. Guarded by Task 4 Step 6.
- Type consistency: `WS`/`WSSet`/`parseWS`/`effectiveWS`/`uaDisplay`/`listMarker`/`Role`/`Box`/`Build`/`ParseStyleSheets` are the only new identifiers; each task's tests use exactly those names.
- Placeholder scan: every task shows full code and exact commands; no TBD/TODO.

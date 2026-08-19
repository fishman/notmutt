// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package mail

// Fuzz targets for the html renderer boundary (AGENTS.md: parser-
// adjacent code passes the fuzz targets in SECURITY.md before it is
// accepted). The properties under test are panic-freedom and bounded
// output: a hostile document never exceeds the render line budget
// (content lines <= maxHTMLLines, each block boundary adds at most one
// blank, plus the truncation marker).

import "testing"

func FuzzRenderHTML(f *testing.F) {
	f.Add("plain text")
	f.Add("<p>hello <b>world</b></p>")
	f.Add("<table><tr><td>a</td><td>b</td></tr><tr><td>c</td></tr></table>")
	f.Add("<style>p { color: red }</style><p>x</p>")
	f.Add("<img src=\"data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==\">")
	f.Add("<pre>  spaced\n\tlines\n</pre>")
	f.Fuzz(func(t *testing.T, body string) {
		lines := RenderHTML(body, nil, 0)
		if len(lines) > 2*maxHTMLLines+1 {
			t.Fatalf("render exceeded the line budget: %d lines", len(lines))
		}
	})
}

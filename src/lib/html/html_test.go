// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

// Fuzz targets for the CSS boundary (AGENTS.md: parser-adjacent code
// passes the fuzz targets in SECURITY.md before it is accepted). The
// properties under test are panic-freedom and determinism on hostile
// stylesheet text.

import "testing"

func FuzzCSSDeclarations(f *testing.F) {
	f.Add("color: red; font-weight: bold")
	f.Add("background-color: #fff; text-align: center")
	f.Add("p { color: red } .x { font-style: italic }")
	f.Add("/* c */ a { color: rgb(1,2,3) }")
	f.Fuzz(func(t *testing.T, s string) {
		cssColor(s)
		ParseDecls(s)
		ParseStyleSheet(s)
	})
}

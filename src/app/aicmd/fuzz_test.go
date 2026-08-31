// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package aicmd

// Fuzz target for the frontmatter boundary (AGENTS.md: parser-adjacent
// code passes SECURITY.md's fuzz targets). Properties: panic-freedom and
// bounded work - parseCommand caps the input at maxCommandSize and never
// indexes past a delimited range, so arbitrary bytes must return an
// error, never hang or crash.

import "testing"

func FuzzLoadCommand(f *testing.F) {
	f.Add([]byte("---\nname: Thread next steps\ndescription: Summarize\naction: view\ndata: [participants, subjects, count]\n---\nAnalyze this thread.\n"))
	f.Add([]byte("---\nname: x\ndescription: y\naction: compose\ndata: [last_body]\naccount_context: true\n---\nbody\n"))
	f.Add([]byte("---\nname: x\nbogus: 1\n---\nbody\n"))
	f.Add([]byte("---\n"))
	f.Add([]byte("garbage"))
	f.Add([]byte(""))
	f.Add([]byte("---\ndata: [a, b, c]\n---\n"))
	f.Add([]byte(string(make([]byte, 70000))))
	f.Fuzz(func(t *testing.T, data []byte) {
		parseCommand(data, "fuzz")
	})
}

package notmuch

// Copyright © 2015 The go.notmuch Authors. Authors can be found in the AUTHORS file.
// Licensed under the GPLv3 or later.
// See COPYING at the root of the repository for details.

// #cgo LDFLAGS: -lnotmuch
// #include <stdlib.h>
// #include <notmuch.h>
import "C"

import (
	"iter"
	"slices"
)

// Tags represents a notmuch tags iterator.
type Tags cStruct

func (ts *Tags) toC() *C.notmuch_tags_t {
	return (*C.notmuch_tags_t)(ts.cptr)
}

func (ts *Tags) Close() {
	(*cStruct)(ts).doClose(func() error {
		C.notmuch_tags_destroy(ts.toC())
		return nil
	})
}

// All iterates over the tags.
func (ts *Tags) All() iter.Seq[string] {
	return func(yield func(string) bool) {
		if !(*cStruct)(ts).live() {
			return
		}
		for C.notmuch_tags_valid(ts.toC()) != 0 {
			if !yield(C.GoString(C.notmuch_tags_get(ts.toC()))) {
				return
			}
			C.notmuch_tags_move_to_next(ts.toC())
		}
	}
}

// slice returns the tags as a slice of strings.
func (ts *Tags) slice() []string {
	return slices.Collect(ts.All())
}

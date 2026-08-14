package notmuch

// Copyright © 2015 The go.notmuch Authors. Authors can be found in the AUTHORS file.
// Licensed under the GPLv3 or later.
// See COPYING at the root of the repository for details.

// #cgo LDFLAGS: -lnotmuch
// #include <stdlib.h>
// #include <notmuch.h>
import "C"

import "iter"

// Filenames is an iterator over the message's filenames.
type Filenames struct {
	cptr    *C.notmuch_filenames_t
	message *Message
}

// All iterates over the filenames.
func (fs *Filenames) All() iter.Seq[string] {
	return func(yield func(string) bool) {
		if fs.cptr == nil {
			return
		}
		for C.notmuch_filenames_valid(fs.cptr) != 0 {
			if !yield(C.GoString(C.notmuch_filenames_get(fs.cptr))) {
				return
			}
			C.notmuch_filenames_move_to_next(fs.cptr)
		}
	}
}

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
	"unsafe"
)

// Threads represents notmuch threads.
type Threads cStruct

func (ts *Threads) toC() *C.notmuch_threads_t {
	return (*C.notmuch_threads_t)(ts.cptr)
}

func (ts *Threads) Close() {
	(*cStruct)(ts).doClose(func() error {
		C.notmuch_threads_destroy(ts.toC())
		return nil
	})
}

// All iterates over the threads of the result set. Check Err after
// ranging.
func (ts *Threads) All() iter.Seq[*Thread] {
	return func(yield func(*Thread) bool) {
		if !(*cStruct)(ts).live() {
			return
		}
		for {
			cthread := C.notmuch_threads_get(ts.toC())
			if cthread == nil {
				return
			}
			thread := &Thread{
				cptr:   unsafe.Pointer(cthread),
				parent: (*cStruct)(ts),
			}
			setGcClose(thread)
			if !yield(thread) {
				return
			}
			C.notmuch_threads_move_to_next(ts.toC())
		}
	}
}

// Err reports an iteration error, e.g. a Xapian exception. It is nil
// after a normal end of the result set.
func (ts *Threads) Err() error {
	st := C.notmuch_threads_status(ts.toC())
	if st == C.NOTMUCH_STATUS_ITERATOR_EXHAUSTED {
		return nil
	}
	return statusErr(st)
}

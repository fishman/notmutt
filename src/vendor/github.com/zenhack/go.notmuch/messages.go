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

// Messages represents notmuch messages.
type Messages cStruct

func (ms *Messages) Close() {
	(*cStruct)(ms).doClose(func() error {
		C.notmuch_messages_destroy(ms.toC())
		return nil
	})
}

func (ms *Messages) toC() *C.notmuch_messages_t {
	return (*C.notmuch_messages_t)(ms.cptr)
}

// All iterates over the messages of the result set. Check Err after
// ranging.
func (ms *Messages) All() iter.Seq[*Message] {
	return func(yield func(*Message) bool) {
		if !(*cStruct)(ms).live() {
			return
		}
		for {
			cmessage := C.notmuch_messages_get(ms.toC())
			if cmessage == nil {
				return
			}
			message := &Message{
				cptr:   unsafe.Pointer(cmessage),
				parent: (*cStruct)(ms),
			}
			setGcClose(message)
			if !yield(message) {
				return
			}
			C.notmuch_messages_move_to_next(ms.toC())
		}
	}
}

// Err reports an iteration error, e.g. a Xapian exception. It is nil
// after a normal end of the result set.
func (ms *Messages) Err() error {
	st := C.notmuch_messages_status(ms.toC())
	if st == C.NOTMUCH_STATUS_ITERATOR_EXHAUSTED {
		return nil
	}
	return statusErr(st)
}

// Tags returns a list of tags from all messages.
//
// WARNING: You can no longer iterate over messages after calling this
// function, because the iterator will point at the end of the list. We do not
// have a function to reset the iterator yet and the only way how you can
// iterate over the list again is to recreate the message list.
func (ms *Messages) Tags() *Tags {
	ctags := C.notmuch_messages_collect_tags(ms.toC())
	if ctags == nil {
		return &Tags{}
	}
	tags := &Tags{
		cptr:   unsafe.Pointer(ctags),
		parent: (*cStruct)(ms),
	}
	setGcClose(tags)
	return tags
}

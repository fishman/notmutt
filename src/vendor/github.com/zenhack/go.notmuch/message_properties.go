package notmuch

// Copyright © 2015 The go.notmuch Authors. Authors can be found in the AUTHORS file.
// Licensed under the GPLv3 or later.
// See COPYING at the root of the repository for details.

// #cgo LDFLAGS: -lnotmuch
// #include <stdlib.h>
// #include <notmuch.h>
import "C"

import "iter"

// MessageProperties represents a notmuch properties iterator.
type MessageProperties cStruct

func (props *MessageProperties) toC() *C.notmuch_message_properties_t {
	return (*C.notmuch_message_properties_t)(props.cptr)
}

func (props *MessageProperties) Close() {
	(*cStruct)(props).doClose(func() error {
		C.notmuch_message_properties_destroy(props.toC())
		return nil
	})
}

// All iterates over the properties.
func (props *MessageProperties) All() iter.Seq[MessageProperty] {
	return func(yield func(MessageProperty) bool) {
		if !(*cStruct)(props).live() {
			return
		}
		for C.notmuch_message_properties_valid(props.toC()) != 0 {
			prop := MessageProperty{
				Key:   C.GoString(C.notmuch_message_properties_key(props.toC())),
				Value: C.GoString(C.notmuch_message_properties_value(props.toC())),
			}
			if !yield(prop) {
				return
			}
			C.notmuch_message_properties_move_to_next(props.toC())
		}
	}
}

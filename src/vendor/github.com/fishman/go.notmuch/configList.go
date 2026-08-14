package notmuch

// Copyright © 2015 The go.notmuch Authors. Authors can be found in the AUTHORS file.
// Licensed under the GPLv3 or later.
// See COPYING at the root of the repository for details.

// #cgo LDFLAGS: -lnotmuch
// #include <stdlib.h>
// #include <notmuch.h>
import "C"

import "iter"

// ConfigList represents the (key, value) configuration pairs of a database,
// including those from the configuration file and built-in defaults.
type ConfigList cStruct

func (cl *ConfigList) Close() {
	(*cStruct)(cl).doClose(func() error {
		C.notmuch_config_pairs_destroy(cl.toC())
		return nil
	})
}

func (cl *ConfigList) toC() *C.notmuch_config_pairs_t {
	return (*C.notmuch_config_pairs_t)(cl.cptr)
}

// All iterates over the config pairs, skipping pairs with empty values.
func (cl *ConfigList) All() iter.Seq2[string, string] {
	return func(yield func(string, string) bool) {
		if !(*cStruct)(cl).live() {
			return
		}
		for C.notmuch_config_pairs_valid(cl.toC()) != 0 {
			key := cl.key()
			value := cl.value()
			C.notmuch_config_pairs_move_to_next(cl.toC())
			if value == "" {
				continue
			}
			if !yield(key, value) {
				return
			}
		}
	}
}

func (cl *ConfigList) key() string {
	cstr := C.notmuch_config_pairs_key(cl.toC())
	if cstr == nil {
		// this should never happen
		return ""
	}
	return C.GoString(cstr)
}

func (cl *ConfigList) value() string {
	cstr := C.notmuch_config_pairs_value(cl.toC())
	if cstr == nil {
		return ""
	}
	return C.GoString(cstr)
}

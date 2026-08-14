//go:build notmuchcgo

package notmuch

/*
#cgo pkg-config: notmuch
#include <notmuch.h>
#include <stdlib.h>
*/
import "C"

import (
	"context"
	"fmt"
	"unsafe"

	"notmutt/core"
)

// CGOBackend is the in-tree binding (notmuch contrib pattern). It exists
// only for the benchmark; the CLI backend stays the default unless cgo
// demonstrably wins (SECURITY.md F10).
type CGOBackend struct {
	db *C.notmuch_database_t
}

func NewCGO() *CGOBackend { return &CGOBackend{} }

func (b *CGOBackend) Open(ctx context.Context, dbPath string) error {
	var db *C.notmuch_database_t
	path := C.CString(dbPath)
	defer C.free(unsafe.Pointer(path))
	if st := C.notmuch_database_open(path, C.NOTMUCH_DATABASE_MODE_READ_ONLY, &db); st != C.NOTMUCH_STATUS_SUCCESS {
		return errStatus(st, "open")
	}
	b.db = db
	return nil
}

func (b *CGOBackend) Close(ctx context.Context) error {
	if b.db != nil {
		C.notmuch_database_destroy(b.db)
		b.db = nil
	}
	return nil
}

func (b *CGOBackend) Revision(ctx context.Context) (string, uint64, error) {
	var uuid *C.char
	rev := C.notmuch_database_get_revision(b.db, &uuid)
	return C.GoString(uuid), uint64(rev), nil
}

func (b *CGOBackend) Query(ctx context.Context, query string, limit int) ([]core.Message, error) {
	qstr := C.CString(query)
	defer C.free(unsafe.Pointer(qstr))
	q := C.notmuch_query_create(b.db, qstr)
	if q == nil {
		return nil, fmt.Errorf("notmuch: query_create failed")
	}
	defer C.notmuch_query_destroy(q)
	C.notmuch_query_set_sort(q, C.NOTMUCH_SORT_NEWEST_FIRST)
	var msgs *C.notmuch_messages_t
	if st := C.notmuch_query_search_messages(q, &msgs); st != C.NOTMUCH_STATUS_SUCCESS {
		return nil, errStatus(st, "search")
	}
	defer C.notmuch_messages_destroy(msgs)
	var out []core.Message
	for C.notmuch_messages_valid(msgs) != 0 {
		if limit > 0 && len(out) >= limit {
			break
		}
		m := C.notmuch_messages_get(msgs)
		header := func(name string) string {
			c := C.CString(name)
			defer C.free(unsafe.Pointer(c))
			return C.GoString(C.notmuch_message_get_header(m, c))
		}
		out = append(out, core.Message{
			ID:        C.GoString(C.notmuch_message_get_message_id(m)),
			ThreadID:  C.GoString(C.notmuch_message_get_thread_id(m)),
			Timestamp: int64(C.notmuch_message_get_date(m)),
			Author:    header("from"),
			Subject:   header("subject"),
			Tags:      tagsOf(m),
			Paths:     pathsOf(m),
		})
		C.notmuch_message_destroy(m)
		C.notmuch_messages_move_to_next(msgs)
	}
	return out, nil
}

func (b *CGOBackend) Thread(ctx context.Context, threadID string) ([]core.Message, error) {
	return b.Query(ctx, "thread:"+threadID, 0)
}

func (b *CGOBackend) Tag(ctx context.Context, query string, ops []TagOp) error {
	return errStatus(C.NOTMUCH_STATUS_UNSUPPORTED_OPERATION, "tag (read-only handle)")
}

func (b *CGOBackend) New(ctx context.Context) error {
	return errStatus(C.NOTMUCH_STATUS_UNSUPPORTED_OPERATION, "new (read-only handle)")
}

func tagsOf(m *C.notmuch_message_t) []string {
	var out []string
	for t := C.notmuch_message_get_tags(m); C.notmuch_tags_valid(t) != 0; C.notmuch_tags_move_to_next(t) {
		out = append(out, C.GoString(C.notmuch_tags_get(t)))
	}
	return out
}

func pathsOf(m *C.notmuch_message_t) []string {
	var out []string
	for f := C.notmuch_message_get_filenames(m); C.notmuch_filenames_valid(f) != 0; C.notmuch_filenames_move_to_next(f) {
		out = append(out, C.GoString(C.notmuch_filenames_get(f)))
	}
	return out
}

func errStatus(st C.notmuch_status_t, op string) error {
	return fmt.Errorf("notmuch %s: %s", op, C.GoString(C.notmuch_status_to_string(st)))
}

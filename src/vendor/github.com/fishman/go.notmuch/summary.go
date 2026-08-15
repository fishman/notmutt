package notmuch

// Copyright © 2015 The go.notmuch Authors. Authors can be found in the AUTHORS file.
// Licensed under the GPLv3 or later.
// See COPYING at the root of the repository for details.

// #cgo LDFLAGS: -lnotmuch
// #include <stdlib.h>
// #include <string.h>
// #include <notmuch.h>
//
// // grow_arena ensures need free bytes after fill, reallocating when
// // needed (doubling). Returns the (possibly new) arena, or NULL on
// // OOM (freeing the old arena: realloc leaves it alive on failure).
// static void *grow_arena(void *arena, size_t *cap, size_t fill, size_t need) {
// 	if (fill + need <= *cap) {
// 		return arena;
// 	}
// 	size_t nc = *cap ? *cap : 4096;
// 	while (nc < fill + need) {
// 		nc *= 2;
// 	}
// 	void *na = realloc(arena, nc);
// 	if (!na) {
// 		free(arena);
// 		return NULL;
// 	}
// 	*cap = nc;
// 	return na;
// }
//
// // summary_walk is a progressive thread-summary walker: the query and
// // its threads iterator stay alive in C across chunk calls, so each
// // chunk continues where the previous stopped. The per-thread
// // header-cache reads amortize in C exactly like `notmuch search`
// // does; one boundary crossing per chunk.
// typedef struct summary_walk {
// 	notmuch_query_t *q;
// 	notmuch_threads_t *threads;
// 	int limit; // <= 0 = no limit
// 	int count; // threads packed so far
// } summary_walk;
//
// static summary_walk *summary_walk_new(notmuch_database_t *db, const char *query_str, int limit) {
// 	summary_walk *w = calloc(1, sizeof(*w));
// 	if (!w) {
// 		return NULL;
// 	}
// 	w->q = notmuch_query_create(db, query_str);
// 	if (!w->q) {
// 		free(w);
// 		return NULL;
// 	}
// 	notmuch_query_set_sort(w->q, NOTMUCH_SORT_NEWEST_FIRST);
// 	if (notmuch_query_search_threads(w->q, &w->threads) != NOTMUCH_STATUS_SUCCESS || !w->threads) {
// 		notmuch_query_destroy(w->q);
// 		free(w);
// 		return NULL;
// 	}
// 	w->limit = limit;
// 	return w;
// }
//
// // summary_walk_chunk packs up to cap more summaries into a fresh
// // arena: [int32 count][per thread: int64 timestamp, int32 idlen, id,
// // int32 authorslen, authors, int32 subjlen, subject, int32 tagslen,
// // tags(NUL-separated, double-NUL terminated)]. Native byte order.
// // The caller frees the buffer. Returns NULL with *out_status 1 when
// // the walk is exhausted, 2 on error.
// static void *summary_walk_chunk(summary_walk *w, int cap, size_t *out_size, int *out_status) {
// 	*out_status = 0;
// 	void *arena = NULL;
// 	size_t cap_ = 0, fill = 4; /* [int32 count] header */
// 	int32_t count = 0;
// 	arena = grow_arena(arena, &cap_, 0, fill);
// 	if (!arena) {
// 		*out_status = 2;
// 		return NULL;
// 	}
// 	while (notmuch_threads_valid(w->threads) && (w->limit <= 0 || w->count < w->limit) && count < cap) {
// 		notmuch_thread_t *t = notmuch_threads_get(w->threads);
// 		if (!t) {
// 			free(arena);
// 			*out_status = 2;
// 			return NULL;
// 		}
// 		long long ts = (long long)notmuch_thread_get_newest_date(t);
// 		const char *id = notmuch_thread_get_thread_id(t);
// 		const char *authors = notmuch_thread_get_authors(t);
// 		const char *subject = notmuch_thread_get_subject(t);
// 		notmuch_tags_t *tags = notmuch_thread_get_tags(t);
// 		if (!id) {
// 			id = "";
// 		}
// 		if (!authors) {
// 			authors = "";
// 		}
// 		if (!subject) {
// 			subject = "";
// 		}
// 		// The tags iterator is forward-only: drain it once into a
// 		// temp buffer, then append the blob.
// 		char *tbuf = NULL;
// 		size_t tcap = 0, tfill = 0;
// 		int ok = 1;
// 		if (tags) {
// 			for (notmuch_tags_valid(tags); notmuch_tags_valid(tags); notmuch_tags_move_to_next(tags)) {
// 				const char *tag = notmuch_tags_get(tags);
// 				size_t l = strlen(tag) + 1;
// 				tbuf = grow_arena(tbuf, &tcap, tfill, l);
// 				if (!tbuf) {
// 					ok = 0;
// 					break;
// 				}
// 				memcpy(tbuf + tfill, tag, l);
// 				tfill += l;
// 			}
// 		}
// 		if (ok) {
// 			tbuf = grow_arena(tbuf, &tcap, tfill, 1);
// 			if (!tbuf) {
// 				ok = 0;
// 			}
// 		}
// 		if (!ok) {
// 			free(tbuf);
// 			free(arena);
// 			*out_status = 2;
// 			return NULL;
// 		}
// 		tbuf[tfill++] = '\0';
// 		size_t idl = strlen(id), al = strlen(authors), sl = strlen(subject);
// 		size_t need = 8 + 4 + idl + 4 + al + 4 + sl + 4 + tfill;
// 		arena = grow_arena(arena, &cap_, fill, need);
// 		if (!arena) {
// 			free(tbuf);
// 			free(arena);
// 			*out_status = 2;
// 			return NULL;
// 		}
// 		memcpy(arena + fill, &ts, 8);
// 		fill += 8;
// 		int32_t n;
// 		n = (int32_t)idl;
// 		memcpy(arena + fill, &n, 4);
// 		fill += 4;
// 		memcpy(arena + fill, id, idl);
// 		fill += idl;
// 		n = (int32_t)al;
// 		memcpy(arena + fill, &n, 4);
// 		fill += 4;
// 		memcpy(arena + fill, authors, al);
// 		fill += al;
// 		n = (int32_t)sl;
// 		memcpy(arena + fill, &n, 4);
// 		fill += 4;
// 		memcpy(arena + fill, subject, sl);
// 		fill += sl;
// 		n = (int32_t)tfill;
// 		memcpy(arena + fill, &n, 4);
// 		fill += 4;
// 		memcpy(arena + fill, tbuf, tfill);
// 		fill += tfill;
// 		free(tbuf);
// 		count++;
// 		w->count++;
// 		notmuch_threads_move_to_next(w->threads);
// 	}
// 	if (count == 0) {
// 		free(arena);
// 		*out_status = 1;
// 		return NULL;
// 	}
// 	memcpy(arena, &count, 4);
// 	*out_size = fill;
// 	return arena;
// }
//
// static void summary_walk_free(summary_walk *w) {
// 	if (!w) {
// 		return;
// 	}
// 	notmuch_threads_destroy(w->threads);
// 	notmuch_query_destroy(w->q);
// 	free(w);
// }
import "C"

import (
	"encoding/binary"
	"strings"
	"unsafe"
)

// ThreadSummary is one thread's index row: the same per-thread data the
// CLI emits (thread id, newest date, authors, subject, tags), read from
// the Xapian header cache - zero file opens.
type ThreadSummary struct {
	ThreadID  string
	Timestamp int64
	Authors   string
	Subject   string
	Tags      []string
}

// ThreadsWalk is a progressive thread-summary walker: the query and
// threads iterator stay alive in C across Next calls, so each chunk
// continues where the previous stopped - one boundary crossing per
// chunk, the CLI's bulk emit spelled as C. Close frees the walk.
type ThreadsWalk cStruct

func (w *ThreadsWalk) toC() *C.summary_walk {
	return (*C.summary_walk)(w.cptr)
}

// NewThreadsWalk opens a progressive walk over the query's threads.
// limit <= 0 walks the whole result.
func (db *DB) NewThreadsWalk(query string, limit int) (*ThreadsWalk, error) {
	if !db.live() {
		return nil, ErrClosedDatabase
	}
	cq := C.CString(query)
	defer C.free(unsafe.Pointer(cq))
	walk := C.summary_walk_new(db.toC(), cq, C.int(limit))
	if walk == nil {
		return nil, ErrUnknownError
	}
	w := &ThreadsWalk{cptr: unsafe.Pointer(walk)}
	setGcCloseErr(w)
	return w, nil
}

// Next packs up to cap more summaries; done=true when the result is
// exhausted (an empty tail chunk ends the walk).
func (w *ThreadsWalk) Next(cap int) (rows []ThreadSummary, done bool, err error) {
	if !(*cStruct)(w).live() {
		return nil, false, ErrClosedDatabase
	}
	var size C.size_t
	var st C.int
	arena := C.summary_walk_chunk(w.toC(), C.int(cap), &size, &st)
	if arena == nil {
		if st == 1 {
			return nil, true, nil
		}
		return nil, false, ErrUnknownError
	}
	defer C.free(arena)
	return decodeSummaries(C.GoBytes(arena, C.int(size))), false, nil
}

// Close frees the walk and its C iterator.
func (w *ThreadsWalk) Close() error {
	return (*cStruct)(w).doClose(func() error {
		C.summary_walk_free(w.toC())
		return nil
	})
}

func decodeSummaries(data []byte) []ThreadSummary {
	if len(data) < 4 {
		return nil
	}
	n := int(binary.LittleEndian.Uint32(data))
	out := make([]ThreadSummary, 0, n)
	read := func(p int) (int, string) {
		l := int(binary.LittleEndian.Uint32(data[p:]))
		p += 4
		s := string(data[p : p+l])
		return p + l, s
	}
	for i, p := 0, 4; i < n; i++ {
		var s ThreadSummary
		s.Timestamp = int64(binary.LittleEndian.Uint64(data[p:]))
		p += 8
		p, s.ThreadID = read(p)
		p, s.Authors = read(p)
		p, s.Subject = read(p)
		var blob string
		p, blob = read(p)
		for _, tag := range strings.Split(blob, "\x00") {
			if tag != "" {
				s.Tags = append(s.Tags, tag)
			}
		}
		out = append(out, s)
	}
	return out
}

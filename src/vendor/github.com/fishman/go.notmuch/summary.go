package notmuch

// Copyright © 2015 The go.notmuch Authors. Authors can be found in the AUTHORS file.
// Licensed under the GPLv3 or later.
// See COPYING at the root of the repository for details.

// #cgo LDFLAGS: -lnotmuch
// #include <stdlib.h>
// #include <string.h>
// #include <stdio.h>
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
// // query_apply_excludes mirrors the CLI's default search behavior:
// // the config search.exclude_tags are excluded and omitted from
// // results. The raw C API excludes nothing by default; without this
// // the flat walks return the deleted/spam messages the CLI search
// // hides. Threaded walks keep full membership on purpose (the view
// // applies the deleted-leaf rule itself).
// static void query_apply_excludes(notmuch_database_t *db, notmuch_query_t *q) {
// 	char *ex = NULL;
// 	if (notmuch_database_get_config(db, "search.exclude_tags", &ex) != NOTMUCH_STATUS_SUCCESS || !ex) {
// 		return;
// 	}
// 	char *copy = strdup(ex);
// 	if (!copy) {
// 		return;
// 	}
// 	char *save = NULL;
// 	for (char *tok = strtok_r(copy, ";", &save); tok; tok = strtok_r(NULL, ";", &save)) {
// 		notmuch_query_add_tag_exclude(q, tok);
// 	}
// 	free(copy);
// 	notmuch_query_set_omit_excluded(q, NOTMUCH_EXCLUDE_TRUE);
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
//
// // full_walk is the progressive full-thread walker: per thread, the
// // summary fields plus every message's id/date/from/subject/tags/paths/
// // references - all header-cache reads, zero file opens, one boundary
// // crossing per chunk like summary_walk.
// typedef struct full_walk {
// 	notmuch_query_t *q;
// 	notmuch_threads_t *threads;
// 	int limit; // <= 0 = no limit
// 	int count; // threads packed so far
// } full_walk;
//
// static full_walk *full_walk_new(notmuch_database_t *db, const char *query_str, int limit) {
// 	full_walk *w = calloc(1, sizeof(*w));
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
// static int full_pack_str(void **arena, size_t *cap, size_t *fill, const char *s) {
// 	size_t l = strlen(s);
// 	void *na = grow_arena(*arena, cap, *fill, 4 + l);
// 	if (!na) {
// 		return 0;
// 	}
// 	*arena = na;
// 	int32_t n = (int32_t)l;
// 	memcpy((char *)*arena + *fill, &n, 4);
// 	*fill += 4;
// 	memcpy((char *)*arena + *fill, s, l);
// 	*fill += l;
// 	return 1;
// }
//
// // full_pack_buf is full_pack_str over an explicit length: blobs
// // carry embedded NULs, strlen would truncate at the first.
// static int full_pack_buf(void **arena, size_t *cap, size_t *fill, const char *s, size_t l) {
// 	void *na = grow_arena(*arena, cap, *fill, 4 + l);
// 	if (!na) {
// 		return 0;
// 	}
// 	*arena = na;
// 	int32_t n = (int32_t)l;
// 	memcpy((char *)*arena + *fill, &n, 4);
// 	*fill += 4;
// 	memcpy((char *)*arena + *fill, s, l);
// 	*fill += l;
// 	return 1;
// }
//
// static int full_pack_i64(void **arena, size_t *cap, size_t *fill, long long v) {
// 	void *na = grow_arena(*arena, cap, *fill, 8);
// 	if (!na) {
// 		return 0;
// 	}
// 	*arena = na;
// 	memcpy((char *)*arena + *fill, &v, 8);
// 	*fill += 8;
// 	return 1;
// }
//
// static int full_pack_i32(void **arena, size_t *cap, size_t *fill, int32_t v) {
// 	void *na = grow_arena(*arena, cap, *fill, 4);
// 	if (!na) {
// 		return 0;
// 	}
// 	*arena = na;
// 	memcpy((char *)*arena + *fill, &v, 4);
// 	*fill += 4;
// 	return 1;
// }
//
// // full_pack_blob drains an NUL-separated tag/path iterator into one
// // double-NUL-terminated blob (the summary_walk tags pattern).
// static int full_pack_blob(void **arena, size_t *cap, size_t *fill, notmuch_tags_t *tags) {
// 	char *tbuf = NULL;
// 	size_t tcap = 0, tfill = 0;
// 	int ok = 1;
// 	if (tags) {
// 		for (notmuch_tags_valid(tags); notmuch_tags_valid(tags); notmuch_tags_move_to_next(tags)) {
// 			const char *tag = notmuch_tags_get(tags);
// 			size_t l = strlen(tag) + 1;
// 			tbuf = grow_arena(tbuf, &tcap, tfill, l);
// 			if (!tbuf) {
// 				ok = 0;
// 				break;
// 			}
// 			memcpy(tbuf + tfill, tag, l);
// 			tfill += l;
// 		}
// 	}
// 	if (ok) {
// 		tbuf = grow_arena(tbuf, &tcap, tfill, 1);
// 		if (!tbuf) {
// 			ok = 0;
// 		}
// 	}
// 	if (!ok) {
// 		free(tbuf);
// 		return 0;
// 	}
// 	tbuf[tfill++] = '\0';
// 	int ok2 = full_pack_buf(arena, cap, fill, tbuf, tfill);
// 	free(tbuf);
// 	return ok2;
// }
//
// // full_pack_msg packs one message's row: id (id: prefix stripped),
// // date, from, subject, tags, paths, and an empty refs slot. The
// // references/in-reply-to reads are a deliberate drop (the refs
// // fallback, docs/refs-from-terms.md): get_header on those headers
// // file-parses every message (~4s of the walk); the chain rides the
// // per-thread fetch, and the libnotmuch getter fix re-adds the reads.
// // full_pack_paths is full_pack_blob over the filenames iterator (the
// // same string-iterator shape, distinct type).
// static int full_pack_paths(void **arena, size_t *cap, size_t *fill, notmuch_filenames_t *paths) {
// 	char *tbuf = NULL;
// 	size_t tcap = 0, tfill = 0;
// 	int ok = 1;
// 	if (paths) {
// 		for (notmuch_filenames_valid(paths); notmuch_filenames_valid(paths); notmuch_filenames_move_to_next(paths)) {
// 			const char *path = notmuch_filenames_get(paths);
// 			size_t l = strlen(path) + 1;
// 			tbuf = grow_arena(tbuf, &tcap, tfill, l);
// 			if (!tbuf) {
// 				ok = 0;
// 				break;
// 			}
// 			memcpy(tbuf + tfill, path, l);
// 			tfill += l;
// 		}
// 	}
// 	if (ok) {
// 		tbuf = grow_arena(tbuf, &tcap, tfill, 1);
// 		if (!tbuf) {
// 			ok = 0;
// 		}
// 	}
// 	if (!ok) {
// 		free(tbuf);
// 		return 0;
// 	}
// 	tbuf[tfill++] = '\0';
// 	int ok2 = full_pack_buf(arena, cap, fill, tbuf, tfill);
// 	free(tbuf);
// 	return ok2;
// }
//
// static int full_pack_msg(void **arena, size_t *cap, size_t *fill, notmuch_message_t *m) {
// 	const char *id = notmuch_message_get_message_id(m);
// 	if (id && strncmp(id, "id:", 3) == 0) {
// 		id += 3;
// 	}
// 	if (!id) {
// 		id = "";
// 	}
// 	long long ts = (long long)notmuch_message_get_date(m);
// 	const char *from = notmuch_message_get_header(m, "from");
// 	const char *subj = notmuch_message_get_header(m, "subject");
// 	if (!from) {
// 		from = "";
// 	}
// 	if (!subj) {
// 		subj = "";
// 	}
// 	notmuch_tags_t *mtags = notmuch_message_get_tags(m);
// 	notmuch_filenames_t *mfiles = notmuch_message_get_filenames(m);
// 	int ok = full_pack_str(arena, cap, fill, id) && full_pack_i64(arena, cap, fill, ts) &&
// 		full_pack_str(arena, cap, fill, from) && full_pack_str(arena, cap, fill, subj) &&
// 		full_pack_blob(arena, cap, fill, mtags) && full_pack_paths(arena, cap, fill, mfiles) &&
// 		full_pack_str(arena, cap, fill, "");
// 	if (mfiles) {
// 		notmuch_filenames_destroy(mfiles);
// 	}
// 	return ok;
// }
//
// // full_walk_chunk packs up to cap more full threads: [int32 count]
// // then per thread [int64 ts, id, authors, subject, tags-blob, int32
// // msgcount, per-message rows (id, ts, from, subject, tags-blob,
// // paths-blob, refs)]. The caller frees the buffer. Returns NULL with
// // *out_status 1 when exhausted, 2 on error.
// static void *full_walk_chunk(full_walk *w, int cap, size_t *out_size, int *out_status) {
// 	*out_status = 0;
// 	void *arena = NULL;
// 	size_t cap_ = 0, fill = 4;
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
// 		if (!full_pack_i64(&arena, &cap_, &fill, ts) || !full_pack_str(&arena, &cap_, &fill, id) ||
// 			!full_pack_str(&arena, &cap_, &fill, authors) || !full_pack_str(&arena, &cap_, &fill, subject) ||
// 			!full_pack_blob(&arena, &cap_, &fill, tags)) {
// 			free(arena);
// 			*out_status = 2;
// 			return NULL;
// 		}
// 		int32_t mcount = (int32_t)notmuch_thread_get_total_messages(t);
// 		if (!full_pack_i32(&arena, &cap_, &fill, mcount)) {
// 			free(arena);
// 			*out_status = 2;
// 			return NULL;
// 		}
// 		notmuch_messages_t *msgs = notmuch_thread_get_messages(t);
// 		if (msgs) {
// 			for (int i = 0; i < mcount && notmuch_messages_valid(msgs); i++) {
// 				notmuch_message_t *m = notmuch_messages_get(msgs);
// 				if (!m) {
// 					notmuch_messages_destroy(msgs);
// 					free(arena);
// 					*out_status = 2;
// 					return NULL;
// 				}
// 				if (!full_pack_msg(&arena, &cap_, &fill, m)) {
// 					notmuch_messages_destroy(msgs);
// 					free(arena);
// 					*out_status = 2;
// 					return NULL;
// 				}
// 				notmuch_messages_move_to_next(msgs);
// 			}
// 			notmuch_messages_destroy(msgs);
// 		}
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
// static void full_walk_free(full_walk *w) {
// 	if (!w) {
// 		return;
// 	}
// 	notmuch_threads_destroy(w->threads);
// 	notmuch_query_destroy(w->q);
// 	free(w);
// }
// // msg_walk is the progressive message-level walker: the query and
// // its messages iterator stay alive in C across chunk calls - the
// // flat views' shape (unread, deleted, search): one row per MATCHED
// // message, no thread drag. limit bounds the message count, like
// // `notmuch search --limit`.
// typedef struct msg_walk {
// 	notmuch_query_t *q;
// 	notmuch_messages_t *msgs;
// 	int limit; // <= 0 = no limit
// 	int count; // messages packed so far
// } msg_walk;
//
// static void msg_walk_free(msg_walk *w) {
// 	if (!w) {
// 		return;
// 	}
// 	notmuch_messages_destroy(w->msgs);
// 	notmuch_query_destroy(w->q);
// 	free(w);
// }
//
// static msg_walk *msg_walk_new(notmuch_database_t *db, const char *query_str, int limit) {
// 	msg_walk *w = calloc(1, sizeof(*w));
// 	if (!w) {
// 		return NULL;
// 	}
// 	w->q = notmuch_query_create(db, query_str);
// 	if (!w->q) {
// 		free(w);
// 		return NULL;
// 	}
// 	notmuch_query_set_sort(w->q, NOTMUCH_SORT_NEWEST_FIRST);
// 	query_apply_excludes(db, w->q);
// 	if (notmuch_query_search_messages(w->q, &w->msgs) != NOTMUCH_STATUS_SUCCESS || !w->msgs) {
// 		notmuch_query_destroy(w->q);
// 		free(w);
// 		return NULL;
// 	}
// 	w->limit = limit;
// 	return w;
// }
//
// // msg_walk_chunk packs up to cap more messages: [int32 count][per
// // message: threadid, then the full_pack_msg row]. The caller frees
// // the buffer. Returns NULL with *out_status 1 when exhausted, 2 on
// // error.
// static void *msg_walk_chunk(msg_walk *w, int cap, size_t *out_size, int *out_status) {
// 	*out_status = 0;
// 	void *arena = NULL;
// 	size_t cap_ = 0, fill = 4; /* [int32 count] header */
// 	int32_t count = 0;
// 	arena = grow_arena(arena, &cap_, 0, fill);
// 	if (!arena) {
// 		*out_status = 2;
// 		return NULL;
// 	}
// 	while (notmuch_messages_valid(w->msgs) && (w->limit <= 0 || w->count < w->limit) && count < cap) {
// 		notmuch_message_t *m = notmuch_messages_get(w->msgs);
// 		if (!m) {
// 			free(arena);
// 			*out_status = 2;
// 			return NULL;
// 		}
// 		const char *tid = notmuch_message_get_thread_id(m);
// 		if (!tid) {
// 			tid = "";
// 		}
// 		if (!full_pack_str(&arena, &cap_, &fill, tid) || !full_pack_msg(&arena, &cap_, &fill, m)) {
// 			free(arena);
// 			*out_status = 2;
// 			return NULL;
// 		}
// 		count++;
// 		w->count++;
// 		notmuch_messages_move_to_next(w->msgs);
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

// FullMessage is one message's index row: everything the pager tree
// needs (id, date, from, subject, tags, paths, the reference chain),
// read from the header cache like ThreadSummary.
type FullMessage struct {
	ID         string
	ThreadID   string // the message-level walk's pack: its thread id
	Timestamp  int64
	Author     string
	Subject    string
	Tags       []string
	Paths      []string
	References string // raw "references in-reply-to" headers, space-joined
}

// FullThread is a thread's summary plus its full message rows, packed
// in one progressive walk - the stub emit and the per-thread fetch as
// a single pass.
type FullThread struct {
	ThreadSummary
	Msgs []FullMessage
}

// FullWalk is the progressive full-thread walker: the query and its
// threads iterator stay alive in C, each chunk packs the next threads
// with their messages - one boundary crossing per chunk, zero file
// opens (header cache only). Close frees the walk.
type FullWalk cStruct

func (w *FullWalk) toC() *C.full_walk {
	return (*C.full_walk)(w.cptr)
}

// NewFullWalk opens a progressive full-thread walk over the query's
// threads.
func (db *DB) NewFullWalk(query string, limit int) (*FullWalk, error) {
	if !db.live() {
		return nil, ErrClosedDatabase
	}
	cq := C.CString(query)
	defer C.free(unsafe.Pointer(cq))
	walk := C.full_walk_new(db.toC(), cq, C.int(limit))
	if walk == nil {
		return nil, ErrUnknownError
	}
	w := &FullWalk{cptr: unsafe.Pointer(walk)}
	setGcCloseErr(w)
	return w, nil
}

// Next packs up to cap more full threads; done=true when the result is
// exhausted (an empty tail chunk ends the walk).
func (w *FullWalk) Next(cap int) (rows []FullThread, done bool, err error) {
	if !(*cStruct)(w).live() {
		return nil, false, ErrClosedDatabase
	}
	var size C.size_t
	var st C.int
	arena := C.full_walk_chunk(w.toC(), C.int(cap), &size, &st)
	if arena == nil {
		if st == 1 {
			return nil, true, nil
		}
		return nil, false, ErrUnknownError
	}
	defer C.free(arena)
	return decodeFull(C.GoBytes(arena, C.int(size))), false, nil
}

// Close frees the walk and its C iterator.
func (w *FullWalk) Close() error {
	return (*cStruct)(w).doClose(func() error {
		C.full_walk_free(w.toC())
		return nil
	})
}

func decodeFull(data []byte) []FullThread {
	if len(data) < 4 {
		return nil
	}
	n := int(binary.LittleEndian.Uint32(data))
	out := make([]FullThread, 0, n)
	read := func(p int) (int, string) {
		l := int(binary.LittleEndian.Uint32(data[p:]))
		p += 4
		s := string(data[p : p+l])
		return p + l, s
	}
	split := func(s string) []string {
		var out []string
		for _, e := range strings.Split(s, "\x00") {
			if e != "" {
				out = append(out, e)
			}
		}
		return out
	}
	for i, p := 0, 4; i < n; i++ {
		var t FullThread
		t.Timestamp = int64(binary.LittleEndian.Uint64(data[p:]))
		p += 8
		p, t.ThreadID = read(p)
		p, t.Authors = read(p)
		p, t.Subject = read(p)
		var tags string
		p, tags = read(p)
		t.Tags = split(tags)
		mcount := int(binary.LittleEndian.Uint32(data[p:]))
		p += 4
		for j := 0; j < mcount; j++ {
			var m FullMessage
			p, m.ID = read(p)
			m.Timestamp = int64(binary.LittleEndian.Uint64(data[p:]))
			p += 8
			p, m.Author = read(p)
			p, m.Subject = read(p)
			p, tags = read(p)
			m.Tags = split(tags)
			p, tags = read(p)
			m.Paths = split(tags)
			p, m.References = read(p)
			t.Msgs = append(t.Msgs, m)
		}
		out = append(out, t)
	}
	return out
}

// MsgWalk is the progressive message-level walker: the flat views'
// shape (unread, deleted, search) - one row per MATCHED message, no
// thread drag. The query and its messages iterator stay alive in C
// across chunk calls; each chunk packs threadid + the full message
// row (the full_pack_msg shape, minus the C-side thread loop).
type MsgWalk cStruct

func (w *MsgWalk) toC() *C.msg_walk {
	return (*C.msg_walk)(w.cptr)
}

// NewMsgWalk opens a progressive message-level walk over the query's
// messages. limit bounds the message count, like `notmuch search
// --limit`.
func (db *DB) NewMsgWalk(query string, limit int) (*MsgWalk, error) {
	if !db.live() {
		return nil, ErrClosedDatabase
	}
	cq := C.CString(query)
	defer C.free(unsafe.Pointer(cq))
	walk := C.msg_walk_new(db.toC(), cq, C.int(limit))
	if walk == nil {
		return nil, ErrUnknownError
	}
	w := &MsgWalk{cptr: unsafe.Pointer(walk)}
	setGcCloseErr(w)
	return w, nil
}

// Next packs up to cap more messages; done=true when the result is
// exhausted (an empty tail chunk ends the walk).
func (w *MsgWalk) Next(cap int) (rows []FullMessage, done bool, err error) {
	if !(*cStruct)(w).live() {
		return nil, false, ErrClosedDatabase
	}
	var size C.size_t
	var st C.int
	arena := C.msg_walk_chunk(w.toC(), C.int(cap), &size, &st)
	if arena == nil {
		if st == 1 {
			return nil, true, nil
		}
		return nil, false, ErrUnknownError
	}
	defer C.free(arena)
	return decodeMsgs(C.GoBytes(arena, C.int(size))), false, nil
}

// Close frees the walk and its C iterator.
func (w *MsgWalk) Close() error {
	return (*cStruct)(w).doClose(func() error {
		C.msg_walk_free(w.toC())
		return nil
	})
}

func decodeMsgs(data []byte) []FullMessage {
	if len(data) < 4 {
		return nil
	}
	n := int(binary.LittleEndian.Uint32(data))
	out := make([]FullMessage, 0, n)
	read := func(p int) (int, string) {
		l := int(binary.LittleEndian.Uint32(data[p:]))
		p += 4
		s := string(data[p : p+l])
		return p + l, s
	}
	split := func(s string) []string {
		var out []string
		for _, e := range strings.Split(s, "\x00") {
			if e != "" {
				out = append(out, e)
			}
		}
		return out
	}
	for i, p := 0, 4; i < n; i++ {
		var m FullMessage
		p, m.ThreadID = read(p)
		p, m.ID = read(p)
		m.Timestamp = int64(binary.LittleEndian.Uint64(data[p:]))
		p += 8
		p, m.Author = read(p)
		p, m.Subject = read(p)
		var blob string
		p, blob = read(p)
		m.Tags = split(blob)
		p, blob = read(p)
		m.Paths = split(blob)
		p, m.References = read(p)
		out = append(out, m)
	}
	return out
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

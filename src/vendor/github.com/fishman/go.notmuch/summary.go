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
// static summary_walk *summary_walk_new(notmuch_database_t *db, const char *query_str, int limit, int *out_status) {
// 	*out_status = 0;
// 	summary_walk *w = calloc(1, sizeof(*w));
// 	if (!w) {
// 		*out_status = -1; // allocation, no notmuch status
// 		return NULL;
// 	}
// 	w->q = notmuch_query_create(db, query_str);
// 	if (!w->q) {
// 		*out_status = -1;
// 		free(w);
// 		return NULL;
// 	}
// 	notmuch_query_set_sort(w->q, NOTMUCH_SORT_NEWEST_FIRST);
// 	notmuch_status_t qs = notmuch_query_search_threads(w->q, &w->threads);
// 	if (qs != NOTMUCH_STATUS_SUCCESS || !w->threads) {
// 		*out_status = qs == NOTMUCH_STATUS_SUCCESS ? -1 : (int)qs;
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
// static full_walk *full_walk_new(notmuch_database_t *db, const char *query_str, int limit, int *out_status) {
// 	*out_status = 0;
// 	full_walk *w = calloc(1, sizeof(*w));
// 	if (!w) {
// 		*out_status = -1; // allocation, no notmuch status
// 		return NULL;
// 	}
// 	w->q = notmuch_query_create(db, query_str);
// 	if (!w->q) {
// 		*out_status = -1;
// 		free(w);
// 		return NULL;
// 	}
// 	notmuch_query_set_sort(w->q, NOTMUCH_SORT_NEWEST_FIRST);
// 	notmuch_status_t qs = notmuch_query_search_threads(w->q, &w->threads);
// 	if (qs != NOTMUCH_STATUS_SUCCESS || !w->threads) {
// 		*out_status = qs == NOTMUCH_STATUS_SUCCESS ? -1 : (int)qs;
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
// // date, from, subject, tags, paths, and the refs slot (full_pack_refs:
// // the reference chain from the fork's term-list getters under
// // NOTMUCH_HAS_REF_GETTERS - the refsfromterms build option, docs/
// // refs-from-terms.md). A stock build packs the slot empty: get_header
// // on the references headers file-parses every message (~4s of the
// // walk), so the drop keeps the walk fast there and the chain rides
// // the per-thread fetch.
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
// // full_pack_refs packs the message's reference chain into the row's
// // refs slot: "refs irt" space-joined, the ids notmuch parsed at index
// // time (the replyto term plus the reference terms). Gated on
// // NOTMUCH_HAS_REF_GETTERS, defined only by the refsfromterms build
// // tag (refsfromterms.go): stock notmuch lacks the getters, so the
// // slot packs empty and the walk keeps its file-open-free fast path.
// static int full_pack_refs(void **arena, size_t *cap, size_t *fill, notmuch_message_t *m) {
// 	const char *refs = "", *irt = "";
// #ifdef NOTMUCH_HAS_REF_GETTERS
// 	refs = notmuch_message_get_references(m);
// 	irt = notmuch_message_get_in_reply_to(m);
// #endif
// 	if (!refs) {
// 		refs = "";
// 	}
// 	if (!irt) {
// 		irt = "";
// 	}
// 	size_t lr = strlen(refs), li = strlen(irt), n = lr + li + (lr && li ? 1 : 0);
// 	char *buf = malloc(n + 1);
// 	if (!buf) {
// 		return 0;
// 	}
// 	memcpy(buf, refs, lr);
// 	if (lr && li) {
// 		buf[lr] = ' ';
// 	}
// 	memcpy(buf + lr + (lr && li ? 1 : 0), irt, li);
// 	int ok = full_pack_buf(arena, cap, fill, buf, n);
// 	free(buf);
// 	return ok;
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
// 		full_pack_refs(arena, cap, fill, m);
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
// 		// pack a placeholder row count and patch it with the rows
// 		// actually emitted after the loop: total_messages and the
// 		// iterator length must agree, but the packed count is the
// 		// truth - a divergence must not let the decoder read past
// 		// the arena
// 		size_t mcount_pos = fill;
// 		if (!full_pack_i32(&arena, &cap_, &fill, mcount)) {
// 			free(arena);
// 			*out_status = 2;
// 			return NULL;
// 		}
// 		notmuch_messages_t *msgs = notmuch_thread_get_messages(t);
// 		int32_t packed = 0;
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
// 				packed++;
// 				notmuch_messages_move_to_next(msgs);
// 			}
// 			notmuch_messages_destroy(msgs);
// 		}
// 		memcpy((char *)arena + mcount_pos, &packed, 4);
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
// static msg_walk *msg_walk_new(notmuch_database_t *db, const char *query_str, int limit, int *out_status) {
// 	*out_status = 0;
// 	msg_walk *w = calloc(1, sizeof(*w));
// 	if (!w) {
// 		*out_status = -1; // allocation, no notmuch status
// 		return NULL;
// 	}
// 	w->q = notmuch_query_create(db, query_str);
// 	if (!w->q) {
// 		*out_status = -1;
// 		free(w);
// 		return NULL;
// 	}
// 	notmuch_query_set_sort(w->q, NOTMUCH_SORT_NEWEST_FIRST);
// 	query_apply_excludes(db, w->q);
// 	notmuch_status_t qs = notmuch_query_search_messages(w->q, &w->msgs);
// 	if (qs != NOTMUCH_STATUS_SUCCESS || !w->msgs) {
// 		*out_status = qs == NOTMUCH_STATUS_SUCCESS ? -1 : (int)qs;
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

// walkOpenErr maps a walk constructor's out_status back to the real
// error: a non-positive status means the NULL came from allocation (no
// notmuch failure), so ErrUnknownError is honest there.
func walkOpenErr(st C.int) error {
	if st <= 0 {
		return ErrUnknownError
	}
	return statusErr(C.notmuch_status_t(st))
}

// NewThreadsWalk opens a progressive walk over the query's threads.
// limit <= 0 walks the whole result.
func (db *DB) NewThreadsWalk(query string, limit int) (*ThreadsWalk, error) {
	if !db.live() {
		return nil, ErrClosedDatabase
	}
	cq := C.CString(query)
	defer C.free(unsafe.Pointer(cq))
	var st C.int = -1
	walk := C.summary_walk_new(db.toC(), cq, C.int(limit), &st)
	if walk == nil {
		return nil, walkOpenErr(st)
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
	if size > 0x7fffffff {
		return nil, false, ErrMalformedData
	}
	rows, err = decodeSummaries(C.GoBytes(arena, C.int(size)))
	if err != nil {
		return nil, false, err
	}
	return rows, false, nil
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
	References string // refs + irt, space-joined: term-list ids (refsfromterms build) or empty (stock)
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
	var st C.int = -1
	walk := C.full_walk_new(db.toC(), cq, C.int(limit), &st)
	if walk == nil {
		return nil, walkOpenErr(st)
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
	if size > 0x7fffffff {
		return nil, false, ErrMalformedData
	}
	rows, err = decodeFull(C.GoBytes(arena, C.int(size)))
	if err != nil {
		return nil, false, err
	}
	return rows, false, nil
}

// Close frees the walk and its C iterator.
func (w *FullWalk) Close() error {
	return (*cStruct)(w).doClose(func() error {
		C.full_walk_free(w.toC())
		return nil
	})
}

// blobReader walks a packed arena; every read checks its bounds so a
// corrupt or truncated arena errors instead of panicking.
type blobReader struct {
	data []byte
	pos  int
}

func (r *blobReader) u32() (int, error) {
	if r.pos+4 > len(r.data) {
		return 0, ErrMalformedData
	}
	n := int(binary.LittleEndian.Uint32(r.data[r.pos:]))
	r.pos += 4
	return n, nil
}

func (r *blobReader) i64() (int64, error) {
	if r.pos+8 > len(r.data) {
		return 0, ErrMalformedData
	}
	n := int64(binary.LittleEndian.Uint64(r.data[r.pos:]))
	r.pos += 8
	return n, nil
}

func (r *blobReader) str() (string, error) {
	if r.pos+4 > len(r.data) {
		return "", ErrMalformedData
	}
	l := int(binary.LittleEndian.Uint32(r.data[r.pos:]))
	r.pos += 4
	if l < 0 || r.pos+l > len(r.data) {
		return "", ErrMalformedData
	}
	s := string(r.data[r.pos : r.pos+l])
	r.pos += l
	return s, nil
}

func splitBlob(s string) []string {
	var out []string
	for _, e := range strings.Split(s, "\x00") {
		if e != "" {
			out = append(out, e)
		}
	}
	return out
}

func decodeFull(data []byte) ([]FullThread, error) {
	if len(data) < 4 {
		return nil, ErrMalformedData
	}
	r := blobReader{data: data}
	n, err := r.u32()
	if err != nil {
		return nil, err
	}
	out := make([]FullThread, 0, n)
	for i := 0; i < n; i++ {
		var t FullThread
		if t.Timestamp, err = r.i64(); err != nil {
			return nil, err
		}
		if t.ThreadID, err = r.str(); err != nil {
			return nil, err
		}
		if t.Authors, err = r.str(); err != nil {
			return nil, err
		}
		if t.Subject, err = r.str(); err != nil {
			return nil, err
		}
		var blob string
		if blob, err = r.str(); err != nil {
			return nil, err
		}
		t.Tags = splitBlob(blob)
		mcount, err := r.u32()
		if err != nil {
			return nil, err
		}
		for j := 0; j < mcount; j++ {
			var m FullMessage
			if m.ID, err = r.str(); err != nil {
				return nil, err
			}
			if m.Timestamp, err = r.i64(); err != nil {
				return nil, err
			}
			if m.Author, err = r.str(); err != nil {
				return nil, err
			}
			if m.Subject, err = r.str(); err != nil {
				return nil, err
			}
			if blob, err = r.str(); err != nil {
				return nil, err
			}
			m.Tags = splitBlob(blob)
			if blob, err = r.str(); err != nil {
				return nil, err
			}
			m.Paths = splitBlob(blob)
			if m.References, err = r.str(); err != nil {
				return nil, err
			}
			t.Msgs = append(t.Msgs, m)
		}
		out = append(out, t)
	}
	return out, nil
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
	var st C.int = -1
	walk := C.msg_walk_new(db.toC(), cq, C.int(limit), &st)
	if walk == nil {
		return nil, walkOpenErr(st)
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
	if size > 0x7fffffff {
		return nil, false, ErrMalformedData
	}
	rows, err = decodeMsgs(C.GoBytes(arena, C.int(size)))
	if err != nil {
		return nil, false, err
	}
	return rows, false, nil
}

// Close frees the walk and its C iterator.
func (w *MsgWalk) Close() error {
	return (*cStruct)(w).doClose(func() error {
		C.msg_walk_free(w.toC())
		return nil
	})
}

func decodeMsgs(data []byte) ([]FullMessage, error) {
	if len(data) < 4 {
		return nil, ErrMalformedData
	}
	r := blobReader{data: data}
	n, err := r.u32()
	if err != nil {
		return nil, err
	}
	out := make([]FullMessage, 0, n)
	for i := 0; i < n; i++ {
		var m FullMessage
		if m.ThreadID, err = r.str(); err != nil {
			return nil, err
		}
		if m.ID, err = r.str(); err != nil {
			return nil, err
		}
		if m.Timestamp, err = r.i64(); err != nil {
			return nil, err
		}
		if m.Author, err = r.str(); err != nil {
			return nil, err
		}
		if m.Subject, err = r.str(); err != nil {
			return nil, err
		}
		var blob string
		if blob, err = r.str(); err != nil {
			return nil, err
		}
		m.Tags = splitBlob(blob)
		if blob, err = r.str(); err != nil {
			return nil, err
		}
		m.Paths = splitBlob(blob)
		if m.References, err = r.str(); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

func decodeSummaries(data []byte) ([]ThreadSummary, error) {
	if len(data) < 4 {
		return nil, ErrMalformedData
	}
	r := blobReader{data: data}
	n, err := r.u32()
	if err != nil {
		return nil, err
	}
	out := make([]ThreadSummary, 0, n)
	for i := 0; i < n; i++ {
		var s ThreadSummary
		if s.Timestamp, err = r.i64(); err != nil {
			return nil, err
		}
		if s.ThreadID, err = r.str(); err != nil {
			return nil, err
		}
		if s.Authors, err = r.str(); err != nil {
			return nil, err
		}
		if s.Subject, err = r.str(); err != nil {
			return nil, err
		}
		var blob string
		if blob, err = r.str(); err != nil {
			return nil, err
		}
		s.Tags = splitBlob(blob)
		out = append(out, s)
	}
	return out, nil
}

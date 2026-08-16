package notmuch

// Copyright © 2026 Reza Jelveh. Authors can be found in the AUTHORS file.
// Licensed under the GPLv3 or later.
// See COPYING at the root of the repository for details.

// #cgo LDFLAGS: -lnotmuch
// #include <stdlib.h>
// #include <string.h>
// #include <notmuch.h>
//
// // grow_arena is repeated from summary.go's preamble: cgo preambles are
// // per-file translation units, so a helper defined there is not visible
// // here.
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
// // addr_walk is a progressive message walker for address harvest: the
// // query and its messages iterator stay alive in C across chunk calls,
// // exactly like summary_walk. Headers are read in C and copied into the
// // arena before the message is destroyed; one boundary crossing per
// // chunk.
// typedef struct addr_walk {
// 	notmuch_query_t *q;
// 	notmuch_messages_t *msgs;
// 	int limit; // <= 0 = no limit
// 	int count; // messages packed so far
// 	int sender;
// 	int recipients;
// } addr_walk;
//
// static addr_walk *addr_walk_new(notmuch_database_t *db, const char *query_str, int limit, int sender, int recipients) {
// 	addr_walk *w = calloc(1, sizeof(*w));
// 	if (!w) {
// 		return NULL;
// 	}
// 	w->q = notmuch_query_create(db, query_str);
// 	if (!w->q) {
// 		free(w);
// 		return NULL;
// 	}
// 	// The harvest dedups in Go, so mset order is irrelevant; UNSORTED
// 	// skips the sort cost (notmuch address does the same for
// 	// --deduplicate=address).
// 	notmuch_query_set_sort(w->q, NOTMUCH_SORT_UNSORTED);
// 	if (notmuch_query_search_messages(w->q, &w->msgs) != NOTMUCH_STATUS_SUCCESS || !w->msgs) {
// 		notmuch_query_destroy(w->q);
// 		free(w);
// 		return NULL;
// 	}
// 	w->limit = limit;
// 	w->sender = sender;
// 	w->recipients = recipients;
// 	return w;
// }
//
// // addr_walk_chunk packs up to cap more messages into a fresh arena:
// // [int32 count][per message: int32 nheaders][per header: int32 len,
// // bytes]. Selected headers in order: from (sender), to, cc, bcc
// // (recipients). Native byte order. The caller frees the buffer.
// // Returns NULL with *out_status 1 when the walk is exhausted, 2 on
// // error (grow_arena frees the old arena on OOM, so a NULL arena is
// // already freed).
// static void *addr_walk_chunk(addr_walk *w, int cap, size_t *out_size, int *out_status) {
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
// 		const char *hdrs[4];
// 		int nh = 0;
// 		if (w->sender) {
// 			hdrs[nh++] = notmuch_message_get_header(m, "from");
// 		}
// 		if (w->recipients) {
// 			hdrs[nh++] = notmuch_message_get_header(m, "to");
// 			hdrs[nh++] = notmuch_message_get_header(m, "cc");
// 			hdrs[nh++] = notmuch_message_get_header(m, "bcc");
// 		}
// 		size_t lens[4] = {0, 0, 0, 0};
// 		size_t need = 4;
// 		for (int i = 0; i < nh; i++) {
// 			if (!hdrs[i]) {
// 				hdrs[i] = "";
// 			}
// 			lens[i] = strlen(hdrs[i]);
// 			need += 4 + lens[i];
// 		}
// 		arena = grow_arena(arena, &cap_, fill, need);
// 		if (!arena) {
// 			notmuch_message_destroy(m);
// 			*out_status = 2;
// 			return NULL;
// 		}
// 		int32_t n = (int32_t)nh;
// 		memcpy(arena + fill, &n, 4);
// 		fill += 4;
// 		for (int i = 0; i < nh; i++) {
// 			n = (int32_t)lens[i];
// 			memcpy(arena + fill, &n, 4);
// 			fill += 4;
// 			memcpy(arena + fill, hdrs[i], lens[i]);
// 			fill += lens[i];
// 		}
// 		notmuch_message_destroy(m);
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
// static void addr_walk_free(addr_walk *w) {
// 	if (!w) {
// 		return;
// 	}
// 	notmuch_messages_destroy(w->msgs);
// 	notmuch_query_destroy(w->q);
// 	free(w);
// }
import "C"

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"strings"
	"unsafe"
)

// AddressEntry is one deduplicated address with its most common name
// and total occurrence count (notmuch address --deduplicate=address
// semantics: per address, the name variant seen most often wins, and
// counts are conflated).
type AddressEntry struct {
	Addr  string
	Name  string
	Count uint
}

// AddressOpts selects which headers to harvest. The zero value means
// sender-only (from:), the CLI default.
type AddressOpts struct {
	Sender     bool // from:
	Recipients bool // to:, cc:, bcc:
	Limit      int  // <= 0 = no limit
}

// Addresses harvests unique addresses from messages matching the
// query. The walk is chunked in C (one boundary crossing per chunk -
// the ThreadsWalk pattern), so a full-mailbox harvest runs at the
// mset walk speed instead of per-message cgo overhead. From/to are
// read from the DB's header cache (zero file opens); cc/bcc open
// message files - the slow path, paid only with Recipients. The
// result is in first-seen order; dedup keys are case-insensitive on
// the address, per notmuch's strcase hash.
func (db *DB) Addresses(query string, opts AddressOpts) ([]AddressEntry, error) {
	if !db.live() {
		return nil, ErrClosedDatabase
	}
	if !opts.Sender && !opts.Recipients {
		opts.Sender = true
	}
	cq := C.CString(query)
	defer C.free(unsafe.Pointer(cq))
	walk := C.addr_walk_new(db.toC(), cq, C.int(opts.Limit), C.int(bool2int(opts.Sender)), C.int(bool2int(opts.Recipients)))
	if walk == nil {
		return nil, ErrUnknownError
	}
	defer C.addr_walk_free(walk)

	buckets := make(map[string]*addrBucket)
	var order []string
	for {
		var size C.size_t
		var st C.int
		arena := C.addr_walk_chunk(walk, C.int(4096), &size, &st)
		if arena == nil {
			if st == 1 {
				break
			}
			return nil, ErrUnknownError
		}
		data := C.GoBytes(arena, C.int(size))
		C.free(arena)
		harvest(data, buckets, &order)
	}
	out := make([]AddressEntry, 0, len(order))
	for _, key := range order {
		b := buckets[key]
		out = append(out, AddressEntry{Addr: b.addr, Name: b.bestName(), Count: b.total})
	}
	return out, nil
}

type addrBucket struct {
	addr  string
	names map[string]uint
	total uint
}

func (b *addrBucket) bestName() string {
	best, bestN := "", uint(0)
	for n, c := range b.names {
		if c > bestN || (c == bestN && n != "" && (best == "" || n < best)) {
			best, bestN = n, c
		}
	}
	return best
}

func harvest(data []byte, buckets map[string]*addrBucket, order *[]string) {
	if len(data) < 4 {
		return
	}
	p := 4
	for i, n := 0, int(binary.LittleEndian.Uint32(data)); i < n; i++ {
		nh := int(binary.LittleEndian.Uint32(data[p:]))
		p += 4
		for j := 0; j < nh; j++ {
			l := int(binary.LittleEndian.Uint32(data[p:]))
			p += 4
			hdr := string(data[p : p+l])
			p += l
			for _, mb := range parseMailboxes(hdr) {
				key := asciiLower(mb[1])
				b := buckets[key]
				if b == nil {
					b = &addrBucket{addr: mb[1], names: make(map[string]uint)}
					buckets[key] = b
					*order = append(*order, key)
				}
				b.names[mb[0]]++
				b.total++
			}
		}
	}
}

// parseMailboxes parses an RFC 5322 address-list header value into
// (name, addr) pairs: display names (plain, quoted, commented),
// angle-addresses, bare addresses, groups, and RFC 2047 encoded words
// in names. Entries without an address are skipped - they are useless
// to a completion cache. Malformed input degrades to fewer entries,
// never an error.
func parseMailboxes(v string) [][2]string {
	var out [][2]string
	v = stripComments(v)
	for _, part := range splitList(v) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if ci := topColon(part); ci >= 0 {
			for _, m := range splitList(part[ci+1:]) {
				if mb, ok := mailbox(m); ok {
					out = append(out, mb)
				}
			}
			continue
		}
		if mb, ok := mailbox(part); ok {
			out = append(out, mb)
		}
	}
	return out
}

// stripComments removes (nested) comments, respecting quoted strings.
func stripComments(s string) string {
	var b strings.Builder
	inQuote, depth := false, 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inQuote {
			b.WriteByte(c)
			if c == '\\' && i+1 < len(s) {
				i++
				b.WriteByte(s[i])
			}
			if c == '"' {
				inQuote = false
			}
			continue
		}
		switch c {
		case '"':
			inQuote = true
			b.WriteByte(c)
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				b.WriteByte(c)
			}
		}
	}
	return b.String()
}

// splitList splits on top-level commas and semicolons, respecting
// quoted strings and angle brackets.
func splitList(s string) []string {
	var parts []string
	start, inQuote, inAngle := 0, false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inQuote {
			if c == '\\' {
				i++
			} else if c == '"' {
				inQuote = false
			}
			continue
		}
		switch c {
		case '"':
			inQuote = true
		case '<':
			inAngle = true
		case '>':
			inAngle = false
		case ',', ';':
			if !inAngle {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, s[start:])
	return parts
}

// topColon returns the index of a top-level ':' (group syntax), or -1
// when the part contains an angle-address (a display name with a
// colon before it is a group per RFC 5322).
func topColon(s string) int {
	inQuote := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inQuote {
			if c == '\\' {
				i++
			} else if c == '"' {
				inQuote = false
			}
			continue
		}
		switch c {
		case '"':
			inQuote = true
		case '<':
			return -1
		case ':':
			return i
		}
	}
	return -1
}

// mailbox parses one mailbox: "Name <addr>", "<addr>", or bare
// "addr@example.com". The address must look like one (contain @, no
// quotes, no control or angle junk) or the whole mailbox is dropped -
// malformed headers must never surface a garbage address into a
// completion cache.
func mailbox(part string) ([2]string, bool) {
	inQuote := false
	lt := -1
	for i := 0; i < len(part); i++ {
		c := part[i]
		if inQuote {
			if c == '\\' {
				i++
			} else if c == '"' {
				inQuote = false
			}
			continue
		}
		switch c {
		case '"':
			inQuote = true
		case '<':
			lt = i
			i = len(part)
		}
	}
	if lt >= 0 {
		// the first '>' closes the addr; anything after is junk
		rest := part[lt+1:]
		gt := strings.IndexByte(rest, '>')
		if gt < 0 {
			return [2]string{}, false
		}
		addr := strings.TrimSpace(rest[:gt])
		if validAddr(addr) {
			return [2]string{decodeName(part[:lt]), addr}, true
		}
		return [2]string{}, false
	}
	addr := strings.TrimSpace(part)
	if validAddr(addr) {
		return [2]string{"", addr}, true
	}
	return [2]string{}, false
}

func validAddr(addr string) bool {
	return strings.Contains(addr, "@") && !strings.ContainsAny(addr, "\"'\r\n<>\\")
}

func decodeName(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		var b strings.Builder
		for i := 1; i < len(s)-1; i++ {
			if s[i] == '\\' && i+1 < len(s)-1 {
				i++
			}
			b.WriteByte(s[i])
		}
		s = b.String()
	}
	return decode2047(s)
}

// decode2047 decodes RFC 2047 encoded words in a display name; all
// other text passes through untouched.
func decode2047(s string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, "=?")
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		rest := s[i+2:]
		j := strings.Index(rest, "?=")
		if j < 0 {
			b.WriteString(s[i:])
			return b.String()
		}
		parts := strings.Split(rest[:j], "?")
		if len(parts) != 3 {
			b.WriteString(s[i : i+2+j+2])
			s = rest[j+2:]
			continue
		}
		switch strings.ToUpper(parts[1]) {
		case "B":
			dec, err := base64.StdEncoding.DecodeString(parts[2])
			if err == nil {
				b.Write(dec)
			} else {
				b.WriteString(s[i : i+2+j+2])
			}
		case "Q":
			b.Write(decodeQ(parts[2]))
		default:
			b.WriteString(s[i : i+2+j+2])
		}
		s = rest[j+2:]
	}
}

// decodeQ decodes the RFC 2047 Q encoding: '_' is space, "=XX" is a
// hex byte.
func decodeQ(s string) []byte {
	var out []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '_' {
			out = append(out, ' ')
			continue
		}
		if c == '=' && i+2 < len(s) {
			if h, err := hex.DecodeString(s[i+1 : i+3]); err == nil {
				out = append(out, h[0])
				i += 2
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

// asciiLower mirrors notmuch's strcase hash: ASCII-only
// case-folding, like the CLI's dedup key.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

func bool2int(b bool) int {
	if b {
		return 1
	}
	return 0
}

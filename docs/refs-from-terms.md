---
layout: default
title: Refs from the term list (libnotmuch API spec)
nav_order: 10
---

# notmutt - walking the reference chain out of the term list

The full walk ships each message's reference chain as the id notmuch
parsed at index time (the replyto term plus the reference terms) - zero
file opens. The reads are gated behind a Go build tag: the
`refsfromterms` build (for the notmutt fork's libnotmuch) enables
them; a default build packs the refs slot empty and keeps the old
fast path, no matter which libnotmuch is installed. This document is
the analysis behind the option and the record of the fix.

## The measured problem

The full walk (src/notmuch/fullwalk_test.go, NOTMUTT_FULLWALK=1) takes
5.74s warm on the real DB (33,256 threads / 38,508 messages,
tag:inbox). The references/in-reply-to header reads are ~4s of it:
`notmuch_message_get_header` has value slots only for from/subject/
message-id, and only under NOTMUCH_FEATURE_FROM_SUBJECT_ID_VALUES
(lib/message.cc:542); references and in-reply-to ALWAYS fall through to
`_notmuch_message_file_get_header` - one file open and re-parse per
message, twice.

Measured floor: a probe pack without the refs+irt reads ran the walk
in 1.745s. The same probe kept from/subject/tags/paths reads, so those
already come from value slots on this DB (the FROM_SUBJECT_ID_VALUES
feature is active - no `notmuch reindex` needed here; a fresh notmuch
DB gets the feature automatically, an old one needs one `notmuch
reindex`).

## The data is already in the DB

The index already stores both values as terms (lib/add-message.cc:230-275):

- `replyto` term: the strict-parsed parent id (fallback chain: strict
  In-Reply-To -> last References id -> first In-Reply-To id)
- `reference` prefix terms: every chain id except the message's own
  (`parse_references` skips self, add-message.cc:27)

Both terms carry the BARE id (no "id:" prefix, no angle brackets):
`_notmuch_message_id_parse` and `_notmuch_message_id_parse_strict`
(lib/message-id.c:42,100) extract the text between `<` and `>`. The
walk's message-id slot is bare too (the pack strips "id:"), so
parent-edge comparisons (core/view.go parentOf) match directly.

Read side: `_notmuch_message_ensure_metadata` (message.cc:348)
decompresses the term list ONCE per message object - the walk's own
message-id read triggers it - and extracts thread_id, tags, message_id,
reference_list and in_reply_to in that single pass. The private
accessors `_notmuch_message_get_references` (message.cc:638) and
`_notmuch_message_get_in_reply_to` (message.cc:589) return them at zero
file cost. The tree's whole need (parent link + full chain, core/view.go
:956) is free after the pass the walk already pays for.

## Why the walk cannot use it with stock notmuch

The private accessors are not exported: lib/notmuch.sym is a
`notmuch_*` glob plus `local: *`, and `nm -D` on the system lib shows
zero `_notmuch_` symbols - a dynamic link against them fails.

The glob is also the unlock: ANY function named with the public
`notmuch_` prefix exports automatically. No version-script edit needed.

## The build option

The build tag is the single switch - the linked library never decides
for you:

- `make build` - stock behavior: the refs slot packs empty, the walk
  keeps the file-open-free fast path, the index tree renders
  structure-less threads as a flat forest (see "Stock builds" below).
  This is the behavior regardless of which libnotmuch is installed;
  the fork's header carries no gate.
- `make build REFSFROMTERMS=1` - adds the `refsfromterms` tag; the
  binding's C compiles with -DNOTMUCH_HAS_REF_GETTERS (the tag file
  refsfromterms.go in the vendored binding), the walk reads the chain
  from the term list via the fork's getters, threads render with
  their hierarchy. Requires the fork's libnotmuch where the build can
  link it (installed over the system lib, or via
  CGO_CFLAGS/CGO_LDFLAGS for a custom prefix).

Mismatches fail loud: the refsfromterms tag with a stock lib produces
undefined-reference link errors, never silent underuse.

## The fix, part 1: two public getters in libnotmuch (references/notmuch)

Declared in lib/notmuch.h, next to notmuch_message_get_thread_id
(line ~1686); the declarations exist so the refsfromterms build
compiles cleanly, the gate is the build tag's -D, not this header:

```c
/* Return the message ID from the In-Reply-To header of 'message', as
 * parsed at index time (the id notmuch itself chose as parent).
 * Returns an empty string if the message has no In-Reply-To header.
 * The returned string is owned by 'message' and valid for its
 * lifetime. */
const char *notmuch_message_get_in_reply_to (notmuch_message_t *message);

/* Return the message's References chain as a space-joined string of
 * message ids (the ids stored as reference terms at index time, the
 * chain minus the message's own id). Returns an empty string when
 * the chain is empty. The returned string is owned by 'message' and
 * valid for its lifetime. */
const char *notmuch_message_get_references (notmuch_message_t *message);
```

Implemented in lib/message.cc. get_in_reply_to is a one-line wrapper
over the private accessor, with the try/catch guard the other public
getters carry (ensure_metadata defaults in_reply_to to "", so the
return is never NULL in practice):

```c
const char *
notmuch_message_get_in_reply_to (notmuch_message_t *message)
{
    try {
	_notmuch_message_ensure_metadata (message, message->in_reply_to);
    } catch (Xapian::Error &error) {
	LOG_XAPIAN_EXCEPTION (message, error);
	return NULL;
    }
    if (! message->in_reply_to)
	return "";
    return message->in_reply_to;
}
```

get_references needs a cached joined string: the private accessor
returns a notmuch_string_list_t (a private type; a public iterator
type is a bigger surface than the consumer needs). New field
`char *reference_string;` in the message struct (next to
reference_list, lib/message.cc:29), built lazily into the message's
talloc context:

```c
const char *
notmuch_message_get_references (notmuch_message_t *message)
{
    try {
	_notmuch_message_ensure_metadata (message, message->reference_list);
    } catch (Xapian::Error &error) {
	LOG_XAPIAN_EXCEPTION (message, error);
	return NULL;
    }
    if (! message->reference_string) {
	char *joined = talloc_strdup (message, "");
	for (notmuch_string_node_t *it = message->reference_list->head;
	     it; it = it->next)
	    joined = talloc_asprintf_append (joined, "%s%s",
					     joined[0] ? " " : "", it->string);
	message->reference_string = joined;
    }
    return message->reference_string;
}
```

(join shape: space-separated ids, no angle brackets; empty list -> "")

Correctness requirement - invalidate with the fields:
`_notmuch_message_invalidate_metadata` (message.cc:471) frees cached
fields so re-indexed messages re-read; it has a replyto case but no
reference case today. Extend it with a reference case (free
reference_list and reference_string, mirror the replyto case), and
also NULL reference_string in the replyto case (the joined string
embeds the irt id as its last token).

Export: automatic via the `notmuch_*` glob in lib/notmuch.sym.

## The fix, part 2: the binding (vendored go.notmuch fork)

src/vendor/github.com/fishman/go.notmuch/summary.go, full_pack_msg
(line ~347): the refs slot packs through full_pack_refs, gated on the
flag:

```c
static int full_pack_refs(void **arena, size_t *cap, size_t *fill, notmuch_message_t *m) {
	const char *refs = "", *irt = "";
#ifdef NOTMUCH_HAS_REF_GETTERS
	refs = notmuch_message_get_references(m);
	irt = notmuch_message_get_in_reply_to(m);
#endif
	...
}
```

The pack still emits "refs irt" space-joined (one of them empty ->
the other alone), and refsSplit (src/notmuch/cgo.go:171) splits on
whitespace - the trim of angle brackets is a no-op on bracket-free
ids. Zero client-side changes.

## Behavior deltas (verified by the fullwalk probe)

1. Parent edge = notmuch's own choice. The replyto term is the id
   notmuch's threading used (strict In-Reply-To, else last References
   id, else first In-Reply-To id - add-message.cc:238-260); the raw
   header text yields a different last token for a multi-id or
   malformed In-Reply-To. Trees built from terms match notmuch's own
   thread linking exactly.
2. Chain minus self. The reference terms skip the message's own id
   (add-message.cc:27); a raw References header can repeat it. The
   backward parent search (core/view.go:1021) is unaffected - the own
   id could never be the parent edge.
3. No brackets, no "id:" prefix anywhere: refsSplit's Trim is a no-op
   and the ids compare bare against the walk's message-id slot.

## Expected result and verification

- Confirmed 2026-08-21 on the real DB: custom walk 1.81s vs stock
  1.74s (~4% for the chains; the term list is already decompressed by
  the walk's message-id read, the join is O(chain length)). The
  both-skipped probe measured 1.745s as the floor; the getter reads
  cost ~0.06s on 38.5k messages.
- Verify: NOTMUTT_FULLWALK=1 before/after on the same DB; the
  missing-thread cross-check must stay green.
- Version pinning: the fork's notmuch and the linked lib must both
  carry the getters before the refsfromterms build links - the flag
  makes the mismatch a link error, never silent underuse.

## Why not "build the tree with less and hydrate later"

The walk's two expensive reads were the refs/irt FILE OPENS, not the
data volume. The chain and parent are already in the DB at index time;
the "less info" path would drop free data and pay for it with a new
lazy-hydration path - the exact machinery class that failed twice (the
4.5-minute threadjob storm; the two threads lost to a stub the scan
cursor could never reach). Keeping the walk's row contract (headers +
paths + chain) also keeps the open-while-loading seam (rows-first,
async-mechanics.md section 2) intact.

## Stock builds: the fallback behavior

A stock build packs an empty refs slot (vendor summary.go
full_pack_refs, the #else path) and runs ~1.75s. Consumers degrade:

- The index tree renders structure-less threads as a flat forest:
  buildTree marks the synthetic root Forest when every message is a
  root (no chains shipped), and the flatten renders those rows at
  depth 0 without the [...] marker. Genuine multi-root threads (at
  least one attached child) keep the marker.
- Reply prefill builds one-hop References chains (the reply still
  threads to the original via In-Reply-To); the fetch fallback for
  pathless rows keeps the full chain.
- The per-thread fetch is unchanged and still carries the chain; the
  pager never needed it (RenderThread renders sequential blocks).

The empty slot is a performance decision, not a feature gap: the
term-list getters read data the walk's message-id pass already paid
for, so the refsfromterms build restores the trees at ~zero walk
cost.

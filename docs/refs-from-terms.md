---
layout: default
title: Refs from the term list (libnotmuch API spec)
nav_order: 10
---

# notmutt - walking the reference chain out of the term list

DEFERRED SPEC: the client must not depend on a custom-built libnotmuch
today. The runtime links the system libnotmuch; nothing here is
load-bearing until the user rebuilds their notmuch with the two new
getters. This documents the full fix for later.

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

Read side: `_notmuch_message_ensure_metadata` (message.cc:348)
decompresses the term list ONCE per message object - the walk's own
message-id read triggers it - and extracts thread_id, tags, message_id,
reference_list and in_reply_to in that single pass. The private
accessors `_notmuch_message_get_references` (message.cc:638) and
`_notmuch_message_get_in_reply_to` (message.cc:589) return them at zero
file cost. The tree's whole need (parent link + full chain, core/view.go
:956) is free after the pass the walk already pays for.

## Why the walk cannot use it today

The private accessors are not exported: lib/notmuch.sym is a
`notmuch_*` glob plus `local: *`, and `nm -D` on the system lib shows
zero `_notmuch_` symbols - a dynamic link against them fails.

The glob is also the unlock: ANY function named with the public
`notmuch_` prefix exports automatically. No version-script edit needed.

## The fix, part 1: two public getters in libnotmuch (references/notmuch)

Declare in lib/notmuch.h, next to notmuch_message_get_thread_id
(line ~1686), doc comments in the existing style:

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

Implement in lib/message.cc. get_in_reply_to is a one-line wrapper
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
    return message->in_reply_to;
}
```

get_references needs a cached joined string: the private accessor
returns a notmuch_string_list_t (a private type; a public iterator
type is a bigger surface than the consumer needs). New field
`char *reference_string;` in the message struct (next to in_reply_to,
notmuch-private.h:~45), built lazily into the message's talloc context:

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

## The fix, part 2: the binding (references/go.notmuch fork, tag, revendor)

src/vendor/github.com/fishman/go.notmuch/summary.go, full_pack_msg
(line ~347): the two file-opening reads

```c
const char *refs = notmuch_message_get_header(m, "references");
const char *irt = notmuch_message_get_header(m, "in-reply-to");
```

become the two term-list reads

```c
const char *refs = notmuch_message_get_references(m);
const char *irt = notmuch_message_get_in_reply_to(m);
```

Everything downstream is untouched: the pack still emits "refs irt"
space-joined, and refsSplit (src/notmuch/cgo.go:126) trims angle
brackets (a no-op on bracket-free ids) and splits on whitespace. Zero
client-side changes.

## Behavior deltas (verify in the probe cross-check)

1. Parent edge = notmuch's own choice. The replyto term is the id
   notmuch's threading used (strict In-Reply-To, else last References
   id, else first In-Reply-To id - add-message.cc:238-260); the
   current walk emits the raw header text, so a multi-id or malformed
   In-Reply-To yields a different last token. Trees built from terms
   match notmuch's own thread linking exactly. The probe cross-check
   (fullwalk_test.go:98-127) compares id sets only - add a parent-edge
   comparison for the two probe threads.
2. Chain minus self. The reference terms skip the message's own id
   (add-message.cc:27); a raw References header can repeat it. The
   backward parent search (core/view.go:956) is unaffected - the own
   id could never be the parent edge.
3. No brackets anywhere: refsSplit's Trim is a no-op; nothing else
   expects brackets.

## Expected result and verification

- Target: warm walk 5.74s -> ~1.8-2.0s. The both-skipped probe measured
  1.745s as the floor of what the refs reads cost; the new reads cost
  ~0 (the term list is already decompressed by the walk's message-id
  read; the join is O(chain length)).
- Verify: NOTMUTT_FULLWALK=1 before/after on the same DB; NOTMUTT_REALPROBE=1
  for the app-level fullReload number; the missing-thread cross-check
  must stay green.
- Version pinning: the fork's notmuch pin and the user's system lib
  must both carry the getters before the revendored binding links.

## Why not "build the tree with less and hydrate later"

The walk's two expensive reads were the refs/irt FILE OPENS, not the
data volume. The chain and parent are already in the DB at index time;
the "less info" path would drop free data and pay for it with a new
lazy-hydration path - the exact machinery class that failed twice (the
4.5-minute threadjob storm; the two threads lost to a stub the scan
cursor could never reach). Keeping the walk's row contract (headers +
paths + chain) also keeps the open-while-loading seam (rows-first,
async-mechanics.md section 2) intact.

Stopgap if the libnotmuch change never lands: drop refs from the pack
(walk 1.745s) and let the pager tree fall back to flat date order,
fetching the chain on open - a visible tree-fidelity regression,
documented here as the fallback, not the plan.

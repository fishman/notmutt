#!/bin/sh
# poll-repro: manual filter-pipeline reproducibility harness.
#
# The poll pipeline is `notmuch new` - the backend's wrapper captures
# the (pre, cur] lastmod bracket around the run - then classify the
# delta. The revision moves with every new run, so the diff is built
# ONCE and stored; reruns replay the SAME window and must reproduce
# the stored output byte-identical.
#
#   poll-repro.sh [FROM TO]     build the diff (dry-run poll) into a text
#                               file; with FROM TO, replay that manual
#                               window instead of a fresh capture
#   poll-repro.sh check [FROM TO]
#                               rerun the window (manual or stored),
#                               compare against the file
#   poll-repro.sh apply [FROM TO]
#                               apply the window (manual or stored)
#   poll-repro.sh rev           print the current lastmod revision
#   poll-repro.sh inside R      is revision R inside the stored window?
#   poll-repro.sh lastmod Q     binary-search the last-write revision of
#                               the messages matching Q (e.g. subject:"test")
#
# NOTMUTT overrides the binary (default: ./notmutt, else notmutt on
# PATH). State lives in $XDG_CACHE_HOME/notmutt/poll-repro/.
set -eu

notmutt=${NOTMUTT:-}
if [ -z "$notmutt" ] && [ -x ./notmutt ]; then notmutt=./notmutt; fi
notmutt=${notmutt:-notmutt}
state=${XDG_CACHE_HOME:-$HOME/.cache}/notmutt/poll-repro
diff_file=$state/diff.txt

usage() { sed -n '2,23p' "$0"; exit 1; }

# range prints the lastmod window of a stored diff summary; empty when
# the diff run saw no new mail (the summary then carries no window).
range() { sed -n 's/^poll: .*: \([0-9][0-9]*\)\.\.\([0-9][0-9]*\):.*/\1 \2/p' "$1"; }

cmd=${1:-diff}
case "$cmd" in
diff)
	# with FROM TO, build the diff for that manual window (a windowed
	# dry-run - nothing is written, the revision must not move);
	# without, a fresh poll captures its own window.
	mkdir -p "$state"
	if [ $# -ge 3 ]; then
		"$notmutt" poll --from "$2" --to "$3" > "$diff_file"
	else
		"$notmutt" poll > "$diff_file"
	fi
	echo "diff saved to $diff_file ($(wc -l < "$diff_file") lines)"
	;;
check)
	[ -f "$diff_file" ] || { echo "no stored diff: run poll-repro.sh first" >&2; exit 1; }
	if [ $# -ge 3 ]; then
		from=$2; to=$3
	else
		set -- $(range "$diff_file")
		from=${1:-}; to=${2:-}
		[ -n "$from" ] || { echo "stored diff has no window (no new mail) - nothing to reproduce" >&2; exit 0; }
	fi
	"$notmutt" poll --from "$from" --to "$to" > "$state/check.txt"
	if cmp -s "$diff_file" "$state/check.txt"; then
		echo "reproducible: the rerun matches the stored diff ($from..$to)"
	else
		echo "MISMATCH: the rerun differs from the stored diff ($from..$to)" >&2
		diff -u "$diff_file" "$state/check.txt" || true
		exit 1
	fi
	;;
apply)
	if [ $# -ge 3 ]; then
		from=$2; to=$3
	else
		[ -f "$diff_file" ] || { echo "no stored diff: run poll-repro.sh first" >&2; exit 1; }
		set -- $(range "$diff_file")
		from=${1:-}; to=${2:-}
		[ -n "$from" ] || { echo "stored diff has no window (no new mail) - nothing to apply" >&2; exit 0; }
	fi
	"$notmutt" poll --from "$from" --to "$to" --apply
	;;
rev)
	# the current lastmod revision (`notmuch count --lastmod ""`
	# prints "count uuid revision"). Take one manually, deliver or
	# move mail, run `notmuch new` yourself, take it again - the
	# manual bracket.
	out=$(notmuch count --lastmod "")
	set -- $out
	[ $# -eq 3 ] || { echo "unexpected --lastmod output: $out" >&2; exit 1; }
	echo "revision: $3 (uuid $2, count $1)"
	;;
inside)
	# compare a manual revision against the stored diff's window: a
	# message whose lastmod falls in (from, to] was part of that
	# diff. A message's lastmod is the revision of its last write
	# (indexed, retagged, or moved) - not its send date.
	rev=${2:?usage: poll-repro.sh inside REV}
	set -- $(range "$diff_file")
	[ -n "${1:-}" ] || { echo "stored diff has no window - nothing to compare" >&2; exit 1; }
	if [ "$rev" -gt "$1" ] && [ "$rev" -le "$2" ]; then
		echo "$rev is inside the stored window ($1..$2): it was part of that diff"
	else
		echo "$rev is outside the stored window ($1..$2): it was not part of that diff"
	fi
	;;
lastmod)
	# per-message lastmod by binary search: the smallest revision
	# whose window `lastmod:0..R` still matches is the revision the
	# message was last written at - where `notmuch new` indexed it
	# (a later retag or move shows a newer revision).
	q=${2:?usage: poll-repro.sh lastmod QUERY}
	ids=$(notmuch search --output=messages "$q")
	[ -n "$ids" ] || { echo "no messages match: $q" >&2; exit 1; }
	cur=$(notmuch count --lastmod "" | awk '{print $3}')
	for id in $ids; do
		lo=0
		hi=$cur
		while [ "$lo" -lt "$hi" ]; do
			mid=$(( (lo + hi) / 2 ))
			if [ "$(notmuch count "lastmod:0..$mid and $id")" -gt 0 ]; then
				hi=$mid
			else
				lo=$((mid + 1))
			fi
		done
		echo "$id: lastmod $lo (current $cur)"
	done
	;;
*)
	usage
	;;
esac

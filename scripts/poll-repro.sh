#!/bin/sh
# poll-repro: manual filter-pipeline reproducibility harness.
#
# The poll pipeline is revision snapshot -> notmuch new -> revision ->
# classify the (pre, cur] lastmod delta. The revision moves with every
# new run, so the diff is built ONCE and stored; reruns replay the SAME
# window and must reproduce the stored output byte-identical.
#
#   poll-repro.sh           build the diff (dry-run poll) into a text file
#   poll-repro.sh check     rerun the stored window, compare against the file
#   poll-repro.sh apply     apply the stored window for real (--apply)
#
# NOTMUTT overrides the binary (default: ./notmutt, else notmutt on
# PATH). State lives in $XDG_CACHE_HOME/notmutt/poll-repro/.
set -eu

notmutt=${NOTMUTT:-}
if [ -z "$notmutt" ] && [ -x ./notmutt ]; then notmutt=./notmutt; fi
notmutt=${notmutt:-notmutt}
state=${XDG_CACHE_HOME:-$HOME/.cache}/notmutt/poll-repro
diff_file=$state/diff.txt

usage() { sed -n '2,11p' "$0"; exit 1; }

# range prints the lastmod window of a stored diff summary; empty when
# the diff run saw no new mail (the summary then carries no window).
range() { sed -n 's/^poll: .*: \([0-9][0-9]*\)\.\.\([0-9][0-9]*\):.*/\1 \2/p' "$1"; }

cmd=${1:-diff}
case "$cmd" in
diff)
	mkdir -p "$state"
	"$notmutt" poll > "$diff_file"
	echo "diff saved to $diff_file ($(wc -l < "$diff_file") lines)"
	;;
check)
	[ -f "$diff_file" ] || { echo "no stored diff: run poll-repro.sh first" >&2; exit 1; }
	set -- $(range "$diff_file")
	[ -n "${1:-}" ] || { echo "stored diff has no window (no new mail) - nothing to reproduce" >&2; exit 0; }
	"$notmutt" poll --from "$1" --to "$2" > "$state/check.txt"
	if cmp -s "$diff_file" "$state/check.txt"; then
		echo "reproducible: the rerun matches the stored diff ($1..$2)"
	else
		echo "MISMATCH: the rerun differs from the stored diff ($1..$2)" >&2
		diff -u "$diff_file" "$state/check.txt" || true
		exit 1
	fi
	;;
apply)
	[ -f "$diff_file" ] || { echo "no stored diff: run poll-repro.sh first" >&2; exit 1; }
	set -- $(range "$diff_file")
	[ -n "${1:-}" ] || { echo "stored diff has no window (no new mail) - nothing to apply" >&2; exit 0; }
	"$notmutt" poll --from "$1" --to "$2" --apply
	;;
*)
	usage
	;;
esac

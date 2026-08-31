#!/bin/sh
# changelog: render the Conventional Commit history since the last tag as
# a grouped markdown changelog - features and fixes land in their own
# sections, not the raw git order.
#
#   changelog.sh [FROM] [TO]
#
# FROM defaults to the previous tag (`git describe --tags --abbrev=0`),
# TO to HEAD. Writes markdown to stdout: a title, the compare range, then
# one section per commit type in canonical order (Features, Bug Fixes,
# Performance, Documentation, Refactoring, Testing, Build, CI, Chores,
# Other). The CI release job uses it as the release body; it also works
# for a hand-maintained CHANGELOG.md section.
set -eu

from=${1:-}
to=${2:-HEAD}
if [ -z "$from" ]; then
	from=$(git describe --tags --abbrev=0 2>/dev/null || true)
fi
[ -n "$from" ] || { echo "changelog: no previous tag to compare against" >&2; exit 1; }

date=$(git log -1 --format=%ad --date=short "$to")

{
	echo "## $to ($date)"
	echo
	echo "Changes since $from:"
	echo
	git log --format='%h %s' "$from..$to" | awk '
BEGIN {
	labels["feat"] = "Features"
	labels["fix"] = "Bug Fixes"
	labels["perf"] = "Performance"
	labels["docs"] = "Documentation"
	labels["refactor"] = "Refactoring"
	labels["test"] = "Testing"
	labels["build"] = "Build"
	labels["ci"] = "CI"
	labels["chore"] = "Chores"
	labels["other"] = "Other"
}
{
	if (NF < 2) next
	rest = $0
	sub(/^[0-9a-f]+ /, "", rest)
	hash = substr($0, 1, index($0, " ") - 1)
	# skip merge commits (and hashless noise); the subject, not the hash,
	# is what starts with "Merge"
	if (rest ~ /^Merge/) next
	type = "other"
	# conventional shape: type, optional scope, optional !, then ": "
	if (match(rest, /^[a-z]+(\([^)]*\))?!?: /)) {
		len = RLENGTH
		type = substr(rest, 1, index(rest, ":") - 1)
		sub(/\([^)]*\)$/, "", type)
		sub(/!$/, "", type)
		rest = substr(rest, len + 1)
	}
	entry = "* " rest " (" hash ")"
	if (!seen[type]) {
		sections[type] = entry
		seen[type] = 1
	} else {
		sections[type] = sections[type] "\n" entry
	}
}
END {
	n = split("feat fix perf docs refactor test build ci chore other", order, " ")
	for (i = 1; i <= n; i++) {
		t = order[i]
		if (sections[t] == "") continue
		printf "### %s\n%s\n\n", labels[t], sections[t]
	}
}
'
} | sed '/^$/N;/^\n$/D'

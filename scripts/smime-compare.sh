#!/bin/sh
# smime-compare: cross-check the in-process S/MIME verifier against
# openssl on the same .eml (R10).
#
#   smime-compare.sh FILE [CA-FILE]
#
# openssl smime -verify reads the message itself; notmutt smime-verify
# extracts the CMS with the mail parser and verifies in-process. Both
# verdicts print side by side; the exit codes align on valid/invalid
# (0 = valid, 1 = invalid). One asymmetry by design: openssl bare
# checks the signature only, notmutt checks the signature AND the chain
# to its roots - an untrusted signer may verify under openssl and fail
# under notmutt.
#
# NOTMUTT overrides the binary (default: ./notmutt, else notmutt on PATH).
set -eu

notmutt=${NOTMUTT:-}
if [ -z "$notmutt" ] && [ -x ./notmutt ]; then notmutt=./notmutt; fi
notmutt=${notmutt:-notmutt}

file=${1:?usage: smime-compare.sh FILE [CA-FILE]}
ca=${2:-}

out=$(mktemp); err=$(mktemp); trap 'rm -f "$out" "$err"' EXIT

ossl_rc=0
set +e
if [ -n "$ca" ]; then
	openssl smime -verify -in "$file" -CAfile "$ca" -out /dev/null >"$out" 2>"$err"
else
	openssl smime -verify -in "$file" -out /dev/null >"$out" 2>"$err"
fi
ossl_rc=$?
set -e
echo "openssl: exit=$ossl_rc"
[ -s "$out" ] && sed 's/^/  /' "$out"
[ -s "$err" ] && sed 's/^/  /' "$err"

go_rc=0
set +e
if [ -n "$ca" ]; then
	"$notmutt" smime-verify "$file" "$ca" >"$out" 2>"$err"
else
	"$notmutt" smime-verify "$file" >"$out" 2>"$err"
fi
go_rc=$?
set -e
echo "notmutt: exit=$go_rc"
[ -s "$out" ] && sed 's/^/  /' "$out"
[ -s "$err" ] && sed 's/^/  /' "$err"

# agreement on the validity bit only - "not signed" and openssl's
# "not an S/MIME message" both mean no valid signature (a divergence of
# reason, not of verdict)
ossl_valid=no; [ "$ossl_rc" -eq 0 ] && ossl_valid=yes
go_valid=no; grep -q '^valid:' "$out" 2>/dev/null && go_valid=yes
if [ "$ossl_valid" = "$go_valid" ]; then
	echo "verdicts: AGREE (valid=$go_valid)"
else
	echo "verdicts: DIFFER (openssl valid=$ossl_valid, notmutt valid=$go_valid)"
fi

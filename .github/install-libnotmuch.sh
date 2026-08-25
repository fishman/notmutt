#!/usr/bin/env bash
set -euo pipefail
# CI-only: the vendored go.notmuch fork needs libnotmuch >= 0.40
# (notmuch_threads_status); ubuntu ships 0.37/0.38. Build 0.40 from
# the notmuch repo's GNU-make recipe into a user-owned prefix that
# actions/cache persists. `--static` builds a static-only prefix (just
# libnotmuch.a) for the release binary: the package then needs no
# distro libnotmuch; the shared libs libnotmuch.a depends on (gmime,
# glib, talloc, xapian, z) stay system-provided.
STATIC=0
[ "${1:-}" = "--static" ] && STATIC=1
PREFIX="$HOME/notmuch-static"
[ "$STATIC" = 1 ] || PREFIX="$HOME/notmuch-install"
echo "$PREFIX/bin" >> "$GITHUB_PATH"
echo "CGO_CFLAGS=-I$PREFIX/include" >> "$GITHUB_ENV"
if [ "$STATIC" = 1 ]; then
    # -l: forces the archive even if a libnotmuch.so sits on the search
    # path; the -l* after it are the archive's own (shared) dependencies.
    # -lstdc++ because the archive's .cc members are C++.
    echo "CGO_LDFLAGS=-L$PREFIX/lib -l:libnotmuch.a -lgmime-3.0 -lgobject-2.0 -lglib-2.0 -ltalloc -lxapian -lz -lstdc++" >> "$GITHUB_ENV"
else
    echo "CGO_LDFLAGS=-L$PREFIX/lib" >> "$GITHUB_ENV"
fi
echo "LD_LIBRARY_PATH=$PREFIX/lib" >> "$GITHUB_ENV"
if [ -f "$PREFIX/lib/libnotmuch.a" ]; then
    echo "libnotmuch 0.40 cached at $PREFIX"
    exit 0
fi
sudo apt-get update
sudo apt-get install -y build-essential pkgconf python3
git clone --depth 1 --branch 0.40 https://github.com/notmuch/notmuch /tmp/notmuch
cd /tmp/notmuch
./configure --prefix="$PREFIX"
make -j "$(nproc)"
make install
if [ "$STATIC" = 1 ]; then
    rm -f "$PREFIX"/lib/libnotmuch.so*
fi

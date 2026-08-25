#!/usr/bin/env bash
set -euo pipefail
# CI-only: the vendored go.notmuch fork needs libnotmuch >= 0.40
# (notmuch_threads_status); ubuntu ships 0.37/0.38. Build 0.40 from
# the notmuch repo's GNU-make recipe into a user-owned prefix that
# actions/cache persists. `--static` builds a static-only prefix (the
# notmuch archives) for the release binary: the package then needs no
# distro libnotmuch; the shared libs libnotmuch.a depends on (gmime,
# glib, talloc, xapian, z) stay system-provided.
STATIC=0
[ "${1:-}" = "--static" ] && STATIC=1
PREFIX="$HOME/notmuch-static"
[ "$STATIC" = 1 ] || PREFIX="$HOME/notmuch-install"
echo "$PREFIX/bin" >> "$GITHUB_PATH"
echo "CGO_CFLAGS=-I$PREFIX/include" >> "$GITHUB_ENV"
if [ "$STATIC" = 1 ]; then
    # Same three archives the shared lib is built from (libnotmuch.so
    # links lib/ + util/ + parse-time-string objects); -L just satisfies
    # the binding's own -lnotmuch. -lgio-2.0 comes from notmuch's own
    # link flags; -lstdc++ because the archives' .cc members are C++.
    echo "CGO_LDFLAGS=-L$PREFIX/lib -Wl,--start-group $PREFIX/lib/libnotmuch.a $PREFIX/lib/libnotmuch_util.a $PREFIX/lib/libparse-time-string.a -Wl,--end-group -lgmime-3.0 -lgio-2.0 -lgobject-2.0 -lglib-2.0 -ltalloc -lz -lxapian -lstdc++" >> "$GITHUB_ENV"
else
    echo "CGO_LDFLAGS=-L$PREFIX/lib" >> "$GITHUB_ENV"
fi
echo "LD_LIBRARY_PATH=$PREFIX/lib" >> "$GITHUB_ENV"
if [ -f "$PREFIX/lib/libnotmuch.a" ] && [ -f "$PREFIX/lib/libnotmuch_util.a" ] && [ -f "$PREFIX/lib/libparse-time-string.a" ]; then
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
    # make install ships only the .so + headers, not the archives
    cp lib/libnotmuch.a util/libnotmuch_util.a parse-time-string/libparse-time-string.a "$PREFIX/lib/"
    rm -f "$PREFIX"/lib/libnotmuch.so*
fi

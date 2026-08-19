#!/usr/bin/env bash
set -euo pipefail
# CI-only: the vendored go.notmuch fork needs libnotmuch >= 0.40
# (notmuch_threads_status); ubuntu images ship 0.37/0.38. Build the
# same 0.40 the dev machine runs, from the notmuch repo's own
# GNU-make recipe (the debian/control Build-Depends list), into a
# user-owned prefix that actions/cache persists between runs.
PREFIX="$HOME/notmuch-install"
echo "$PREFIX/bin" >> "$GITHUB_PATH"
echo "CGO_CFLAGS=-I$PREFIX/include" >> "$GITHUB_ENV"
echo "CGO_LDFLAGS=-L$PREFIX/lib" >> "$GITHUB_ENV"
echo "LD_LIBRARY_PATH=$PREFIX/lib" >> "$GITHUB_ENV"
if [ -f "$PREFIX/include/notmuch.h" ]; then
    echo "libnotmuch 0.40 cached at $PREFIX"
    exit 0
fi
sudo apt-get update
sudo apt-get install -y build-essential pkgconf python3 \
  libxapian-dev libtalloc-dev libgmime-3.0-dev libz-dev
git clone --depth 1 --branch 0.40 https://github.com/notmuch/notmuch /tmp/notmuch
cd /tmp/notmuch
./configure --prefix="$PREFIX"
make -j "$(nproc)"
make install

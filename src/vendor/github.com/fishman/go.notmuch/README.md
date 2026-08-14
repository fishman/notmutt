[![Build Status][ci-img]][ci]

Idiomatic Go bindings for [notmuch mail][notmuch], the fast email
search and tagging engine, via cgo.

- Current C API: `notmuch_database_open_with_config`, `index_file`,
  config key/value pairs, database reopen
- GC-managed object lifetimes: call `Close` on the database, the
  garbage collector handles the rest, so query results, messages,
  and tags can never outlive their parents
- Full coverage of tags, search, threads, message properties,
  maildir flag sync, and atomic transactions
- Go-idiomatic errors (`ErrNotFound`, `ErrReadOnlyDB`, ...), not raw
  status codes

Licensed under the GPLv3 or later (like notmuch itself).

# Development

## Running tests
The project uses `make` to setup and download additional assets for the tests.

Run `make test` to run the tests.

## Pre PR checks
Next to the tests, you should also run gofmt on the sourcecode.
Running `make fmtcheck` checks for formatting issues.

To run both tests and format checks, use `make ci`.

[notmuch]: http://notmuchmail.org/
[ci-img]: https://gitlab.com/isd/go-notmuch/badges/master/build.svg
[ci]: https://gitlab.com/isd/go-notmuch/pipelines

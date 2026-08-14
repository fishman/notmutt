package notmuch

import (
	"context"
	"errors"

	"notmutt/core"
)

var ErrLockTimeout = errors.New("notmuch lock timeout")

// Message is the core type; the alias keeps the Backend interface text short.
type Message = core.Message

// TagOp is the core type; the alias keeps the Backend interface text short.
type TagOp = core.TagOp

// Backend is the notmuch access boundary. M1 ships the CLI backend; the
// cgo backend implements the same interface for the benchmark (task 13).
type Backend interface {
	Open(ctx context.Context, dbPath string) error
	Close(ctx context.Context) error
	Query(ctx context.Context, query string, limit, offset int) ([]Message, error)
	Count(ctx context.Context, query string) (int, error)
	Thread(ctx context.Context, threadID string) ([]Message, error)
	Tag(ctx context.Context, query string, ops []TagOp) error
	Revision(ctx context.Context) (uuid string, rev uint64, err error)
	New(ctx context.Context) error
}

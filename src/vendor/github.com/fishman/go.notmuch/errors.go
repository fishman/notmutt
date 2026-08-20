package notmuch

// Copyright © 2015 The go.notmuch Authors. Authors can be found in the AUTHORS file.
// Licensed under the GPLv3 or later.
// See COPYING at the root of the repository for details.

// #cgo LDFLAGS: -lnotmuch
// #include <stdlib.h>
// #include <notmuch.h>
import "C"
import "errors"

type status C.notmuch_status_t

var (
	// ErrOutOfMemory is returned when an Out of memory occured.
	ErrOutOfMemory = statusErr(C.NOTMUCH_STATUS_OUT_OF_MEMORY)

	// ErrReadOnlyDB is returned when an attempt was made to write to a
	// database opened in read-only mode.
	ErrReadOnlyDB = statusErr(C.NOTMUCH_STATUS_READ_ONLY_DATABASE)

	// ErrXapianException is returned when a xapian exception occured.
	ErrXapianException = statusErr(C.NOTMUCH_STATUS_XAPIAN_EXCEPTION)

	// ErrFileError is returned when an error occurred trying to read or write to
	// a file (this could be file not found, permission denied, etc.)
	ErrFileError = statusErr(C.NOTMUCH_STATUS_FILE_ERROR)

	// ErrFileNotEmail is returned when a file was presented that doesn't appear
	// to be an email message.
	ErrFileNotEmail = statusErr(C.NOTMUCH_STATUS_FILE_NOT_EMAIL)

	// ErrDuplicateMessageID is returned when a file contains a message ID that
	// is identical to a message already in the database.
	ErrDuplicateMessageID = statusErr(C.NOTMUCH_STATUS_DUPLICATE_MESSAGE_ID)

	// ErrNullPointer is returned when the user erroneously passed a NULL pointer
	// to a notmuch function.
	ErrNullPointer = statusErr(C.NOTMUCH_STATUS_NULL_POINTER)

	// ErrTagTooLong is returned when a tag value is too long (exceeds TagMax)
	ErrTagTooLong = statusErr(C.NOTMUCH_STATUS_TAG_TOO_LONG)

	// ErrUnbalancedFreezeThaw is returned when Message.Thaw() was called more
	// times than Message.Freeze().
	ErrUnbalancedFreezeThaw = statusErr(C.NOTMUCH_STATUS_UNBALANCED_FREEZE_THAW)

	// ErrUnbalancedAtomic DB.EndAtomic() has been called more times than DB.BeginAtomic()
	ErrUnbalancedAtomic = statusErr(C.NOTMUCH_STATUS_UNBALANCED_ATOMIC)

	// ErrUnsupportedOperation is returned when the operation is not supported.
	ErrUnsupportedOperation = statusErr(C.NOTMUCH_STATUS_UNSUPPORTED_OPERATION)

	// ErrUpgradeRequired is returned when the database requires an upgrade.
	ErrUpgradeRequired = statusErr(C.NOTMUCH_STATUS_UPGRADE_REQUIRED)

	// ErrIgnored is returned if the operation was ignored
	ErrIgnored = statusErr(C.NOTMUCH_STATUS_IGNORED)

	// ErrPathError is returned when there is a problem with the proposed path,
	// e.g. a relative path passed to a function expecting an absolute path.
	ErrPathError = statusErr(C.NOTMUCH_STATUS_PATH_ERROR)

	// ErrIllegalArgument is returned when the argument is illegal.
	ErrIllegalArgument = statusErr(C.NOTMUCH_STATUS_ILLEGAL_ARGUMENT)

	// ErrMalformedCryptoProtocol is returned when the crypto protocol is malformed.
	ErrMalformedCryptoProtocol = statusErr(C.NOTMUCH_STATUS_MALFORMED_CRYPTO_PROTOCOL)

	// ErrFailedCryptoContextCreation is returned when a crypto context could not be created.
	ErrFailedCryptoContextCreation = statusErr(C.NOTMUCH_STATUS_FAILED_CRYPTO_CONTEXT_CREATION)

	// ErrUnknownCryptoProtocol is returned when the crypto protocol is unknown.
	ErrUnknownCryptoProtocol = statusErr(C.NOTMUCH_STATUS_UNKNOWN_CRYPTO_PROTOCOL)

	// ErrNoConfig is returned when no config file was found.
	ErrNoConfig = statusErr(C.NOTMUCH_STATUS_NO_CONFIG)

	// ErrNoDatabase is returned when there is no database information in the config.
	ErrNoDatabase = statusErr(C.NOTMUCH_STATUS_NO_DATABASE)

	// ErrDatabaseExists is returned when the database already exists and was not created.
	ErrDatabaseExists = statusErr(C.NOTMUCH_STATUS_DATABASE_EXISTS)

	// ErrBadQuerySyntax is returned when the query syntax is invalid.
	ErrBadQuerySyntax = statusErr(C.NOTMUCH_STATUS_BAD_QUERY_SYNTAX)

	// ErrNoMailRoot is returned when no mail root is configured.
	ErrNoMailRoot = statusErr(C.NOTMUCH_STATUS_NO_MAIL_ROOT)

	// ErrClosedDatabase is returned when an operation is attempted on a closed database.
	ErrClosedDatabase = statusErr(C.NOTMUCH_STATUS_CLOSED_DATABASE)

	// ErrIteratorExhausted is returned when an iterator is exhausted.
	ErrIteratorExhausted = statusErr(C.NOTMUCH_STATUS_ITERATOR_EXHAUSTED)

	// ErrOperationInvalidated is returned when an operation was invalidated, e.g.
	// by closing the database while iterating.
	ErrOperationInvalidated = statusErr(C.NOTMUCH_STATUS_OPERATION_INVALIDATED)

	// ErrNotFound is returned when Find* did not find the thread/message by id or filename.
	ErrNotFound = errors.New("not found")

	// ErrUnknownError is returned when notmuch returns NULL indicating an error.
	ErrUnknownError = errors.New("unknown error occured")

	// ErrNoRepliesOrPointerNotFromThread is returned if a message has no replies or if the message's C
	// pointer did not come from a thread.
	ErrNoRepliesOrPointerNotFromThread = errors.New("message has no replies or message's pointer not from a thread")

	// ErrMalformedData is returned when a walk arena is corrupt or
	// truncated - the decode path never panics, every read is
	// bounds-checked.
	ErrMalformedData = errors.New("malformed walk data")
)

// Convert a notmuch status to an error. This is almost a simple cast, but
// we need to return nil if it's a success, rather than NOTMUCH_STATUS_SUCCESS.
func statusErr(s C.notmuch_status_t) error {
	if s != C.NOTMUCH_STATUS_SUCCESS {
		return status(s)
	}
	return nil
}

func (s status) Error() string {
	cstr := C.notmuch_status_to_string(C.notmuch_status_t(s))
	return C.GoString(cstr)
}

# Automatic configuration reload

## Status

Design approved 2026-09-18. The interactive client polls its configuration
directory once per second. A changed TOML file set reloads atomically through
the config store.

## Behavior

The watcher fingerprints every top-level `*.toml` file by sorted name, size,
and modification time. Rename, create, delete, and atomic-save updates produce
a new fingerprint. An unchanged directory does no TOML decode.

A changed fingerprint loads the complete configuration before touching the
store. Parse or validation failure retains the previous configuration and
publishes `JobError{Job: "config"}`. The watcher retries after the next change.
A successful load replaces the store configuration then notifies `ui`, `view`,
`theme`, and `refresh` observers. The existing refresh loop processes view and
interval changes; the TUI resolves styles and bindings from its store snapshot.

The watcher owns no UI and terminates with the application context. It uses
stdlib polling instead of a platform-specific filesystem notification API.

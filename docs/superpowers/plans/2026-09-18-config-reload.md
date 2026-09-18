# Configuration reload implementation plan

1. Add an atomic config-store replacement method and tests for observer delivery.
2. Add a stdlib file-set fingerprint and watcher in the app layer.
3. Reload on change, retain prior configuration on load failure, and publish a
   configuration job error.
4. Update TUI configuration handling for live bindings and styles.
5. Run config, app, TUI, and full test matrices.

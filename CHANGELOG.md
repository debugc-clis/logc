# Changelog

## Unreleased

- Stream complete regular and gzip logs during search instead of silently limiting results to 16 MiB.
- Bootstrap `watch --since` from complete matching history and use event timestamps for FIRST, LAST, and event-rate calculations.
- Discover new default log files while following, support file symlinks and explicit extensionless files, and select files fairly across source directories.
- Add source category/module metadata, stable auto-source IDs, `--category` and `--module` filters, and metadata in JSON output.
- Report inaccessible roots, sanitize terminal control sequences, adapt polling for large source sets, and truncate long watch rows unless `--full` is used.
- Apply regex, context, deduplication, highlighting, noise filters, and JSON rendering to system and Docker streams while preserving Docker option passthrough.
- Support systemd units in the aggregated `logc watch` view.

- Group repeated watch alerts across changing timestamps and common request/trace identifiers.
- Show source recency and configured patterns in `logc ls`.
- Exclude common macOS system logs from default application-log discovery.
- Label log block timestamps explicitly as render times and improve empty-discovery guidance.

## v0.2.0

- Added natural `logc TARGET REGEX` search syntax.
- Added fallback `logc REGEX` search across discovered application logs.
- Added named log groups in `~/.logc.conf`.
- Added `logc ls` and `logc where TARGET` discovery helpers.
- Added smart selectors for paths, directories, globs, Linux processes/PIDs, ports, and systemd-unit fallback.
- Added regex context (`-C`), case-insensitive search (`-i`), `--since`, `--dedup`, and configurable `ignore_line` filters.
- Added automatic rotated-log and gzip search, with `--current` to restrict to active files.
- Added search-then-follow mode (`logc api ERROR -f`).
- Added severity shortcuts for error/warning searches.
- Added log-level terminal highlighting.
- Tightened automatic log-file detection to reduce false positives.
- Preserved fair multi-file follow, hot-log buffering bounds, rotation detection, and truncation detection.

## v0.1.0

- Initial multi-file log discovery and fair follow implementation.
- Application/system log separation.
- User configuration, glob support, Homebrew Formula template, and release workflow.

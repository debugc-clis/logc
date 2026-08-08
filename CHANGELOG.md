# Changelog

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

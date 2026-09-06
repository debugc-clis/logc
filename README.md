# logc


> One command to find, search, follow, and watch local logs.

`logc` replaces the usual `find`, `grep`, `zgrep`, and `tail` chain with a focused local troubleshooting workflow. Start with the logs available on the machine, narrow to the signal you need, and keep watching as new events arrive.

<img src="assets/logc-demo.jpg" alt="logc displays recent application logs from payment API, worker, and nginx files" width="480" />

```bash
logc api 'timeout|reset' --since 30m -f
```

| Before | After |
| --- | --- |
| `find` + `grep` + `zgrep` + `tail` | `logc` |
| Remember paths, rotated files, and shell pipelines. | Name the service and follow the signal. |

**[Download a release](https://github.com/debugc-clis/logc/releases)** · **[Get started](#quick-start)** · **[Configuration](#configuration)** · **[Contribute](#contributing)**

## Why logc

`logc` is a dependency-free Go CLI for local log triage. It brings application-log discovery, regular-expression search, history, and fair multi-file follow into one interface. It focuses on local logs and SRE ergonomics: it does not send logs to a remote service or perform AI analysis.

- **Discover** recent application logs without memorizing paths.
- **Classify** sources as application, system, database, web, network, container, or custom logs without interpreting their meaning.
- **Search** complete active, rotated, and gzip-compressed logs without switching between `grep` and `zgrep`.
- **Follow** multiple files fairly, even when one source is noisy.
- **Watch** matching events as a live alert summary with rates and duplicate counts.
- **Resolve** services by configured name, file path, directory, glob, Linux process/PID, or listening port.
- **Separate** application logs from common operating-system logs.

## Quick Start

```bash
# Discover and follow recent application logs.
logc

# Follow a named source from ~/.logc.conf.
logc api

# Search a source, then follow new matching lines.
logc api 'timeout|reset' --since 30m -f
```

`logc` searches `/var/log`, `/opt/var/log`, and `/opt/log` by default. It considers files modified in the last 24 hours, selects up to 20 files fairly across source directories, shows 10 initial lines per file, then follows those files and discovers newly created logs. File symlinks are supported, including Kubernetes-style container log links. It applies configured `ignore_line` filters throughout.

## Install

Requires Go 1.22+.

```bash
git clone https://github.com/debugc-clis/logc.git
cd logc
make build
sudo install -m 755 bin/logc /usr/local/bin/logc
logc version
```

For a user-local install managed by Go instead, run:

```bash
go install .
```

Pushing a `v*` tag publishes Linux and macOS archives with SHA-256 checksums to GitHub Releases.

## Common Workflows

Start with what you know. You do not need to know the exact log path before using logc.

| Incident situation | Start with | What logc does |
| --- | --- | --- |
| You just opened an unfamiliar host | `logc` | Finds recent application logs, shows their latest lines, and keeps following them. |
| You know the service name | `logc api` | Resolves the configured or discovered source and follows its active files. |
| A symptom started recently | `logc api 'timeout\|reset' --since 30m` | Searches active, rotated, and `.gz` logs within the requested window. |
| You need search results and new events | `logc api ERROR --since 30m -f` | Searches existing history first, then follows new matching lines. |
| Errors are repeating too quickly to read | `logc watch ERROR api --since 30m` | Groups repeated errors and shows counts, rate, and first/last occurrence. |
| You only know a process, PID, or port | `logc @nginx`, `logc @12345`, or `logc :8080` | Resolves the process to its open log files on Linux. |
| The workload runs in Docker | `logc docker api` | Follows the container like `docker logs api -f`, with optional logc filtering. |
| The problem may be at host level | `logc system ERROR --since 30m` | Searches and follows journald/system log output. |
| Recent logs are not enough | `logc api ERROR --all` | Explicitly scans all available history for the resolved source. |

### Find the right source

```bash
logc ls                    # List discovered and configured sources.
logc ls --category web     # List web/proxy log sources.
logc where api             # Show the files matched by a source.
logc /srv/api/log          # Follow all supported logs in a directory.
logc '/srv/**/logs/*.log'  # Follow a recursive glob and discover new files.
```

`logc ls` shows a stable source ID, category, module, type, file count, latest activity age, logrotate status, and location. `ROTATE=yes` means every discovered file in that source is managed by logrotate; `partial` means only some files are managed. Auto-discovered source IDs such as `web/nginx` can be passed back to `logc` directly.

On Linux, logc reads `/etc/logrotate.conf` and included files such as `/etc/logrotate.d/nginx`. Managed files are marked as `logrotate` in normal log block headers, as `[logrotate]` in `logc where`, and as `[R]` in the compact `logc watch` source column. Historical `.log.1` and `.log.1.gz` files are associated with the matching active logrotate path when possible. The inspection is read-only; logc never changes rotation policy or runs logrotate.

Configure memorable names when paths are inconvenient:

```ini
group.api=/srv/api/log/*.log

[group.payment]
category=app
module=payment
path=/srv/payment/**/*.log
```

### Search without pipelines

```bash
logc api ERROR
logc api 'timeout|connection reset'
logc ERROR                         # Search default application logs.
logc api error -i -C 3 --since 2h
logc api ERROR --all               # Explicitly search all available history.
logc api ERROR --dedup
logc api ERROR --current            # Skip rotated and .gz history.
logc api ERROR --json | jq           # One JSON object per source block.
logc --category app,web ERROR         # Search selected source categories.
logc --module payment ERROR           # Search a configured module.
```

The expression is a Go-compatible regular expression. By default, searches prioritize the newest files and use the configured `recent` window (`24h` by default), because incident response usually starts with the latest events. Use `--since 30m`, `--since 7d`, or `--since today` for an explicit window; use `--all` with a search expression only when complete available history is required. Matching active, rotated, and `.gz` logs are scanned completely within that scope, while `--current` skips rotated and compressed files. For system logs, `--all` requires journald on Linux. `ERROR`, `errors`, `WARN`, and `warnings` are severity shortcuts.

### Resolve a running service (Linux)

```bash
logc @nginx   # Process name; falls back to a systemd unit when appropriate.
logc @12345   # PID.
logc :8080    # Listening port → PID → open log files.
```

`logc` inspects `/proc/<pid>/fd` for process-owned log files. Port lookup uses `lsof`, with `ss` as a Linux fallback.

### Stream system logs

```bash
logc system
logc system --kernel
logc system ERROR --since 30m
logc @api.service ERROR -f
```

On Linux, `logc` uses `journalctl` when available and falls back to `dmesg`; macOS uses `log stream`. System streams use the same regex, context, deduplication, color, noise-filter, and JSON rendering pipeline as file logs.

### Follow Docker container logs

`logc docker` forwards Docker log options directly to `docker logs` and follows by default.

```bash
logc docker api
logc docker --tail 100 api
logc docker --since 30m --timestamps api
logc docker -f api
logc docker --since 30m -m ERROR -C 2 api
logc docker --json api
```

Use Docker's native flags exactly as you would with `docker logs`; place options before the container name. `-m/--match`, `-i`, `-C`, `--dedup`, `--no-color`, and `--json` are handled by logc while all other options are forwarded to Docker. Docker must be installed and accessible in `PATH`.

### Watch live alerts

`logc watch` aggregates matching events instead of printing every line. It normalizes timestamps and common request/trace identifiers so repeated failures are counted together, then refreshes once per second with the event rate from the last minute and first/last occurrence time.

```bash
logc watch ERROR
logc watch api 'timeout|reset'
logc watch api ERROR --since 30m
logc watch ERROR /var/log/nginx '/opt/log/**/*.log' /etc/myapp/app.log
logc watch 'timeout|reset' '/opt/var/*log,/etc/log.log'
logc watch ERROR @api.service --since 30m
logc watch ERROR --category app --module payment
logc watch ERROR --full
```

Use `logc watch REGEX [TARGET...]` to watch one or more named sources, directories, files, quoted glob patterns, or a systemd unit. Comma-separated targets are also supported. `logc watch api 'timeout|reset'` remains available for compatibility. With `--since`, logc scans the complete matching history first, then follows active files without counting historical events as current traffic. `watch` intentionally requires a bounded window and does not accept `--all`. FIRST/LAST use event timestamps when available; `~` marks a file or observation-time estimate for lines without timestamps. Long rows are truncated to terminal width unless `--full` is set.

## Incident Playbooks

### 1. Triage an unfamiliar machine

Start broad, identify the active source, and then narrow the stream:

```bash
logc                    # Aggregate and follow the latest discovered app logs.
logc ls                 # Show stable source IDs and their latest activity.
logc where web/nginx    # Confirm exactly which files a source resolves to.
logc web/nginx          # Follow only that source.
```

Plain `logc` is the fastest first command after SSHing into a host. It shows the latest configured number of lines from each selected file, then continues following appended data and discovers newly created log files.

### 2. Investigate a recent API timeout

Search a bounded window first so old incidents do not bury the current signal:

```bash
logc api 'timeout|connection reset' --since 30m -C 2
```

This searches the active file plus matching rotated and gzip-compressed history, prints two context lines around each match, and exits. Once the pattern looks useful, add `-f` to continue watching only new matching lines:

```bash
logc api 'timeout|connection reset' --since 30m -C 2 -f
```

Use `--all` only when the bounded search is insufficient:

```bash
logc api 'timeout|connection reset' --all
```

### 3. Measure a repeating error storm

Use `watch` when raw output scrolls too quickly to understand frequency:

```bash
logc watch ERROR api --since 30m
```

Approximate output:

```text
logc watch "ERROR"  [13:50:03]
2 alert groups · 7 events/min · 3 sources

COUNT  FIRST     LAST      SOURCE                 ALERT
12     13:42:02  13:49:11  app/api/app.log        ERROR upstream timeout after 30s
4      13:45:18  13:49:44  app/worker/worker.log  ERROR queue connection reset

Refreshes every second · Press Ctrl+C to stop
```

Changing request IDs and common trace identifiers are normalized before grouping, so repeated copies of the same failure increase `COUNT` instead of creating a new row every time.

### 4. Correlate application and platform failures

Use separate terminals when an application error may be caused by its container, service manager, or host:

```bash
# Terminal 1: application files
logc api ERROR --since 30m -f

# Terminal 2: systemd unit
logc @api.service ERROR -f

# Or, for a containerized service
logc docker --since 30m --timestamps -m 'ERROR|timeout' api
```

This keeps each source readable while preserving the original timestamps needed to correlate failures. logc aggregates and highlights the evidence; the operator remains responsible for diagnosis.

## Configuration

The default configuration path is `~/.logc.conf`. Set `LOGC_CONFIG=/path/to/logc.conf` to use another file.

```bash
logc config init
logc config path
logc config show
```

Example configuration:

```ini
# Roots scanned by plain `logc`.
default_log_dir=/var/log
default_log_dir=/opt/var/log
default_log_dir=/srv

# To use only custom roots, reset the built-in list first.
# default_log_dir=!
# default_log_dir=/srv/logs

# Add custom exclusions. Prefix an exact built-in pattern with ! to remove it.
exclude=/srv/**/debug*.log
# exclude=!/var/log/syslog*

# Hide recurring request noise everywhere.
ignore_line=.*GET /health.*
ignore_line=.*GET /metrics.*

# Named sources.
# `mysql` is built in and searches common MySQL/MariaDB file-log paths.
# Add a custom path when your database uses a nonstandard data directory.
group.mysql=/srv/mysql/log/*.log
group.api=/srv/api/log/*.log
[group.payment]
category=app
module=payment
path=/srv/payment/**/*.log

lines=10
max_files=20
recent=24h
flush_interval=500ms
scan_interval=5s
max_batch_lines=10
max_buffer_lines=2000
color=true
```

On Linux, logc detects the distribution from `/etc/os-release` and excludes distribution-specific system-log paths from default application-log discovery. Use `logc system` for operating-system logs, or `logc config show` to inspect the active patterns. Category and module labels classify where logs come from; logc does not infer root causes or interpret application meaning.

## Operational Limits

- `logc` is read-only, but access to host, container, and system logs still depends on the current user's permissions.
- Follow mode streams completed batches directly to stdout and diagnostics to stderr; it does not retain previously printed output. Each file's pending queue is capped by both `max_buffer_lines` and an 8 MiB safety limit, so a slow terminal or pipe causes older buffered lines to be dropped with an explicit warning instead of allowing unbounded process memory growth. Terminal scrollback and redirected output files are managed by the terminal or shell, not by logc.
- Active files stay open across normal rename-and-create log rotation so logc can drain the old file before following the replacement. Open descriptors are capped at 256; larger explicit source sets fall back to path-based polling. Descriptors are closed when a source is removed, replaced, or logc exits.
- Files already being followed remain active when they become older than the `recent` discovery window, provided the default `max_files` budget has room. A temporarily missing path is retained for 30 seconds to bridge common rotation gaps.
- Recursive filesystem-root scans are refused. Keep source groups and glob roots narrow; new files are rescanned every five seconds by default.
- Follow and watch modes cap each per-file read at 4 MiB and truncate an individual or unterminated line after 2 MiB to protect the host during high-volume incidents.
- Recent default-source searches skip files whose modification time is older than the requested window, process newer files first, and fairly select up to `max_files × 5` candidates; logc warns when that safety limit applies. Narrow the target or increase `max_files` when needed. Explicit `--all` searches are not file-limited, can take time on large histories, and can be interrupted with Ctrl+C.
- Explicit directories containing more than 1,000 files produce a polling-load warning and use a slower poll interval.
- Terminal control sequences in log lines and paths are removed before human-readable rendering; JSON preserves the original text through normal JSON escaping.
- Inaccessible roots and files are reported as warnings instead of being silently treated as empty.
- `watch` retains at most 200 distinct alert groups and renders the 20 most recent groups.

## Roadmap

### Implemented

- [x] Discover and fairly follow recent application logs across multiple files.
- [x] Search active, rotated, and gzip-compressed logs with regex, context, time filtering, and deduplication.
- [x] Resolve named sources, files, directories, globs, Linux processes/PIDs, and ports.
- [x] Configure log roots, source groups, exclusions, noise filters, and MySQL/MariaDB log paths.
- [x] Stream system logs and Docker container logs.
- [x] Watch matching events with live error rates, duplicate counts, and first/last occurrence times.
- [x] Classify and filter sources by category and configured module.
- [x] Discover new files dynamically, follow file symlinks, and select default sources fairly.
- [x] Search complete regular and gzip history without silent byte limits.
- [x] Sanitize terminal output and report inaccessible roots.

### Planned

- [ ] Publish and maintain Homebrew package distribution.

## Contributing

logc is open source and free to use. Fork the repository, build the feature you need, and open a pull request.

- [Fork logc](https://github.com/debugc-clis/logc/fork)
- [Report an issue](https://github.com/debugc-clis/logc/issues)
- [Browse the source](https://github.com/debugc-clis/logc)

## AI and Liability Notice

Parts of this project were generated or assisted by AI and are provided as-is. To the maximum extent permitted by law, the author and contributors are not liable for any results, damages, losses, or other consequences arising from use of this software. Review and test it before using it in production.

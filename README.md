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
- **Search** active, rotated, and gzip-compressed logs without switching between `grep` and `zgrep`.
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

`logc` searches `/var/log`, `/opt/var/log`, and `/opt/log` by default. It considers files modified in the last 24 hours, shows up to 20 files and 10 initial lines per file, then follows those files for new lines. It applies configured `ignore_line` filters throughout.

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

### Find the right source

```bash
logc ls                    # List discovered and configured sources.
logc where api             # Show the files matched by a source.
logc /srv/api/log          # Follow all supported logs in a directory.
logc '/srv/**/logs/*.log'  # Follow a recursive glob and discover new files.
```

Configure memorable names when paths are inconvenient:

```ini
group.api=/srv/api/log/*.log

[group.payment]
path=/srv/payment/**/*.log
```

### Search without pipelines

```bash
logc api ERROR
logc api 'timeout|connection reset'
logc ERROR                         # Search default application logs.
logc api error -i -C 3 --since 2h
logc api ERROR --dedup
logc api ERROR --current            # Skip rotated and .gz history.
logc api ERROR --json | jq           # One JSON object per source block.
```

The expression is a Go-compatible regular expression. Searches include matching rotated and `.gz` logs by default; `--current` restricts the result to active files. `ERROR`, `errors`, `WARN`, and `warnings` are severity shortcuts.

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
```

On Linux, `logc` uses `journalctl` when available and falls back to `dmesg`; macOS uses `log stream`.

### Follow Docker container logs

`logc docker` forwards Docker log options directly to `docker logs` and follows by default.

```bash
logc docker api
logc docker --tail 100 api
logc docker --since 30m --timestamps api
logc docker -f api
```

Use Docker's native flags exactly as you would with `docker logs`; place options before the container name. Docker must be installed and accessible in `PATH`.

### Watch live alerts

`logc watch` aggregates matching events instead of printing every line. It refreshes once per second with the event rate from the last minute, duplicate count, and first/last occurrence time.

```bash
logc watch ERROR
logc watch api 'timeout|reset'
logc watch api ERROR --since 30m
logc watch ERROR /var/log/nginx '/opt/log/**/*.log' /etc/myapp/app.log
logc watch 'timeout|reset' '/opt/var/*log,/etc/log.log'
```

Use `logc watch REGEX [TARGET...]` to watch one or more named sources, directories, files, or quoted glob patterns. Comma-separated targets are also supported. `logc watch api 'timeout|reset'` remains available for compatibility. The terminal dashboard redraws in place. When output is redirected, each refresh is printed as a new snapshot.

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

On Linux, logc detects the distribution from `/etc/os-release` and excludes distribution-specific system-log paths from default application-log discovery. Use `logc system` for operating-system logs, or `logc config show` to inspect the active patterns.

## Operational Limits

- `logc` is read-only, but access to host, container, and system logs still depends on the current user's permissions.
- Recursive filesystem-root scans are refused. Keep source groups and glob roots narrow; new files are rescanned every five seconds by default.
- Follow and watch modes cap each per-file read at 4 MiB and truncate an unterminated line after 2 MiB to protect the host during high-volume incidents.
- `watch` retains at most 200 distinct alert groups and renders the 20 most recent groups.

## Roadmap

### Implemented

- [x] Discover and fairly follow recent application logs across multiple files.
- [x] Search active, rotated, and gzip-compressed logs with regex, context, time filtering, and deduplication.
- [x] Resolve named sources, files, directories, globs, Linux processes/PIDs, and ports.
- [x] Configure log roots, source groups, exclusions, noise filters, and MySQL/MariaDB log paths.
- [x] Stream system logs and Docker container logs.
- [x] Watch matching events with live error rates, duplicate counts, and first/last occurrence times.

### Planned

- [ ] Publish and maintain Homebrew package distribution.

## Contributing

logc is open source and free to use. Fork the repository, build the feature you need, and open a pull request.

- [Fork logc](https://github.com/debugc-clis/logc/fork)
- [Report an issue](https://github.com/debugc-clis/logc/issues)
- [Browse the source](https://github.com/debugc-clis/logc)

## AI and Liability Notice

Parts of this project were generated or assisted by AI and are provided as-is. To the maximum extent permitted by law, the author and contributors are not liable for any results, damages, losses, or other consequences arising from use of this software. Review and test it before using it in production.

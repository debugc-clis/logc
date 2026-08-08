# logc

`logc` is a small SRE-oriented CLI that makes local logs feel like one interface instead of a collection of `find`, `tail`, `grep`, `zgrep`, `journalctl`, `lsof`, and `/proc` tricks.

The design rule is simple:

> Remember `logc`, not log paths and command pipelines.

## Natural CLI

```bash
# latest application logs discovered from configured roots
logc

# follow a source by name
logc api

# search a source (regex is a positional argument)
logc api ERROR
logc api 'timeout|reset|refused'

# search all default application logs
logc ERROR
logc request_id=9f82ac

# search existing logs, then continue following matches
logc api ERROR -f

# a few optional modifiers
logc api ERROR --since 30m
logc api ERROR -C 3
logc api error -i
logc api ERROR --dedup

# direct paths, directories, globs, or multiple files
logc /srv/api/log
logc /srv/api/app.log /srv/worker/worker.log
logc '/srv/**/logs/*.log'

# process/PID/port selectors on Linux
logc @nginx
logc @12345
logc :8080

# system logs stay separate
logc system
logc system --kernel
```

`ERROR`, `errors`, `WARN`, and `warnings` are convenient severity searches. `errors` includes common fatal/panic/exception forms.

## What is implemented

### Log discovery

`logc` scans configured application-log roots, excludes common operating-system logs from the normal view, and ranks recent files by modification time.

Default roots:

```text
/var/log
/opt/var/log
/opt/log
```

Use `logc ls` to see discovered local sources and `logc where api` to see exactly what a selector resolves to.

### Default machine snapshot

Running plain `logc` prints a clean, one-time snapshot; it does not continuously follow logs. With the default configuration, it:

- searches `/var/log`, `/opt/var/log`, and `/opt/log`
- excludes configured system-log paths such as `syslog`, `messages`, `kern.log`, `auth.log`, and `dmesg`
- considers files modified within the last 24 hours, selects at most 20 files, and prints the latest 10 lines from each
- prints a full-path block per file, applies `ignore_line` filters, and exits
- highlights ERROR/WARN/DEBUG lines only when stdout is a color-capable terminal

For example, imagine the following recent application logs:

```text
/opt/log/payment/api.log
/opt/log/payment/worker.log
/opt/var/log/nginx/access.log

/var/log/syslog        <-- excluded system log
/var/log/auth.log      <-- excluded system log
```

If `/opt/log/payment/api.log` contains 12 lines, the default `lines=10` omits its first two lines. A `logc` invocation would look approximately like this (ANSI colors omitted):

```text
$ logc

/opt/log/payment/api.log [13:22:18]
  2026-08-08 13:18:05 INFO  listening on :8080
  2026-08-08 13:18:14 INFO  request_id=req-100 POST /payments
  2026-08-08 13:18:14 INFO  request_id=req-100 payment authorized
  2026-08-08 13:18:29 INFO  request_id=req-101 POST /payments
  2026-08-08 13:18:30 WARN  request_id=req-101 stripe latency=1820ms
  2026-08-08 13:18:31 INFO  request_id=req-101 retrying attempt=1
  2026-08-08 13:18:32 ERROR request_id=req-101 upstream timeout
  2026-08-08 13:18:33 INFO  request_id=req-101 retrying attempt=2
  2026-08-08 13:18:34 INFO  request_id=req-101 payment completed
  2026-08-08 13:20:02 INFO  request_id=req-102 GET /payments/123 200

/opt/log/payment/worker.log [13:22:18]
  2026-08-08 13:19:01 INFO  worker started
  2026-08-08 13:19:03 INFO  job=invoice-882 picked_up
  2026-08-08 13:19:04 INFO  job=invoice-882 sending email
  2026-08-08 13:19:05 INFO  job=invoice-882 completed
  2026-08-08 13:20:41 WARN  queue_depth=842 threshold=800

/opt/var/log/nginx/access.log [13:22:18]
  10.0.2.14 - GET /api/payments 200 42ms
  10.0.2.18 - POST /api/payments 201 218ms
  10.0.2.31 - GET /health 200 2ms
  10.0.2.14 - GET /api/invoices 500 81ms
```

`/var/log/syslog` and `/var/log/auth.log` do not appear. The header time (`[13:22:18]`) is the time `logc` rendered the block, not the file modification or log-event time.

With these filters in `~/.logc.conf`:

```ini
ignore_line=.*GET /health.*
ignore_line=.*GET /metrics.*
```

the health-check line is removed from the snapshot. In short:

```text
logc                 Show a one-time overview of recent application activity.
logc payment         Show payment logs and keep watching them.
logc payment ERROR   Find errors in payment logs.
```

> Future improvement: the default snapshot can prioritize the 5–10 most active or relevant files and include compact activity metadata, instead of potentially displaying 20 files × 10 lines. This is not implemented yet.

### Smart selectors

A target can be:

```text
api                 auto-discovered name or configured group
/srv/api/log        directory
/srv/api/app.log    file
/srv/**/logs/*.log  glob
@nginx              process (or systemd unit fallback on Linux)
@12345              PID
:8080               listening port -> PID -> open log files
```

For Linux process selectors, `logc` inspects `/proc/<pid>/fd`. Port lookup uses `lsof` when available, with `ss` as a Linux fallback.

### Search without grep pipelines

```bash
logc api ERROR
logc api 'timeout|connection reset'
logc ERROR
```

The second positional expression is a Go-compatible regular expression. A one-argument expression that does not resolve to a source becomes a search across default application logs.

Useful optional flags:

```text
-i                  case-insensitive
-C N                N lines before and after a match
--since 10m         recent time window
--since 2h
--since 7d
--since today
--dedup             collapse consecutive duplicate lines
--current           do not search rotated/.gz history
-f                  continue following matching new lines after the search
```

Search mode automatically checks recent rotated logs and `.gz` files unless `--current` is specified. This replaces the common `grep` + `zgrep` split.

By default a search is constrained to `recent` from config (normally 24h). `--since` changes that window.

### Fair multi-file follow

A hot file should not visually starve quieter files. `logc` buffers updates and prints small per-file batches in round-robin order.

Defaults:

```text
initial lines/file  10
flush interval      500ms
max batch/file      10 lines
max buffer/file     2000 lines
```

If a source produces logs faster than the terminal can display them, old buffered lines are bounded and `logc` tells you how many were skipped rather than growing memory forever.

Log rotation and truncation are detected while following.

### Output

Each group is headed by the full path:

```text
/srv/api/app.log [09:42:31]
  2026-08-08 09:42:30 INFO request complete
  2026-08-08 09:42:31 ERROR database timeout
```

When stdout is a terminal, common ERROR/FATAL/PANIC levels are highlighted red, warnings yellow, and DEBUG/TRACE dimmed. `NO_COLOR=1` and `--no-color` are honored.

### Noise filtering

Configure recurring lines you never want to see:

```ini
ignore_line=.*GET /health.*
ignore_line=.*GET /metrics.*
ignore_line=.*kube-probe.*
```

These filters apply to both snapshots/searches and follow mode.

## Configuration

Default config:

```text
~/.logc.conf
```

Override it with:

```bash
LOGC_CONFIG=/path/to/logc.conf logc
```

Create a starter file:

```bash
logc config init
```

Inspect it:

```bash
logc config path
logc config show
```

Example:

```ini
default_log_dir=/var/log
default_log_dir=/opt/var/log
default_log_dir=/srv

exclude=/var/log/journal/**
exclude=/var/log/syslog*
exclude=/var/log/messages*
exclude=/var/log/kern.log*
exclude=/srv/**/debug*.log

# Linux loads only common debugging-oriented system-log exclusions automatically.
# User, cron, package-manager, and installer logs are not excluded by default.
# Add exclusions, or remove an exact built-in pattern with !.
# exclude=!/var/log/syslog*
# exclude=/srv/platform/**

ignore_line=.*GET /health.*

# Simple named source
group.api=/srv/api/log/*.log

# Section form is also supported
[group.payment]
path=/srv/payment/**/*.log

lines=10
max_files=20
recent=24h
flush_interval=500ms
scan_interval=1s
max_batch_lines=10
max_buffer_lines=2000
color=true
```

## Install from source

Requires Go 1.22+ to build.

```bash
go build -trimpath -o logc .
sudo install -m 755 logc /usr/local/bin/logc
```

or:

```bash
make build
sudo install -m 755 bin/logc /usr/local/bin/logc
```

## Homebrew

This repository includes `Formula/logc.rb`. After publishing the repo and a release tarball, replace `YOUR_GITHUB_USER` and the release SHA256 in the Formula and publish it through a Homebrew tap:

```bash
brew tap YOUR_GITHUB_USER/tap
brew install logc
```

If the project is eventually accepted into Homebrew/core, installation becomes simply:

```bash
brew install logc
```

## Release

`.github/workflows/release.yml` builds:

```text
linux/amd64
linux/arm64
darwin/amd64
darwin/arm64
```

and uploads compressed artifacts plus checksums when a `v*` tag is pushed.

## Scope

This release deliberately focuses on local logs and SRE ergonomics. AI analysis is not part of the core yet. The architecture keeps filtering and source resolution inside `logc` so future analyzers can consume a consistent log stream rather than reimplement path discovery and command composition.

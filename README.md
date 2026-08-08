# logg

`logg` is a small SRE-oriented CLI that makes local logs feel like one interface instead of a collection of `find`, `tail`, `grep`, `zgrep`, `journalctl`, `lsof`, and `/proc` tricks.

The design rule is simple:

> Remember `logg`, not log paths and command pipelines.

## Natural CLI

```bash
# latest application logs discovered from configured roots
logg

# follow a source by name
logg api

# search a source (regex is a positional argument)
logg api ERROR
logg api 'timeout|reset|refused'

# search all default application logs
logg ERROR
logg request_id=9f82ac

# search existing logs, then continue following matches
logg api ERROR -f

# a few optional modifiers
logg api ERROR --since 30m
logg api ERROR -C 3
logg api error -i
logg api ERROR --dedup

# direct paths, directories, globs, or multiple files
logg /srv/api/log
logg /srv/api/app.log /srv/worker/worker.log
logg '/srv/**/logs/*.log'

# process/PID/port selectors on Linux
logg @nginx
logg @12345
logg :8080

# system logs stay separate
logg system
logg system --kernel
```

`ERROR`, `errors`, `WARN`, and `warnings` are convenient severity searches. `errors` includes common fatal/panic/exception forms.

## What is implemented

### Log discovery

`logg` scans configured application-log roots, excludes common operating-system logs from the normal view, and ranks recent files by modification time.

Default roots:

```text
/var/log
/opt/var/log
/opt/log
```

Use `logg ls` to see discovered local sources and `logg where api` to see exactly what a selector resolves to.

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

For Linux process selectors, `logg` inspects `/proc/<pid>/fd`. Port lookup uses `lsof` when available, with `ss` as a Linux fallback.

### Search without grep pipelines

```bash
logg api ERROR
logg api 'timeout|connection reset'
logg ERROR
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

A hot file should not visually starve quieter files. `logg` buffers updates and prints small per-file batches in round-robin order.

Defaults:

```text
initial lines/file  10
flush interval      500ms
max batch/file      10 lines
max buffer/file     2000 lines
```

If a source produces logs faster than the terminal can display them, old buffered lines are bounded and `logg` tells you how many were skipped rather than growing memory forever.

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
~/.logg.conf
```

Override it with:

```bash
LOGG_CONFIG=/path/to/logg.conf logg
```

Create a starter file:

```bash
logg config init
```

Inspect it:

```bash
logg config path
logg config show
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
go build -trimpath -o logg .
sudo install -m 755 logg /usr/local/bin/logg
```

or:

```bash
make build
sudo install -m 755 bin/logg /usr/local/bin/logg
```

## Homebrew

This repository includes `Formula/logg.rb`. After publishing the repo and a release tarball, replace `YOUR_GITHUB_USER` and the release SHA256 in the Formula and publish it through a Homebrew tap:

```bash
brew tap YOUR_GITHUB_USER/tap
brew install logg
```

If the project is eventually accepted into Homebrew/core, installation becomes simply:

```bash
brew install logg
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

This release deliberately focuses on local logs and SRE ergonomics. AI analysis is not part of the core yet. The architecture keeps filtering and source resolution inside `logg` so future analyzers can consume a consistent log stream rather than reimplement path discovery and command composition.

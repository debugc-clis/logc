# logc

> One command to find, search, follow, and watch local logs.

`logc` is a read-only Linux command-line tool for operators who still want to inspect logs themselves, but do not want to repeatedly assemble `find`, `grep`, `zgrep`, `journalctl`, and `tail -F` pipelines.

<img src="./assets/logc-demo.jpg" alt="logc searching and following several log sources in one terminal" width="560">

```bash
logc api 'timeout|reset' --since 30m -f
```

| Before | After |
| --- | --- |
| `find + grep + zgrep + tail -F + journalctl` | `logc` |

[View the landing page](https://www.logc.us/) · [Browse the source](https://github.com/debugc-clis/logc) · [Report an issue](https://github.com/debugc-clis/logc/issues)

> [!IMPORTANT]
> Homebrew packaging is **still being worked on**. `brew install logc` is not available yet; use the source installation below.

## Why logc

During an incident, the first problem is often not understanding the error—it is finding the right log source quickly.

`logc` focuses on local log aggregation and operator-driven debugging:

- discovers recent application logs across common Linux paths;
- follows several files as one live stream, including newly created and rotated files;
- searches current, rotated, and gzip-compressed logs;
- resolves services by name, PID, or listening port;
- groups sources by application, module, system, network, database, or custom configuration;
- highlights `FATAL`, `ERROR`, `WARN`, `INFO`, and `DEBUG` in terminals;
- exposes systemd and Docker logs without replacing their native tools;
- provides a live error-rate and repeated-error view with `logc watch`.

It intentionally stays local and transparent. It does not upload logs, run AI analysis, or replace a centralized observability platform.

## Install

### Build from source — available now

Requires Go 1.22 or newer.

```bash
git clone https://github.com/debugc-clis/logc.git
cd logc
make build
sudo install -m 0755 ./logc /usr/local/bin/logc
logc version
```

For a user-local installation:

```bash
mkdir -p ~/.local/bin
install -m 0755 ./logc ~/.local/bin/logc
export PATH="$HOME/.local/bin:$PATH"
```

You can also install from the checked-out source with:

```bash
go install .
```

### Homebrew — working on it

```bash
# Not available yet
brew install logc
```

The Homebrew formula and tap are on the roadmap. Do not use this command as the current installation method.

## Quick Start

```bash
# Discover recent logs, print their latest lines, then keep following
logc

# Follow sources matching a service, path, PID, or port
logc api
logc /opt/log/payment
logc 1234
logc :8080

# Search recent API logs
logc api ERROR --since 30m

# Search first, then continue following matching sources
logc api 'timeout|reset' --since 30m -f

# Watch live error rate and repeated messages
logc watch ERROR
```

Plain `logc` is a follow command: it prints the latest 10 lines from up to 20 recently modified files, then refreshes as those files receive new content. It also rescans periodically so newly created matching log files can join the stream.

Stop any follow or watch command with `Ctrl+C`.

## Choose a Command

| Goal | Command |
| --- | --- |
| See what is happening on the machine now | `logc` |
| Follow one application or module | `logc api` |
| Search recent matching logs | `logc api ERROR --since 30m` |
| Search and continue following | `logc api 'timeout|reset' --since 30m -f` |
| Inspect a process by PID | `logc 1234` |
| Inspect the process listening on a port | `logc :8080` |
| Watch repeated failures and error rate | `logc watch ERROR` |
| Stream systemd journal entries | `logc system api -f` |
| Follow a Docker container | `logc docker --tail 100 -f api` |
| List discovered log sources | `logc ls` |
| Explain why a target matched | `logc where api` |

## Command Guide

### 1. Aggregate and follow local logs

```bash
logc
```

The default command:

1. scans configured roots such as `/var/log`, `/opt/var/log`, and `/opt/log`;
2. skips known system-log paths that are better handled by `logc system`;
3. selects recently modified application logs;
4. prints a small tail from each source;
5. keeps following updates like a multi-file `tail -F`;
6. detects truncation, replacement, and common logrotate activity;
7. periodically discovers newly created matching files.

Target a service, path, process, or port to narrow the stream:

```bash
logc payment
logc /srv/payment/log
logc 1234
logc :8080
```

### 2. Search recent logs

The second positional argument is a Go regular expression:

```bash
logc api ERROR
logc api 'timeout|connection reset'
logc nginx ' 5[0-9][0-9] '
```

Searches include active logs, rotated files such as `.1`, and gzip files such as `.2.gz`. By default, candidates are limited to recently modified files.

```bash
logc api ERROR --since 30m
logc api ERROR --since 2026-08-08T13:00:00
logc api ERROR --all
```

Useful output controls:

```bash
logc api ERROR -C 2
logc api ERROR --dedup
logc api ERROR --category app --module api
logc api ERROR --json
```

### 3. Search, then follow

Add `-f` to search historical candidates first and then follow active files:

```bash
logc api 'timeout|reset' --since 30m -f
```

This is useful when you need immediate context before waiting for the next occurrence.

### 4. Watch live alerts

```bash
logc watch ERROR
logc watch 'timeout|connection reset' api
logc watch ERROR '/opt/log/*.log,/etc/service.log'
```

The watch view reports:

- current error rate;
- total and repeated-event counts;
- first and last occurrence times;
- grouped normalized messages;
- source category, module, and logrotate status.

Path arguments may be files, directories, glob patterns, or comma-separated selectors. The dashboard refreshes every second and calculates its live rate over a one-minute window.

```bash
logc watch ERROR --since 30m
logc watch ERROR api --full
logc watch ERROR --category app --module api
```

`logc watch` is a terminal dashboard and intentionally does not support `--json` or `--all`.

### 5. Stream system logs

```bash
logc system
logc system api
logc system api --since 30m -f
logc system api -n 200
```

On systemd machines, `logc system` uses `journalctl`. Distribution-specific file paths remain configurable for systems that use traditional log files.

### 6. Follow Docker containers

```bash
logc docker api
logc docker --tail 100 -f api
logc docker --since 30m --timestamps api
logc docker --details api
```

`logc docker CONTAINER [docker logs options]` preserves Docker's logging behavior while keeping the command under the same `logc` workflow. The container name or ID is passed directly to `docker logs`.

### 7. Inspect discovered sources

```bash
logc ls
logc ls --json
logc where api
logc where :8080 --json
```

- `logc ls` lists discoverable sources with category, module, recency, and logrotate metadata.
- `logc where TARGET` explains matched paths and source resolution without following them.

## Common Options

| Option | Purpose |
| --- | --- |
| `-f`, `--follow` | Continue following after the initial output |
| `-n N`, `--lines N` | Set initial lines per file |
| `--since VALUE` | Limit search by duration or timestamp |
| `--all` | Disable the recent-file search cutoff |
| `-C N`, `--context N` | Show context lines around matches |
| `--dedup` | Collapse repeated search results |
| `--category NAME` | Filter by source category |
| `--module NAME` | Filter by module |
| `-i`, `--ignore-case` | Match without case sensitivity |
| `--current` | Skip rotated and gzip history |
| `--json` | Emit machine-readable output where supported |
| `--no-color` | Disable terminal colors |
| `--full` | Do not truncate long rows in `logc watch` |
| `-m REGEX`, `--match REGEX` | Provide the match expression explicitly |
| `--exclude PATTERN` | Add a path exclusion for this run |

Run `logc --help` or `logc COMMAND --help` for the authoritative option list.

## Configuration

The user configuration file is `~/.logc.conf`.

Create a starter configuration:

```bash
logc init-config
```

Print the effective merged configuration:

```bash
logc show-config
```

### Minimal example

```ini
default_log_dir=/var/log
default_log_dir=/opt/var/log
default_log_dir=/opt/log

lines=10
max_files=20
recent=24h
flush_interval=500ms
scan_interval=5s
max_batch_lines=10
max_buffer_lines=2000
color=true

ignore_line=.*GET /health.*
ignore_line=.*GET /metrics.*

[group.api]
path=/srv/api/log/*.log
path=/opt/log/api/*.log
category=app
module=api
```

Custom source sections use `[group.NAME]`; a bare section such as `[api]` is not interpreted as a source group.

### Main settings

| Setting | Default | Meaning |
| --- | --- | --- |
| `default_log_dir` | common Linux log roots | Directories scanned for logs |
| `lines` | `10` | Initial lines shown per file |
| `max_files` | `20` | Maximum files selected |
| `recent` | `24h` | Recent-file discovery window |
| `flush_interval` | `500ms` | Maximum wait before buffered lines are printed |
| `scan_interval` | `5s` | Follow-mode discovery interval |
| `max_batch_lines` | `10` | Lines emitted per source in one batch |
| `max_buffer_lines` | `2000` | Maximum pending lines retained per source |
| `color` | `true` | Enable terminal colors when stdout is a TTY |
| `ignore_line` | none | Regular expressions omitted from output |
| `exclude` | distribution defaults | Discovery exclusions |
| `group.NAME` | none | Custom source selectors |

Built-in discovery includes common application, web-server, and database locations, including MySQL and MariaDB log paths. User configuration is merged after defaults, so search roots, exclusions, line filters, and source groups can be extended or overridden.

### Distribution-aware system paths

`logc` recognizes commonly used debugging logs across Debian, Ubuntu, CentOS, RHEL, Fedora, Amazon Linux, Alpine, Arch, and related distributions. It keeps the most useful system sources—such as general messages, kernel, daemon, audit, and package-independent service failures—while excluding user-session, cron, and installer noise from default application discovery.

Use `logc system` for system logs and override the built-in paths in `~/.logc.conf` when the machine uses a custom layout.

### Logrotate awareness

When a discovered file is covered by a readable logrotate configuration, `logc` marks it as rotated-managed in list, location, normal, watch, and JSON output. The follower also detects file replacement and truncation so rotation does not permanently detach the stream.

Because logrotate configuration may include unreadable files or shell-expanded rules, detection is best effort and never required for reading a log.

## Operational Behavior

- **Read-only:** `logc` does not modify log files or services.
- **Permissions:** it only reads paths available to the current user.
- **Bounded discovery:** the default recent window and file limit prevent accidental full-disk scans.
- **Safe roots:** recursive discovery refuses filesystem-root scans.
- **Long-running follow:** active files are polled and discovery is refreshed without retaining full log history in memory.
- **Rotation handling:** truncation and inode replacement reopen the active source; rotated and gzip files remain searchable.
- **Open-file control:** follow mode limits active descriptors and falls back to polling where needed.
- **Terminal safety:** untrusted control characters are sanitized before terminal rendering.
- **Backpressure:** a slow output consumer can delay refreshes because stdout and stderr writes remain synchronous.
- **JSON mode:** paths and message text are preserved for downstream tools.

## Troubleshooting Recipes

### Investigate recent API timeouts

```bash
logc where api
logc api 'timeout|reset' --since 30m -C 2
logc api 'timeout|reset' --since 30m -f
```

### Identify a repeating error storm

```bash
logc watch ERROR api --since 30m
```

### Correlate application and platform failures

```bash
logc api ERROR --since 30m
logc system api --since 30m
logc docker --since 30m api
```

## Roadmap

### Implemented

- [x] Discover, classify, and follow recent local logs.
- [x] Search active, rotated, and gzip-compressed files.
- [x] Resolve targets by name, path, PID, and listening port.
- [x] Filter by time, category, module, context, and deduplication.
- [x] Stream systemd journal and Docker container logs.
- [x] Watch error rate, repeated events, and occurrence times.
- [x] Detect common logrotate coverage and follow rotated files.
- [x] Emit colored terminal output and structured JSON.
- [x] Load defaults plus user-defined source groups from `~/.logc.conf`.

### Planned

- [ ] Publish and maintain Homebrew distribution so `brew install logc` becomes available.
- [ ] Publish and validate tagged binary releases for supported platforms.
- [ ] Expand distribution-specific discovery fixtures and integration tests.
- [ ] Improve source ranking for very large hosts.
- [ ] Add optional shell completions and manual pages.

## Contributing

Contributions are welcome.

1. Fork [debugc-clis/logc](https://github.com/debugc-clis/logc).
2. Create a focused branch.
3. Add tests for behavior changes.
4. Run `go test ./...`.
5. Open a pull request describing the operational problem and the chosen behavior.

Please keep `logc` local-first, read-only, scriptable, and predictable.

## AI and Liability Notice

Parts of this project and its documentation may be generated or assisted by AI. AI-assisted changes should be reviewed and tested by maintainers and contributors before release.

This software is provided without warranty. To the maximum extent permitted by law, the author and contributors are not liable for data loss, service interruption, lost profits, operational impact, or other direct or indirect damages resulting from installation, configuration, or use. Users remain responsible for validating the tool in their own environments.

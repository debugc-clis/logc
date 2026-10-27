# Iteration log

Record evidence from each scheduled run here. A local test pass, a pushed commit,
remote CI success and a confirmed website deployment are separate outcomes.

## 2026-10-25 — Start the recurring improvement program

### PM scope and result

Established a PM, Staff Go engineering and independent QA workflow, scheduled
every two days at 09:00 America/Los_Angeles through the desktop app. A PM agent
reviewed current CLI journeys and returned the evidence-based backlog below.
These backlog findings are static-review hypotheses until reproduced by the
implementation cycle; they are not shipped fixes.

The first engineering task repaired the checkout installation journey. The
Makefile writes `bin/logc`, but README installation commands referred to `./logc`.
Both system and user-local installation examples now reference `./bin/logc`.
AGENTS.md and ITERATION_PLAYBOOK.md capture the operating rules for future runs.

### Verification evidence

- Staff Go engineer: `go test -race ./...`, `go vet ./...`, temporary-path build
  and `make build` passed on Go 1.22.2, darwin/arm64.
- The built binary and a copy installed into an isolated user-local directory
  both printed `logc dev (darwin/arm64)` and matched byte for byte.
- `git diff --check` passed. Production log files and services were not modified.
- Existing CI covers Ubuntu/macOS with Go 1.22 and stable; local validation does
  not establish the outcome of that remote matrix.
- Coordinator fetched origin/main and confirmed the pre-setup revision was
  `e19d309`. Its existing GitHub Actions run completed successfully:
  https://github.com/debugc-clis/logc/actions/runs/37269259861.
- A separate PM/reviewer approved the setup documents and installation correction
  after inspecting the README diff, Makefile and CI definitions.
- Landing-page commands already use Go installation and require no functional
  change for this checkout-path correction. No new live deployment is claimed.

### Next backlog

1. **Filter before discovery limits.** Reproduce category/module starvation with
   `max_files=2`, newer nonmatching logs and two eligible matching sources. Apply
   eligibility before fair selection across initial discovery, follow/watch
   rescans and bounded recent search. Preserve unfiltered behavior and budgets.
2. **Preserve search-follow selectors.** For directory, glob and configured-group
   searches with `-f`, preserve discovery scope so new matching active files join
   after a rescan. Keep rotated history out of live following, and do not broaden
   explicit single-file targets into sibling directories.
3. **Deterministic overlapping groups.** Reproduce classification changes caused
   by map iteration, then establish/document deterministic group precedence and
   verify metadata and category/module filters agree across commands.
4. **Streaming regression coverage.** Add same-inode copytruncate tests for
   follow/watch with partial-line carry, plus explicit producer-cancellation and
   process-reaping tests for Docker/system/journal streams.

### Publication

This entry describes setup and local verification. Use the setup run's commit
and remote-check report for publication status; do not infer CI or deployment
success from these local results. Later cycles should append their own commit,
push, CI and site evidence rather than rewriting historical claims.

## 2026-10-27 — Keep search-follow attached across deployments

### PM selection

Selected the previous backlog's search-follow selector fix: an SRE running
`logc /srv/api/log ERROR --since 30m -f` should discover a new active filename
created by a deployment, then show matching lines appended after discovery.
The PM agent chose this bounded incident-response improvement ahead of the
larger filter-before-limit change. Category/module discovery starvation remains
the next priority.

The starting checkout was clean and matched origin/main at `1a7f84a`. Existing
CI for that revision passed. GitHub had no open PRs; open issue #2 requested
Homebrew installation, which remains planned and is not advertised as available.
Work was performed in an isolated temporary clone to preserve the owner's
read-only checkout and avoid overlapping edits.

### Changes and acceptance evidence

- Staff Go engineer preserved directory, glob and configured-group selectors at
  the actual search-to-follow handoff. Historical concrete paths remain outside
  live following; an explicit single-file target stays narrow.
- The engineer first reproduced the failure: directory, glob and group cases
  reported one live source instead of two after a new file and rescan.
- The regression now passes nine scopes: directory, a directory containing
  `.log.` in its name, glob, group, single-file, active-path fallback, fuzzy
  resolution, process-path snapshot and default discovery. It checks initial
  archive search, source boundaries, filtering and exactly-once live appends.
- The targeted race regression passed 20 repetitions. Independent QA reviewed
  the final helper and reran all nine selector cases without blocking findings.
- Coordinator CLI smoke tests used the built binary, isolated configuration,
  real files and JSON stdout. Directory/glob/group each emitted a new-file error
  once; single-file emitted none from its sibling. All four preserved initial
  active/rotated/gzip search, suppressed nonmatching and live historical lines,
  and exited successfully on SIGTERM.
- Existing initial-content semantics are unchanged: a newly attached file's
  pre-discovery content is skipped. Only subsequent matching appends are
  promised. Follow still requires an active file when it starts.
- README and the landing-page search-follow example document the behavior and
  its limits, including a practical directory command.
- Browser validation also reproduced an existing mobile hero overflow caused
  by intrinsic grid sizing around the long Go install command. Zero-minimum
  grid tracks and a shrinkable hero column corrected page width from 463 px to
  390 px at a 390 px viewport. Desktop width remained 1280 px at a 1280 px
  viewport. Local browser screenshots checked light/dark themes and the updated
  command card; internal anchors resolved correctly.

### Validation and publication boundary

`go test -race ./...`, `go vet ./...`, a temporary-path build, changed-file
formatting checks and `git diff --check` passed locally on macOS. Independent
QA's related rotation/discovery/stream regressions also passed under race
testing. These results do not substitute for the remote Linux/macOS CI matrix.

This entry accompanies the implementation commit. Push, remote CI and live
website confirmation are checked after committing and reported separately by
the scheduled run; no new deployment success is assumed here.

### Remaining priorities

1. Reproduce and fix category/module filtering after discovery limits.
2. Establish deterministic precedence for overlapping configured groups.
3. Add copytruncate/partial-carry and explicit subprocess-cancellation coverage.
4. Continue Homebrew packaging work from issue #2 when install validation can be
   completed; keep the current under-construction notice until then.

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

Implementation commit `29f6f18` was pushed to origin/main. Its complete
Linux/macOS, Go 1.22/stable CI matrix passed:
https://github.com/debugc-clis/logc/actions/runs/40479154760.
The owner's original clean main checkout was fast-forwarded to that revision.

Live website confirmation is blocked by TLS configuration: `https://logc.com/`
resolved to `66.96.149.22` and presented a certificate with subject
`CN=*.bizland.com`; hostname verification failed with curl error 60. No TLS check
was bypassed, and no DNS, certificate or hosting configuration was changed.
The owner needs to verify the intended public domain and its existing hosting
binding/certificate. Page source changes are pushed; a successful live website
deployment is not confirmed. This evidence-only follow-up records remote
results separately from the implementation's local verification.

### Remaining priorities

1. Reproduce and fix category/module filtering after discovery limits.
2. Establish deterministic precedence for overlapping configured groups.
3. Add copytruncate/partial-carry and explicit subprocess-cancellation coverage.
4. Continue Homebrew packaging work from issue #2 when install validation can be
   completed; keep the current under-construction notice until then.

## 2026-10-29 — Reserve discovery capacity for matching sources

### PM selection and reproduction

Selected category/module starvation during default discovery. During a database
incident, newer unrelated application logs could consume every discovery slot
before `--category database` or `--module billing` was applied. Operators could
see an empty filtered stream even though eligible, recent logs existed.

The starting checkout was clean at `89f4100`, matched origin/main and had passing
CI. No open PRs were listed; Homebrew issue #2 remains open. Changes were made in
an isolated temporary clone because the owner's checkout was read-only to the
sandbox. PM and engineering responsibilities were delegated separately, with an
independent QA agent reviewing the implementation.

Coordinator reproduced the problem using the pre-change binary, `max_files=2`,
twelve newer application directories and two older-but-recent database sources.
All twelve CLI cases failed to show the eligible sources: category-only,
module-only and combined filters, each exercised through ordinary follow,
recent search, watch, and watch with `--since`.

### Changes

- Apply source eligibility before existing fair selection in default active
  discovery and bounded recent historical search. The active `max_files` limit
  and recent-search `5 * max_files` budget are unchanged.
- Propagate filters through the actual CLI resolution and watch history
  bootstrap. Follow/watch rescans use the same eligible candidate set.
- Evaluate quiet retained sources against filters before they consume remaining
  capacity. Keep existing recency/exclusion rules, fresh-source precedence and
  missing-path grace; do not reattach ineligible quiet sources.
- Count eligible candidates when issuing recent-search truncation warnings.
  Preserve filter list semantics and unlimited `--all` behavior.
- Update README and the landing page with `logc --category database` and explain
  why unrelated services no longer consume filtered discovery slots. Homebrew
  stays marked under construction.

### Validation evidence

- The same twelve coordinator CLI scenarios passed with the updated binary.
  Search/watch bootstrap included eligible rotated history; unrelated app lines
  stayed absent. Follow/watch discovered a new eligible file on rescan, watch
  source counts stayed within two, and streaming processes exited on SIGTERM.
- Go regressions cover category/module combinations, list/case semantics,
  default resolution and option propagation, eligible historical budgets and
  warnings, follow/watch discovery and exactly-once appends, watch bootstrap,
  quiet-file retention and unchanged unfiltered behavior.
- The final full local `go test -race ./...`, vet, a temporary-path build,
  formatting verification and `git diff --check` passed on macOS. The engineer's
  filtered regressions passed ten race repetitions, and independent QA approved
  the final tree after a fresh targeted race run covering filtering, historical
  discovery, follow/watch and the previous search-follow fix. Remote Linux/macOS
  CI is checked separately after pushing.
- Local browser checks passed at desktop 1280 px and mobile 390 px in light and
  dark modes. The expanded command description remained readable, page widths
  matched the viewports and internal navigation anchors resolved correctly.
- The existing `https://logc.com/` TLS hostname mismatch was rechecked and is
  unchanged (curl error 60). No TLS bypass, DNS or hosting changes were made;
  live deployment is not confirmed by these source updates.

### Publication and next work

Implementation commit `ba802aa` was pushed normally to origin/main. All four
Linux/macOS, Go 1.22/stable CI jobs passed, including race tests, vet and builds:
https://github.com/debugc-clis/logc/actions/runs/40847301305.
The owner's original clean main checkout was fast-forwarded to the published
revision. This documentation-only follow-up records the remote evidence.

Remaining product priorities are deterministic overlapping-group precedence,
copytruncate/partial-carry and explicit subprocess-cancellation regressions,
and validated Homebrew packaging. The previously reported live-domain TLS
issue remains an external blocker; website source changes are pushed, but a
successful live deployment is not confirmed.

## 2026-10-31 — Make overlapping source classification predictable

### PM selection and reproduction

Selected the next backlog item: overlapping groups could classify the same
file differently between metadata lookups because group matching iterated a
Go map. This could make `where --json`, emitted metadata and category/module
filtering disagree during an incident.

The starting checkout was clean at `50252b5`, matched origin/main and had
passing CI. No open PRs were listed; Homebrew issue #2 remains open. Coordinator
used an isolated temporary clone, PM confirmed the bounded scope, a Staff Go
engineer implemented the change, and a separate QA agent reviewed it.

Coordinator reproduced the defect with 128 real `where --json` invocations.
With two conflicting overlapping groups, one configuration order produced
55 `database/payments` results and 9 `app/other` results; reversed declaration
order produced 6 `database/payments` and 58 `app/other` results.

### Implemented behavior

- Match effective group names in ascending, case-sensitive lexical order and
  use the first matching group for category/module metadata. Built-in groups
  participate in the same ordering. Pattern matching itself is unchanged.
- Preserve explicit group selection: selecting a later group still resolves
  its own paths. Each `ls` configured-group row still describes its own settings.
- Preserve missing category/module defaults, ungrouped classification and
  case-insensitive category/module filters. Do not introduce specificity,
  declaration-order priority, caching, dependencies or new flags.
- Add a README overlap example and explain precedence in the landing-page
  configuration guide. Homebrew remains marked under construction.

### Validation evidence

- The updated binary returned `database/payments` for all 128 `where --json`
  samples across both configuration orders. All 32 real filtered searches
  selected the expected source and emitted consistent JSON metadata.
- Real explicit-group following retained both the overlapping and exclusive
  file, with each file's correct metadata, then exited successfully on SIGTERM.
  Source-list JSON retained each group's configured module and file count.
- Regression tests cover repeated forward/reverse map insertion, uppercase
  lexical precedence, directory/exact/glob overlaps, an earlier nonmatch,
  `where` JSON, source filters/default discovery, explicit selectors and
  nonoverlapping/built-in/ungrouped compatibility.
- Final `go test -race ./...`, `go vet ./...`, temporary-path build, formatting
  and `git diff --check` passed locally. Independent QA approved the patch after
  a fresh targeted race run with three repetitions.
- Static HTML parsing, unique IDs, internal anchors and new documentation text
  passed checks. Browser surfaces were unavailable (`iab` unavailable and an
  empty browser inventory), so desktop/mobile and light/dark rendering were
  not visually revalidated in this cycle. Only explanatory page text changed.
- `https://logc.com/` still fails TLS hostname verification with curl error 60.
  No TLS bypass or DNS/hosting changes were made; live deployment is unverified.

### Publication and next work

Implementation commit `30de72c` was pushed normally to origin/main and the
owner's original clean main checkout was fast-forwarded to that revision.
Its complete Linux/macOS, Go 1.22/stable CI matrix passed:
https://github.com/debugc-clis/logc/actions/runs/41187404462.
This documentation-only follow-up records the remote result.

Next priorities are copytruncate/partial-carry and explicit subprocess-
cancellation coverage, then validated Homebrew packaging. The existing domain
TLS issue remains an external deployment blocker; page source is published,
but neither a new live deployment nor visual page validation is claimed.

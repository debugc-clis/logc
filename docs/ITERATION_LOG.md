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

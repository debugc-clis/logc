# Recurring product and engineering iteration

## Goal and cadence

Run an improvement cycle every two days. The local Codex scheduled task returns
to the project thread at 09:00 America/Los_Angeles. The computer must be on, the
desktop app running, and the checkout, Go toolchain and Git access available.
The actual schedule is managed in the app; this document defines how each run
works. A scheduled run is not a guarantee of a shipped change or deployment.

Deliver small, tested improvements that make incident response easier. A run
should aim for one or two concrete deliverables, sized so implementation, review,
documentation and verification can all finish in that run. Record a blocker or
an evidence-based no-change outcome rather than making cosmetic filler commits.

## Roles and execution

1. **Coordinator:** inspect the checkout, current origin/main, recent commits,
   available issues, pull requests, CI and the last iteration entry. Detect an
   overlapping run before edits. Preserve dirty work and use an isolated worktree
   where needed. Do not publish from a checkout that contains unreviewed changes.
2. **PM agent:** examine actual SRE journeys and rank opportunities by incident
   value, frequency, evidence, implementation cost and regression risk. State the
   scenario, current gap, chosen scope and observable acceptance criteria before
   development. Prioritize broken behavior and failing CI over new features.
3. **Staff Go engineer agents:** implement the selected work in bounded tasks with
   disjoint write scopes. Reproduce failures, fix root causes, preserve existing
   CLI/config contracts and add meaningful tests. Report files changed, tests,
   assumptions and limitations. The coordinator integrates and reviews results.
4. **Independent QA/reviewer:** test the acceptance criteria and relevant edge
   cases, inspect the patch, and check command/documentation agreement. Return
   concrete failures to engineering and require a retest before publication.
5. **Coordinator:** run final validation, update product descriptions and the
   iteration log, commit intended files, push and check available remote results.

Use real delegation tools for separate agents. If unavailable, execute these
roles sequentially and describe them honestly. Do useful independent work while
delegates run. Do not duplicate their edits or let multiple runs write the same
files concurrently.

## Product priorities

- Find the latest relevant logs quickly, including useful source discovery.
- Follow multiple files correctly through append, rename/create, truncation and
  new-file discovery while bounding memory, buffers and open descriptors.
- Keep search, time windows, context, severity colors and shell pipelines useful.
- Classify sources by application, module, system, web/network, database and
  container without hiding the original path or operator control.
- Make installation, configuration, empty results and errors easy to understand.
- Keep Docker/systemd integrations predictable and document native limitations.

Every proposed feature needs a realistic command and an observable SRE benefit.
Use current code and tests as evidence. Do not invent customer feedback, usage
metrics, demand or claimed performance improvements. Keep AI log interpretation,
remote log uploading and a centralized analysis platform outside this scope.

## Definition of done

- Selected acceptance criteria are implemented and independently reviewed.
- Relevant regression/CLI tests and the full race suite pass.
- `go vet ./...`, a temporary-path build, formatting checks and
  `git diff --check` pass.
- For streaming changes, check relevant cancellation, backpressure, large-line,
  rotation and cleanup cases with isolated fixtures.
- README and the landing page accurately describe changed user-visible behavior.
  Use verified commands and clearly label illustrative output. Keep planned work
  and the unavailable Homebrew installation separate from available features.
- For page changes, check relevant links, desktop/mobile layout and light/dark
  rendering with available browser tools. If browser validation is unavailable,
  record that limitation rather than claiming visual verification.
- Append a dated entry to docs/ITERATION_LOG.md and keep the next backlog concise.
- Review staged files, commit the intended changes, and perform a normal
  fast-forward push to origin/main. Fetch/reconcile concurrent remote changes and
  rerun affected checks. Never force-push, delete user work, or rewrite history.
- Check CI and existing site deployment when accessible. Track local pass,
  pushed commit, CI success and confirmed live deployment as separate outcomes.
  A successful push alone does not prove that the website is live.

## Deployment and blockers

Use the existing GitHub and Cloudflare setup. Do not create duplicate hosting
projects or change production infrastructure to make a page update. Do not add
paid services or publish unverified release tags as part of routine iteration.
If authentication, network, permissions or CI infrastructure prevents a step,
finish independent work, retain reviewable changes, and report the exact blocked
step and the minimum action needed. Never label a failed check as passed.

## Run report

Return a concise Chinese report with the SRE scenario, completed behavior,
verification evidence, commit and push status, CI/site status, next priority and
any concrete blocker. Append durable findings to the iteration log so subsequent
runs do not repeat completed work or forget known issues.

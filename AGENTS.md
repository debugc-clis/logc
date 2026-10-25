# logc engineering instructions

## Product intent

logc helps Linux SREs find recent logs, aggregate sources, search, highlight and
follow events while doing their own analysis. Keep the default command useful
during an incident. Preserve read-only access to logs, bounded resource usage,
predictable CLI behavior, and Linux/macOS compatibility. Do not turn the product
into an AI analysis service or a centralized logging platform.

## Implementation and verification

- Read the affected code and reproduce the behavior before changing it.
- Prefer small changes that fix root causes and preserve CLI/config compatibility.
- Add behavioral regression tests for defects and meaningful features. Use
  isolated temporary logs and test producers rather than production services.
- Keep dependencies minimal; justify introducing an external dependency.
- Format changed Go files. Before publishing run targeted tests, then
  `go test -race ./...`, `go vet ./...`, `go build -o /tmp/logc-check .`, and
  `git diff --check`. Use a unique temporary build path for concurrent runs.
- Test rotation, truncation, discovery, cancellation and large lines when the
  change affects those behaviors. Distinguish local tests from Linux CI results.
- Update README.md and index.html when user-facing behavior changes. Examples
  must match the implemented command syntax and actual output. Homebrew remains
  under construction until its installation has been published and verified.

## Recurring iteration

The owner authorizes a recurring PM, Staff Go engineering and independent QA
workflow. Follow docs/ITERATION_PLAYBOOK.md for scheduled runs. Delegate bounded
tasks with disjoint write scopes when multi-agent tools are available; otherwise
perform the roles sequentially and report that limitation.

The recurring workflow is authorized to commit and normally push verified changes
to origin/main. Preserve unrelated user work, stage only intended files, and
never force-push or reset another contributor's changes. Do not publish a feature
or claim a deployment succeeded without supporting evidence.

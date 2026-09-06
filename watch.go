package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	alertRateWindow      = time.Minute
	maxAlertGroups       = 200
	maxAlertEvents       = 100_000
	maxRenderedAlertRows = 20
)

var (
	alertTimestampPrefix = regexp.MustCompile(`^\s*(?:\d{4}[-/]\d{2}[-/]\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?|[A-Z][a-z]{2}\s+\d{1,2}\s+\d{2}:\d{2}:\d{2})\s*`)
	alertUUID            = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\b`)
	alertLongHex         = regexp.MustCompile(`(?i)\b(?:0x)?[0-9a-f]{12,}\b`)
	alertDynamicField    = regexp.MustCompile(`(?i)\b(request_id|trace_id|span_id|correlation_id|transaction_id|job_id|task_id)=\S+`)
)

type alertSummary struct {
	Source      string
	Line        string
	Key         string
	Count       int
	First       time.Time
	Last        time.Time
	Approximate bool
}

type alertTracker struct {
	alerts     map[string]*alertSummary
	events     []time.Time
	rateCapped bool
	nextEvent  int
}

func newAlertTracker() *alertTracker {
	return &alertTracker{alerts: map[string]*alertSummary{}}
}

func (t *alertTracker) add(source, line string, at time.Time) {
	t.addEvent(source, line, at, false)
}

func (t *alertTracker) addEvent(source, line string, at time.Time, approximate bool) {
	signature := normalizeAlertLine(line)
	key := source + "\x00" + signature
	alert := t.alerts[key]
	if alert == nil {
		if len(t.alerts) >= maxAlertGroups {
			t.evictOldestAlert()
		}
		alert = &alertSummary{Source: source, Line: line, Key: signature, First: at}
		t.alerts[key] = alert
	}
	alert.Line = line
	alert.Count++
	alert.Last = at
	alert.Approximate = alert.Approximate || approximate
	if len(t.events) < maxAlertEvents {
		t.events = append(t.events, at)
	} else {
		t.events[t.nextEvent] = at
		t.nextEvent = (t.nextEvent + 1) % len(t.events)
		t.rateCapped = true
	}
}

func normalizeAlertLine(line string) string {
	line = alertTimestampPrefix.ReplaceAllString(strings.TrimSpace(line), "")
	line = alertDynamicField.ReplaceAllString(line, "$1=<id>")
	line = alertUUID.ReplaceAllString(line, "<id>")
	line = alertLongHex.ReplaceAllString(line, "<id>")
	return strings.Join(strings.Fields(line), " ")
}

func (t *alertTracker) evictOldestAlert() {
	var oldestKey string
	var oldest time.Time
	for key, alert := range t.alerts {
		if oldestKey == "" || alert.Last.Before(oldest) {
			oldestKey, oldest = key, alert.Last
		}
	}
	delete(t.alerts, oldestKey)
}

func (t *alertTracker) prune(now time.Time) {
	cutoff := now.Add(-alertRateWindow)
	kept := t.events[:0]
	for _, event := range t.events {
		if !event.Before(cutoff) && !event.After(now.Add(5*time.Second)) {
			kept = append(kept, event)
		}
	}
	t.events = kept
	t.nextEvent = 0
	if len(t.events) < maxAlertEvents {
		t.rateCapped = false
	}
}

func (t *alertTracker) summaries(now time.Time) ([]alertSummary, int) {
	t.prune(now)
	items := make([]alertSummary, 0, len(t.alerts))
	for _, alert := range t.alerts {
		items = append(items, *alert)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Last.Equal(items[j].Last) {
			return items[i].Count > items[j].Count
		}
		return items[i].Last.After(items[j].Last)
	})
	return items, len(t.events)
}

type alertWatcher struct {
	cfg              Config
	patterns         []string
	excludes         []string
	query            Query
	states           map[string]*fileState
	tracker          *alertTracker
	failures         map[string]string
	bootstrapPaths   []string
	defaultDiscovery bool
	categories       []string
	modules          []string
	fullLines        bool
}

func newAlertWatcher(cfg Config, patterns, excludes, bootstrapPaths []string, query Query) *alertWatcher {
	return &alertWatcher{
		cfg: cfg, patterns: patterns, excludes: excludes, query: query,
		states: map[string]*fileState{}, tracker: newAlertTracker(), failures: map[string]string{},
		bootstrapPaths: bootstrapPaths,
	}
}

func (w *alertWatcher) reportFailure(path string, err error) {
	message := err.Error()
	if w.failures[path] == message {
		return
	}
	w.failures[path] = message
	fmt.Fprintf(os.Stderr, "logc watch: skipping %s: %s\n", sanitizeTerminalText(path), sanitizeTerminalText(err.Error()))
}

func (w *alertWatcher) clearFailure(path string) { delete(w.failures, path) }

func (w *alertWatcher) reportWarning(message string) {
	key := "warning:" + message
	if w.failures[key] == message {
		return
	}
	w.failures[key] = message
	fmt.Fprintf(os.Stderr, "logc watch: warning: %s\n", sanitizeTerminalText(message))
}

func (w *alertWatcher) observe(path string, lines []string, observedAt, fallbackTime time.Time) {
	for _, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		eventTime := fallbackTime
		timestamp, timestamped := lineTime(line)
		if timestamped {
			eventTime = timestamp
		}
		if eventTime.IsZero() {
			eventTime = observedAt
		}
		if !w.query.ignored(line) && w.query.match(line) && !eventTime.Before(w.query.Since) {
			w.tracker.addEvent(path, line, eventTime, !timestamped)
		}
	}
}

func watchLineEligible(line string, observed time.Time, query Query) bool {
	if query.Since.IsZero() {
		return true
	}
	if timestamp, ok := lineTime(line); ok {
		return !timestamp.Before(query.Since)
	}
	return !observed.Before(query.Since)
}

func (w *alertWatcher) bootstrap() {
	paths := append([]string(nil), w.bootstrapPaths...)
	sortHistorical(paths)
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			w.reportFailure(path, err)
			continue
		}
		w.clearFailure(path)
		_, err = scanLogPathEach(path, w.query, func(line string) {
			w.observe(path, []string{line}, time.Now(), info.ModTime())
		})
		if err != nil {
			w.reportFailure(path, err)
		}
	}
}

func (w *alertWatcher) addPath(path string, observeInitial bool) {
	if _, ok := w.states[path]; ok {
		return
	}
	lines, offset, info, err := readLastLines(path, w.cfg.Lines)
	if err != nil {
		w.reportFailure(path, err)
		return
	}
	w.clearFailure(path)
	w.states[path] = &fileState{Path: path, Info: info, Offset: offset}
	if observeInitial {
		w.observe(path, lines, time.Now(), info.ModTime())
	}
}

func (w *alertWatcher) rescan(observeInitial bool) {
	var paths []string
	var warnings []string
	var err error
	if w.defaultDiscovery {
		paths, warnings, err = discoverDefaultDetailed(w.cfg)
	} else {
		paths, err = resolvePatterns(w.patterns, w.excludes)
	}
	paths = filterSourcePaths(w.cfg, paths, w.categories, w.modules)
	if err != nil {
		w.reportFailure("log source scan", err)
		return
	}
	for _, warning := range warnings {
		w.reportWarning(warning)
	}
	w.clearFailure("log source scan")
	for _, path := range paths {
		w.addPath(path, observeInitial)
	}
	active := make(map[string]bool, len(paths))
	for _, path := range paths {
		active[path] = true
	}
	for path := range w.states {
		if !active[path] {
			delete(w.states, path)
			w.clearFailure(path)
		}
	}
}

func (w *alertWatcher) poll() {
	for path, state := range w.states {
		info, err := os.Stat(path)
		if err != nil {
			w.reportFailure(path, err)
			continue
		}
		w.clearFailure(path)
		if state.Info != nil && !os.SameFile(state.Info, info) {
			lines, offset, nextInfo, err := readLastLines(path, w.cfg.Lines)
			if err == nil {
				state.Info, state.Offset, state.Carry, state.CarryTruncated = nextInfo, offset, "", false
				w.observe(path, lines, time.Now(), nextInfo.ModTime())
			}
			if err != nil {
				w.reportFailure(path, err)
			}
			continue
		}
		if info.Size() < state.Offset {
			state.Offset, state.Carry, state.CarryTruncated = 0, "", false
		}
		if info.Size() == state.Offset {
			state.Info = info
			continue
		}
		data, err := readAppended(path, state.Offset, maxFollowReadBytes)
		if err != nil {
			w.reportFailure(path, err)
			continue
		}
		state.Offset += int64(len(data))
		state.Info = info
		parts, carry, carryTruncated := splitAppended(state.Carry, state.CarryTruncated, data)
		state.Carry, state.CarryTruncated = carry, carryTruncated
		observedAt := time.Now()
		w.observe(path, parts, observedAt, observedAt)
	}
}

func compactSourcePath(cfg Config, path string) string {
	base := filepath.Base(path)
	category, module := sourceMetadata(cfg, path)
	if strings.HasPrefix(path, "systemd:") {
		category, module = "system", strings.TrimPrefix(path, "systemd:")
	}
	return category + "/" + module + "/" + base
}

func truncateRunes(text string, limit int) string {
	if limit < 2 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit-1]) + "…"
}

func terminalColumns() int {
	if columns, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && columns >= 60 {
		return columns
	}
	return 120
}

func (w *alertWatcher) render(out io.Writer, color, clear bool) {
	now := time.Now()
	alerts, rate := w.tracker.summaries(now)
	if clear {
		fmt.Fprint(out, "\x1b[H\x1b[2J")
	}
	fmt.Fprintf(out, "logc watch %q  [%s]\n", sanitizeTerminalText(w.query.Pattern), now.Format("15:04:05"))
	rateLabel := fmt.Sprintf("%d", rate)
	if w.tracker.rateCapped {
		rateLabel += "+"
	}
	fmt.Fprintf(out, "%d alert groups · %s events/min · %d sources\n\n", len(alerts), rateLabel, len(w.states))
	if len(alerts) == 0 {
		fmt.Fprintln(out, "No matching events yet. Watching for new lines…")
	} else {
		fmt.Fprintln(out, "COUNT  FIRST     LAST      SOURCE                 ALERT")
		if len(alerts) > maxRenderedAlertRows {
			fmt.Fprintf(out, "Showing the %d most recent groups.\n", maxRenderedAlertRows)
			alerts = alerts[:maxRenderedAlertRows]
		}
		for _, alert := range alerts {
			source := truncateRunes(sanitizeTerminalText(compactSourcePath(w.cfg, alert.Source)), 28)
			line := sanitizeTerminalText(alert.Line)
			if !w.fullLines {
				line = truncateRunes(line, max(30, terminalColumns()-58))
			}
			if color {
				line = (&printer{color: true}).decorate(line)
			}
			fmt.Fprintf(out, "%-6d %-9s %-9s %-28s %s\n",
				alert.Count,
				formatAlertTime(alert.First, alert.Approximate),
				formatAlertTime(alert.Last, alert.Approximate),
				source,
				line,
			)
		}
	}
	fmt.Fprintln(out, "\nRefreshes every second · Press Ctrl+C to stop")
}

func formatAlertTime(timestamp time.Time, approximate bool) string {
	formatted := timestamp.Format("15:04:05")
	if approximate {
		return "~" + formatted
	}
	return formatted
}

func (w *alertWatcher) run(ctx context.Context, out io.Writer, color, clear bool) {
	if len(w.bootstrapPaths) > 0 {
		w.bootstrap()
	}
	w.rescan(len(w.bootstrapPaths) == 0)
	w.render(out, color, clear)
	pollTicker := time.NewTicker(250 * time.Millisecond)
	scanTicker := time.NewTicker(w.cfg.ScanInterval)
	renderTicker := time.NewTicker(time.Second)
	defer pollTicker.Stop()
	defer scanTicker.Stop()
	defer renderTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			w.render(out, color, clear)
			return
		case <-pollTicker.C:
			w.poll()
		case <-scanTicker.C:
			w.rescan(true)
		case <-renderTicker.C:
			w.render(out, color, clear)
		}
	}
}

func watchCommand(args []string) int {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "logc:", err)
		return 1
	}
	opts, err := parseCLI(args, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "logc watch:", err)
		return 2
	}
	if opts.JSON {
		fmt.Fprintln(os.Stderr, "logc watch: --json is not supported; use the terminal dashboard")
		return 2
	}
	if opts.All {
		fmt.Fprintln(os.Stderr, "logc watch: --all is not supported; use an explicit --since window")
		return 2
	}
	resolved, pattern, err := resolveWatchArgs(cfg, opts.Positionals, opts.Match)
	if err != nil {
		fmt.Fprintln(os.Stderr, "logc watch:", err)
		return 2
	}
	inputPattern := pattern
	pattern = severityPattern(pattern)
	since, err := parseSince(opts.SinceRaw)
	if err != nil {
		fmt.Fprintln(os.Stderr, "logc watch:", err)
		return 2
	}
	query, err := buildQuery(pattern, opts.IgnoreCase, since, 0, 0, false, cfg.IgnoreLines)
	if err != nil {
		fmt.Fprintln(os.Stderr, "logc watch:", err)
		return 2
	}
	query.Pattern = inputPattern
	cfg.Lines = opts.Lines
	cfg.Color = cfg.Color && !opts.NoColor
	excludes := append(append([]string(nil), cfg.Excludes...), opts.Excludes...)
	patterns := resolved.Patterns
	if len(patterns) == 0 {
		patterns = resolved.Paths
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	color := newPrinter(cfg.Color).color
	if resolved.JournalUnit != "" {
		if err := runJournalAlertWatch(ctx, resolved.JournalUnit, cfg, query, os.Stdout, color, stdoutIsTerminal(), opts.Full); err != nil && ctx.Err() == nil {
			fmt.Fprintln(os.Stderr, "logc watch:", err)
			return 1
		}
		return 0
	}
	var bootstrapPaths []string
	if !since.IsZero() {
		historical, _, historyErr := resolveWatchArgsWithHistory(cfg, opts.Positionals, opts.Match, true, since)
		if historyErr != nil {
			fmt.Fprintln(os.Stderr, "logc watch:", historyErr)
			return 2
		}
		bootstrapPaths = filterSourcePaths(cfg, historical.Paths, opts.Categories, opts.Modules)
	}
	w := newAlertWatcher(cfg, patterns, excludes, bootstrapPaths, query)
	w.defaultDiscovery = resolved.DefaultDiscovery
	w.categories, w.modules = opts.Categories, opts.Modules
	w.fullLines = opts.Full
	w.run(ctx, os.Stdout, color, stdoutIsTerminal())
	return 0
}

func resolveWatchArgs(cfg Config, positionals []string, explicitMatch string) (ResolvedTarget, string, error) {
	return resolveWatchArgsWithHistory(cfg, positionals, explicitMatch, false, time.Time{})
}

func resolveWatchArgsWithHistory(cfg Config, positionals []string, explicitMatch string, includeHistory bool, cutoff time.Time) (ResolvedTarget, string, error) {
	if explicitMatch != "" {
		resolved, err := resolveWatchTargetsWithHistory(cfg, positionals, includeHistory, cutoff)
		return resolved, explicitMatch, err
	}
	if len(positionals) == 0 {
		return ResolvedTarget{}, "", fmt.Errorf("usage: logc watch REGEX [TARGET...]")
	}

	// Keep the original TARGET REGEX form working for existing users. The new
	// preferred form puts the regex first so it can accept any number of targets.
	if len(positionals) == 2 && !looksLikeSearchExpression(positionals[0]) && looksLikeSearchExpression(positionals[1]) {
		resolved, err := resolveWatchTargetsWithHistory(cfg, positionals[:1], includeHistory, cutoff)
		if err == nil {
			return resolved, positionals[1], nil
		}
	}

	resolved, err := resolveWatchTargetsWithHistory(cfg, positionals[1:], includeHistory, cutoff)
	if err != nil {
		return ResolvedTarget{}, "", err
	}
	return resolved, positionals[0], nil
}

func resolveWatchTargets(cfg Config, rawTargets []string) (ResolvedTarget, error) {
	return resolveWatchTargetsWithHistory(cfg, rawTargets, false, time.Time{})
}

func resolveWatchTargetsWithHistory(cfg Config, rawTargets []string, includeHistory bool, cutoff time.Time) (ResolvedTarget, error) {
	targets := splitWatchTargets(rawTargets)
	if len(targets) == 0 {
		return resolveDefaultTarget(cfg, includeHistory, cutoff)
	}
	resolved, consumed, err := resolveLeadingTargets(cfg, targets, includeHistory)
	if err != nil {
		return ResolvedTarget{}, err
	}
	if consumed != len(targets) {
		return ResolvedTarget{}, fmt.Errorf("no log source matched %q", targets[consumed])
	}
	return resolved, nil
}

func runJournalAlertWatch(ctx context.Context, unit string, cfg Config, query Query, out io.Writer, color, clear, fullLines bool) error {
	command, err := unitCommand(ctx, unit, cfg.Lines, true, query)
	if err != nil {
		return err
	}
	pipe, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		return err
	}

	source := "systemd:" + unit
	watcher := newAlertWatcher(cfg, nil, nil, nil, query)
	watcher.fullLines = fullLines
	watcher.states[source] = &fileState{Path: source}
	lines := make(chan string, 1024)
	done := make(chan error, 1)
	go scanCommandLines(ctx, pipe, command, lines, done)
	renderTicker := time.NewTicker(time.Second)
	defer renderTicker.Stop()
	watcher.render(out, color, clear)
	for {
		select {
		case <-ctx.Done():
			watcher.render(out, color, clear)
			return ctx.Err()
		case line, ok := <-lines:
			if !ok {
				lines = nil
				continue
			}
			observedAt := time.Now()
			watcher.observe(source, []string{line}, observedAt, observedAt)
		case runErr := <-done:
			watcher.render(out, color, clear)
			return runErr
		case <-renderTicker.C:
			watcher.render(out, color, clear)
		}
	}
}

func scanCommandLines(ctx context.Context, reader io.Reader, command *exec.Cmd, lines chan<- string, done chan<- error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxCarryBytes)
	for scanner.Scan() {
		select {
		case lines <- strings.TrimSuffix(scanner.Text(), "\r"):
		case <-ctx.Done():
			_ = command.Wait()
			close(lines)
			done <- ctx.Err()
			return
		}
	}
	close(lines)
	if err := scanner.Err(); err != nil {
		_ = command.Wait()
		done <- err
		return
	}
	done <- command.Wait()
}

func splitWatchTargets(rawTargets []string) []string {
	var targets []string
	for _, raw := range rawTargets {
		for _, target := range strings.Split(raw, ",") {
			if target = strings.TrimSpace(target); target != "" {
				targets = append(targets, target)
			}
		}
	}
	return targets
}

func stdoutIsTerminal() bool {
	info, err := os.Stdout.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

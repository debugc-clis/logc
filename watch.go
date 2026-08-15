package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
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

type alertSummary struct {
	Source string
	Line   string
	Count  int
	First  time.Time
	Last   time.Time
}

type alertTracker struct {
	alerts     map[string]*alertSummary
	events     []time.Time
	rateCapped bool
}

func newAlertTracker() *alertTracker {
	return &alertTracker{alerts: map[string]*alertSummary{}}
}

func (t *alertTracker) add(source, line string, at time.Time) {
	key := source + "\x00" + line
	alert := t.alerts[key]
	if alert == nil {
		if len(t.alerts) >= maxAlertGroups {
			t.evictOldestAlert()
		}
		alert = &alertSummary{Source: source, Line: line, First: at}
		t.alerts[key] = alert
	}
	alert.Count++
	alert.Last = at
	if len(t.events) < maxAlertEvents {
		t.events = append(t.events, at)
	} else {
		t.rateCapped = true
	}
	t.prune(at)
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
	first := 0
	for first < len(t.events) && t.events[first].Before(cutoff) {
		first++
	}
	if first > 0 {
		t.events = append([]time.Time(nil), t.events[first:]...)
	}
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
	cfg      Config
	patterns []string
	excludes []string
	query    Query
	states   map[string]*fileState
	tracker  *alertTracker
	failures map[string]string
}

func newAlertWatcher(cfg Config, patterns, excludes []string, query Query) *alertWatcher {
	return &alertWatcher{
		cfg: cfg, patterns: patterns, excludes: excludes, query: query,
		states: map[string]*fileState{}, tracker: newAlertTracker(), failures: map[string]string{},
	}
}

func (w *alertWatcher) reportFailure(path string, err error) {
	message := err.Error()
	if w.failures[path] == message {
		return
	}
	w.failures[path] = message
	fmt.Fprintf(os.Stderr, "logc watch: skipping %s: %v\n", path, err)
}

func (w *alertWatcher) clearFailure(path string) { delete(w.failures, path) }

func (w *alertWatcher) observe(path string, lines []string, at time.Time) {
	for _, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		if !w.query.ignored(line) && w.query.match(line) && watchLineEligible(line, at, w.query) {
			w.tracker.add(path, line, at)
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

func (w *alertWatcher) addPath(path string) {
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
	w.observe(path, lines, time.Now())
}

func (w *alertWatcher) rescan() {
	paths, err := resolvePatterns(w.patterns, w.excludes)
	if err != nil {
		w.reportFailure("log source scan", err)
		return
	}
	w.clearFailure("log source scan")
	for _, path := range paths {
		w.addPath(path)
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
				w.observe(path, lines, time.Now())
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
		w.observe(path, parts, time.Now())
	}
}

func (w *alertWatcher) render(out io.Writer, color, clear bool) {
	now := time.Now()
	alerts, rate := w.tracker.summaries(now)
	if clear {
		fmt.Fprint(out, "\x1b[H\x1b[2J")
	}
	fmt.Fprintf(out, "logc watch %q  [%s]\n", w.query.Pattern, now.Format("15:04:05"))
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
			line := alert.Line
			if color {
				line = (&printer{color: true}).decorate(line)
			}
			fmt.Fprintf(out, "%-6d %-9s %-9s %-22s %s\n",
				alert.Count,
				alert.First.Format("15:04:05"),
				alert.Last.Format("15:04:05"),
				filepath.Base(alert.Source),
				line,
			)
		}
	}
	fmt.Fprintln(out, "\nRefreshes every second · Press Ctrl+C to stop")
}

func (w *alertWatcher) run(ctx context.Context, out io.Writer, color, clear bool) {
	w.rescan()
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
			w.rescan()
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
	w := newAlertWatcher(cfg, patterns, excludes, query)
	w.run(ctx, os.Stdout, newPrinter(cfg.Color).color, stdoutIsTerminal())
	return 0
}

func resolveWatchArgs(cfg Config, positionals []string, explicitMatch string) (ResolvedTarget, string, error) {
	if explicitMatch != "" {
		resolved, err := resolveWatchTargets(cfg, positionals)
		return resolved, explicitMatch, err
	}
	if len(positionals) == 0 {
		return ResolvedTarget{}, "", fmt.Errorf("usage: logc watch REGEX [TARGET...]")
	}

	// Keep the original TARGET REGEX form working for existing users. The new
	// preferred form puts the regex first so it can accept any number of targets.
	if len(positionals) == 2 && !looksLikeSearchExpression(positionals[0]) && looksLikeSearchExpression(positionals[1]) {
		resolved, err := resolveWatchTargets(cfg, positionals[:1])
		if err == nil {
			return resolved, positionals[1], nil
		}
	}

	resolved, err := resolveWatchTargets(cfg, positionals[1:])
	if err != nil {
		return ResolvedTarget{}, "", err
	}
	return resolved, positionals[0], nil
}

func resolveWatchTargets(cfg Config, rawTargets []string) (ResolvedTarget, error) {
	targets := splitWatchTargets(rawTargets)
	if len(targets) == 0 {
		return resolveTarget(cfg, "", false)
	}
	resolved, consumed, err := resolveLeadingTargets(cfg, targets, false)
	if err != nil {
		return ResolvedTarget{}, err
	}
	if consumed != len(targets) {
		return ResolvedTarget{}, fmt.Errorf("no log source matched %q", targets[consumed])
	}
	if resolved.JournalUnit != "" {
		return ResolvedTarget{}, fmt.Errorf("systemd journal targets are not supported by logc watch")
	}
	return resolved, nil
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

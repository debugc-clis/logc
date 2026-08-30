package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAlertTrackerCountsDuplicatesAndRate(t *testing.T) {
	tracker := newAlertTracker()
	now := time.Date(2026, time.August, 13, 10, 0, 0, 0, time.Local)
	tracker.add("/srv/api.log", "ERROR timeout", now.Add(-10*time.Second))
	tracker.add("/srv/api.log", "ERROR timeout", now.Add(-5*time.Second))
	tracker.add("/srv/worker.log", "ERROR job failed", now.Add(-2*time.Second))

	alerts, rate := tracker.summaries(now)
	if rate != 3 || len(alerts) != 2 {
		t.Fatalf("alerts=%#v rate=%d", alerts, rate)
	}
	if alerts[1].Line != "ERROR timeout" || alerts[1].Count != 2 {
		t.Fatalf("duplicate alert=%#v", alerts[1])
	}
	if !alerts[1].First.Equal(now.Add(-10*time.Second)) || !alerts[1].Last.Equal(now.Add(-5*time.Second)) {
		t.Fatalf("timestamps=%#v", alerts[1])
	}
}

func TestAlertTrackerPrunesEventsOutsideRateWindow(t *testing.T) {
	tracker := newAlertTracker()
	now := time.Date(2026, time.August, 13, 10, 0, 0, 0, time.Local)
	tracker.add("/srv/api.log", "ERROR old", now.Add(-2*time.Minute))
	tracker.add("/srv/api.log", "ERROR current", now.Add(-time.Second))
	_, rate := tracker.summaries(now)
	if rate != 1 {
		t.Fatalf("rate=%d, want 1", rate)
	}
}

func TestAlertTrackerPrunesOutOfOrderEvents(t *testing.T) {
	tracker := newAlertTracker()
	now := time.Date(2026, time.August, 13, 10, 0, 0, 0, time.Local)
	tracker.add("/srv/api.log", "ERROR current", now.Add(-time.Second))
	tracker.add("/srv/old.log", "ERROR old", now.Add(-2*time.Minute))
	tracker.add("/srv/worker.log", "ERROR current", now.Add(-2*time.Second))
	_, rate := tracker.summaries(now)
	if rate != 2 {
		t.Fatalf("rate=%d, want 2", rate)
	}
}

func TestAlertTrackerBoundsUniqueGroups(t *testing.T) {
	tracker := newAlertTracker()
	now := time.Date(2026, time.August, 13, 10, 0, 0, 0, time.Local)
	for i := 0; i < maxAlertGroups+1; i++ {
		tracker.add("/srv/api.log", fmt.Sprintf("ERROR request=%d", i), now.Add(time.Duration(i)*time.Second))
	}
	if len(tracker.alerts) != maxAlertGroups {
		t.Fatalf("groups=%d, want %d", len(tracker.alerts), maxAlertGroups)
	}
}

func TestAlertTrackerGroupsDynamicRequestIDs(t *testing.T) {
	tracker := newAlertTracker()
	now := time.Date(2026, time.August, 25, 10, 0, 0, 0, time.Local)
	tracker.add("/srv/api.log", "2026-08-25 10:00:00 ERROR request_id=req-100 upstream timeout", now)
	tracker.add("/srv/api.log", "2026-08-25 10:00:01 ERROR request_id=req-101 upstream timeout", now.Add(time.Second))

	alerts, rate := tracker.summaries(now.Add(time.Second))
	if rate != 2 || len(alerts) != 1 || alerts[0].Count != 2 {
		t.Fatalf("alerts=%#v rate=%d", alerts, rate)
	}
	if alerts[0].Key != "ERROR request_id=<id> upstream timeout" {
		t.Fatalf("key=%q", alerts[0].Key)
	}
}

func TestWatchLineEligibleHonorsSince(t *testing.T) {
	since := time.Date(2026, time.August, 13, 10, 0, 0, 0, time.Local)
	query := Query{Since: since}
	if watchLineEligible("2026-08-13 09:59:59 ERROR old", since.Add(time.Minute), query) {
		t.Fatal("old timestamped line was accepted")
	}
	if !watchLineEligible("2026-08-13 10:00:01 ERROR current", since, query) {
		t.Fatal("current timestamped line was rejected")
	}
}

func TestAlertWatcherUsesEventTimestamp(t *testing.T) {
	query, err := buildQuery("ERROR", false, time.Time{}, 0, 0, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	watcher := newAlertWatcher(defaultConfig(), nil, nil, nil, query)
	eventTime := time.Date(2026, time.August, 13, 9, 55, 0, 0, time.Local)
	watcher.observe("/srv/api.log", []string{"2026-08-13 09:55:00 ERROR timeout"}, eventTime.Add(5*time.Minute), time.Time{})
	alerts, _ := watcher.tracker.summaries(eventTime.Add(5 * time.Minute))
	if len(alerts) != 1 || !alerts[0].First.Equal(eventTime) || !alerts[0].Last.Equal(eventTime) {
		t.Fatalf("alerts=%#v", alerts)
	}
}

func TestAlertWatcherBootstrapsAllLinesSince(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "api.log")
	var lines []string
	for index := 0; index < 20; index++ {
		lines = append(lines, fmt.Sprintf("2026-08-13 10:00:%02d ERROR failure", index))
	}
	for index := 0; index < 10; index++ {
		lines = append(lines, fmt.Sprintf("2026-08-13 10:01:%02d INFO healthy", index))
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	query, err := buildQuery("ERROR", false, time.Date(2026, time.August, 13, 10, 0, 0, 0, time.Local), 0, 0, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	watcher := newAlertWatcher(defaultConfig(), []string{path}, nil, []string{path}, query)
	watcher.bootstrap()
	alerts, _ := watcher.tracker.summaries(time.Date(2026, time.August, 13, 10, 2, 0, 0, time.Local))
	if len(alerts) != 1 || alerts[0].Count != 20 {
		t.Fatalf("alerts=%#v", alerts)
	}
}

func TestResolveWatchArgsSupportsMultiplePathsAndCommaSeparatedTargets(t *testing.T) {
	directory := t.TempDir()
	first := writeWatchTestLog(t, directory, "api.log")
	second := writeWatchTestLog(t, directory, "worker.log")
	third := writeWatchTestLog(t, directory, "proxy.log")
	cfg := defaultConfig()
	cfg.Excludes = nil

	resolved, pattern, err := resolveWatchArgs(cfg, []string{"error", first + "," + second, third}, "")
	if err != nil {
		t.Fatal(err)
	}
	if pattern != "error" {
		t.Fatalf("pattern=%q", pattern)
	}
	if len(resolved.Paths) != 3 {
		t.Fatalf("paths=%#v", resolved.Paths)
	}
}

func TestResolveWatchArgsKeepsLegacyTargetFirstSyntax(t *testing.T) {
	directory := t.TempDir()
	path := writeWatchTestLog(t, directory, "api.log")
	cfg := defaultConfig()
	cfg.Excludes = nil

	resolved, pattern, err := resolveWatchArgs(cfg, []string{path, "timeout|reset"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if pattern != "timeout|reset" || len(resolved.Paths) != 1 || resolved.Paths[0] != path {
		t.Fatalf("pattern=%q paths=%#v", pattern, resolved.Paths)
	}
}

func writeWatchTestLog(t *testing.T, directory, name string) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte("ERROR test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

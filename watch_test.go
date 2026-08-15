package main

import (
	"fmt"
	"os"
	"path/filepath"
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

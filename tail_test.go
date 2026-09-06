package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadLastLines(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "x.log")
	if err := os.WriteFile(p, []byte("1\n2\n3\n4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _, _, err := readLastLines(p, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "3" || got[1] != "4" {
		t.Fatalf("got %#v", got)
	}
}

func TestSplitAppendedTruncatesCompletedLongLine(t *testing.T) {
	line := strings.Repeat("x", maxCarryBytes+1024) + "\n"
	lines, carry, truncated := splitAppended("", false, []byte(line))
	if len(lines) != 1 || carry != "" || truncated {
		t.Fatalf("lines=%d carry=%d truncated=%t", len(lines), len(carry), truncated)
	}
	if !strings.HasPrefix(lines[0], "[truncated long line] ") || len(lines[0]) > maxCarryBytes+64 {
		t.Fatalf("long line was not bounded: length=%d", len(lines[0]))
	}
}

func TestDedupStreamLinesAcrossBatches(t *testing.T) {
	state := &fileState{}
	first := dedupStreamLines(state, []string{"ERROR repeated"}, true)
	second := dedupStreamLines(state, []string{"ERROR repeated", "ERROR repeated", "INFO recovered"}, true)
	if len(first) != 1 || len(second) != 2 || second[0] != "↳ previous line repeated 2 additional times" || second[1] != "INFO recovered" {
		t.Fatalf("first=%#v second=%#v", first, second)
	}
}

func TestDefaultFollowerRediscoversNewFiles(t *testing.T) {
	directory := t.TempDir()
	first := filepath.Join(directory, "first.log")
	if err := os.WriteFile(first, []byte("INFO first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	config := defaultConfig()
	config.DefaultLogDirs = []string{directory}
	config.Excludes = nil
	config.Recent = time.Hour
	follower := newFollower(config, config.DefaultLogDirs, nil, &printer{}, Query{}, false)
	follower.defaultDiscovery = true
	paths, _, err := follower.resolvePaths()
	if err != nil || len(paths) != 1 {
		t.Fatalf("initial paths=%#v err=%v", paths, err)
	}
	second := filepath.Join(directory, "second.log")
	if err := os.WriteFile(second, []byte("INFO second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	paths, _, err = follower.resolvePaths()
	if err != nil || len(paths) != 2 {
		t.Fatalf("rescanned paths=%#v err=%v", paths, err)
	}
}

func TestFollowerReadsOldAndNewFilesAcrossRotation(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "app.log")
	if err := os.WriteFile(path, []byte("INFO initial\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writer, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	config := defaultConfig()
	config.MaxBufferLines = 100
	follower := newFollower(config, []string{path}, nil, &printer{}, Query{}, true)
	follower.addPath(path, false)
	defer follower.closeStates()
	if follower.states[path] == nil || follower.states[path].File == nil {
		t.Fatal("follower did not keep the active log file open")
	}

	rotated := path + ".1"
	if err := os.Rename(path, rotated); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteString("ERROR final line from old file\n"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("INFO first line from new file\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	follower.poll()
	pending := strings.Join(follower.states[path].Pending, "\n")
	for _, expected := range []string{
		"ERROR final line from old file",
		"↻ file rotated/replaced",
		"INFO first line from new file",
	} {
		if !strings.Contains(pending, expected) {
			t.Fatalf("pending output %q does not contain %q", pending, expected)
		}
	}
}

func TestFollowerFlushesPartialLineDuringRotation(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "partial.log")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	config := defaultConfig()
	follower := newFollower(config, []string{path}, nil, &printer{}, Query{}, true)
	follower.addPath(path, false)
	defer follower.closeStates()

	if err := os.WriteFile(path, []byte("WARN partial before rotation"), 0o644); err != nil {
		t.Fatal(err)
	}
	follower.poll()
	if follower.states[path].Carry == "" {
		t.Fatal("expected unterminated line to remain in carry")
	}
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("INFO replacement\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	follower.poll()
	pending := strings.Join(follower.states[path].Pending, "\n")
	if !strings.Contains(pending, "WARN partial before rotation") {
		t.Fatalf("partial line was lost during rotation: %q", pending)
	}
}

func TestDefaultFollowerRetainsQuietExistingFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "quiet.log")
	if err := os.WriteFile(path, []byte("INFO quiet\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	config := defaultConfig()
	config.DefaultLogDirs = []string{directory}
	config.Excludes = nil
	config.Recent = time.Second
	follower := newFollower(config, config.DefaultLogDirs, nil, &printer{}, Query{}, true)
	follower.defaultDiscovery = true
	follower.addPath(path, false)
	defer follower.closeStates()

	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	follower.rescan()
	if follower.states[path] == nil {
		t.Fatal("default follower removed a quiet file solely because it aged beyond recent")
	}
}

func TestFollowerPendingBufferRemainsBounded(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "hot.log")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	config := defaultConfig()
	config.MaxBufferLines = 10
	follower := newFollower(config, []string{path}, nil, &printer{}, Query{}, true)
	follower.addPath(path, false)
	defer follower.closeStates()

	var content strings.Builder
	for index := 0; index < 100; index++ {
		content.WriteString("INFO event\n")
	}
	if err := os.WriteFile(path, []byte(content.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	follower.poll()
	state := follower.states[path]
	if len(state.Pending) != config.MaxBufferLines {
		t.Fatalf("pending lines=%d, want %d", len(state.Pending), config.MaxBufferLines)
	}
	if state.Dropped != 90 {
		t.Fatalf("dropped lines=%d, want 90", state.Dropped)
	}
}

func TestFollowerPendingBytesRemainBounded(t *testing.T) {
	config := defaultConfig()
	config.MaxBufferLines = 2000
	follower := newFollower(config, nil, nil, &printer{}, Query{}, true)
	state := &fileState{}
	line := strings.Repeat("x", 128*1024)
	for index := 0; index < 100; index++ {
		state.Pending = append(state.Pending, line)
	}
	follower.capPending(state)

	pendingBytes := 0
	for _, pending := range state.Pending {
		pendingBytes += len(pending) + 1
	}
	if pendingBytes > maxPendingBytes {
		t.Fatalf("pending bytes=%d, limit=%d", pendingBytes, maxPendingBytes)
	}
	if state.Dropped == 0 {
		t.Fatal("byte limit did not record dropped lines")
	}
}

func TestFollowerClosesFileWhenSourceIsRemoved(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "removed.log")
	if err := os.WriteFile(path, []byte("INFO active\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	config := defaultConfig()
	config.DefaultLogDirs = []string{directory}
	config.Excludes = nil
	follower := newFollower(config, config.DefaultLogDirs, nil, &printer{}, Query{}, true)
	follower.defaultDiscovery = true
	follower.addPath(path, false)
	state := follower.states[path]
	if state == nil || state.File == nil {
		t.Fatal("follower did not open source")
	}

	follower.cfg.Excludes = []string{path}
	follower.rescan()
	if follower.states[path] != nil {
		t.Fatal("excluded source was not removed")
	}
	if state.File != nil {
		t.Fatal("removed source file was not closed")
	}
}

func TestPruneFailureCacheRemovesInactiveEntries(t *testing.T) {
	failures := map[string]string{
		"/active.log":               "permission denied",
		"/gone.log":                 "not found",
		warningFailureKey("active"): "active",
		warningFailureKey("stale"):  "stale",
	}
	pruneFailureCache(failures, map[string]bool{"/active.log": true}, []string{"active"})
	if len(failures) != 2 || failures["/active.log"] == "" || failures[warningFailureKey("active")] == "" {
		t.Fatalf("unexpected pruned failures: %#v", failures)
	}
}

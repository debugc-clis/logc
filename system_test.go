package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSystemAllHistoryRejectsUnsupportedPlatform(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("platform has Linux journald support")
	}
	if _, err := systemLogsCommand(context.Background(), 50, false, Query{All: true}); err == nil {
		t.Fatal("expected --all system-log search to reject this platform")
	}
}

func TestResolveSystemLogFilesIncludesRotatedHistory(t *testing.T) {
	directory := t.TempDir()
	active := filepath.Join(directory, "messages.log")
	rotated := active + ".1"
	for _, path := range []string{active, rotated} {
		if err := os.WriteFile(path, []byte("entry\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := defaultConfigForPlatform(runtime.GOOS, "")
	cfg.SystemLogPaths = []string{active}

	current := resolveSystemLogFiles(cfg, false)
	if len(current) != 1 || current[0] != active {
		t.Fatalf("unexpected active paths: %v", current)
	}
	history := resolveSystemLogFiles(cfg, true)
	if len(history) != 2 || !containsString(history, rotated) {
		t.Fatalf("expected rotated history, got %v", history)
	}
}

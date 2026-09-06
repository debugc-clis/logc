package main

import (
	"context"
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

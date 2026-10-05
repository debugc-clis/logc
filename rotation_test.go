package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRotationDrainsBacklogBeforeSwitching(t *testing.T) {
	for _, mode := range []string{"follow", "watch"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "api.log")
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			_, state, err := openFileState(path, 10, true)
			if err != nil {
				t.Fatal(err)
			}
			defer state.close()
			query, err := buildQuery("ERROR", false, time.Time{}, 0, 0, false, nil)
			if err != nil {
				t.Fatal(err)
			}
			padding := "INFO padding\n"
			body := strings.Repeat(padding, int(maxFollowReadBytes)/len(padding)+100) + "ERROR old tail\n"
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			oldInfo, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(path, path+".1"); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("ERROR new file\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			cfg := defaultConfig()
			follower := newFollower(cfg, []string{path}, nil, &printer{stdout: &bytes.Buffer{}}, query, false)
			watcher := newAlertWatcher(cfg, []string{path}, nil, nil, query)
			poll := func() error { return follower.pollOpenState(path, state) }
			if mode == "watch" {
				poll = func() error { return watcher.pollOpenState(path, state) }
			}
			if err := poll(); err != nil {
				t.Fatal(err)
			}
			if !os.SameFile(state.Info, oldInfo) {
				t.Fatal("switched files before draining the old inode")
			}
			if err := poll(); err != nil {
				t.Fatal(err)
			}
			if mode == "watch" {
				alerts, _ := watcher.tracker.summaries(time.Now())
				if len(alerts) != 2 {
					t.Fatalf("expected both old and new errors, got %#v", alerts)
				}
			} else if got := strings.Join(state.Pending, "\n"); !strings.Contains(got, "ERROR old tail") || !strings.Contains(got, "ERROR new file") {
				t.Fatalf("rotation lost lines: %q", got)
			}
		})
	}
}

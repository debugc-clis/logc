package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCopytruncateClearsPartialCarry(t *testing.T) {
	for _, mode := range []string{"follow", "watch"} {
		for _, keepOpen := range []bool{true, false} {
			name := mode + "/pathpoll"
			if keepOpen {
				name = mode + "/openfd"
			}
			for _, variant := range []string{"short", "empty", "long-short", "long-empty"} {
				t.Run(name+"/"+variant, func(t *testing.T) {
					path := filepath.Join(t.TempDir(), "api.log")
					if err := os.WriteFile(path, nil, 0o600); err != nil {
						t.Fatal(err)
					}
					_, state, err := openFileState(path, 10, keepOpen)
					if err != nil {
						t.Fatal(err)
					}
					defer state.close()
					original := state.Info
					cfg := defaultConfig()
					follower := newFollower(cfg, []string{path}, nil, &printer{stdout: &bytes.Buffer{}}, Query{}, true)
					watcher := newAlertWatcher(cfg, []string{path}, nil, nil, Query{})
					follower.states[path], watcher.states[path] = state, state
					poll := follower.poll
					if mode == "watch" {
						poll = watcher.poll
					}
					write := func(body string) {
						t.Helper()
						if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
							t.Fatal(err)
						}
					}
					oldCarry := "ERROR unfinished old record with enough padding to exceed replacement size"
					longCarry := strings.HasPrefix(variant, "long-")
					if longCarry {
						oldCarry = strings.Repeat("x", maxCarryBytes+100)
					}
					write("ERROR before\n" + oldCarry)
					poll()
					if longCarry {
						oldCarry = oldCarry[len(oldCarry)-maxCarryBytes:]
					}
					if state.Carry != oldCarry || state.CarryTruncated != longCarry {
						t.Fatalf("carry length=%d truncated=%t, want length=%d truncated=%t", len(state.Carry), state.CarryTruncated, len(oldCarry), longCarry)
					}
					emptyFirst := strings.HasSuffix(variant, "empty")
					if emptyFirst {
						write("")
					} else {
						write("ERROR after\nERROR next")
					}
					info, err := os.Stat(path)
					if err != nil || !os.SameFile(original, info) || info.Size() >= state.Offset {
						t.Fatalf("fixture must shrink the same inode below offset: info=%v err=%v offset=%d", info, err, state.Offset)
					}
					poll()
					poll()
					if emptyFirst {
						if state.Carry != "" || state.CarryTruncated || state.Offset != 0 {
							t.Fatalf("empty truncate carry length=%d truncated=%t offset=%d", len(state.Carry), state.CarryTruncated, state.Offset)
						}
						write("ERROR after\nERROR next")
						poll()
					}
					if state.Carry != "ERROR next" || state.CarryTruncated {
						t.Fatalf("replacement carry=%q truncated=%t", state.Carry, state.CarryTruncated)
					}
					writer, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
					if err != nil {
						t.Fatal(err)
					}
					_, writeErr := writer.WriteString(" complete\n")
					closeErr := writer.Close()
					if writeErr != nil || closeErr != nil {
						t.Fatalf("append=%v close=%v", writeErr, closeErr)
					}
					poll()
					poll()
					if state.Carry != "" || state.Offset != int64(len("ERROR after\nERROR next complete\n")) {
						t.Fatalf("final carry=%q offset=%d", state.Carry, state.Offset)
					}
					if mode == "follow" {
						if longCarry {
							oldCarry = "[truncated long line] " + oldCarry
						}
						want := []string{"ERROR before", oldCarry, "↻ file truncated", "ERROR after", "ERROR next complete"}
						if !reflect.DeepEqual(state.Pending, want) {
							t.Fatalf("pending differs from expected standalone carry, marker and complete lines (got %d lines, want %d)", len(state.Pending), len(want))
						}
					} else {
						alerts, rate := watcher.tracker.summaries(time.Now())
						counts := make(map[string]int)
						for _, alert := range alerts {
							if alert.Source != path {
								t.Fatalf("unexpected source %q", alert.Source)
							}
							counts[alert.Line] = alert.Count
						}
						want := map[string]int{"ERROR before": 1, "ERROR after": 1, "ERROR next complete": 1}
						if !reflect.DeepEqual(counts, want) || rate != 3 {
							t.Fatalf("counts=%v rate=%d, want %v rate=3", counts, rate, want)
						}
					}
				})
			}
		}
	}
}

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

package main

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSearchFollowDiscovery(t *testing.T) {
	for _, scope := range []string{"directory", "directory-log-suffix", "glob", "group", "single-file", "fallback", "fuzzy", "process-snapshot", "default"} {
		t.Run(scope, func(t *testing.T) {
			root := t.TempDir()
			directory := filepath.Join(root, "service")
			if scope == "directory-log-suffix" {
				directory = filepath.Join(root, "service.log.d")
			}
			if err := os.Mkdir(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			active := filepath.Join(directory, "app.log")
			writeSearchFollowLog(t, active, "ERROR initial active\n")
			writeSearchFollowLog(t, active+".1", "ERROR initial rotated\n")
			writeSearchFollowLog(t, active+".2.gz", "ERROR initial compressed\n")
			cfg := defaultConfig()
			cfg.DefaultLogDirs = []string{directory}
			cfg.Excludes = nil
			cfg.IgnoreLines = nil
			cfg.Groups = map[string][]string{"service": {filepath.Join(directory, "*.log*"), active + ".1", active + ".2.gz"}}
			selector := directory
			switch scope {
			case "glob":
				selector = filepath.Join(directory, "*.log*")
			case "group", "fuzzy":
				selector = "service"
				if scope == "fuzzy" {
					cfg.Groups = nil
				}
			case "single-file", "fallback", "process-snapshot":
				selector = active
			}
			positionals := []string{selector, "ERROR"}
			if scope == "default" {
				positionals = []string{"ERROR"}
			}
			resolved, pattern, err := interpretPositionals(cfg, positionals, "", true, time.Time{})
			if err != nil {
				t.Fatal(err)
			}
			if scope == "fallback" {
				resolved.Patterns = nil
			}
			if scope == "process-snapshot" {
				resolved.Patterns = append([]string(nil), resolved.Paths...)
			}
			query, err := buildQuery(pattern, false, time.Now().Add(-time.Hour), 0, 0, false, cfg.IgnoreLines)
			if err != nil {
				t.Fatal(err)
			}
			var output, diagnostics bytes.Buffer
			out := &printer{stdout: &output, stderr: &diagnostics}
			if matches := searchPaths(resolved.Paths, query, out); matches != 3 {
				t.Fatalf("initial search matches=%d, want active and both historical files; paths=%v", matches, resolved.Paths)
			}
			output.Reset()
			follower := newSearchFollower(cfg, resolved, nonHistorical(resolved.Paths), cfg.Excludes, out, query)
			defer follower.closeStates()
			if !follower.query.Since.IsZero() {
				t.Fatal("live follow retained the historical search cutoff")
			}
			follower.rescan()
			assertSearchFollowSources(t, follower, []string{active})
			fresh := filepath.Join(directory, "worker.log")
			writeSearchFollowLog(t, fresh, "ERROR new file existing content\n")
			writeSearchFollowLog(t, fresh+".1", "ERROR new rotated\n")
			writeSearchFollowLog(t, fresh+".2.gz", "ERROR new compressed\n")
			writeSearchFollowLog(t, filepath.Join(root, "outside.log"), "ERROR outside selector\n")
			if scope == "glob" || scope == "group" {
				writeSearchFollowLog(t, filepath.Join(directory, "unselected.out"), "ERROR outside glob\n")
			}
			follower.rescan()
			wantSources := []string{active}
			discovers := scope == "directory" || scope == "directory-log-suffix" || scope == "glob" || scope == "group" || scope == "default"
			if discovers {
				wantSources = append(wantSources, fresh)
			}
			assertSearchFollowSources(t, follower, wantSources)
			for _, path := range []string{active, fresh} {
				file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
				if err != nil {
					t.Fatal(err)
				}
				_, writeErr := file.WriteString("INFO ignored append\nERROR append " + filepath.Base(path) + "\n")
				closeErr := file.Close()
				if writeErr != nil || closeErr != nil {
					t.Fatalf("append: write=%v close=%v", writeErr, closeErr)
				}
			}
			for iteration := 0; iteration < 2; iteration++ {
				follower.poll()
				follower.flushFair()
				follower.rescan()
			}
			if count := strings.Count(output.String(), "ERROR append app.log"); count != 1 {
				t.Fatalf("active append count=%d output=%q", count, output.String())
			}
			wantFresh := 0
			if discovers {
				wantFresh = 1
			}
			if count := strings.Count(output.String(), "ERROR append worker.log"); count != wantFresh {
				t.Fatalf("new-file append count=%d want=%d output=%q", count, wantFresh, output.String())
			}
			for _, unwanted := range []string{"initial active", "initial rotated", "initial compressed", "existing content", "new rotated", "new compressed", "outside selector", "INFO ignored"} {
				if strings.Contains(output.String(), unwanted) {
					t.Errorf("live output contains %q: %q", unwanted, output.String())
				}
			}
			if diagnostics.Len() != 0 {
				t.Fatalf("unexpected follow diagnostics: %s", diagnostics.String())
			}
		})
	}
}

func assertSearchFollowSources(t *testing.T, follower *follower, want []string) {
	t.Helper()
	if len(follower.states) != len(want) {
		t.Fatalf("live source count=%d want=%d; sources=%v patterns=%v", len(follower.states), len(want), follower.states, follower.patterns)
	}
	for _, path := range want {
		if _, ok := follower.states[path]; !ok {
			t.Errorf("missing live source %s", path)
		}
	}
}

func writeSearchFollowLog(t *testing.T, path, body string) {
	t.Helper()
	var content bytes.Buffer
	if strings.HasSuffix(path, ".gz") {
		writer := gzip.NewWriter(&content)
		if _, err := writer.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		content.WriteString(body)
	}
	if err := os.WriteFile(path, content.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

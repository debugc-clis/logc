package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func corruptSearchStatusGzip(t *testing.T, path, body string) {
	t.Helper()
	writeSearchFollowLog(t, path, body)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-8] ^= 0xff
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSearchStatusCLI(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "logc")
	buildContext, cancelBuild := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancelBuild()
	if output, err := exec.CommandContext(buildContext, "go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	config := filepath.Join(root, "config")
	if err := os.WriteFile(config, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name       string
		kind       string
		flags      []string
		pattern    string
		wantCode   int
		wantPrefix bool
		wantError  string
		wantCount  int
	}{
		{name: "checksum prefix", kind: "checksum", pattern: "ERROR", wantCode: 1, wantPrefix: true, wantError: "invalid checksum", wantCount: 1},
		{name: "invalid gzip header", kind: "header", pattern: "ERROR", wantCode: 1, wantError: "invalid header"},
		{name: "oversized token prefix", kind: "large", pattern: "ERROR", wantCode: 1, wantPrefix: true, wantError: "token too long", wantCount: 1},
		{name: "oversized token no prefix", kind: "large-first", pattern: "ERROR", wantCode: 1, wantError: "token too long"},
		{name: "checksum no matches", kind: "checksum", pattern: "absent", wantCode: 1, wantError: "invalid checksum"},
		{name: "all good", kind: "good", pattern: "ERROR", wantPrefix: true, wantCount: 1},
		{name: "zero matches", kind: "good", pattern: "absent"},
		{name: "since without regex failure", kind: "checksum", flags: []string{"--since", "1h"}, wantCode: 1, wantPrefix: true, wantError: "invalid checksum"},
		{name: "since without regex success", kind: "good", flags: []string{"--since", "1h"}, wantPrefix: true},
		{name: "follow fails before waiting", kind: "checksum", pattern: "ERROR", flags: []string{"-f"}, wantCode: 1, wantPrefix: true, wantError: "invalid checksum", wantCount: 1},
		{name: "invalid regex", kind: "good", pattern: "[", wantCode: 2, wantError: "invalid regex"},
		{name: "invalid since", kind: "good", flags: []string{"--since", "not-a-time"}, wantCode: 2, wantError: "invalid --since"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "app.log")
			body := "ERROR readable prefix\n"
			switch test.kind {
			case "checksum":
				path += ".gz"
				corruptSearchStatusGzip(t, path, body)
			case "header":
				path += ".gz"
				if err := os.WriteFile(path, []byte("not a gzip header"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "large", "large-first":
				if test.kind == "large-first" {
					body = ""
				}
				writeSearchFollowLog(t, path, body+strings.Repeat("x", maxCarryBytes+1)+"\n")
			default:
				writeSearchFollowLog(t, path, body)
			}
			args := []string{path}
			if test.pattern != "" {
				args = append(args, "--match", test.pattern)
			}
			args = append(args, test.flags...)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, args...)
			cmd.Env = append(os.Environ(), "LOGC_CONFIG="+config, "NO_COLOR=1")
			var output, diagnostics bytes.Buffer
			cmd.Stdout, cmd.Stderr = &output, &diagnostics
			err := cmd.Run()
			if ctx.Err() != nil {
				t.Fatalf("CLI did not terminate: %v; stderr=%s", ctx.Err(), &diagnostics)
			}
			code := 0
			if err != nil {
				if exit, ok := err.(*exec.ExitError); ok {
					code = exit.ExitCode()
				} else {
					t.Fatal(err)
				}
			}
			if code != test.wantCode || strings.Contains(output.String(), "ERROR readable prefix") != test.wantPrefix {
				t.Fatalf("code=%d want=%d stdout=%q stderr=%q", code, test.wantCode, &output, &diagnostics)
			}
			if test.wantError != "" && !strings.Contains(diagnostics.String(), test.wantError) {
				t.Fatalf("missing %q: %s", test.wantError, &diagnostics)
			}
			if test.wantCode != 2 && test.pattern != "" && !strings.Contains(diagnostics.String(), fmt.Sprintf("%d matching lines", test.wantCount)) {
				t.Fatalf("missing match summary: %s", &diagnostics)
			}
			if test.wantCode == 1 && !strings.Contains(diagnostics.String(), "failed to read 1 of 1 files") {
				t.Fatalf("missing failure summary: %s", &diagnostics)
			}
			if test.wantCode == 0 && strings.Contains(diagnostics.String(), "error:") {
				t.Fatalf("unexpected error: %s", &diagnostics)
			}
			if test.wantCode == 1 && strings.Contains(strings.Join(test.flags, " "), "-f") {
				for _, want := range []string{"historical search incomplete", "live follow not started", "--current", "does not repair"} {
					if !strings.Contains(diagnostics.String(), want) {
						t.Errorf("missing follow guidance %q: %s", want, &diagnostics)
					}
				}
			} else if strings.Contains(diagnostics.String(), "live follow not started") {
				t.Fatalf("unexpected follow guidance: %s", &diagnostics)
			}
		})
	}
}

func TestSearchStatusMixedCLI(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "logc")
	buildContext, cancelBuild := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancelBuild()
	if output, err := exec.CommandContext(buildContext, "go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	config := filepath.Join(root, "config")
	if err := os.WriteFile(config, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, badFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("badFirst=%t", badFirst), func(t *testing.T) {
			directory := t.TempDir()
			active := filepath.Join(directory, "app.log")
			archive := active + ".1.gz"
			writeSearchFollowLog(t, active, "ERROR active match\n")
			corruptSearchStatusGzip(t, archive, "ERROR archive prefix\n")
			now := time.Now()
			older, newer := active, archive
			if !badFirst {
				older, newer = archive, active
			}
			for path, stamp := range map[string]time.Time{older: now.Add(-time.Minute), newer: now} {
				if err := os.Chtimes(path, stamp, stamp); err != nil {
					t.Fatal(err)
				}
			}
			for _, current := range []bool{false, true} {
				for _, target := range []string{directory, active} {
					args := []string{target, "--match", "ERROR", "--json"}
					if current {
						args = append(args, "--current")
					}
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					cmd := exec.CommandContext(ctx, binary, args...)
					cmd.Env = append(os.Environ(), "LOGC_CONFIG="+config)
					var output, diagnostics bytes.Buffer
					cmd.Stdout, cmd.Stderr = &output, &diagnostics
					err := cmd.Run()
					cancel()
					if current {
						if err != nil || strings.Contains(output.String(), "archive prefix") || strings.Contains(diagnostics.String(), "error:") || !strings.Contains(diagnostics.String(), "1 matching lines") {
							t.Fatalf("current target=%s error=%v stdout=%s stderr=%s", target, err, &output, &diagnostics)
						}
					} else {
						exit, ok := err.(*exec.ExitError)
						if !ok || exit.ExitCode() != 1 || !strings.Contains(diagnostics.String(), "2 matching lines") || !strings.Contains(diagnostics.String(), "failed to read 1 of 2 files") {
							t.Fatalf("mixed target=%s error=%v stdout=%s stderr=%s", target, err, &output, &diagnostics)
						}
						first := strings.Index(output.String(), "archive prefix")
						second := strings.Index(output.String(), "active match")
						if first < 0 || second < 0 || (first < second) != badFirst {
							t.Fatalf("wrong output order: %s", &output)
						}
					}
					if !strings.Contains(output.String(), "ERROR active match") {
						t.Fatalf("missing active output: %s", &output)
					}
				}
			}
		})
	}
}

func TestSearchStatusCountsFailuresAndAttemptsAllPaths(t *testing.T) {
	root := t.TempDir()
	good := filepath.Join(root, "good.log")
	bad := filepath.Join(root, "bad.log.gz")
	missing := filepath.Join(root, "missing.log")
	writeSearchFollowLog(t, good, "ERROR good\n")
	corruptSearchStatusGzip(t, bad, "ERROR partial\n")
	for _, paths := range [][]string{{bad, missing, good}, {good, missing, bad}, {missing, good, bad}} {
		var output, diagnostics bytes.Buffer
		matches, err := searchPaths(paths, searchBatchQuery(t, "ERROR", 0, 0, false), &printer{stdout: &output, stderr: &diagnostics})
		if matches != 2 || err == nil || err.Error() != "search incomplete: failed to read 2 of 3 files" {
			t.Fatalf("paths=%v matches=%d error=%v", paths, matches, err)
		}
		for _, want := range []string{"ERROR good", "ERROR partial"} {
			if !strings.Contains(output.String(), want) {
				t.Errorf("missing %q: %s", want, &output)
			}
		}
		if strings.Count(diagnostics.String(), "logc: error:") != 2 {
			t.Fatalf("expected one diagnostic per failed path: %s", &diagnostics)
		}
	}
}

func TestSearchStatusSystemFileBootstrapFailure(t *testing.T) {
	for _, all := range []bool{false, true} {
		t.Run(fmt.Sprintf("all=%t", all), func(t *testing.T) {
			root := t.TempDir()
			active := filepath.Join(root, "system.log")
			writeSearchFollowLog(t, active, "ERROR active\n")
			corruptSearchStatusGzip(t, active+".1.gz", "ERROR partial\n")
			cfg := defaultConfig()
			cfg.SystemLogPaths = []string{active}
			query := searchBatchQuery(t, "ERROR", 0, 0, false)
			query.All = all
			if !all {
				query.Since = time.Now().Add(-time.Hour)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			var output, diagnostics bytes.Buffer
			err := runSystemFileLogs(ctx, cfg, []string{active}, 10, query, &printer{stdout: &output, stderr: &diagnostics})
			if ctx.Err() != nil || err == nil || !strings.Contains(err.Error(), "failed to read 1 of 2 files") {
				t.Fatalf("context=%v error=%v stderr=%s", ctx.Err(), err, &diagnostics)
			}
			if !strings.Contains(output.String(), "ERROR active") || !strings.Contains(output.String(), "ERROR partial") || !strings.Contains(diagnostics.String(), "2 matching lines") {
				t.Fatalf("stdout=%s stderr=%s", &output, &diagnostics)
			}
		})
	}
}

func TestSearchStatusEmptyPaths(t *testing.T) {
	matches, err := searchPaths(nil, Query{}, &printer{stdout: io.Discard, stderr: io.Discard})
	if matches != 0 || err != nil {
		t.Fatalf("matches=%d error=%v", matches, err)
	}
}

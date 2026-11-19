package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestExplicitMatchRequiresEveryTarget(t *testing.T) {
	directory := t.TempDir()
	first := filepath.Join(directory, "first.log")
	second := filepath.Join(directory, "second.log")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte("ERROR event\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := defaultConfig()
	cfg.DefaultLogDirs = []string{directory}
	cfg.Excludes = nil
	cfg.Groups = map[string][]string{"empty": {filepath.Join(directory, "absent-*.log")}}
	for _, history := range []bool{false, true} {
		for _, invalid := range []string{":99999", "unmatched-source-selector", "@0"} {
			for index := 0; index < 3; index++ {
				t.Run(fmt.Sprintf("history=%t/%s/position=%d", history, invalid, index), func(t *testing.T) {
					positionals := append([]string{first, second}[:index:index], invalid)
					positionals = append(positionals, []string{first, second}[index:]...)
					resolved, query, err := interpretPositionals(cfg, positionals, "ERROR", history, time.Time{})
					if err == nil || query != "" || len(resolved.Paths) != 0 {
						t.Fatalf("partial resolution accepted: resolved=%+v query=%q err=%v", resolved, query, err)
					}
				})
			}
		}
		for _, test := range []struct {
			name        string
			positionals []string
			match       string
			wantPaths   []string
			wantQuery   string
			wantDefault bool
		}{
			{"two files", []string{first, second}, "ERROR", []string{first, second}, "ERROR", false},
			{"default", nil, "ERROR", []string{first, second}, "ERROR", true},
			{"implicit", []string{first, "ERROR"}, "", []string{first}, "ERROR", false},
			{"empty group", []string{"empty"}, "ERROR", nil, "ERROR", false},
			{"unmatched glob", []string{filepath.Join(directory, "missing-*.log")}, "ERROR", nil, "ERROR", false},
		} {
			t.Run(fmt.Sprintf("history=%t/%s", history, test.name), func(t *testing.T) {
				resolved, query, err := interpretPositionals(cfg, test.positionals, test.match, history, time.Time{})
				if test.name == "empty group" || test.name == "unmatched glob" {
					if err == nil || query != "" || len(resolved.Paths) != 0 {
						t.Fatalf("expected existing no-match error: resolved=%+v query=%q err=%v", resolved, query, err)
					}
					return
				}
				if err != nil || query != test.wantQuery || !reflect.DeepEqual(uniqueSorted(resolved.Paths), uniqueSorted(test.wantPaths)) || resolved.DefaultDiscovery != test.wantDefault {
					t.Fatalf("resolved=%+v query=%q err=%v", resolved, query, err)
				}
			})
		}
	}
}

func TestExplicitMatchCLI(t *testing.T) {
	directory := t.TempDir()
	binary := filepath.Join(directory, "logc")
	buildContext, cancelBuild := context.WithTimeout(context.Background(), time.Minute)
	defer cancelBuild()
	if output, err := exec.CommandContext(buildContext, "go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	first := filepath.Join(directory, "first.log")
	second := filepath.Join(directory, "second.log")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte("ERROR explicit-match-row\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	config := filepath.Join(directory, "config")
	contents := fmt.Sprintf("default_log_dir=!\ndefault_log_dir=%s\ngroup.empty=%s\n", directory, filepath.Join(directory, "absent-*.log"))
	if err := os.WriteFile(config, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(t *testing.T, args []string, wantCode, wantRows int) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, binary, args...)
		command.Env = append(os.Environ(), "LOGC_CONFIG="+config, "NO_COLOR=1")
		var output, diagnostics bytes.Buffer
		command.Stdout, command.Stderr = &output, &diagnostics
		err := command.Run()
		if ctx.Err() != nil {
			t.Fatalf("CLI did not terminate: %v stdout=%q stderr=%q", ctx.Err(), &output, &diagnostics)
		}
		code := 0
		if err != nil {
			if exit, ok := err.(*exec.ExitError); ok {
				code = exit.ExitCode()
			} else {
				t.Fatal(err)
			}
		}
		if code != wantCode || strings.Count(output.String(), "ERROR explicit-match-row") != wantRows {
			t.Fatalf("args=%q code=%d want=%d stdout=%q stderr=%q", args, code, wantCode, &output, &diagnostics)
		}
		if wantCode != 0 && (output.Len() != 0 || diagnostics.Len() == 0 || strings.Contains(diagnostics.String(), "matching lines")) {
			t.Fatalf("search started or missing diagnostic: stdout=%q stderr=%q", &output, &diagnostics)
		}
		return diagnostics.String()
	}
	for _, flag := range []string{"-m", "--match"} {
		for _, mode := range []string{"--all", "--current", "--follow"} {
			for _, json := range []bool{false, true} {
				for _, invalid := range []string{":99999", "unmatched-source-selector", "@0"} {
					for index := 0; index < 3; index++ {
						t.Run(fmt.Sprintf("%s/%s/json=%t/%s/position=%d", flag, mode, json, invalid, index), func(t *testing.T) {
							args := append([]string{first, second}[:index:index], invalid)
							args = append(args, []string{first, second}[index:]...)
							args = append(args, flag, "ERROR", mode)
							if json {
								args = append(args, "--json")
							}
							diagnostic := run(t, args, 1, 0)
							if invalid != "@0" && !strings.Contains(diagnostic, invalid) {
								t.Fatalf("diagnostic missing selector %q: %s", invalid, diagnostic)
							}
						})
					}
				}
			}
		}
		for _, test := range []struct {
			name string
			pos  []string
			rows int
		}{
			{"two files", []string{first, second}, 2},
			{"default", nil, 2},
			{"empty group", []string{"empty"}, 0},
			{"unmatched glob", []string{filepath.Join(directory, "missing-*.log")}, 0},
		} {
			t.Run(flag+"/"+test.name, func(t *testing.T) {
				wantCode := 0
				if test.name == "empty group" || test.name == "unmatched glob" {
					wantCode = 1
				}
				run(t, append(test.pos, flag, "ERROR", "--all"), wantCode, test.rows)
			})
		}
	}
	t.Run("implicit", func(t *testing.T) { run(t, []string{first, second, "ERROR", "--all"}, 0, 2) })
	t.Run("empty explicit match", func(t *testing.T) {
		run(t, []string{first, "ERROR", "-m", "", "--all"}, 0, 1)
	})
}

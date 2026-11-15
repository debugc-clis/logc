package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestContextSafetySnapshotMaxInt(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	lines := []string{"before", "ERROR middle", "ERROR last"}
	for _, test := range []struct {
		pattern string
		before  int
		want    []string
	}{
		{"middle", maxInt, lines},
		{"last", maxInt, lines},
		{"middle", 0, lines[1:]},
		{"last", 0, lines[2:]},
	} {
		t.Run(fmt.Sprintf("%s/before=%d", test.pattern, test.before), func(t *testing.T) {
			query := searchBatchQuery(t, test.pattern, test.before, maxInt, false)
			if got := selectLines(lines, time.Now(), query); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("selected=%q want=%q", got, test.want)
			}
		})
	}
}

func TestContextSafetyScan(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	since := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	for _, compressed := range []bool{false, true} {
		for _, test := range []struct {
			name    string
			pattern string
			before  int
			after   int
			lines   []string
			want    []string
			matches int
			since   time.Time
			ignore  []string
		}{
			{"max both", "ERROR", maxInt, maxInt, []string{"before", "ERROR middle", "after", "ERROR last"}, []string{"before", "ERROR middle", "after", "ERROR last"}, 2, time.Time{}, nil},
			{"max before", "ERROR", maxInt, 0, []string{"before", "ERROR middle", "after"}, []string{"before", "ERROR middle"}, 1, time.Time{}, nil},
			{"max after", "ERROR", 0, maxInt, []string{"before", "ERROR middle", "after"}, []string{"ERROR middle", "after"}, 1, time.Time{}, nil},
			{"max no matches", "absent", maxInt, maxInt, []string{"before", "after"}, []string{}, 0, time.Time{}, nil},
			{"time only", "", maxInt, maxInt, []string{"2025-12-31T23:59:59Z old", "2026-01-01T00:00:00Z new", "ignored", "untimed"}, []string{"2026-01-01T00:00:00Z new", "--", "untimed"}, 2, since, []string{"ignored"}},
			{"overlap", "ERROR", 1, 1, []string{"before", "ERROR one", "between", "ERROR two", "after", "far", "gap", "ERROR three", "end"}, []string{"before", "ERROR one", "between", "ERROR two", "after", "--", "gap", "ERROR three", "end"}, 3, time.Time{}, nil},
			{"filtered context", "ERROR", 1, 1, []string{"2025-12-31T23:59:59Z old", "ERROR one", "ignored", "ERROR two", "after"}, []string{"ERROR one", "--", "ERROR two", "after"}, 2, since, []string{"ignored"}},
		} {
			t.Run(fmt.Sprintf("%s/gzip=%t", test.name, compressed), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "app.log")
				if compressed {
					path += ".gz"
				}
				writeSearchFollowLog(t, path, strings.Join(test.lines, "\n")+"\n")
				query, err := buildQuery(test.pattern, false, test.since, test.before, test.after, false, test.ignore)
				if err != nil {
					t.Fatal(err)
				}
				got, matches, err := scanLogPath(path, query)
				if err != nil || matches != test.matches || !reflect.DeepEqual(got, test.want) {
					t.Fatalf("selected=%q want=%q matches=%d want=%d err=%v", got, test.want, matches, test.matches, err)
				}
				wantSnapshot := test.want
				if test.pattern == "" {
					wantSnapshot = []string{"2026-01-01T00:00:00Z new", "untimed"}
				}
				if snapshot := selectLines(test.lines, time.Now(), query); len(snapshot) != len(wantSnapshot) || (len(snapshot) > 0 && !reflect.DeepEqual(snapshot, wantSnapshot)) {
					t.Fatalf("snapshot=%q want=%q", snapshot, wantSnapshot)
				}
			})
		}
	}
}

func TestContextSafetyCLI(t *testing.T) {
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
	maxInt := int(^uint(0) >> 1)
	for _, compressed := range []bool{false, true} {
		for _, timeOnly := range []bool{false, true} {
			t.Run(fmt.Sprintf("gzip=%t/timeOnly=%t", compressed, timeOnly), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "tiny.log")
				if compressed {
					path += ".gz"
				}
				want := []string{"before", "ERROR middle", "after"}
				writeSearchFollowLog(t, path, strings.Join(want, "\n")+"\n")
				args := []string{path, "--json", "-C", strconv.Itoa(maxInt)}
				matches := 1
				if timeOnly {
					args = append(args, "--since", "2000-01-01")
					matches = len(want)
				} else {
					args = append(args, "--all", "--match", "ERROR")
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				command := exec.CommandContext(ctx, binary, args...)
				command.Env = append(os.Environ(), "LOGC_CONFIG="+config, "NO_COLOR=1")
				var output, diagnostics bytes.Buffer
				command.Stdout, command.Stderr = &output, &diagnostics
				if err := command.Run(); err != nil {
					t.Fatalf("CLI: %v deadline=%v stdout=%q stderr=%q", err, ctx.Err(), &output, &diagnostics)
				}
				var block searchTestBlock
				if err := json.Unmarshal(output.Bytes(), &block); err != nil {
					t.Fatalf("JSON: %v output=%q", err, &output)
				}
				if !reflect.DeepEqual(block.Lines, want) || block.Suffix != fmt.Sprintf("%d matches", matches) {
					t.Fatalf("block=%+v want lines=%q matches=%d", block, want, matches)
				}
			})
		}
	}
}

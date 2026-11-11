package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

type searchTestBlock struct {
	Path   string   `json:"path"`
	Suffix string   `json:"suffix"`
	Lines  []string `json:"lines"`
}

type searchBlockWriter struct {
	t      *testing.T
	blocks int
	rows   int
	last   searchTestBlock
	visit  func(searchTestBlock)
}

func (writer *searchBlockWriter) Write(data []byte) (int, error) {
	writer.t.Helper()
	var block searchTestBlock
	if err := json.Unmarshal(data, &block); err != nil {
		writer.t.Fatal(err)
	}
	if len(block.Lines) == 0 || len(block.Lines) > 256 {
		writer.t.Fatalf("block rows=%d", len(block.Lines))
	}
	textBytes := 0
	for _, line := range block.Lines {
		textBytes += len(line)
	}
	if textBytes > 1024*1024 && len(block.Lines) != 1 {
		writer.t.Fatalf("block bytes=%d rows=%d", textBytes, len(block.Lines))
	}
	if writer.blocks > 0 && writer.last.Path == block.Path && writer.last.Suffix != "search continues" {
		writer.t.Fatalf("nonfinal suffix=%q", writer.last.Suffix)
	}
	writer.blocks++
	writer.rows += len(block.Lines)
	writer.last = block
	if writer.visit != nil {
		writer.visit(block)
	}
	return len(data), nil
}

func searchBatchQuery(t *testing.T, pattern string, before, after int, dedup bool) Query {
	t.Helper()
	query, err := buildQuery(pattern, false, time.Time{}, before, after, dedup, nil)
	if err != nil {
		t.Fatal(err)
	}
	return query
}

func TestSearchBatchesLimits(t *testing.T) {
	for _, compressed := range []bool{false, true} {
		for _, test := range []struct {
			name   string
			lines  []string
			blocks int
		}{
			{"255 rows", strings.Split(strings.TrimSuffix(strings.Repeat("hit\n", 255), "\n"), "\n"), 1},
			{"256 rows", strings.Split(strings.TrimSuffix(strings.Repeat("hit\n", 256), "\n"), "\n"), 1},
			{"257 rows", strings.Split(strings.TrimSuffix(strings.Repeat("hit\n", 257), "\n"), "\n"), 2},
			{"exact bytes", []string{strings.Repeat("a", 512*1024), strings.Repeat("b", 512*1024)}, 1},
			{"over bytes", []string{strings.Repeat("a", 512*1024), strings.Repeat("b", 512*1024), "c"}, 2},
			{"large line", []string{"before", strings.Repeat("x", 1024*1024+1), "after"}, 3},
		} {
			t.Run(fmt.Sprintf("%s/gzip=%t", test.name, compressed), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "app.log")
				if compressed {
					path += ".gz"
				}
				writeSearchFollowLog(t, path, strings.Join(test.lines, "\n")+"\n")
				next := 0
				writer := &searchBlockWriter{t: t, visit: func(block searchTestBlock) {
					for _, line := range block.Lines {
						if next >= len(test.lines) || line != test.lines[next] {
							t.Fatalf("unexpected row %d", next)
						}
						next++
					}
				}}
				matches := searchPaths([]string{path}, searchBatchQuery(t, "", 0, 0, false), &printer{json: true, stdout: writer, stderr: io.Discard})
				if matches != len(test.lines) || next != len(test.lines) || writer.blocks != test.blocks {
					t.Fatalf("matches=%d rows=%d blocks=%d", matches, next, writer.blocks)
				}
				wantSuffix := fmt.Sprintf("%d matches", matches)
				if test.blocks > 1 {
					wantSuffix += " total"
				}
				if writer.last.Suffix != wantSuffix {
					t.Fatalf("suffix=%q want=%q", writer.last.Suffix, wantSuffix)
				}
			})
		}
	}
}

func TestSearchBatchesEmitsBeforeEOF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	writeSearchFollowLog(t, path, strings.Repeat("hit "+strings.Repeat("x", 1000)+"\n", 4096))
	writer := &searchBlockWriter{t: t}
	writer.visit = func(block searchTestBlock) {
		if writer.blocks == 1 {
			if block.Suffix != "search continues" {
				t.Fatalf("first suffix=%q", block.Suffix)
			}
			if err := os.Truncate(path, 0); err != nil {
				t.Fatal(err)
			}
		}
	}
	matches := searchPaths([]string{path}, searchBatchQuery(t, "hit", 0, 0, false), &printer{json: true, stdout: writer, stderr: io.Discard})
	if matches < 256 || matches >= 4096 || writer.rows != matches {
		t.Fatalf("matches=%d output rows=%d; expected unread tail to be truncated", matches, writer.rows)
	}
}

func TestSearchBatchesContextAndDedup(t *testing.T) {
	for _, compressed := range []bool{false, true} {
		for _, dedup := range []bool{false, true} {
			t.Run(fmt.Sprintf("gzip=%t/dedup=%t", compressed, dedup), func(t *testing.T) {
				lines := make([]string, 0, 800)
				for index := 0; index < 260; index++ {
					lines = append(lines, fmt.Sprintf("hit %d", index))
				}
				lines = append(lines, "before", "hit repeated", "hit repeated", "hit repeated", "after", "gap", "before second", "hit second", "after second", "ignore hit", "2020-01-01T00:00:00Z hit old")
				path := filepath.Join(t.TempDir(), "app.log")
				if compressed {
					path += ".gz"
				}
				writeSearchFollowLog(t, path, strings.Join(lines, "\n")+"\n")
				query := searchBatchQuery(t, "hit", 1, 1, dedup)
				query.IgnoreRegexes = append(query.IgnoreRegexes, regexp.MustCompile("ignore"))
				query.Since = time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
				want := selectLines(lines, time.Now(), query)
				if dedup {
					want = dedupLines(want)
				}
				var got []string
				writer := &searchBlockWriter{t: t, visit: func(block searchTestBlock) { got = append(got, block.Lines...) }}
				matches := searchPaths([]string{path}, query, &printer{json: true, stdout: writer, stderr: io.Discard})
				if matches != 264 || !reflect.DeepEqual(got, want) || writer.last.Suffix != "264 matches total" {
					t.Fatalf("matches=%d rows=%d want=%d suffix=%q", matches, len(got), len(want), writer.last.Suffix)
				}
			})
		}
	}
}

func TestSearchBatchesDedupBoundary(t *testing.T) {
	for _, run := range []int{1, 2, 10000} {
		t.Run(fmt.Sprint(run), func(t *testing.T) {
			var lines []string
			for index := 0; index < 256; index++ {
				lines = append(lines, fmt.Sprintf("hit %d", index))
			}
			for index := 0; index < run; index++ {
				lines = append(lines, "hit repeated")
			}
			lines = append(lines, "hit last")
			path := filepath.Join(t.TempDir(), "app.log")
			writeSearchFollowLog(t, path, strings.Join(lines, "\n")+"\n")
			var got []string
			writer := &searchBlockWriter{t: t, visit: func(block searchTestBlock) { got = append(got, block.Lines...) }}
			matches := searchPaths([]string{path}, searchBatchQuery(t, "hit", 0, 0, true), &printer{json: true, stdout: writer, stderr: io.Discard})
			if matches != len(lines) || !reflect.DeepEqual(got, dedupLines(lines)) {
				t.Fatalf("matches=%d rows=%d", matches, len(got))
			}
		})
	}
}

func TestSearchBatchesPartialReadError(t *testing.T) {
	for _, compressed := range []bool{false, true} {
		for _, count := range []int{3, 300} {
			for _, dedup := range []bool{false, true} {
				t.Run(fmt.Sprintf("gzip=%t/count=%d/dedup=%t", compressed, count, dedup), func(t *testing.T) {
					path := filepath.Join(t.TempDir(), "broken.log")
					if compressed {
						path += ".gz"
					}
					body := strings.Repeat("hit repeated\n", count)
					if !compressed {
						body += strings.Repeat("x", maxCarryBytes+1) + "\n"
					}
					writeSearchFollowLog(t, path, body)
					if compressed {
						data, err := os.ReadFile(path)
						if err != nil {
							t.Fatal(err)
						}
						data[len(data)-8] ^= 0xff
						if err := os.WriteFile(path, data, 0o600); err != nil {
							t.Fatal(err)
						}
					}
					good := filepath.Join(t.TempDir(), "good.log")
					writeSearchFollowLog(t, good, "hit next\n")
					var diagnostics bytes.Buffer
					var got []string
					var suffix string
					writer := &searchBlockWriter{t: t, visit: func(block searchTestBlock) {
						if block.Path == path {
							got = append(got, block.Lines...)
							suffix = block.Suffix
						}
					}}
					matches := searchPaths([]string{path, good}, searchBatchQuery(t, "hit", 0, 0, dedup), &printer{json: true, stdout: writer, stderr: &diagnostics})
					want := strings.Split(strings.TrimSuffix(strings.Repeat("hit repeated\n", count), "\n"), "\n")
					if dedup {
						want = dedupLines(want)
					}
					if matches != count+1 || !reflect.DeepEqual(got, want) || suffix != fmt.Sprintf("%d matches before read error", count) || !strings.Contains(diagnostics.String(), path) || writer.last.Path != good {
						t.Fatalf("matches=%d suffix=%q rows=%d stderr=%q", matches, suffix, len(got), diagnostics.String())
					}
				})
			}
		}
	}
}

func TestSearchBatchesSmallTextAndNoMatches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	writeSearchFollowLog(t, path, "hit repeated\nhit repeated\n")
	for _, pattern := range []string{"hit", "absent"} {
		var output, diagnostics bytes.Buffer
		out := &printer{stdout: &output, stderr: &diagnostics, now: func() time.Time { return time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC) }}
		matches := searchPaths([]string{path}, searchBatchQuery(t, pattern, 0, 0, true), out)
		want := ""
		if pattern == "hit" {
			want = "\n" + path + " [shown 12:00:00 · 2 matches]\n  hit repeated  × 2\n"
			if matches != 2 {
				t.Fatal(matches)
			}
		} else if matches != 0 {
			t.Fatal(matches)
		}
		if output.String() != want || diagnostics.Len() != 0 {
			t.Fatalf("stdout=%q stderr=%q", output.String(), diagnostics.String())
		}
	}
}

func TestSearchBatchesDedupFormattedByteLimit(t *testing.T) {
	for _, extra := range []int{0, 1} {
		t.Run(fmt.Sprint(extra), func(t *testing.T) {
			first := strings.Repeat("a", 1024*1024-len("repeated  × 2")+extra)
			path := filepath.Join(t.TempDir(), "app.log")
			writeSearchFollowLog(t, path, first+"\nrepeated\nrepeated\n")
			writer := &searchBlockWriter{t: t}
			matches := searchPaths([]string{path}, searchBatchQuery(t, "", 0, 0, true), &printer{json: true, stdout: writer, stderr: io.Discard})
			if matches != 3 || writer.rows != 2 || writer.blocks != 1+extra {
				t.Fatalf("matches=%d rows=%d blocks=%d", matches, writer.rows, writer.blocks)
			}
		})
	}
}

func TestSearchBatchesDenseBoundedWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	const rows = 8192
	line := "hit " + strings.Repeat("x", 8192)
	for index := 0; index < rows; index++ {
		if _, err := fmt.Fprintln(file, line); err != nil {
			file.Close()
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	writer := &searchBlockWriter{t: t}
	matches := searchPaths([]string{path}, searchBatchQuery(t, "hit", 0, 0, false), &printer{json: true, stdout: writer, stderr: io.Discard})
	if matches != rows || writer.rows != rows || writer.blocks < 2 || writer.last.Suffix != "8192 matches total" {
		t.Fatalf("matches=%d rows=%d blocks=%d suffix=%q", matches, writer.rows, writer.blocks, writer.last.Suffix)
	}
}

package main

import (
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSeverityPattern(t *testing.T) {
	q, err := buildQuery(severityPattern("errors"), false, time.Time{}, 0, 0, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"ERROR boom", "fatal: nope", "panic happened", "exception thrown"} {
		if !q.match(s) {
			t.Fatalf("expected match: %q", s)
		}
	}
	if q.match("INFO healthy") {
		t.Fatal("unexpected info match")
	}
}

func TestSelectLinesContextAndIgnore(t *testing.T) {
	q, err := buildQuery("ERROR", false, time.Time{}, 1, 1, false, []string{`health`})
	if err != nil {
		t.Fatal(err)
	}
	lines := []string{"before", "health", "ERROR bad", "after", "far"}
	got := selectLines(lines, time.Now(), q)
	want := []string{"ERROR bad", "after"}
	if len(got) != len(want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %#v want %#v", got, want)
		}
	}
}

func TestGzipReader(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "app.log.1.gz")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	_, _ = gz.Write([]byte("INFO ok\nERROR old\n"))
	_ = gz.Close()
	_ = f.Close()
	lines, _, err := readAllLogLines(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || lines[1] != "ERROR old" {
		t.Fatalf("got %#v", lines)
	}
}

func TestGzipReaderHonorsReadLimit(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "large.log.gz")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	if _, err := gz.Write([]byte(strings.Repeat("x", 4096))); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	lines, _, err := readAllLogLines(p, 128)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || len(lines[0]) != 128 {
		t.Fatalf("lines=%d length=%d", len(lines), len(lines[0]))
	}
}

func TestParseSinceFriendlyDays(t *testing.T) {
	before := time.Now().Add(-25 * time.Hour)
	after := time.Now().Add(-23 * time.Hour)
	got, err := parseSince("1d")
	if err != nil {
		t.Fatal(err)
	}
	if got.Before(before) || got.After(after) {
		t.Fatalf("unexpected 1d time: %v", got)
	}
}

func TestParseYearlessSyslogTimestampAcrossNewYear(t *testing.T) {
	now := time.Date(2027, time.January, 2, 10, 0, 0, 0, time.Local)
	parsed, ok := parseTimePrefixAt("Dec 31 23:59:59 host app: message", now)
	if !ok || parsed.Year() != 2026 {
		t.Fatalf("parsed=%v ok=%t", parsed, ok)
	}
}

func TestScanLogPathSkipsFilesOlderThanSince(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.log")
	if err := os.WriteFile(path, []byte("2026-08-01 10:00:00 ERROR old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Date(2026, time.August, 1, 10, 0, 0, 0, time.Local)
	if err := os.Chtimes(path, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	query, err := buildQuery("ERROR", false, oldTime.Add(time.Hour), 0, 0, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	lines, matches, err := scanLogPath(path, query)
	if err != nil || matches != 0 || len(lines) != 0 {
		t.Fatalf("lines=%#v matches=%d err=%v", lines, matches, err)
	}
}

func TestSortRecentFirst(t *testing.T) {
	directory := t.TempDir()
	oldPath := filepath.Join(directory, "old.log")
	newPath := filepath.Join(directory, "new.log")
	for _, path := range []string{oldPath, newPath} {
		if err := os.WriteFile(path, []byte("INFO event\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	oldTime := time.Now().Add(-time.Hour)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	paths := []string{oldPath, newPath}
	sortRecentFirst(paths)
	if paths[0] != newPath {
		t.Fatalf("paths=%#v", paths)
	}
}

func TestScanLogPathSearchesCompleteRegularAndGzipFiles(t *testing.T) {
	directory := t.TempDir()
	query, err := buildQuery("needle", false, time.Time{}, 0, 0, false, nil)
	if err != nil {
		t.Fatal(err)
	}

	regular := filepath.Join(directory, "large.log")
	padding := strings.Repeat("INFO "+strings.Repeat("x", 1018)+"\n", 17*1024)
	if err := os.WriteFile(regular, []byte("ERROR needle-at-start\n"+padding), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, matches, err := scanLogPath(regular, query)
	if err != nil || matches != 1 || len(lines) != 1 || !strings.Contains(lines[0], "needle-at-start") {
		t.Fatalf("regular lines=%#v matches=%d err=%v", lines, matches, err)
	}

	compressed := filepath.Join(directory, "large.log.gz")
	file, err := os.Create(compressed)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter := gzip.NewWriter(file)
	if _, err := gzipWriter.Write([]byte(padding + "ERROR needle-at-end\n")); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	lines, matches, err = scanLogPath(compressed, query)
	if err != nil || matches != 1 || len(lines) != 1 || !strings.Contains(lines[0], "needle-at-end") {
		t.Fatalf("gzip lines=%#v matches=%d err=%v", lines, matches, err)
	}
}

func TestScanLogPathCountsMatchesBeforeDedup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	if err := os.WriteFile(path, []byte("ERROR repeated\nERROR repeated\nERROR repeated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	query, err := buildQuery("ERROR", false, time.Time{}, 0, 0, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	lines, matches, err := scanLogPath(path, query)
	if err != nil || matches != 3 || len(lines) != 3 {
		t.Fatalf("lines=%#v matches=%d err=%v", lines, matches, err)
	}
}

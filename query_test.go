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

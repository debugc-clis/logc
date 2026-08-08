package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNaturalSearchInterpretation(t *testing.T) {
	d := t.TempDir()
	api := filepath.Join(d, "api")
	if err := os.MkdirAll(api, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"app.log", "error.log", "app.log.1"} {
		if err := os.WriteFile(filepath.Join(api, name), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := defaultConfig()
	cfg.DefaultLogDirs = []string{d}
	cfg.Excludes = nil
	cfg.Groups = map[string][]string{"api": {filepath.Join(api, "*.log")}}
	r, q, err := interpretPositionals(cfg, []string{"api", "ERROR"}, "", false, timeZero)
	if err != nil {
		t.Fatal(err)
	}
	if q != "ERROR" {
		t.Fatalf("query=%q", q)
	}
	if len(r.Paths) != 2 {
		t.Fatalf("paths=%#v", r.Paths)
	}
}

func TestGroupHistoryExpandsRotatedSiblings(t *testing.T) {
	d := t.TempDir()
	api := filepath.Join(d, "api")
	_ = os.MkdirAll(api, 0o755)
	for _, name := range []string{"app.log", "app.log.1", "app.log.2.gz"} {
		_ = os.WriteFile(filepath.Join(api, name), []byte("x"), 0o644)
	}
	cfg := defaultConfig()
	cfg.Excludes = nil
	cfg.Groups = map[string][]string{"api": {filepath.Join(api, "*.log")}}
	r, err := resolveTarget(cfg, "api", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Paths) != 3 {
		t.Fatalf("history paths=%#v", r.Paths)
	}
}

func TestLikelyLogDoesNotMatchLoggConfig(t *testing.T) {
	if likelyLog("/tmp/logc.conf") {
		t.Fatal("logc.conf should not be treated as a log")
	}
	if !likelyLog("/tmp/app.log") {
		t.Fatal("app.log should be a log")
	}
}

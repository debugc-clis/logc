package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigGroupsAndIgnoreLines(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "logg.conf")
	body := "group.api=/srv/api/*.log\n[group.worker]\npath=/srv/worker/*.log\nignore_line=.*health.*\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOGG_CONFIG", p)
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Groups["api"]) != 1 || len(cfg.Groups["worker"]) != 1 {
		t.Fatalf("groups: %#v", cfg.Groups)
	}
	if len(cfg.IgnoreLines) != 1 {
		t.Fatalf("ignore lines: %#v", cfg.IgnoreLines)
	}
}

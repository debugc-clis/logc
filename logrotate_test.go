package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadLogrotateRegistryFollowsIncludes(t *testing.T) {
	directory := t.TempDir()
	configDirectory := filepath.Join(directory, "logrotate.d")
	if err := os.Mkdir(configDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	apiLog := filepath.Join(directory, "api.log")
	workerLog := filepath.Join(directory, "worker log.log")
	mainConfig := filepath.Join(directory, "logrotate.conf")
	if err := os.WriteFile(mainConfig, []byte("weekly\ninclude "+configDirectory+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := apiLog + "\n\"" + workerLog + "\"\n{\n  rotate 7\n}\n"
	if err := os.WriteFile(filepath.Join(configDirectory, "application"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDirectory, "application.rpmnew"), []byte(filepath.Join(directory, "ignored.log")+" {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	registry := loadLogrotateRegistry(mainConfig)
	if !registry.managed(apiLog) {
		t.Fatalf("expected %s to be managed; patterns=%#v", apiLog, registry.patterns)
	}
	if !registry.managed(workerLog) {
		t.Fatalf("expected quoted path %s to be managed; patterns=%#v", workerLog, registry.patterns)
	}
	if registry.managed(filepath.Join(directory, "ignored.log")) {
		t.Fatal("parsed ignored package-manager backup config")
	}
}

func TestLogrotateRegistryMatchesGlobsAndHistoricalFiles(t *testing.T) {
	directory := t.TempDir()
	registry := &logrotateRegistry{patterns: []string{filepath.Join(directory, "*.log")}}
	for _, path := range []string{
		filepath.Join(directory, "api.log"),
		filepath.Join(directory, "api.log.1"),
		filepath.Join(directory, "api.log.2.gz"),
		filepath.Join(directory, "api.log-20260906.gz"),
	} {
		if !registry.managed(path) {
			t.Errorf("expected %s to match logrotate pattern", path)
		}
	}
	if registry.managed(filepath.Join(directory, "notes.txt")) {
		t.Fatal("matched unrelated file")
	}
}

func TestLogrotateStatus(t *testing.T) {
	registry := &logrotateRegistry{patterns: []string{"/var/log/nginx/*.log"}}
	if got := logrotateStatus([]string{"/var/log/nginx/access.log"}, registry); got != "yes" {
		t.Fatalf("status=%q, want yes", got)
	}
	if got := logrotateStatus([]string{"/var/log/nginx/access.log", "/opt/log/api.log"}, registry); got != "partial" {
		t.Fatalf("status=%q, want partial", got)
	}
	if got := logrotateStatus([]string{"/opt/log/api.log"}, registry); got != "-" {
		t.Fatalf("status=%q, want -", got)
	}
}

func TestPrinterHeaderMarksLogrotateManagedFile(t *testing.T) {
	printer := &printer{logrotate: func(path string) bool { return path == "/var/log/api.log" }}
	header := printer.header("/var/log/api.log", "new file")
	if !strings.Contains(header, "new file · logrotate") {
		t.Fatalf("header=%q", header)
	}
}

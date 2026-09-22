package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestShouldFollowDefaultsToTrue(t *testing.T) {
	tests := []struct {
		name string
		opts cliOptions
		want bool
	}{
		{name: "default", want: true},
		{name: "explicit follow", opts: cliOptions{FollowSet: true, Follow: true}, want: true},
		{name: "disabled follow", opts: cliOptions{FollowSet: true, Follow: false}, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldFollow(test.opts); got != test.want {
				t.Fatalf("shouldFollow(%+v) = %t, want %t", test.opts, got, test.want)
			}
		})
	}
}

func TestParseCLIRejectsRemovedNoFollowFlag(t *testing.T) {
	_, err := parseCLI([]string{"--no-follow"}, defaultConfig())
	if err == nil {
		t.Fatal("parseCLI accepted removed --no-follow flag")
	}
}

func TestParseCLIRecognizesJSON(t *testing.T) {
	opts, err := parseCLI([]string{"--json", "ERROR"}, defaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !opts.JSON || len(opts.Positionals) != 1 || opts.Positionals[0] != "ERROR" {
		t.Fatalf("options=%#v", opts)
	}
}

func TestParseCLIRecognizesSourceFiltersAndFullRows(t *testing.T) {
	options, err := parseCLI([]string{"--category", "app,web", "--module", "payment", "--full", "ERROR"}, defaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(options.Categories) != 1 || options.Categories[0] != "app,web" || len(options.Modules) != 1 || options.Modules[0] != "payment" || !options.Full {
		t.Fatalf("options=%#v", options)
	}
}

func TestParseCLIRecognizesAllHistory(t *testing.T) {
	options, err := parseCLI([]string{"--all", "ERROR"}, defaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !options.All {
		t.Fatalf("options=%#v", options)
	}
}

func TestFormatAge(t *testing.T) {
	now := time.Now()
	for _, test := range []struct {
		at   time.Time
		want string
	}{
		{time.Time{}, "-"},
		{now.Add(-30 * time.Second), "30s"},
		{now.Add(-5 * time.Minute), "5m"},
		{now.Add(-2 * time.Hour), "2h"},
		{now.Add(-3 * 24 * time.Hour), "3d"},
	} {
		if got := formatAgeAt(test.at, now); got != test.want {
			t.Fatalf("formatAge(%s)=%q, want %q", test.at, got, test.want)
		}
	}
}

func TestWriteSourceListJSON(t *testing.T) {
	latest := time.Date(2026, time.August, 13, 10, 0, 0, 0, time.UTC)
	sources := []sourceSummary{{
		ID: "api", Category: "app", Module: "payment", Kind: "group",
		Root: "/srv/api", Paths: []string{"/srv/api/app.log"}, Latest: latest,
	}}
	var output bytes.Buffer
	if code := writeSourceListJSON(&output, sources, &logrotateRegistry{}); code != 0 {
		t.Fatalf("exit code=%d", code)
	}
	var rows []map[string]any
	if err := json.Unmarshal(output.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["id"] != "api" || rows[0]["location"] != "/srv/api" {
		t.Fatalf("rows=%#v", rows)
	}
}

func TestWriteResolvedTargetJSON(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "api.log")
	cfg := defaultConfig()
	cfg.Groups = map[string][]string{"api": {path}}
	cfg.GroupCategories = map[string]string{"api": "app"}
	cfg.GroupModules = map[string]string{"api": "payment"}
	var output bytes.Buffer
	if code := writeResolvedTargetJSON(&output, cfg, "api", []string{path}, &logrotateRegistry{}); code != 0 {
		t.Fatalf("exit code=%d", code)
	}
	var result struct {
		Target string `json:"target"`
		Type   string `json:"type"`
		Paths  []struct {
			Path     string `json:"path"`
			Category string `json:"category"`
			Module   string `json:"module"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Target != "api" || result.Type != "files" || len(result.Paths) != 1 || result.Paths[0].Path != path || result.Paths[0].Module != "payment" {
		t.Fatalf("result=%#v", result)
	}
}

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func overlappingGroupFixture(t *testing.T, reverse bool) (Config, string, string) {
	t.Helper()
	root := t.TempDir()
	shared := filepath.Join(root, "logs", "api.log")
	exclusive := filepath.Join(root, "exclusive", "worker.log")
	for _, path := range []string{shared, exclusive} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("ERROR fixture\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := defaultConfig()
	cfg.DefaultLogDirs = []string{root}
	cfg.Excludes = nil
	cfg.MaxFiles = 1
	cfg.Recent = time.Hour
	cfg.Groups = map[string][]string{}
	cfg.GroupCategories = map[string]string{"A-broad": "database", "a-exact": "app", "z-glob": "web"}
	cfg.GroupModules = map[string]string{"A-broad": "payments", "a-exact": "worker", "z-glob": "frontend"}
	names := []string{"0-unmatched", "A-broad", "a-exact", "z-glob"}
	patterns := [][]string{{filepath.Join(root, "missing", "*.log")}, {filepath.Dir(shared)}, {shared, exclusive}, {filepath.Join(filepath.Dir(shared), "*.log")}}
	for index := range names {
		position := index
		if reverse {
			position = len(names) - 1 - index
		}
		cfg.Groups[names[position]] = patterns[position]
	}
	return cfg, shared, exclusive
}

func TestOverlappingGroupMetadataPrecedence(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		name := "forward-insertion"
		if reverse {
			name = "reverse-insertion"
		}
		t.Run(name, func(t *testing.T) {
			cfg, shared, _ := overlappingGroupFixture(t, reverse)
			for iteration := 0; iteration < 256; iteration++ {
				if group := groupNameForPath(cfg, shared); group != "A-broad" {
					t.Fatalf("lookup %d: group=%q, want case-sensitive lexical first match A-broad", iteration, group)
				}
				category, module := sourceMetadata(cfg, shared)
				if category != "database" || module != "payments" {
					t.Fatalf("lookup %d: category=%q module=%q", iteration, category, module)
				}
			}
		})
	}
}

func TestOverlappingGroupWhereJSONPrecedence(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		cfg, shared, _ := overlappingGroupFixture(t, reverse)
		for iteration := 0; iteration < 64; iteration++ {
			var output bytes.Buffer
			if code := writeResolvedTargetJSON(&output, cfg, "z-glob", []string{shared}, &logrotateRegistry{}); code != 0 {
				t.Fatalf("write where JSON exit=%d", code)
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
			if result.Target != "z-glob" || result.Type != "files" || len(result.Paths) != 1 || result.Paths[0].Path != shared || result.Paths[0].Category != "database" || result.Paths[0].Module != "payments" {
				t.Fatalf("reverse=%t lookup=%d where JSON=%s", reverse, iteration, output.String())
			}
		}
	}
}

func TestOverlappingGroupFiltersAndDiscovery(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		cfg, shared, exclusive := overlappingGroupFixture(t, reverse)
		for iteration := 0; iteration < 64; iteration++ {
			for _, filters := range []sourceFilter{
				{categories: []string{"DATABASE"}},
				{modules: []string{"PAYMENTS"}},
				{categories: []string{"database"}, modules: []string{"payments"}},
				{categories: []string{"web"}, modules: []string{"frontend"}},
			} {
				want := []string{shared}
				if len(filters.categories) > 0 && filters.categories[0] == "web" {
					want = nil
				}
				paths := filterSourcePaths(cfg, []string{shared, exclusive}, filters.categories, filters.modules)
				requireGroupPaths(t, paths, want)
				for _, history := range []bool{false, true} {
					resolved, err := resolveDefaultTarget(cfg, history, time.Now().Add(-time.Hour), filters)
					if err != nil || !resolved.DefaultDiscovery {
						t.Fatalf("resolve default: %+v err=%v", resolved, err)
					}
					requireGroupPaths(t, resolved.Paths, want)
				}
			}
		}
	}
}

func TestOverlappingGroupExplicitSelectorsUnchanged(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		cfg, shared, exclusive := overlappingGroupFixture(t, reverse)
		for iteration := 0; iteration < 64; iteration++ {
			for _, name := range []string{"A-broad", "a-exact", "z-glob"} {
				resolved, err := resolveTarget(cfg, name, false)
				if err != nil || resolved.DefaultDiscovery || resolved.Name != name {
					t.Fatalf("explicit group %q: %+v err=%v", name, resolved, err)
				}
				want := []string{shared}
				if name == "a-exact" {
					want = append(want, exclusive)
				}
				requireGroupPaths(t, resolved.Paths, want)
				requireGroupPaths(t, resolved.Patterns, cfg.Groups[name])
				category, module := sourceMetadata(cfg, shared)
				if category != "database" || module != "payments" {
					t.Fatalf("selecting %q must not override shared-path metadata: %s/%s", name, category, module)
				}
			}
		}
	}
}

func TestGroupPrecedenceNonOverlappingCompatibility(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "logs", "api.log")
	for _, pattern := range []string{path, filepath.Dir(path), filepath.Join(filepath.Dir(path), "*.log")} {
		cfg := defaultConfig()
		cfg.Groups = map[string][]string{"only": {pattern}}
		cfg.GroupCategories = nil
		cfg.GroupModules = nil
		if group := groupNameForPath(cfg, path); group != "only" {
			t.Fatalf("single group pattern=%s group=%q", pattern, group)
		}
		category, module := sourceMetadata(cfg, path)
		if category != "app" || module != "only" {
			t.Fatalf("single group defaults changed: %s/%s", category, module)
		}
	}
	cfg := defaultConfig()
	category, module := sourceMetadata(cfg, "/var/log/mysql/error.log")
	if category != "database" || module != "mysql" {
		t.Fatalf("built-in mysql metadata changed: %s/%s", category, module)
	}
	cfg.Groups = map[string][]string{"only": {filepath.Dir(path)}}
	ungrouped := filepath.Join(directory, "logs-other", "api.log")
	if group := groupNameForPath(cfg, ungrouped); group != "" {
		t.Fatalf("directory-prefix sibling incorrectly matched group %q", group)
	}
	category, module = sourceMetadata(cfg, ungrouped)
	if category != "app" || module != "logs-other" {
		t.Fatalf("ungrouped metadata changed: %s/%s", category, module)
	}
	cfg.Groups = nil
	category, module = sourceMetadata(cfg, "/var/log/nginx/access.log")
	if category != "web" || module != "nginx" {
		t.Fatalf("ungrouped heuristics changed: %s/%s", category, module)
	}
}

func requireGroupPaths(t *testing.T, got, want []string) {
	t.Helper()
	got = uniqueSorted(got)
	want = uniqueSorted(want)
	if len(got) != len(want) {
		t.Fatalf("paths=%v want=%v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("paths=%v want=%v", got, want)
		}
	}
}

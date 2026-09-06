package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestExplicitMatchIncludesDefaultHistory(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{"app.log", "app.log.1", "app.log.2.gz"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("ERROR event\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	config := defaultConfig()
	config.DefaultLogDirs = []string{directory}
	config.Excludes = nil
	resolved, query, err := interpretPositionals(config, nil, "ERROR", true, timeZero)
	if err != nil {
		t.Fatal(err)
	}
	if query != "ERROR" || len(resolved.Paths) != 3 {
		t.Fatalf("query=%q paths=%#v", query, resolved.Paths)
	}
}

func TestDefaultRecentHistoryFiltersBeforeFairLimit(t *testing.T) {
	root := t.TempDir()
	hotDirectory := filepath.Join(root, "hot")
	if err := os.MkdirAll(hotDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	hotPaths := []string{
		filepath.Join(hotDirectory, "app.log"),
		filepath.Join(hotDirectory, "app.log.1"),
	}
	for index, path := range hotPaths {
		if err := os.WriteFile(path, []byte("ERROR recent\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		modified := now.Add(-time.Duration(index) * time.Hour)
		if err := os.Chtimes(path, modified, modified); err != nil {
			t.Fatal(err)
		}
	}
	for index := 0; index < 4; index++ {
		directory := filepath.Join(root, fmt.Sprintf("old-%d", index))
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(directory, "app.log")
		if err := os.WriteFile(path, []byte("ERROR old\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		old := now.Add(-48 * time.Hour)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	config := defaultConfig()
	config.DefaultLogDirs = []string{root}
	config.Excludes = nil
	config.MaxFiles = 1
	resolved, err := resolveDefaultTarget(config, true, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.Paths) != len(hotPaths) {
		t.Fatalf("paths=%#v", resolved.Paths)
	}
	for _, path := range hotPaths {
		if !containsString(resolved.Paths, path) {
			t.Fatalf("recent history missing %q: %#v", path, resolved.Paths)
		}
	}
	allHistory, err := resolveDefaultTarget(config, true, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(allHistory.Paths) != 6 {
		t.Fatalf("all history was limited: %#v", allHistory.Paths)
	}
}

func TestDefaultRecentHistoryWarnsWhenCandidateLimitApplies(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	for index := 0; index < 6; index++ {
		directory := filepath.Join(root, fmt.Sprintf("service-%d", index))
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(directory, "app.log")
		if err := os.WriteFile(path, []byte("ERROR recent\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, now, now); err != nil {
			t.Fatal(err)
		}
	}
	config := defaultConfig()
	config.DefaultLogDirs = []string{root}
	config.Excludes = nil
	config.MaxFiles = 1
	resolved, err := resolveDefaultTarget(config, true, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.Paths) != 5 || len(resolved.Warnings) != 1 {
		t.Fatalf("paths=%#v warnings=%#v", resolved.Paths, resolved.Warnings)
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

func TestListSourcesKeepsConfiguredPatternForInactiveGroup(t *testing.T) {
	cfg := defaultConfig()
	cfg.DefaultLogDirs = nil
	cfg.Groups = map[string][]string{"api": {"/srv/api/*.log"}}

	sources := listSources(cfg)
	if len(sources) != 1 || sources[0].Root != "/srv/api/*.log" || len(sources[0].Paths) != 0 {
		t.Fatalf("sources=%#v", sources)
	}
}

func TestExplicitExtensionlessFileIsAccepted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events")
	if err := os.WriteFile(path, []byte("INFO event\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := defaultConfig()
	cfg.Excludes = nil
	resolved, err := resolveTarget(cfg, path, true)
	if err != nil || len(resolved.Paths) != 1 || resolved.Paths[0] != path {
		t.Fatalf("resolved=%#v err=%v", resolved, err)
	}
}

func TestDirectoryDiscoveryIncludesLogSymlink(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target.log")
	if err := os.WriteFile(target, []byte("INFO event\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	links := filepath.Join(directory, "links")
	if err := os.Mkdir(links, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(links, "pod.log")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	paths, err := resolvePatterns([]string{links}, nil)
	if err != nil || len(paths) != 1 || paths[0] != link {
		t.Fatalf("paths=%#v err=%v", paths, err)
	}
}

func TestSourceMetadataAndFilters(t *testing.T) {
	directory := t.TempDir()
	nginxDirectory := filepath.Join(directory, "nginx")
	appDirectory := filepath.Join(directory, "payment")
	if err := os.MkdirAll(nginxDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(appDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	nginx := filepath.Join(nginxDirectory, "access.log")
	app := filepath.Join(appDirectory, "api.log")
	for _, path := range []string{nginx, app} {
		if err := os.WriteFile(path, []byte("INFO event\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	config := defaultConfig()
	config.DefaultLogDirs = []string{directory}
	config.Excludes = nil
	config.Groups = map[string][]string{"payment-api": {app}}
	config.GroupCategories = map[string]string{"payment-api": "app"}
	config.GroupModules = map[string]string{"payment-api": "payment"}

	category, module := sourceMetadata(config, app)
	if category != "app" || module != "payment" {
		t.Fatalf("category=%q module=%q", category, module)
	}
	filtered := filterSourcePaths(config, []string{nginx, app}, []string{"web"}, nil)
	if len(filtered) != 1 || filtered[0] != nginx {
		t.Fatalf("filtered=%#v", filtered)
	}
	sources := listSources(config)
	foundUniqueAutoID := false
	for _, source := range sources {
		if source.Kind == "auto" && source.ID == "web/nginx" {
			foundUniqueAutoID = true
		}
	}
	if !foundUniqueAutoID {
		t.Fatalf("sources=%#v", sources)
	}
	resolved, err := resolveTarget(config, "web/nginx", false)
	if err != nil || len(resolved.Paths) != 1 || resolved.Paths[0] != nginx {
		t.Fatalf("resolved=%#v err=%v", resolved, err)
	}
}

func TestAutoSourceIDsDisambiguateSameRelativeDirectory(t *testing.T) {
	base := t.TempDir()
	firstRoot := filepath.Join(base, "first")
	secondRoot := filepath.Join(base, "second")
	for _, root := range []string{firstRoot, secondRoot} {
		directory := filepath.Join(root, "log")
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "app.log"), []byte("INFO event\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	config := defaultConfig()
	config.DefaultLogDirs = []string{firstRoot, secondRoot}
	config.Excludes = nil
	config.Groups = nil
	sources := listSources(config)
	if len(sources) != 2 || sources[0].ID == sources[1].ID {
		t.Fatalf("sources=%#v", sources)
	}
}

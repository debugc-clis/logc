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

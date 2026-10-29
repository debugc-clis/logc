package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func filteredDiscoveryFixture(t *testing.T) (Config, []string) {
	t.Helper()
	root := t.TempDir()
	cfg := defaultConfig()
	cfg.DefaultLogDirs = []string{root}
	cfg.MaxFiles = 2
	cfg.Recent = time.Hour
	cfg.Excludes = nil
	cfg.IgnoreLines = nil
	cfg.Groups = map[string][]string{}
	cfg.GroupCategories = map[string]string{}
	cfg.GroupModules = map[string]string{}
	now := time.Now()
	for index := 0; index < 12; index++ {
		path := filepath.Join(root, fmt.Sprintf("noise-%02d", index), "app.log")
		writeFilteredDiscoveryLog(t, path, now.Add(-time.Duration(index)*time.Second))
	}
	var eligible []string
	for index := 0; index < 2; index++ {
		name := fmt.Sprintf("db-%d", index)
		directory := filepath.Join(root, name)
		path := filepath.Join(directory, "db.log")
		writeFilteredDiscoveryLog(t, path, now.Add(-time.Duration(10+index)*time.Minute))
		cfg.Groups[name] = []string{filepath.Join(directory, "*.log*")}
		cfg.GroupCategories[name] = "database"
		cfg.GroupModules[name] = "payments"
		eligible = append(eligible, path)
	}
	return cfg, eligible
}

func writeFilteredDiscoveryLog(t *testing.T, path string, modified time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	writeSearchFollowLog(t, path, "ERROR fixture "+path+"\n")
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatal(err)
	}
}

func requireFilteredPaths(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(uniqueSorted(got), "\n") != strings.Join(uniqueSorted(want), "\n") {
		t.Fatalf("paths=%v want=%v", got, want)
	}
}

func TestFilteredDefaultResolutionBeforeLimits(t *testing.T) {
	for _, flags := range [][]string{
		{"--category", "DATABASE,web"},
		{"--module", "PAYMENTS,missing"},
		{"--category", "database", "--module", "payments"},
		{"--category", "app", "--module", "payments"},
	} {
		t.Run(strings.Join(flags, "_"), func(t *testing.T) {
			cfg, eligible := filteredDiscoveryFixture(t)
			if flags[1] == "app" {
				eligible = nil
			}
			for _, mode := range []string{"plain", "severity", "match", "unknown-regex", "watch", "watch-match"} {
				t.Run(mode, func(t *testing.T) {
					args := append([]string(nil), flags...)
					switch mode {
					case "severity", "watch":
						args = append(args, "ERROR")
					case "match", "watch-match":
						args = append(args, "--match", "ERROR")
					case "unknown-regex":
						args = append(args, "no-such-source-needle")
					}
					opts, err := parseCLI(args, cfg)
					if err != nil {
						t.Fatal(err)
					}
					filters := sourceFilter{categories: opts.Categories, modules: opts.Modules}
					var resolved ResolvedTarget
					var pattern string
					if strings.HasPrefix(mode, "watch") {
						resolved, pattern, err = resolveWatchArgsWithHistory(cfg, opts.Positionals, opts.Match, false, time.Time{}, filters)
					} else {
						resolved, pattern, err = interpretPositionals(cfg, opts.Positionals, opts.Match, false, time.Time{}, filters)
					}
					if err != nil || !resolved.DefaultDiscovery {
						t.Fatalf("resolved=%+v err=%v", resolved, err)
					}
					if mode == "unknown-regex" && pattern != "no-such-source-needle" {
						t.Fatalf("unknown regex changed meaning: %q", pattern)
					}
					paths := filterSourcePaths(cfg, resolved.Paths, opts.Categories, opts.Modules)
					requireFilteredPaths(t, paths, eligible)
				})
			}
		})
	}
}

func TestFilteredRecentHistoryBudget(t *testing.T) {
	for _, filters := range []sourceFilter{
		{categories: []string{"database"}},
		{modules: []string{"payments"}},
		{categories: []string{"database"}, modules: []string{"payments"}},
	} {
		t.Run(fmt.Sprint(filters), func(t *testing.T) {
			cfg, eligible := filteredDiscoveryFixture(t)
			cutoff := time.Now().Add(-time.Hour)
			var all []string
			for _, path := range eligible {
				all = append(all, path)
				for index := 1; index <= 5; index++ {
					history := fmt.Sprintf("%s.%d", path, index)
					writeFilteredDiscoveryLog(t, history, time.Now().Add(-time.Duration(12+index)*time.Minute))
					all = append(all, history)
				}
				writeFilteredDiscoveryLog(t, path+".old.gz", cutoff.Add(-time.Minute))
				writeFilteredDiscoveryLog(t, filepath.Join(filepath.Dir(path), "excluded.log"), time.Now())
			}
			cfg.Excludes = []string{filepath.Join(cfg.DefaultLogDirs[0], "*", "excluded.log")}
			for _, watch := range []bool{false, true} {
				var resolved ResolvedTarget
				var err error
				if watch {
					resolved, _, err = resolveWatchArgsWithHistory(cfg, []string{"ERROR"}, "", true, cutoff, filters)
				} else {
					resolved, _, err = interpretPositionals(cfg, []string{"ERROR"}, "", true, cutoff, filters)
				}
				if err != nil {
					t.Fatal(err)
				}
				paths := filterSourcePaths(cfg, resolved.Paths, filters.categories, filters.modules)
				if len(paths) != cfg.MaxFiles*5 || len(resolved.Warnings) != 1 || !strings.Contains(resolved.Warnings[0], "found 12 files; searching 10") {
					t.Fatalf("watch=%t paths=%v warnings=%v", watch, paths, resolved.Warnings)
				}
				for _, path := range paths {
					if !containsString(all, path) {
						t.Errorf("ineligible recent candidate %s", path)
					}
				}
				for _, active := range eligible {
					count := 0
					for _, path := range paths {
						if filepath.Dir(path) == filepath.Dir(active) {
							count++
						}
					}
					if count != 5 {
						t.Errorf("unfair history allocation for %s: %d", active, count)
					}
				}
			}
			uncapped, err := resolveDefaultTarget(cfg, true, time.Time{}, filters)
			if err != nil || len(uncapped.Paths) != 14 || len(uncapped.Warnings) != 0 {
				t.Fatalf("all-history paths=%v warnings=%v err=%v", uncapped.Paths, uncapped.Warnings, err)
			}
		})
	}
}

func TestFilteredDefaultFollowWatchRescan(t *testing.T) {
	for _, mode := range []string{"follow", "watch"} {
		for _, filters := range []sourceFilter{{categories: []string{"database"}}, {modules: []string{"payments"}}, {categories: []string{"database"}, modules: []string{"payments"}}} {
			t.Run(mode+fmt.Sprint(filters), func(t *testing.T) {
				cfg, eligible := filteredDiscoveryFixture(t)
				if err := os.Remove(eligible[1]); err != nil {
					t.Fatal(err)
				}
				var output, diagnostics bytes.Buffer
				query, err := buildQuery("ERROR", false, time.Time{}, 0, 0, false, nil)
				if err != nil {
					t.Fatal(err)
				}
				follower := newFollower(cfg, cfg.DefaultLogDirs, cfg.Excludes, &printer{stdout: &output, stderr: &diagnostics}, query, true)
				follower.defaultDiscovery = true
				follower.categories, follower.modules = filters.categories, filters.modules
				watcher := newAlertWatcher(cfg, cfg.DefaultLogDirs, cfg.Excludes, nil, query)
				watcher.defaultDiscovery = true
				watcher.categories, watcher.modules = filters.categories, filters.modules
				defer follower.closeStates()
				defer func() {
					for _, state := range watcher.states {
						state.close()
					}
				}()
				states := follower.states
				rescan := follower.rescan
				if mode == "watch" {
					states = watcher.states
					rescan = func() { watcher.rescan(false) }
				}
				rescan()
				if len(states) != 1 || states[eligible[0]] == nil {
					t.Fatalf("initial filtered %s states=%v", mode, states)
				}
				writeFilteredDiscoveryLog(t, eligible[1], time.Now().Add(-time.Minute))
				rescan()
				if len(states) != 2 || states[eligible[1]] == nil {
					t.Fatalf("new eligible file not discovered: %v", states)
				}
				file, err := os.OpenFile(eligible[1], os.O_APPEND|os.O_WRONLY, 0o600)
				if err != nil {
					t.Fatal(err)
				}
				_, writeErr := file.WriteString("ERROR fresh eligible append\n")
				closeErr := file.Close()
				if writeErr != nil || closeErr != nil {
					t.Fatalf("append write=%v close=%v", writeErr, closeErr)
				}
				for iteration := 0; iteration < 2; iteration++ {
					if mode == "follow" {
						follower.poll()
						follower.flushFair()
					} else {
						watcher.poll()
					}
					rescan()
				}
				if mode == "follow" && strings.Count(output.String(), "ERROR fresh eligible append") != 1 {
					t.Fatalf("append not emitted exactly once: %s", output.String())
				}
				if mode == "watch" {
					alerts, _ := watcher.tracker.summaries(time.Now())
					if len(alerts) != 1 || alerts[0].Count != 1 {
						t.Fatalf("append not observed exactly once: %v", alerts)
					}
				}
			})
		}
	}
}

func TestFilteredWatchSinceBootstrap(t *testing.T) {
	cfg, eligible := filteredDiscoveryFixture(t)
	for _, path := range eligible {
		writeFilteredDiscoveryLog(t, path+".1", time.Now().Add(-15*time.Minute))
	}
	cutoff := time.Now().Add(-time.Hour)
	filters := sourceFilter{categories: []string{"database"}, modules: []string{"payments"}}
	resolved, pattern, err := resolveWatchArgsWithHistory(cfg, []string{"ERROR"}, "", true, cutoff, filters)
	if err != nil {
		t.Fatal(err)
	}
	query, err := buildQuery(pattern, false, cutoff, 0, 0, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	paths := filterSourcePaths(cfg, resolved.Paths, filters.categories, filters.modules)
	watcher := newAlertWatcher(cfg, resolved.Patterns, cfg.Excludes, paths, query)
	watcher.bootstrap()
	alerts, _ := watcher.tracker.summaries(time.Now())
	if len(paths) != 4 || len(alerts) != 4 {
		t.Fatalf("bootstrap paths=%v alerts=%v warnings=%v", paths, alerts, resolved.Warnings)
	}
}

func TestFilteredQuietRetentionBeforeBudget(t *testing.T) {
	for _, mode := range []string{"follow", "watch"} {
		for _, filters := range []sourceFilter{{categories: []string{"database"}}, {modules: []string{"payments"}}, {categories: []string{"database"}, modules: []string{"payments"}}} {
			t.Run(mode+fmt.Sprint(filters), func(t *testing.T) {
				cfg, eligible := filteredDiscoveryFixture(t)
				old := time.Now().Add(-2 * cfg.Recent)
				ineligible := filepath.Join(cfg.DefaultLogDirs[0], "a-ineligible", "app.log")
				writeFilteredDiscoveryLog(t, ineligible, old)
				if err := os.Chtimes(eligible[0], old, old); err != nil {
					t.Fatal(err)
				}
				var output, diagnostics bytes.Buffer
				follower := newFollower(cfg, cfg.DefaultLogDirs, nil, &printer{stdout: &output, stderr: &diagnostics}, Query{}, true)
				follower.defaultDiscovery = true
				follower.categories, follower.modules = filters.categories, filters.modules
				defer follower.closeStates()
				watcher := newAlertWatcher(cfg, cfg.DefaultLogDirs, nil, nil, Query{})
				watcher.defaultDiscovery = true
				watcher.categories, watcher.modules = filters.categories, filters.modules
				defer func() {
					for _, state := range watcher.states {
						state.close()
					}
				}()
				states := follower.states
				add := func(path string) { follower.addPath(path, false) }
				rescan := follower.rescan
				if mode == "watch" {
					states = watcher.states
					add = func(path string) { watcher.addPath(path, false) }
					rescan = func() { watcher.rescan(false) }
				}
				add(ineligible)
				add(eligible[0])
				rescan()
				if len(states) != cfg.MaxFiles || states[eligible[0]] == nil || states[eligible[1]] == nil || states[ineligible] != nil {
					t.Fatalf("quiet eligible source lost spare slot to earlier-sorted ineligible state: %v", states)
				}
				fresh := filepath.Join(cfg.DefaultLogDirs[0], "db-2", "db.log")
				cfg.Groups["db-2"] = []string{fresh}
				cfg.GroupCategories["db-2"] = "database"
				cfg.GroupModules["db-2"] = "payments"
				writeFilteredDiscoveryLog(t, fresh, time.Now())
				rescan()
				if len(states) != cfg.MaxFiles || states[fresh] == nil || states[eligible[1]] == nil || states[eligible[0]] != nil {
					t.Fatalf("fresh eligible sources did not replace quiet source within cap: %v", states)
				}
			})
		}
	}
}

func TestFilteredDiscoveryCompatibility(t *testing.T) {
	cfg, eligible := filteredDiscoveryFixture(t)
	for _, filters := range []sourceFilter{{}, {categories: []string{" , "}, modules: []string{" "}}} {
		resolved, _, err := interpretPositionals(cfg, nil, "", false, time.Time{}, filters)
		if err != nil {
			t.Fatal(err)
		}
		requireFilteredPaths(t, resolved.Paths, []string{
			filepath.Join(cfg.DefaultLogDirs[0], "noise-00", "app.log"),
			filepath.Join(cfg.DefaultLogDirs[0], "noise-01", "app.log"),
		})
	}
	filters := sourceFilter{categories: []string{"database"}, modules: []string{"payments"}}
	for _, path := range eligible {
		writeFilteredDiscoveryLog(t, path+".1", time.Now().Add(-15*time.Minute))
	}
	for _, selector := range []string{cfg.DefaultLogDirs[0], "db-0", eligible[0]} {
		resolved, _, err := interpretPositionals(cfg, []string{selector, "ERROR"}, "", true, time.Now().Add(-time.Hour), filters)
		unfiltered, _, unfilteredErr := interpretPositionals(cfg, []string{selector, "ERROR"}, "", true, time.Now().Add(-time.Hour))
		if err != nil || unfilteredErr != nil || resolved.DefaultDiscovery {
			t.Fatalf("explicit selector=%s resolved=%+v err=%v unfilteredErr=%v", selector, resolved, err, unfilteredErr)
		}
		requireFilteredPaths(t, resolved.Paths, unfiltered.Paths)
	}
	opts, err := parseCLI([]string{"--current", "--category", "database", "--module", "payments", "ERROR"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	current, _, err := interpretPositionals(cfg, opts.Positionals, opts.Match, !opts.CurrentOnly, time.Now().Add(-time.Hour), filters)
	if err != nil {
		t.Fatal(err)
	}
	requireFilteredPaths(t, current.Paths, eligible)
	unknown, pattern, err := interpretPositionals(cfg, []string{"unmatched-source-expression"}, "", false, time.Now().Add(-time.Hour), filters)
	if err != nil || pattern != "unmatched-source-expression" || !unknown.DefaultDiscovery || len(unknown.Paths) != 4 {
		t.Fatalf("unknown default regex resolution changed: resolved=%+v pattern=%q err=%v", unknown, pattern, err)
	}
	outside := filepath.Join(t.TempDir(), "external.log")
	writeFilteredDiscoveryLog(t, outside, time.Now())
	cfg.Groups["external"] = []string{outside}
	cfg.GroupCategories["external"] = "database"
	cfg.GroupModules["external"] = "external"
	externalFilter := sourceFilter{modules: []string{"external"}}
	resolved, err := resolveDefaultTarget(cfg, false, time.Time{}, externalFilter)
	if err != nil || len(resolved.Paths) != 0 {
		t.Fatalf("default discovery expanded groups outside roots: %+v err=%v", resolved, err)
	}
}

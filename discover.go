package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type fileCandidate struct {
	Path    string
	ModTime time.Time
	Size    int64
}

type discoveryResult struct {
	Candidates []fileCandidate
	Warnings   []string
}

func excluded(path string, patterns []string) bool {
	abs, _ := filepath.Abs(path)
	for _, p := range patterns {
		ep := expandHome(p)
		if globMatch(ep, abs) || globMatch(ep, path) {
			return true
		}
	}
	return false
}

func likelyLog(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	if strings.HasSuffix(base, ".gz") {
		base = strings.TrimSuffix(base, ".gz")
	}
	ext := strings.ToLower(filepath.Ext(base))
	if ext == ".log" || ext == ".out" || ext == ".err" || ext == ".txt" {
		return true
	}
	if strings.Contains(base, ".log.") {
		return true
	}
	switch base {
	case "access_log", "error_log", "stdout", "stderr", "current":
		return true
	}
	return ext == "" && strings.Contains(base, "log")
}

func historicalLog(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	if strings.HasSuffix(base, ".gz") {
		return true
	}
	if strings.Contains(base, ".log.") {
		parts := strings.Split(base, ".log.")
		if len(parts) > 1 && parts[1] != "" {
			return true
		}
	}
	if strings.Contains(base, ".log-") {
		return true
	}
	return false
}

func collectLogCandidates(roots, excludes []string, cutoff time.Time, includeHistory bool) []fileCandidate {
	return collectLogCandidatesDetailed(roots, excludes, cutoff, includeHistory).Candidates
}

func collectLogCandidatesDetailed(roots, excludes []string, cutoff time.Time, includeHistory bool) discoveryResult {
	seen := map[string]bool{}
	warningSeen := map[string]bool{}
	var cs []fileCandidate
	var warnings []string
	warn := func(path string, err error) {
		message := fmt.Sprintf("cannot scan %s: %v", path, err)
		if !warningSeen[message] {
			warningSeen[message] = true
			warnings = append(warnings, message)
		}
	}
	for _, root := range roots {
		root = expandHome(root)
		info, err := os.Stat(root)
		if err != nil {
			if !os.IsNotExist(err) {
				warn(root, err)
			}
			continue
		}
		if !info.IsDir() {
			continue
		}
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				warn(path, err)
				if d != nil && d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if path != root && excluded(path, excludes) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				return nil
			}
			if !likelyLog(path) {
				return nil
			}
			if !includeHistory && historicalLog(path) {
				return nil
			}
			fi, err := os.Stat(path)
			if err != nil {
				warn(path, err)
				return nil
			}
			if !fi.Mode().IsRegular() {
				return nil
			}
			if !cutoff.IsZero() && fi.ModTime().Before(cutoff) {
				return nil
			}
			abs, _ := filepath.Abs(path)
			if !seen[abs] {
				seen[abs] = true
				cs = append(cs, fileCandidate{Path: abs, ModTime: fi.ModTime(), Size: fi.Size()})
			}
			return nil
		})
	}
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].ModTime.Equal(cs[j].ModTime) {
			return cs[i].Path < cs[j].Path
		}
		return cs[i].ModTime.After(cs[j].ModTime)
	})
	return discoveryResult{Candidates: cs, Warnings: warnings}
}

func discoverDefault(cfg Config) ([]string, error) {
	paths, _, err := discoverDefaultDetailed(cfg)
	return paths, err
}

func discoverDefaultDetailed(cfg Config) ([]string, []string, error) {
	result := collectLogCandidatesDetailed(cfg.DefaultLogDirs, cfg.Excludes, time.Now().Add(-cfg.Recent), false)
	cs := selectFairCandidates(result.Candidates, cfg.MaxFiles)
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Path)
	}
	return out, result.Warnings, nil
}

func selectFairCandidates(candidates []fileCandidate, limit int) []fileCandidate {
	if limit <= 0 || len(candidates) <= limit {
		return candidates
	}
	buckets := map[string][]fileCandidate{}
	var directories []string
	for _, candidate := range candidates {
		directory := filepath.Dir(candidate.Path)
		if _, exists := buckets[directory]; !exists {
			directories = append(directories, directory)
		}
		buckets[directory] = append(buckets[directory], candidate)
	}
	sort.Slice(directories, func(i, j int) bool {
		left, right := buckets[directories[i]][0], buckets[directories[j]][0]
		if left.ModTime.Equal(right.ModTime) {
			return directories[i] < directories[j]
		}
		return left.ModTime.After(right.ModTime)
	})

	selected := make([]fileCandidate, 0, limit)
	for round := 0; len(selected) < limit; round++ {
		added := false
		for _, directory := range directories {
			bucket := buckets[directory]
			if round >= len(bucket) {
				continue
			}
			selected = append(selected, bucket[round])
			added = true
			if len(selected) == limit {
				break
			}
		}
		if !added {
			break
		}
	}
	return selected
}

func resolvePatterns(patterns []string, excludes []string) ([]string, error) {
	return resolvePatternsWithHistory(patterns, excludes, false)
}

func resolvePatternsWithHistory(patterns []string, excludes []string, includeHistory bool) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	add := func(path string, explicit bool) {
		abs, _ := filepath.Abs(path)
		if excluded(abs, excludes) || seen[abs] || (!explicit && !likelyLog(abs)) {
			return
		}
		if !explicit && !includeHistory && historicalLog(abs) {
			return
		}
		seen[abs] = true
		out = append(out, abs)
	}
	for _, raw := range patterns {
		p := expandHome(raw)
		if filepath.Clean(literalRoot(p)) == string(filepath.Separator) {
			return nil, fmt.Errorf("refusing to recursively scan filesystem root")
		}
		if !hasMeta(p) {
			fi, err := os.Stat(p)
			if err != nil {
				continue
			}
			if fi.IsDir() {
				_ = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
					if err != nil {
						return nil
					}
					if path != p && excluded(path, excludes) {
						if d.IsDir() {
							return filepath.SkipDir
						}
						return nil
					}
					if d.IsDir() {
						return nil
					}
					info, statErr := os.Stat(path)
					if statErr != nil || !info.Mode().IsRegular() {
						return nil
					}
					add(path, false)
					return nil
				})
			} else if fi.Mode().IsRegular() {
				add(p, true)
			}
			continue
		}
		root := literalRoot(p)
		if _, err := os.Stat(root); err != nil {
			continue
		}
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if path != root && excluded(path, excludes) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				return nil
			}
			info, statErr := os.Stat(path)
			if statErr != nil || !info.Mode().IsRegular() {
				return nil
			}
			abs, _ := filepath.Abs(path)
			if globMatch(p, abs) || globMatch(p, path) {
				add(abs, false)
			}
			return nil
		})
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil, fmt.Errorf("no log files matched")
	}
	return out, nil
}

func fuzzyDiscover(cfg Config, name string, includeHistory bool) []string {
	needle := strings.ToLower(name)
	cs := collectLogCandidates(cfg.DefaultLogDirs, cfg.Excludes, time.Time{}, includeHistory)
	type scored struct {
		path  string
		score int
		mod   time.Time
	}
	var matches []scored
	for _, c := range cs {
		lp := strings.ToLower(filepath.ToSlash(c.Path))
		base := strings.ToLower(filepath.Base(c.Path))
		dir := strings.ToLower(filepath.Base(filepath.Dir(c.Path)))
		score := 0
		switch {
		case dir == needle:
			score = 100
		case strings.TrimSuffix(base, filepath.Ext(base)) == needle:
			score = 95
		case strings.Contains(dir, needle):
			score = 80
		case strings.Contains(base, needle):
			score = 70
		case strings.Contains(lp, "/"+needle+"/"):
			score = 90
		case strings.Contains(lp, needle):
			score = 40
		}
		if score > 0 {
			matches = append(matches, scored{c.Path, score, c.ModTime})
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].score == matches[j].score {
			return matches[i].mod.After(matches[j].mod)
		}
		return matches[i].score > matches[j].score
	})
	if len(matches) > cfg.MaxFiles {
		matches = matches[:cfg.MaxFiles]
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m.path)
	}
	return out
}

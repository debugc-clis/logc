package main

import (
	"bufio"
	"context"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

type ResolvedTarget struct {
	Name             string
	Patterns         []string
	Paths            []string
	JournalUnit      string
	Warnings         []string
	DefaultDiscovery bool
}

const selectorCommandTimeout = 2 * time.Second

func selectorOutput(path string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), selectorCommandTimeout)
	defer cancel()
	return exec.CommandContext(ctx, path, args...).Output()
}

func looksLikePath(s string) bool {
	return strings.ContainsAny(s, "/\\*?[~") || strings.HasPrefix(s, ".")
}

func resolveTarget(cfg Config, raw string, includeHistory bool) (ResolvedTarget, error) {
	if raw == "" {
		if includeHistory {
			result := collectLogCandidatesDetailed(cfg.DefaultLogDirs, cfg.Excludes, time.Time{}, true)
			limit := cfg.MaxFiles * 5
			if limit < cfg.MaxFiles {
				limit = cfg.MaxFiles
			}
			cs := selectFairCandidates(result.Candidates, limit)
			paths := make([]string, 0, len(cs))
			for _, c := range cs {
				paths = append(paths, c.Path)
			}
			return ResolvedTarget{Name: "default", Paths: paths, Patterns: cfg.DefaultLogDirs, Warnings: result.Warnings, DefaultDiscovery: true}, nil
		}
		paths, warnings, err := discoverDefaultDetailed(cfg)
		return ResolvedTarget{Name: "default", Paths: paths, Patterns: cfg.DefaultLogDirs, Warnings: warnings, DefaultDiscovery: true}, err
	}
	if pats, ok := cfg.Groups[raw]; ok {
		paths, err := resolvePatternsWithHistory(pats, cfg.Excludes, includeHistory)
		if err != nil {
			return ResolvedTarget{Name: raw, Patterns: pats}, err
		}
		if includeHistory {
			paths = expandRotatedSiblings(paths, cfg.Excludes)
		}
		return ResolvedTarget{Name: raw, Patterns: pats, Paths: paths}, nil
	}
	if strings.Contains(raw, "/") && !filepath.IsAbs(raw) && !strings.HasPrefix(raw, ".") && !hasMeta(raw) {
		if source, ok := sourceByID(cfg, raw); ok {
			return ResolvedTarget{Name: source.ID, Patterns: []string{source.Root}, Paths: source.Paths}, nil
		}
	}
	if strings.HasPrefix(raw, "@") {
		name := strings.TrimPrefix(raw, "@")
		if pid, err := strconv.Atoi(name); err == nil {
			paths := discoverPIDLogs(pid)
			if len(paths) == 0 {
				return ResolvedTarget{}, fmt.Errorf("no log files found for pid %d", pid)
			}
			return ResolvedTarget{Name: raw, Paths: paths, Patterns: paths}, nil
		}
		paths := discoverProcessLogs(name)
		if len(paths) > 0 {
			return ResolvedTarget{Name: raw, Paths: paths, Patterns: paths}, nil
		}
		if runtime.GOOS == "linux" && systemdUnitExists(name) {
			return ResolvedTarget{Name: raw, JournalUnit: name}, nil
		}
		return ResolvedTarget{}, fmt.Errorf("no process or systemd unit matched %q", name)
	}
	if strings.HasPrefix(raw, ":") {
		port, err := strconv.Atoi(strings.TrimPrefix(raw, ":"))
		if err != nil || port < 1 || port > 65535 {
			return ResolvedTarget{}, fmt.Errorf("invalid port selector %q", raw)
		}
		pid := pidForPort(port)
		if pid == 0 {
			return ResolvedTarget{}, fmt.Errorf("no process found listening on :%d", port)
		}
		paths := discoverPIDLogs(pid)
		if len(paths) == 0 {
			return ResolvedTarget{}, fmt.Errorf("pid %d owns :%d but no file logs were found", pid, port)
		}
		return ResolvedTarget{Name: raw, Paths: paths, Patterns: paths}, nil
	}
	if looksLikePath(raw) {
		paths, err := resolvePatternsWithHistory([]string{raw}, cfg.Excludes, includeHistory)
		if err == nil && includeHistory {
			paths = expandRotatedSiblings(paths, cfg.Excludes)
		}
		return ResolvedTarget{Name: raw, Patterns: []string{raw}, Paths: paths}, err
	}
	if fi, err := os.Stat(expandHome(raw)); err == nil && (fi.IsDir() || fi.Mode().IsRegular()) {
		paths, err := resolvePatternsWithHistory([]string{raw}, cfg.Excludes, includeHistory)
		if err == nil && includeHistory {
			paths = expandRotatedSiblings(paths, cfg.Excludes)
		}
		return ResolvedTarget{Name: raw, Patterns: []string{raw}, Paths: paths}, err
	}
	paths := fuzzyDiscover(cfg, raw, includeHistory)
	if len(paths) > 0 {
		return ResolvedTarget{Name: raw, Paths: paths, Patterns: paths}, nil
	}
	return ResolvedTarget{}, fmt.Errorf("no log source matched %q", raw)
}

func expandRotatedSiblings(paths, excludes []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(paths))
	add := func(p string, explicit bool) {
		abs, _ := filepath.Abs(p)
		if seen[abs] || excluded(abs, excludes) {
			return
		}
		fi, err := os.Stat(abs)
		if err != nil || !fi.Mode().IsRegular() || (!explicit && !likelyLog(abs)) {
			return
		}
		seen[abs] = true
		out = append(out, abs)
	}
	for _, p := range paths {
		add(p, true)
	}
	for _, p := range paths {
		dir, base := filepath.Dir(p), filepath.Base(p)
		ents, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, ent := range ents {
			if ent.IsDir() {
				continue
			}
			name := ent.Name()
			if strings.HasPrefix(name, base+".") || strings.HasPrefix(name, base+"-") {
				add(filepath.Join(dir, name), false)
			}
		}
	}
	sort.Strings(out)
	return out
}

func discoverPIDLogs(pid int) []string {
	root := fmt.Sprintf("/proc/%d/fd", pid)
	ents, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, ent := range ents {
		link, err := os.Readlink(filepath.Join(root, ent.Name()))
		if err != nil {
			continue
		}
		link = strings.TrimSuffix(link, " (deleted)")
		if !filepath.IsAbs(link) || !likelyLog(link) {
			continue
		}
		fi, err := os.Stat(link)
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		abs, _ := filepath.Abs(link)
		if !seen[abs] {
			seen[abs] = true
			out = append(out, abs)
		}
	}
	sort.Strings(out)
	return out
}

func discoverProcessLogs(name string) []string {
	if runtime.GOOS != "linux" {
		return nil
	}
	needle := strings.ToLower(name)
	seen := map[string]bool{}
	var out []string
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	for _, ent := range ents {
		pid, err := strconv.Atoi(ent.Name())
		if err != nil {
			continue
		}
		comm, _ := os.ReadFile(filepath.Join("/proc", ent.Name(), "comm"))
		cmd, _ := os.ReadFile(filepath.Join("/proc", ent.Name(), "cmdline"))
		text := strings.ToLower(strings.TrimSpace(string(comm)) + " " + strings.ReplaceAll(string(cmd), "\x00", " "))
		if !strings.Contains(text, needle) {
			continue
		}
		for _, p := range discoverPIDLogs(pid) {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	sort.Strings(out)
	return out
}

func pidForPort(port int) int {
	if path, err := exec.LookPath("lsof"); err == nil {
		b, _ := selectorOutput(path, "-nP", "-t", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN")
		if fields := strings.Fields(string(b)); len(fields) > 0 {
			if pid, e := strconv.Atoi(fields[0]); e == nil {
				return pid
			}
		}
	}
	if runtime.GOOS == "linux" {
		if path, err := exec.LookPath("ss"); err == nil {
			b, _ := selectorOutput(path, "-ltnp", "sport = :"+strconv.Itoa(port))
			s := bufio.NewScanner(strings.NewReader(string(b)))
			for s.Scan() {
				line := s.Text()
				i := strings.Index(line, "pid=")
				if i < 0 {
					continue
				}
				rest := line[i+4:]
				j := strings.IndexAny(rest, ",)")
				if j >= 0 {
					rest = rest[:j]
				}
				if pid, e := strconv.Atoi(rest); e == nil {
					return pid
				}
			}
		}
	}
	return 0
}

func systemdUnitExists(unit string) bool {
	path, err := exec.LookPath("systemctl")
	if err != nil {
		return false
	}
	b, err := selectorOutput(path, "show", "-p", "LoadState", "--value", unit)
	if err != nil {
		return false
	}
	state := strings.TrimSpace(string(b))
	return state != "" && state != "not-found"
}

func listSources(cfg Config) []sourceSummary {
	sources, _ := listSourcesDetailed(cfg)
	return sources
}

func listSourcesDetailed(cfg Config) ([]sourceSummary, []string) {
	byDir := map[string]*sourceSummary{}
	for name, pats := range cfg.Groups {
		paths, _ := resolvePatternsWithHistory(pats, cfg.Excludes, false)
		category := cfg.GroupCategories[name]
		if category == "" {
			category = "app"
		}
		module := cfg.GroupModules[name]
		if module == "" {
			module = name
		}
		summary := &sourceSummary{ID: name, Name: name, Category: category, Module: module, Paths: paths, Kind: "group"}
		if len(pats) > 0 {
			summary.Root = pats[0]
		}
		for _, path := range paths {
			if info, err := os.Stat(path); err == nil && info.ModTime().After(summary.Latest) {
				summary.Latest = info.ModTime()
			}
		}
		byDir["group:"+name] = summary
	}
	result := collectLogCandidatesDetailed(cfg.DefaultLogDirs, cfg.Excludes, timeZero, false)
	for _, c := range result.Candidates {
		dir := filepath.Dir(c.Path)
		name := filepath.Base(dir)
		key := "dir:" + dir
		if _, ok := byDir[key]; !ok {
			category, module := sourceMetadata(cfg, c.Path)
			byDir[key] = &sourceSummary{ID: autoSourceID(cfg, category, dir), Name: name, Category: category, Module: module, Kind: "auto", Root: dir}
		}
		byDir[key].Paths = append(byDir[key].Paths, c.Path)
		if c.ModTime.After(byDir[key].Latest) {
			byDir[key].Latest = c.ModTime
		}
	}
	out := make([]sourceSummary, 0, len(byDir))
	idCounts := map[string]int{}
	for _, source := range byDir {
		idCounts[source.ID]++
	}
	for _, v := range byDir {
		if v.Kind == "auto" {
			if groupNameForPath(cfg, v.Paths[0]) != "" {
				continue
			}
		}
		if idCounts[v.ID] > 1 {
			v.ID += "-" + sourceIDHash(v.Root)
		}
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind == out[j].Kind {
			return out[i].ID < out[j].ID
		}
		return out[i].Kind < out[j].Kind
	})
	return out, result.Warnings
}

func sourceIDHash(value string) string {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(value))
	return fmt.Sprintf("%08x", hash.Sum32())
}

var timeZero = func() (z time.Time) { return }()

type sourceSummary struct {
	ID, Name, Category, Module, Kind, Root string
	Paths                                  []string
	Latest                                 time.Time
}

func sourceByID(cfg Config, id string) (sourceSummary, bool) {
	for _, source := range listSources(cfg) {
		if source.ID == id {
			return source, true
		}
	}
	return sourceSummary{}, false
}

func groupNameForPath(cfg Config, path string) string {
	abs, _ := filepath.Abs(path)
	for name, patterns := range cfg.Groups {
		for _, pattern := range patterns {
			expanded := expandHome(pattern)
			if hasMeta(expanded) && (globMatch(expanded, abs) || globMatch(expanded, path)) {
				return name
			}
			if !hasMeta(expanded) {
				clean := filepath.Clean(expanded)
				if abs == clean || strings.HasPrefix(abs, clean+string(filepath.Separator)) {
					return name
				}
			}
		}
	}
	return ""
}

func sourceMetadata(cfg Config, path string) (string, string) {
	if group := groupNameForPath(cfg, path); group != "" {
		category := cfg.GroupCategories[group]
		if category == "" {
			category = "app"
		}
		module := cfg.GroupModules[group]
		if module == "" {
			module = group
		}
		return category, module
	}
	lower := strings.ToLower(filepath.ToSlash(path))
	category := "app"
	switch {
	case strings.Contains(lower, "/containers/") || strings.Contains(lower, "/pods/") || strings.Contains(lower, "/docker/"):
		category = "container"
	case strings.Contains(lower, "mysql") || strings.Contains(lower, "mariadb") || strings.Contains(lower, "postgres") || strings.Contains(lower, "redis"):
		category = "database"
	case strings.Contains(lower, "nginx") || strings.Contains(lower, "apache") || strings.Contains(lower, "httpd") || strings.Contains(lower, "haproxy") || strings.Contains(lower, "traefik") || strings.Contains(lower, "caddy"):
		category = "web"
	case strings.Contains(lower, "firewall") || strings.Contains(lower, "ufw") || strings.Contains(lower, "ipsec") || strings.Contains(lower, "wireguard") || strings.Contains(lower, "/network/"):
		category = "network"
	case strings.Contains(lower, "/journal/") || strings.Contains(lower, "/audit/") || strings.Contains(lower, "syslog") || strings.Contains(lower, "kern.log") || strings.Contains(lower, "/messages"):
		category = "system"
	}
	return category, filepath.Base(filepath.Dir(path))
}

func autoSourceID(cfg Config, category, directory string) string {
	best := ""
	for _, root := range cfg.DefaultLogDirs {
		relative, err := filepath.Rel(expandHome(root), directory)
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		candidate := filepath.ToSlash(relative)
		if best == "" || len(candidate) < len(best) {
			best = candidate
		}
	}
	if best == "" {
		best = strings.TrimPrefix(filepath.ToSlash(directory), "/")
	}
	return category + "/" + best
}

func filterSourcePaths(cfg Config, paths, categories, modules []string) []string {
	if len(categories) == 0 && len(modules) == 0 {
		return paths
	}
	wantedCategories := stringSet(categories)
	wantedModules := stringSet(modules)
	filtered := make([]string, 0, len(paths))
	for _, path := range paths {
		category, module := sourceMetadata(cfg, path)
		if len(wantedCategories) > 0 && !wantedCategories[strings.ToLower(category)] {
			continue
		}
		if len(wantedModules) > 0 && !wantedModules[strings.ToLower(module)] {
			continue
		}
		filtered = append(filtered, path)
	}
	return filtered
}

func stringSet(values []string) map[string]bool {
	set := map[string]bool{}
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			if normalized := strings.ToLower(strings.TrimSpace(part)); normalized != "" {
				set[normalized] = true
			}
		}
	}
	return set
}

package main

import (
	"bufio"
	"fmt"
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
	Name        string
	Patterns    []string
	Paths       []string
	JournalUnit string
}

func looksLikePath(s string) bool {
	return strings.ContainsAny(s, "/\\*?[~") || strings.HasPrefix(s, ".")
}

func resolveTarget(cfg Config, raw string, includeHistory bool) (ResolvedTarget, error) {
	if raw == "" {
		if includeHistory {
			cs := collectLogCandidates(cfg.DefaultLogDirs, cfg.Excludes, time.Time{}, true)
			limit := cfg.MaxFiles * 5
			if limit < cfg.MaxFiles {
				limit = cfg.MaxFiles
			}
			if len(cs) > limit {
				cs = cs[:limit]
			}
			paths := make([]string, 0, len(cs))
			for _, c := range cs {
				paths = append(paths, c.Path)
			}
			return ResolvedTarget{Name: "default", Paths: paths, Patterns: paths}, nil
		}
		paths, err := discoverDefault(cfg)
		return ResolvedTarget{Name: "default", Paths: paths, Patterns: paths}, err
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
	add := func(p string) {
		abs, _ := filepath.Abs(p)
		if seen[abs] || excluded(abs, excludes) {
			return
		}
		fi, err := os.Stat(abs)
		if err != nil || !fi.Mode().IsRegular() || !likelyLog(abs) {
			return
		}
		seen[abs] = true
		out = append(out, abs)
	}
	for _, p := range paths {
		add(p)
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
				add(filepath.Join(dir, name))
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
		cmd := exec.Command(path, "-nP", "-t", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN")
		b, _ := cmd.Output()
		if fields := strings.Fields(string(b)); len(fields) > 0 {
			if pid, e := strconv.Atoi(fields[0]); e == nil {
				return pid
			}
		}
	}
	if runtime.GOOS == "linux" {
		if path, err := exec.LookPath("ss"); err == nil {
			cmd := exec.Command(path, "-ltnp", "sport = :"+strconv.Itoa(port))
			b, _ := cmd.Output()
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
	cmd := exec.Command(path, "show", "-p", "LoadState", "--value", unit)
	b, err := cmd.Output()
	if err != nil {
		return false
	}
	state := strings.TrimSpace(string(b))
	return state != "" && state != "not-found"
}

func listSources(cfg Config) []sourceSummary {
	byDir := map[string]*sourceSummary{}
	for name, pats := range cfg.Groups {
		paths, _ := resolvePatternsWithHistory(pats, cfg.Excludes, false)
		byDir["group:"+name] = &sourceSummary{Name: name, Paths: paths, Kind: "group"}
	}
	cs := collectLogCandidates(cfg.DefaultLogDirs, cfg.Excludes, timeZero, false)
	for _, c := range cs {
		dir := filepath.Dir(c.Path)
		name := filepath.Base(dir)
		key := "dir:" + dir
		if _, ok := byDir[key]; !ok {
			byDir[key] = &sourceSummary{Name: name, Kind: "auto", Root: dir}
		}
		byDir[key].Paths = append(byDir[key].Paths, c.Path)
	}
	out := make([]sourceSummary, 0, len(byDir))
	for _, v := range byDir {
		if v.Kind == "auto" {
			if _, ok := cfg.Groups[v.Name]; ok {
				continue
			}
		}
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind == out[j].Kind {
			return out[i].Name < out[j].Name
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

var timeZero = func() (z time.Time) { return }()

type sourceSummary struct {
	Name, Kind, Root string
	Paths            []string
}

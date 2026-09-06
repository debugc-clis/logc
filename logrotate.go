package main

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
)

type logrotateRegistry struct {
	patterns []string
	warnings []string
}

var (
	defaultLogrotateOnce          sync.Once
	defaultLogrotateRegistryValue *logrotateRegistry
)

func defaultLogrotateRegistry() *logrotateRegistry {
	defaultLogrotateOnce.Do(func() {
		if runtime.GOOS != "linux" {
			defaultLogrotateRegistryValue = &logrotateRegistry{}
			return
		}
		defaultLogrotateRegistryValue = loadLogrotateRegistry("/etc/logrotate.conf", "/etc/logrotate.d")
	})
	return defaultLogrotateRegistryValue
}

func loadLogrotateRegistry(roots ...string) *logrotateRegistry {
	registry := &logrotateRegistry{}
	seenPatterns := map[string]bool{}
	visited := map[string]bool{}
	var visit func(string, string)
	visit = func(rawPath, relativeTo string) {
		path := expandHome(rawPath)
		if !filepath.IsAbs(path) && relativeTo != "" {
			path = filepath.Join(relativeTo, path)
		}
		if hasMeta(path) {
			matches, err := filepath.Glob(path)
			if err != nil {
				registry.warnings = append(registry.warnings, "cannot expand logrotate include "+path+": "+err.Error())
				return
			}
			for _, match := range matches {
				visit(match, "")
			}
			return
		}
		absolute, err := filepath.Abs(path)
		if err != nil || visited[absolute] {
			return
		}
		visited[absolute] = true
		info, err := os.Stat(absolute)
		if err != nil {
			if !os.IsNotExist(err) {
				registry.warnings = append(registry.warnings, "cannot inspect logrotate config "+absolute+": "+err.Error())
			}
			return
		}
		if info.IsDir() {
			entries, err := os.ReadDir(absolute)
			if err != nil {
				registry.warnings = append(registry.warnings, "cannot inspect logrotate directory "+absolute+": "+err.Error())
				return
			}
			for _, entry := range entries {
				if entry.IsDir() || ignoredLogrotateConfigName(entry.Name()) {
					continue
				}
				visit(filepath.Join(absolute, entry.Name()), "")
			}
			return
		}
		body, err := os.ReadFile(absolute)
		if err != nil {
			registry.warnings = append(registry.warnings, "cannot read logrotate config "+absolute+": "+err.Error())
			return
		}
		parseLogrotateConfig(string(body), filepath.Dir(absolute), func(pattern string) {
			pattern = expandHome(pattern)
			if !filepath.IsAbs(pattern) {
				pattern = filepath.Join(filepath.Dir(absolute), pattern)
			}
			pattern = filepath.Clean(pattern)
			if !seenPatterns[pattern] {
				seenPatterns[pattern] = true
				registry.patterns = append(registry.patterns, pattern)
			}
		}, visit)
	}
	for _, root := range roots {
		visit(root, "")
	}
	sort.Strings(registry.patterns)
	registry.warnings = uniqueSorted(registry.warnings)
	return registry
}

func parseLogrotateConfig(body, baseDir string, addPattern func(string), include func(string, string)) {
	inStanza := false
	var pendingPaths []string
	for _, rawLine := range strings.Split(body, "\n") {
		fields := splitLogrotateFields(rawLine)
		if len(fields) == 0 {
			continue
		}
		if inStanza {
			for _, field := range fields {
				if field == "}" {
					inStanza = false
					break
				}
			}
			continue
		}
		if fields[0] == "include" {
			pendingPaths = nil
			for _, path := range fields[1:] {
				include(path, baseDir)
			}
			continue
		}
		brace := -1
		for index, field := range fields {
			if field == "{" {
				brace = index
				break
			}
		}
		if brace < 0 {
			if logrotatePathFields(fields) {
				pendingPaths = append(pendingPaths, fields...)
			} else {
				pendingPaths = nil
			}
			continue
		}
		paths := append(append([]string(nil), pendingPaths...), fields[:brace]...)
		pendingPaths = nil
		for _, path := range paths {
			if looksLikeLogrotatePath(path) {
				addPattern(path)
			}
		}
		inStanza = true
		for _, field := range fields[brace+1:] {
			if field == "}" {
				inStanza = false
				break
			}
		}
	}
}

func splitLogrotateFields(line string) []string {
	var fields []string
	var current strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		if current.Len() > 0 {
			fields = append(fields, current.String())
			current.Reset()
		}
	}
	for _, char := range line {
		if escaped {
			current.WriteRune(char)
			escaped = false
			continue
		}
		if char == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if char == quote {
				quote = 0
			} else {
				current.WriteRune(char)
			}
			continue
		}
		switch char {
		case '\'', '"':
			quote = char
		case '#':
			flush()
			return fields
		case '{', '}':
			flush()
			fields = append(fields, string(char))
		case ' ', '\t', '\r':
			flush()
		default:
			current.WriteRune(char)
		}
	}
	if escaped {
		current.WriteRune('\\')
	}
	flush()
	return fields
}

func logrotatePathFields(fields []string) bool {
	if len(fields) == 0 {
		return false
	}
	for _, field := range fields {
		if !looksLikeLogrotatePath(field) {
			return false
		}
	}
	return true
}

func looksLikeLogrotatePath(value string) bool {
	return strings.HasPrefix(value, "/") || strings.HasPrefix(value, "~/") || strings.HasPrefix(value, "./") || strings.HasPrefix(value, "../")
}

func ignoredLogrotateConfigName(name string) bool {
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, ".") || strings.HasSuffix(lower, "~") {
		return true
	}
	for _, suffix := range []string{".bak", ".disabled", ".dpkg-dist", ".dpkg-new", ".dpkg-old", ".rpmnew", ".rpmorig", ".rpmsave", ".swp"} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

func (r *logrotateRegistry) managed(path string) bool {
	if r == nil || len(r.patterns) == 0 || path == "" {
		return false
	}
	for _, candidate := range logrotatePathCandidates(path) {
		for _, pattern := range r.patterns {
			if globMatch(pattern, candidate) {
				return true
			}
		}
	}
	return false
}

func logrotatePathCandidates(path string) []string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		absolute = path
	}
	candidates := []string{filepath.Clean(absolute)}
	withoutCompression := strings.TrimSuffix(absolute, ".gz")
	if withoutCompression != absolute {
		candidates = append(candidates, filepath.Clean(withoutCompression))
	}
	lower := strings.ToLower(withoutCompression)
	for _, marker := range []string{".log.", ".log-"} {
		if index := strings.LastIndex(lower, marker); index >= 0 {
			candidates = append(candidates, filepath.Clean(withoutCompression[:index+4]))
		}
	}
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		candidates = append(candidates, filepath.Clean(resolved))
	}
	return uniqueSorted(candidates)
}

func logrotateStatus(paths []string, registry *logrotateRegistry) string {
	managed := 0
	for _, path := range paths {
		if registry.managed(path) {
			managed++
		}
	}
	switch {
	case managed == 0:
		return "-"
	case managed == len(paths):
		return "yes"
	default:
		return "partial"
	}
}

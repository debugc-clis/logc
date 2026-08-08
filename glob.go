package main

import (
	"path/filepath"
	"regexp"
	"strings"
)

func globMatch(pattern, path string) bool {
	pattern = filepath.ToSlash(filepath.Clean(expandHome(pattern)))
	path = filepath.ToSlash(filepath.Clean(path))
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch c {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i++
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	ok, _ := regexp.MatchString(b.String(), path)
	return ok
}

func hasMeta(s string) bool { return strings.ContainsAny(s, "*?[") }

func literalRoot(pattern string) string {
	pattern = expandHome(pattern)
	slash := filepath.ToSlash(pattern)
	idx := strings.IndexAny(slash, "*?[")
	if idx < 0 {
		return pattern
	}
	prefix := slash[:idx]
	cut := strings.LastIndex(prefix, "/")
	if cut <= 0 {
		if strings.HasPrefix(slash, "/") {
			return "/"
		}
		return "."
	}
	return filepath.FromSlash(prefix[:cut])
}

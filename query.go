package main

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Query struct {
	Pattern       string
	Regex         *regexp.Regexp
	IgnoreRegexes []*regexp.Regexp
	Since         time.Time
	Before        int
	After         int
	Dedup         bool
}

func buildQuery(pattern string, ignoreCase bool, since time.Time, before, after int, dedup bool, ignoreLines []string) (Query, error) {
	q := Query{Pattern: pattern, Since: since, Before: before, After: after, Dedup: dedup}
	if pattern != "" {
		p := pattern
		if ignoreCase {
			p = "(?i)" + p
		}
		r, err := regexp.Compile(p)
		if err != nil {
			return q, fmt.Errorf("invalid regex %q: %w", pattern, err)
		}
		q.Regex = r
	}
	for _, raw := range ignoreLines {
		r, err := regexp.Compile(raw)
		if err != nil {
			return q, fmt.Errorf("invalid ignore_line regex %q: %w", raw, err)
		}
		q.IgnoreRegexes = append(q.IgnoreRegexes, r)
	}
	return q, nil
}

func severityPattern(s string) string {
	switch strings.ToLower(s) {
	case "error", "errors":
		return `(?i)\b(error|err|fatal|panic|critical|severe|exception|traceback)\b`
	case "warn", "warning", "warnings":
		return `(?i)\b(warn|warning)\b`
	}
	return s
}

func (q Query) ignored(line string) bool {
	for _, r := range q.IgnoreRegexes {
		if r.MatchString(line) {
			return true
		}
	}
	return false
}

func (q Query) match(line string) bool { return q.Regex == nil || q.Regex.MatchString(line) }

func openLogReader(path string) (io.ReadCloser, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(strings.ToLower(path), ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			f.Close()
			return nil, err
		}
		return &compoundCloser{Reader: gz, closers: []io.Closer{gz, f}}, nil
	}
	return f, nil
}

type compoundCloser struct {
	io.Reader
	closers []io.Closer
}

func (c *compoundCloser) Close() error {
	for _, x := range c.closers {
		_ = x.Close()
	}
	return nil
}

func readAllLogLines(path string, maxBytes int64) ([]string, os.FileInfo, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	r, err := openLogReader(path)
	if err != nil {
		return nil, fi, err
	}
	defer r.Close()
	var reader io.Reader = r
	if maxBytes > 0 {
		if !strings.HasSuffix(strings.ToLower(path), ".gz") && fi.Size() > maxBytes {
			f, ok := r.(*os.File)
			if ok {
				_, _ = f.Seek(fi.Size()-maxBytes, io.SeekStart)
				reader = f
			}
		} else {
			reader = io.LimitReader(reader, maxBytes)
		}
	}
	s := bufio.NewScanner(reader)
	buf := make([]byte, 64*1024)
	s.Buffer(buf, 2*1024*1024)
	var lines []string
	for s.Scan() {
		lines = append(lines, strings.TrimSuffix(s.Text(), "\r"))
	}
	return lines, fi, s.Err()
}

func lineTime(line string) (time.Time, bool) {
	trim := strings.TrimSpace(line)
	if strings.HasPrefix(trim, "{") {
		var obj map[string]any
		if json.Unmarshal([]byte(trim), &obj) == nil {
			for _, k := range []string{"timestamp", "time", "ts", "@timestamp"} {
				if v, ok := obj[k].(string); ok {
					if t, ok := parseTimePrefix(v); ok {
						return t, true
					}
				}
			}
		}
	}
	return parseTimePrefix(trim)
}

func parseTimePrefix(s string) (time.Time, bool) {
	loc := time.Local
	layouts := []struct {
		layout string
		n      int
	}{
		{time.RFC3339Nano, 35}, {time.RFC3339, 25},
		{"2006-01-02 15:04:05.000", 23}, {"2006-01-02 15:04:05", 19},
		{"2006/01/02 15:04:05", 19}, {"01/02/2006 15:04:05", 19},
		{"Jan 02 15:04:05", 15},
	}
	for _, x := range layouts {
		max := x.n
		if len(s) < max {
			max = len(s)
		}
		for n := max; n >= 15; n-- {
			part := strings.TrimSpace(s[:n])
			if t, err := time.ParseInLocation(x.layout, part, loc); err == nil {
				if x.layout == "Jan 02 15:04:05" {
					t = time.Date(time.Now().Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, loc)
				}
				return t, true
			}
		}
	}
	return time.Time{}, false
}

func searchPaths(paths []string, q Query, out *printer) int {
	total := 0
	for _, path := range paths {
		lines, fi, err := readAllLogLines(path, 16*1024*1024)
		if err != nil {
			out.errorf("%s: %v", path, err)
			continue
		}
		selected := selectLines(lines, fi.ModTime(), q)
		if len(selected) == 0 {
			continue
		}
		if q.Dedup {
			selected = dedupLines(selected)
		}
		out.block(path, selected, fmt.Sprintf("%d matches", countDirectMatches(selected, q)))
		total += countDirectMatches(selected, q)
	}
	return total
}

func selectLines(lines []string, fileMod time.Time, q Query) []string {
	eligible := make([]bool, len(lines))
	for i, line := range lines {
		if q.ignored(line) {
			continue
		}
		if !q.Since.IsZero() {
			if t, ok := lineTime(line); ok {
				if t.Before(q.Since) {
					continue
				}
			} else if fileMod.Before(q.Since) {
				continue
			}
		}
		eligible[i] = true
	}
	if q.Regex == nil {
		var out []string
		for i, line := range lines {
			if eligible[i] {
				out = append(out, line)
			}
		}
		return out
	}
	selected := make([]bool, len(lines))
	for i, line := range lines {
		if !eligible[i] || !q.match(line) {
			continue
		}
		start := i - q.Before
		if start < 0 {
			start = 0
		}
		end := i + q.After
		if end >= len(lines) {
			end = len(lines) - 1
		}
		for j := start; j <= end; j++ {
			if eligible[j] {
				selected[j] = true
			}
		}
	}
	var out []string
	last := -2
	for i, ok := range selected {
		if !ok {
			continue
		}
		if last >= 0 && i > last+1 {
			out = append(out, "--")
		}
		out = append(out, lines[i])
		last = i
	}
	return out
}

func countDirectMatches(lines []string, q Query) int {
	if q.Regex == nil {
		return len(lines)
	}
	n := 0
	for _, line := range lines {
		if line != "--" && q.match(line) {
			n++
		}
	}
	return n
}

func dedupLines(lines []string) []string {
	if len(lines) < 2 {
		return lines
	}
	var out []string
	prev := ""
	count := 0
	flush := func() {
		if count == 0 {
			return
		}
		if count == 1 {
			out = append(out, prev)
		} else {
			out = append(out, fmt.Sprintf("%s  × %d", prev, count))
		}
		count = 0
	}
	for _, line := range lines {
		if line == prev {
			count++
			continue
		}
		flush()
		prev = line
		count = 1
	}
	flush()
	return out
}

func sortHistorical(paths []string) {
	sort.Slice(paths, func(i, j int) bool {
		a, _ := os.Stat(paths[i])
		b, _ := os.Stat(paths[j])
		if a == nil || b == nil {
			return paths[i] < paths[j]
		}
		return a.ModTime().Before(b.ModTime())
	})
}

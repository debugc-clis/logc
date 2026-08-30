package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	maxFollowReadBytes = int64(4 * 1024 * 1024)
	maxCarryBytes      = 2 * 1024 * 1024
)

type streamLine struct {
	Seq  int64
	Text string
}

type fileState struct {
	Path            string
	Info            os.FileInfo
	Offset          int64
	Carry           string
	CarryTruncated  bool
	Pending         []string
	Dropped         int
	Seq             int64
	Prev            []streamLine
	LastEmittedSeq  int64
	AfterRemaining  int
	DedupLast       string
	DedupSuppressed int
}

func readAppended(path string, offset, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	return io.ReadAll(io.LimitReader(file, limit))
}

func splitAppended(carry string, carryTruncated bool, data []byte) (lines []string, next string, nextTruncated bool) {
	text := carry + string(data)
	parts := strings.Split(text, "\n")
	if strings.HasSuffix(text, "\n") {
		parts = parts[:len(parts)-1]
	} else {
		next = parts[len(parts)-1]
		parts = parts[:len(parts)-1]
	}
	if carryTruncated && len(parts) > 0 {
		parts[0] = "[truncated long line] " + parts[0]
	}
	if len(next) > maxCarryBytes {
		next = next[len(next)-maxCarryBytes:]
		nextTruncated = true
	}
	return parts, next, nextTruncated
}

type follower struct {
	cfg              Config
	patterns         []string
	excludes         []string
	out              *printer
	query            Query
	skipInitial      bool
	defaultDiscovery bool
	categories       []string
	modules          []string
	mu               sync.Mutex
	states           map[string]*fileState
	failures         map[string]string
}

func newFollower(cfg Config, patterns []string, excludes []string, out *printer, q Query, skipInitial bool) *follower {
	return &follower{cfg: cfg, patterns: patterns, excludes: excludes, out: out, query: q, skipInitial: skipInitial, states: map[string]*fileState{}, failures: map[string]string{}}
}

func (f *follower) reportFailure(path string, err error) {
	message := err.Error()
	if f.failures[path] == message {
		return
	}
	f.failures[path] = message
	f.out.errorf("skipping %s: %v", path, err)
}

func (f *follower) clearFailure(path string) { delete(f.failures, path) }

func (f *follower) reportWarning(message string) {
	key := "warning:" + message
	if f.failures[key] == message {
		return
	}
	f.failures[key] = message
	f.out.infof("warning: %s", message)
}

func readLastLines(path string, n int) ([]string, int64, os.FileInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, 0, nil, err
	}
	size := info.Size()
	if size == 0 {
		return nil, 0, info, nil
	}
	const block = int64(32 * 1024)
	pos := size
	data := make([]byte, 0, block)
	newlineCount := 0
	for pos > 0 && newlineCount <= n {
		take := block
		if pos < take {
			take = pos
		}
		pos -= take
		buf := make([]byte, take)
		if _, err := f.ReadAt(buf, pos); err != nil && err != io.EOF {
			return nil, 0, nil, err
		}
		data = append(buf, data...)
		newlineCount = bytes.Count(data, []byte{'\n'})
		if len(data) > 4*1024*1024 {
			break
		}
	}
	text := strings.TrimSuffix(string(data), "\n")
	if text == "" {
		return nil, size, info, nil
	}
	lines := strings.Split(text, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, size, info, nil
}

func showSnapshot(paths []string, lines int, q Query, out *printer) {
	for _, p := range paths {
		var ls []string
		var info os.FileInfo
		var err error
		if strings.HasSuffix(strings.ToLower(p), ".gz") {
			ls, info, err = readAllLogLines(p, maxFollowReadBytes)
			if len(ls) > max(lines*4, lines) {
				ls = ls[len(ls)-max(lines*4, lines):]
			}
		} else {
			ls, _, info, err = readLastLines(p, max(lines*4, lines))
		}
		if err != nil {
			out.errorf("%s: %v", p, err)
			continue
		}
		filtered := selectLines(ls, info.ModTime(), q)
		if len(filtered) > lines && q.Regex == nil {
			filtered = filtered[len(filtered)-lines:]
		}
		if q.Dedup {
			filtered = dedupLines(filtered)
		}
		if len(filtered) > 0 || q.Regex == nil {
			out.block(p, filtered, "")
		}
	}
}

func (f *follower) initialFiltered(lines []string, info os.FileInfo) []string {
	q := f.query
	q.Since = time.Time{}
	out := selectLines(lines, info.ModTime(), q)
	if q.Dedup {
		out = dedupLines(out)
	}
	return out
}

func (f *follower) addPath(path string, announce bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.states[path]; ok {
		return
	}
	lines, off, info, err := readLastLines(path, f.cfg.Lines)
	if err != nil {
		f.reportFailure(path, err)
		return
	}
	f.clearFailure(path)
	f.states[path] = &fileState{Path: path, Info: info, Offset: off}
	if f.skipInitial {
		return
	}
	lines = f.initialFiltered(lines, info)
	if announce {
		f.out.block(path, lines, "new file")
	} else {
		f.out.block(path, lines, "")
	}
}

func (f *follower) rescan() {
	paths, warnings, err := f.resolvePaths()
	if err != nil {
		f.reportFailure("log source scan", err)
		return
	}
	for _, warning := range warnings {
		f.reportWarning(warning)
	}
	f.clearFailure("log source scan")
	for _, p := range paths {
		f.addPath(p, true)
	}
	active := make(map[string]bool, len(paths))
	for _, p := range paths {
		active[p] = true
	}
	for path := range f.states {
		if !active[path] {
			delete(f.states, path)
			f.clearFailure(path)
		}
	}
}

func (f *follower) resolvePaths() ([]string, []string, error) {
	if f.defaultDiscovery {
		paths, warnings, err := discoverDefaultDetailed(f.cfg)
		return filterSourcePaths(f.cfg, paths, f.categories, f.modules), warnings, err
	}
	paths, err := resolvePatterns(f.patterns, f.excludes)
	return filterSourcePaths(f.cfg, paths, f.categories, f.modules), nil, err
}

func (f *follower) streamFilter(st *fileState, parts []string) []string {
	var out []string
	for _, line := range parts {
		if f.query.ignored(line) {
			continue
		}
		st.Seq++
		cur := streamLine{Seq: st.Seq, Text: line}
		matched := f.query.match(line)
		if f.query.Regex == nil || matched {
			if f.query.Regex != nil {
				for _, prev := range st.Prev {
					if prev.Seq > st.LastEmittedSeq {
						out = append(out, prev.Text)
						st.LastEmittedSeq = prev.Seq
					}
				}
			}
			if cur.Seq > st.LastEmittedSeq {
				out = append(out, line)
				st.LastEmittedSeq = cur.Seq
			}
			if matched {
				st.AfterRemaining = f.query.After
			}
		} else if st.AfterRemaining > 0 {
			if cur.Seq > st.LastEmittedSeq {
				out = append(out, line)
				st.LastEmittedSeq = cur.Seq
			}
			st.AfterRemaining--
		}
		if f.query.Before > 0 {
			st.Prev = append(st.Prev, cur)
			if len(st.Prev) > f.query.Before {
				st.Prev = st.Prev[len(st.Prev)-f.query.Before:]
			}
		}
	}
	return dedupStreamLines(st, out, f.query.Dedup)
}

func dedupStreamLines(state *fileState, lines []string, enabled bool) []string {
	if !enabled {
		return lines
	}
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		if line == state.DedupLast {
			state.DedupSuppressed++
			continue
		}
		if state.DedupSuppressed > 0 {
			filtered = append(filtered, fmt.Sprintf("↳ previous line repeated %d additional times", state.DedupSuppressed))
			state.DedupSuppressed = 0
		}
		filtered = append(filtered, line)
		state.DedupLast = line
	}
	return filtered
}

func takeStreamDedupSummary(state *fileState) (string, bool) {
	if state.DedupSuppressed == 0 {
		return "", false
	}
	summary := fmt.Sprintf("↳ previous line repeated %d additional times", state.DedupSuppressed)
	state.DedupSuppressed = 0
	return summary, true
}

func (f *follower) poll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for path, st := range f.states {
		info, err := os.Stat(path)
		if err != nil {
			f.reportFailure(path, err)
			continue
		}
		f.clearFailure(path)
		if st.Info != nil && !os.SameFile(st.Info, info) {
			lines, off, ni, e := readLastLines(path, f.cfg.Lines)
			if e == nil {
				st.Info, st.Offset, st.Carry, st.CarryTruncated = ni, off, "", false
				st.Prev = nil
				st.Seq, st.LastEmittedSeq, st.AfterRemaining = 0, 0, 0
				st.DedupLast, st.DedupSuppressed = "", 0
				st.Pending = append(st.Pending, "↻ file rotated/replaced")
				st.Pending = append(st.Pending, f.streamFilter(st, lines)...)
			}
			if e != nil {
				f.reportFailure(path, e)
			}
			continue
		}
		if info.Size() < st.Offset {
			st.Offset = 0
			st.Carry, st.CarryTruncated = "", false
			st.Pending = append(st.Pending, "↻ file truncated")
		}
		if info.Size() == st.Offset {
			st.Info = info
			continue
		}
		b, err := readAppended(path, st.Offset, maxFollowReadBytes)
		if err != nil {
			f.reportFailure(path, err)
			continue
		}
		st.Offset += int64(len(b))
		st.Info = info
		parts, carry, carryTruncated := splitAppended(st.Carry, st.CarryTruncated, b)
		st.Carry, st.CarryTruncated = carry, carryTruncated
		st.Pending = append(st.Pending, f.streamFilter(st, parts)...)
		if len(st.Pending) > f.cfg.MaxBufferLines {
			drop := len(st.Pending) - f.cfg.MaxBufferLines
			st.Pending = append([]string(nil), st.Pending[drop:]...)
			st.Dropped += drop
		}
	}
}

func (f *follower) flushFair() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.query.Dedup {
		for _, state := range f.states {
			if summary, ok := takeStreamDedupSummary(state); ok {
				state.Pending = append(state.Pending, summary)
			}
		}
	}
	paths := make([]string, 0, len(f.states))
	for p, st := range f.states {
		if len(st.Pending) > 0 {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	for _, p := range paths {
		st := f.states[p]
		n := f.cfg.MaxBatchLines
		if len(st.Pending) < n {
			n = len(st.Pending)
		}
		batch := append([]string(nil), st.Pending[:n]...)
		st.Pending = st.Pending[n:]
		if st.Dropped > 0 {
			batch = append([]string{fmt.Sprintf("⚠ skipped %d older buffered lines (hot log)", st.Dropped)}, batch...)
			st.Dropped = 0
		}
		suffix := ""
		if len(st.Pending) > 0 {
			suffix = fmt.Sprintf("%d buffered", len(st.Pending))
		}
		f.out.block(p, batch, suffix)
	}
}

func (f *follower) run(ctx context.Context) {
	if paths, _, err := f.resolvePaths(); err == nil {
		for _, p := range paths {
			f.addPath(p, false)
		}
	}
	pollInterval := 250 * time.Millisecond
	if len(f.states) > 500 {
		pollInterval = time.Second
	} else if len(f.states) > 100 {
		pollInterval = 500 * time.Millisecond
	}
	pollTicker := time.NewTicker(pollInterval)
	flushTicker := time.NewTicker(f.cfg.FlushInterval)
	scanTicker := time.NewTicker(f.cfg.ScanInterval)
	defer pollTicker.Stop()
	defer flushTicker.Stop()
	defer scanTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			f.flushFair()
			return
		case <-pollTicker.C:
			f.poll()
		case <-flushTicker.C:
			f.flushFair()
		case <-scanTicker.C:
			f.rescan()
		}
	}
}

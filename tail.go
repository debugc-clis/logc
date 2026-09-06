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
	maxPendingBytes    = 8 * 1024 * 1024
	maxOpenFollowFiles = 256
	missingPathGrace   = 30 * time.Second
)

type streamLine struct {
	Seq  int64
	Text string
}

type fileState struct {
	Path            string
	File            *os.File
	Info            os.FileInfo
	Offset          int64
	MissingSince    time.Time
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

func (s *fileState) close() {
	if s.File != nil {
		_ = s.File.Close()
		s.File = nil
	}
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
	for index := range parts {
		truncated := len(parts[index]) > maxCarryBytes || index == 0 && carryTruncated
		if len(parts[index]) > maxCarryBytes {
			parts[index] = parts[index][len(parts[index])-maxCarryBytes:]
		}
		if truncated {
			parts[index] = "[truncated long line] " + parts[index]
		}
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
	key := warningFailureKey(message)
	if f.failures[key] == message {
		return
	}
	f.failures[key] = message
	f.out.infof("warning: %s", message)
}

func warningFailureKey(message string) string { return "warning:" + message }

func pruneFailureCache(failures map[string]string, active map[string]bool, warnings []string) {
	keep := make(map[string]bool, len(active)+len(warnings))
	for path := range active {
		keep[path] = true
	}
	for _, warning := range warnings {
		keep[warningFailureKey(warning)] = true
	}
	for key := range failures {
		if !keep[key] {
			delete(failures, key)
		}
	}
}

func readLastLines(path string, n int) ([]string, int64, os.FileInfo, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, nil, err
	}
	defer file.Close()
	return readLastLinesFrom(file, n)
}

func readLastLinesFrom(file *os.File, n int) ([]string, int64, os.FileInfo, error) {
	info, err := file.Stat()
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
		if _, err := file.ReadAt(buf, pos); err != nil && err != io.EOF {
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

func openFileState(path string, lines int, keepOpen bool) ([]string, *fileState, error) {
	if !keepOpen {
		initial, offset, info, err := readLastLines(path, lines)
		if err != nil {
			return nil, nil, err
		}
		return initial, &fileState{Path: path, Info: info, Offset: offset}, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	initial, offset, info, err := readLastLinesFrom(file, lines)
	if err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	return initial, &fileState{Path: path, File: file, Info: info, Offset: offset}, nil
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
	openFiles := 0
	for _, state := range f.states {
		if state.File != nil {
			openFiles++
		}
	}
	lines, state, err := openFileState(path, f.cfg.Lines, openFiles < maxOpenFollowFiles)
	if err != nil {
		f.reportFailure(path, err)
		return
	}
	f.clearFailure(path)
	f.states[path] = state
	if f.skipInitial {
		return
	}
	lines = f.initialFiltered(lines, state.Info)
	if announce {
		f.out.block(path, lines, "new file")
	} else {
		f.out.block(path, lines, "")
	}
}

func retainExistingPaths(paths []string, states map[string]*fileState, cfg Config) []string {
	limit := cfg.MaxFiles
	if limit <= 0 || len(paths) >= limit {
		return paths
	}
	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		seen[path] = true
	}
	existing := make([]string, 0, len(states))
	for path := range states {
		existing = append(existing, path)
	}
	sort.Strings(existing)
	now := time.Now()
	for _, path := range existing {
		if len(paths) >= limit || seen[path] {
			continue
		}
		state := states[path]
		info, err := os.Stat(path)
		if err == nil && info.Mode().IsRegular() && !historicalLog(path) && !excluded(path, cfg.Excludes) {
			paths = append(paths, path)
			seen[path] = true
			continue
		}
		if os.IsNotExist(err) && !state.MissingSince.IsZero() && now.Sub(state.MissingSince) < missingPathGrace {
			paths = append(paths, path)
			seen[path] = true
		}
	}
	return paths
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
	if f.defaultDiscovery {
		paths = retainExistingPaths(paths, f.states, f.cfg)
	}
	for _, p := range paths {
		f.addPath(p, true)
	}
	active := make(map[string]bool, len(paths))
	for _, p := range paths {
		active[p] = true
	}
	for path := range f.states {
		if !active[path] {
			f.states[path].close()
			delete(f.states, path)
			f.clearFailure(path)
		}
	}
	pruneFailureCache(f.failures, active, warnings)
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

func resetStreamState(state *fileState) {
	state.Carry, state.CarryTruncated = "", false
	state.Prev = nil
	state.Seq, state.LastEmittedSeq, state.AfterRemaining = 0, 0, 0
	state.DedupLast, state.DedupSuppressed = "", 0
}

func (f *follower) capPending(state *fileState) {
	pendingBytes := 0
	for _, line := range state.Pending {
		pendingBytes += len(line) + 1
	}
	if len(state.Pending) <= f.cfg.MaxBufferLines && pendingBytes <= maxPendingBytes {
		return
	}
	drop := 0
	for drop < len(state.Pending) && (len(state.Pending)-drop > f.cfg.MaxBufferLines || pendingBytes > maxPendingBytes) {
		pendingBytes -= len(state.Pending[drop]) + 1
		drop++
	}
	state.Pending = append([]string(nil), state.Pending[drop:]...)
	state.Dropped += drop
}

func (f *follower) queueAppended(state *fileState, data []byte) {
	parts, carry, carryTruncated := splitAppended(state.Carry, state.CarryTruncated, data)
	state.Carry, state.CarryTruncated = carry, carryTruncated
	state.Pending = append(state.Pending, f.streamFilter(state, parts)...)
	f.capPending(state)
}

func (f *follower) prepareReset(state *fileState, marker string) {
	if state.Carry != "" {
		line := state.Carry
		if state.CarryTruncated {
			line = "[truncated long line] " + line
		}
		state.Carry, state.CarryTruncated = "", false
		state.Pending = append(state.Pending, f.streamFilter(state, []string{line})...)
	}
	if f.query.Dedup {
		if summary, ok := takeStreamDedupSummary(state); ok {
			state.Pending = append(state.Pending, summary)
		}
	}
	resetStreamState(state)
	state.Pending = append(state.Pending, marker)
	f.capPending(state)
}

func (f *follower) readOpenFile(state *fileState) error {
	info, err := state.File.Stat()
	if err != nil {
		return err
	}
	if info.Size() < state.Offset {
		if _, err := state.File.Seek(0, io.SeekStart); err != nil {
			return err
		}
		state.Offset = 0
		f.prepareReset(state, "↻ file truncated")
	}
	state.Info = info
	if info.Size() == state.Offset {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(state.File, maxFollowReadBytes))
	if err != nil {
		return err
	}
	state.Offset += int64(len(data))
	f.queueAppended(state, data)
	return nil
}

func (f *follower) replaceOpenFile(state *fileState) error {
	next, err := os.Open(state.Path)
	if err != nil {
		return err
	}
	info, err := next.Stat()
	if err != nil {
		_ = next.Close()
		return err
	}
	previous := state.File
	state.File, state.Info, state.Offset, state.MissingSince = next, info, 0, time.Time{}
	f.prepareReset(state, "↻ file rotated/replaced")
	if previous != nil {
		_ = previous.Close()
	}
	return f.readOpenFile(state)
}

func (f *follower) pollOpenState(path string, state *fileState) error {
	if err := f.readOpenFile(state); err != nil {
		return err
	}
	pathInfo, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) && state.MissingSince.IsZero() {
			state.MissingSince = time.Now()
		}
		return err
	}
	state.MissingSince = time.Time{}
	if state.Info != nil && !os.SameFile(state.Info, pathInfo) {
		return f.replaceOpenFile(state)
	}
	return nil
}

func (f *follower) pollPathState(path string, state *fileState) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) && state.MissingSince.IsZero() {
			state.MissingSince = time.Now()
		}
		return err
	}
	state.MissingSince = time.Time{}
	if state.Info != nil && !os.SameFile(state.Info, info) {
		state.Info, state.Offset = info, 0
		f.prepareReset(state, "↻ file rotated/replaced")
	}
	if info.Size() < state.Offset {
		state.Offset = 0
		f.prepareReset(state, "↻ file truncated")
	}
	if info.Size() == state.Offset {
		state.Info = info
		return nil
	}
	data, err := readAppended(path, state.Offset, maxFollowReadBytes)
	if err != nil {
		return err
	}
	state.Offset += int64(len(data))
	state.Info = info
	f.queueAppended(state, data)
	return nil
}

func (f *follower) poll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for path, state := range f.states {
		var err error
		if state.File != nil {
			err = f.pollOpenState(path, state)
		} else {
			err = f.pollPathState(path, state)
		}
		if err != nil {
			f.reportFailure(path, err)
			continue
		}
		f.clearFailure(path)
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
	defer f.closeStates()
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

func (f *follower) closeStates() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, state := range f.states {
		state.close()
	}
}

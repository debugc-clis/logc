package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadLastLines(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "x.log")
	if err := os.WriteFile(p, []byte("1\n2\n3\n4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _, _, err := readLastLines(p, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "3" || got[1] != "4" {
		t.Fatalf("got %#v", got)
	}
}

func TestDedupStreamLinesAcrossBatches(t *testing.T) {
	state := &fileState{}
	first := dedupStreamLines(state, []string{"ERROR repeated"}, true)
	second := dedupStreamLines(state, []string{"ERROR repeated", "ERROR repeated", "INFO recovered"}, true)
	if len(first) != 1 || len(second) != 2 || second[0] != "↳ previous line repeated 2 additional times" || second[1] != "INFO recovered" {
		t.Fatalf("first=%#v second=%#v", first, second)
	}
}

func TestDefaultFollowerRediscoversNewFiles(t *testing.T) {
	directory := t.TempDir()
	first := filepath.Join(directory, "first.log")
	if err := os.WriteFile(first, []byte("INFO first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	config := defaultConfig()
	config.DefaultLogDirs = []string{directory}
	config.Excludes = nil
	config.Recent = time.Hour
	follower := newFollower(config, config.DefaultLogDirs, nil, &printer{}, Query{}, false)
	follower.defaultDiscovery = true
	paths, _, err := follower.resolvePaths()
	if err != nil || len(paths) != 1 {
		t.Fatalf("initial paths=%#v err=%v", paths, err)
	}
	second := filepath.Join(directory, "second.log")
	if err := os.WriteFile(second, []byte("INFO second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	paths, _, err = follower.resolvePaths()
	if err != nil || len(paths) != 2 {
		t.Fatalf("rescanned paths=%#v err=%v", paths, err)
	}
}

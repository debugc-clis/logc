package main

import (
	"os"
	"path/filepath"
	"testing"
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

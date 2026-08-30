package main

import (
	"testing"
	"time"
)

func TestShouldFollowDefaultsToTrue(t *testing.T) {
	tests := []struct {
		name string
		opts cliOptions
		want bool
	}{
		{name: "default", want: true},
		{name: "explicit follow", opts: cliOptions{FollowSet: true, Follow: true}, want: true},
		{name: "disabled follow", opts: cliOptions{FollowSet: true, Follow: false}, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldFollow(test.opts); got != test.want {
				t.Fatalf("shouldFollow(%+v) = %t, want %t", test.opts, got, test.want)
			}
		})
	}
}

func TestParseCLIRejectsRemovedNoFollowFlag(t *testing.T) {
	_, err := parseCLI([]string{"--no-follow"}, defaultConfig())
	if err == nil {
		t.Fatal("parseCLI accepted removed --no-follow flag")
	}
}

func TestParseCLIRecognizesJSON(t *testing.T) {
	opts, err := parseCLI([]string{"--json", "ERROR"}, defaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !opts.JSON || len(opts.Positionals) != 1 || opts.Positionals[0] != "ERROR" {
		t.Fatalf("options=%#v", opts)
	}
}

func TestParseCLIRecognizesSourceFiltersAndFullRows(t *testing.T) {
	options, err := parseCLI([]string{"--category", "app,web", "--module", "payment", "--full", "ERROR"}, defaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(options.Categories) != 1 || options.Categories[0] != "app,web" || len(options.Modules) != 1 || options.Modules[0] != "payment" || !options.Full {
		t.Fatalf("options=%#v", options)
	}
}

func TestFormatAge(t *testing.T) {
	now := time.Now()
	for _, test := range []struct {
		at   time.Time
		want string
	}{
		{time.Time{}, "-"},
		{now.Add(-30 * time.Second), "30s"},
		{now.Add(-5 * time.Minute), "5m"},
		{now.Add(-2 * time.Hour), "2h"},
		{now.Add(-3 * 24 * time.Hour), "3d"},
	} {
		if got := formatAgeAt(test.at, now); got != test.want {
			t.Fatalf("formatAge(%s)=%q, want %q", test.at, got, test.want)
		}
	}
}

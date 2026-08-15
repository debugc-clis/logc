package main

import "testing"

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

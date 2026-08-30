package main

import (
	"reflect"
	"testing"
)

func TestDockerLogsArgsFollowsByDefault(t *testing.T) {
	got := dockerLogsArgs([]string{"--tail", "100", "api"})
	want := []string{"logs", "--follow", "--tail", "100", "api"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dockerLogsArgs() = %#v, want %#v", got, want)
	}
}

func TestDockerLogsArgsPreservesExplicitFollow(t *testing.T) {
	for _, args := range [][]string{{"-f", "api"}, {"--follow", "api"}, {"--follow=true", "api"}} {
		got := dockerLogsArgs(args)
		want := append([]string{"logs"}, args...)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("dockerLogsArgs(%#v) = %#v, want %#v", args, got, want)
		}
	}
}

func TestParseDockerViewArgsKeepsDockerOptions(t *testing.T) {
	forwarded, options, err := parseDockerViewArgs([]string{"--since", "30m", "--timestamps", "-m", "ERROR", "-C", "2", "api"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--since", "30m", "--timestamps", "api"}
	if !reflect.DeepEqual(forwarded, want) {
		t.Fatalf("forwarded=%#v want=%#v", forwarded, want)
	}
	if options.match != severityPattern("ERROR") || options.context != 2 {
		t.Fatalf("options=%#v", options)
	}
}

package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDockerCommandHelper(t *testing.T) {
	if os.Getenv("LOGC_DOCKER_COMMAND_HELPER") != "1" {
		return
	}
	for index, arg := range os.Args {
		if arg == "--" {
			os.Exit(dockerCommand(os.Args[index+1:]))
		}
	}
	os.Exit(2)
}

func TestDockerCommandJSONContainerAndForwarding(t *testing.T) {
	for _, test := range []struct {
		args      []string
		want      []string
		container string
	}{
		{[]string{"api", "--tail", "100", "--follow=false", "--json"}, []string{"logs", "api", "--tail", "100", "--follow=false"}, "api"},
		{[]string{"api", "--since", "30m", "--json"}, []string{"logs", "--follow", "api", "--since", "30m"}, "api"},
		{[]string{"--json", "--until=1m", "api", "-tn100", "-f=false"}, []string{"logs", "--until=1m", "api", "-tn100", "-f=false"}, "api"},
		{[]string{"--json", "--", "api", "--timestamps", "--details"}, []string{"logs", "--follow", "api", "--timestamps", "--details"}, "api"},
		{[]string{"--since=30m", "api", "-tfn", "100", "--json"}, []string{"logs", "--since=30m", "api", "-tfn", "100"}, "api"},
		{[]string{"a1b2c3d4e5f6", "--since", "30m", "--json"}, []string{"logs", "--follow", "a1b2c3d4e5f6", "--since", "30m"}, "a1b2c3d4e5f6"},
	} {
		args := test.args
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			directory := t.TempDir()
			capture := filepath.Join(directory, "argv")
			mock := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$LOGC_DOCKER_ARGV\"\nprintf 'ERROR test event\\n'\n"
			if err := os.WriteFile(filepath.Join(directory, "docker"), []byte(mock), 0700); err != nil {
				t.Fatal(err)
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, executable, append([]string{"-test.run=^TestDockerCommandHelper$", "--"}, args...)...)
			cmd.Env = append(os.Environ(), "LOGC_DOCKER_COMMAND_HELPER=1", "LOGC_DOCKER_ARGV="+capture,
				"PATH="+directory, "LOGC_CONFIG="+filepath.Join(directory, "missing.conf"))
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("dockerCommand: %v\n%s", err, output)
			}
			var record struct {
				Path     string   `json:"path"`
				Category string   `json:"category"`
				Module   string   `json:"module"`
				Lines    []string `json:"lines"`
			}
			if err := json.Unmarshal(output, &record); err != nil {
				t.Fatalf("JSON output %q: %v", output, err)
			}
			if record.Path != "docker:"+test.container || record.Category != "container" || record.Module != test.container || !reflect.DeepEqual(record.Lines, []string{"ERROR test event"}) {
				t.Errorf("JSON record = %+v", record)
			}
			captured, err := os.ReadFile(capture)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Split(strings.TrimSuffix(string(captured), "\n"), "\n"); !reflect.DeepEqual(got, test.want) {
				t.Errorf("forwarded argv = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestDockerLogsArgsFollowsByDefault(t *testing.T) {
	got := dockerLogsArgs([]string{"--tail", "100", "api"})
	want := []string{"logs", "--follow", "--tail", "100", "api"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dockerLogsArgs() = %#v, want %#v", got, want)
	}
}

func TestDockerLogsArgsPreservesExplicitFollow(t *testing.T) {
	for _, args := range [][]string{{"-f", "api"}, {"--follow", "api"}, {"--follow=true", "api"}, {"api", "--follow=false"}, {"api", "-f=false"}, {"-tf", "api"}, {"api", "-ftn100"}} {
		got := dockerLogsArgs(args)
		want := append([]string{"logs"}, args...)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("dockerLogsArgs(%#v) = %#v, want %#v", args, got, want)
		}
	}
}

func TestInspectDockerLogsArgs(t *testing.T) {
	for _, options := range [][]string{
		nil,
		{"--details"}, {"--details=false"},
		{"--follow"}, {"--follow=false"}, {"--follow=true"},
		{"--timestamps"}, {"--timestamps=false"},
		{"--since", "30m"}, {"--since=30m"},
		{"--until", "2026-11-08T10:00:00Z"}, {"--until=1m"},
		{"--tail", "100"}, {"--tail=100"},
		{"-n", "100"}, {"-n100"}, {"-n=100"},
		{"-f"}, {"-t"}, {"-ft"}, {"-tf"},
		{"-f=false"}, {"-t=false"}, {"-ft=false"},
		{"-ftn100"}, {"-tfn", "100"}, {"-tn=100"},
		{"--tail", "100", "--follow=false", "--since", "30m", "--until=1m", "--details"},
	} {
		for _, containerFirst := range []bool{false, true} {
			args := append([]string{}, options...)
			if containerFirst {
				args = append([]string{"api"}, args...)
			} else {
				args = append(args, "api")
			}
			t.Run(strings.Join(args, " "), func(t *testing.T) {
				original := append([]string{}, args...)
				container, _ := inspectDockerLogsArgs(args)
				if container != "api" {
					t.Errorf("container = %q, want api", container)
				}
				if !reflect.DeepEqual(args, original) {
					t.Errorf("arguments mutated: %#v, want %#v", args, original)
				}
			})
		}
	}
}

func TestDockerLogsArgsDoesNotReadFollowFromValues(t *testing.T) {
	for _, args := range [][]string{
		{"--since", "-f", "api"}, {"--tail", "--follow=false", "api"},
		{"-n-f", "api"}, {"--", "--follow"}, {"api", "--unknown"},
	} {
		want := append([]string{"logs", "--follow"}, args...)
		if got := dockerLogsArgs(args); !reflect.DeepEqual(got, want) {
			t.Errorf("dockerLogsArgs(%#v) = %#v, want %#v", args, got, want)
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

func TestParseDockerViewArgsRejectsEmptyContainerAfterSeparator(t *testing.T) {
	for _, args := range [][]string{{"--"}, {"-m", "ERROR", "--"}} {
		if _, _, err := parseDockerViewArgs(args); err == nil {
			t.Fatalf("accepted arguments without a container: %v", args)
		}
	}
}

func TestParseDockerViewArgsPreservesContainerAfterSeparator(t *testing.T) {
	forwarded, _, err := parseDockerViewArgs([]string{"--", "api"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(forwarded, []string{"api"}) {
		t.Fatalf("forwarded=%#v", forwarded)
	}
}

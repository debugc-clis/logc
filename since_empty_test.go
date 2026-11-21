package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestParseCLIEmptySince(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want string
		err  string
	}{
		{name: "omitted"},
		{name: "valid", args: []string{"--since", "1h"}, want: "1h"},
		{name: "repeated valid", args: []string{"--since", "1h", "--since", "2h"}, want: "2h"},
		{name: "empty", args: []string{"--since", ""}, err: `invalid --since ""`},
		{name: "empty then valid", args: []string{"--since", "", "--since", "1h"}, err: `invalid --since ""`},
		{name: "valid then empty", args: []string{"--since", "1h", "--since", ""}, err: `invalid --since ""`},
		{name: "missing value", args: []string{"--since"}, err: "--since requires a value"},
	} {
		t.Run(test.name, func(t *testing.T) {
			options, err := parseCLI(test.args, Config{Lines: 10})
			if test.err != "" {
				if err == nil || err.Error() != test.err {
					t.Fatalf("parseCLI(%q) error = %v, want %q", test.args, err, test.err)
				}
				return
			}
			if err != nil || options.SinceRaw != test.want {
				t.Fatalf("parseCLI(%q) SinceRaw = %q, error = %v; want %q", test.args, options.SinceRaw, err, test.want)
			}
		})
	}
}

func TestParseSinceEmptySentinel(t *testing.T) {
	got, err := parseSince("")
	if err != nil || !got.IsZero() {
		t.Fatalf("parseSince(empty) = %v, %v; want zero time and nil", got, err)
	}
}

func TestDockerEmptySinceForwarding(t *testing.T) {
	for _, args := range [][]string{
		{"--since", "", "api"},
		{"api", "--since", ""},
		{"--since", "", "--since", "1h", "api"},
		{"--since", "1h", "--since", "", "api"},
	} {
		forwarded, _, err := parseDockerViewArgs(args)
		if err != nil || !reflect.DeepEqual(forwarded, args) {
			t.Fatalf("parseDockerViewArgs(%q) = %q, %v; want unchanged forwarding", args, forwarded, err)
		}
	}
}

func TestEmptySinceCLI(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "logc")
	buildContext, cancelBuild := context.WithTimeout(context.Background(), time.Minute)
	defer cancelBuild()
	if output, err := exec.CommandContext(buildContext, "go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	logPath := filepath.Join(root, "app.log")
	if err := os.WriteFile(logPath, []byte(time.Now().Format(time.RFC3339)+" ERROR isolated event\n"), 0600); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(root, "config")
	if err := os.WriteFile(config, []byte(fmt.Sprintf("default_log_dir=!\ndefault_log_dir=%s\nsystem_log=!\nsystem_log=%s\ncolor=false\n", root, logPath)), 0600); err != nil {
		t.Fatal(err)
	}
	trapDir := filepath.Join(root, "traps")
	if err := os.Mkdir(trapDir, 0700); err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(root, "external-started")
	for _, name := range []string{"journalctl", "docker", "dmesg", "systemctl", "lsof", "ss", "ps", "log"} {
		if err := os.WriteFile(filepath.Join(trapDir, name), []byte("#!/bin/sh\nprintf '%s\\n' \"$0\" >> \"$LOGC_SINCE_TRAP\"\nexit 97\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	run := func(t *testing.T, args []string, wantCode int, wantError string, wantEvent bool) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, binary, args...)
		command.Dir = root
		command.Env = append(os.Environ(), "LOGC_CONFIG="+config, "PATH="+trapDir, "HOME="+root, "NO_COLOR=1", "LOGC_SINCE_TRAP="+capture)
		var stdout, stderr bytes.Buffer
		command.Stdout, command.Stderr = &stdout, &stderr
		err := command.Run()
		if ctx.Err() != nil {
			t.Fatalf("CLI did not terminate: %v; stdout=%q stderr=%q", ctx.Err(), &stdout, &stderr)
		}
		code := 0
		if err != nil {
			exit, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatal(err)
			}
			code = exit.ExitCode()
		}
		if code != wantCode || (wantError != "" && stderr.String() != wantError+"\n") {
			t.Fatalf("args=%q: code=%d want=%d stdout=%q stderr=%q wantError=%q", args, code, wantCode, &stdout, &stderr, wantError)
		}
		if wantCode == 2 && stdout.Len() != 0 {
			t.Fatalf("invalid CLI emitted stdout: %q", &stdout)
		}
		if wantEvent && !bytes.Contains(stdout.Bytes(), []byte("ERROR isolated event")) {
			t.Fatalf("control did not read isolated log: stdout=%q stderr=%q", &stdout, &stderr)
		}
		if started, err := os.ReadFile(capture); !os.IsNotExist(err) {
			t.Fatalf("external command started: %q (read error %v)", started, err)
		}
	}
	for _, mode := range []struct {
		name   string
		args   []string
		prefix string
	}{
		{"root default", nil, "logc:"},
		{"root finite", []string{logPath, "ERROR"}, "logc:"},
		{"root follow", []string{logPath, "ERROR", "-f"}, "logc:"},
		{"root json", []string{logPath, "ERROR", "--json"}, "logc:"},
		{"root selector", []string{"@logc-since-test-nonexistent"}, "logc:"},
		{"watch", []string{"watch", "ERROR", logPath}, "logc watch:"},
		{"watch discovery", []string{"watch", "ERROR"}, "logc watch:"},
		{"system", []string{"system"}, "logc system:"},
		{"system json", []string{"system", "ERROR", "--json"}, "logc system:"},
	} {
		for _, value := range []struct {
			name string
			args []string
			err  string
		}{
			{"empty", []string{"--since", ""}, `invalid --since ""`},
			{"empty then valid", []string{"--since", "", "--since", "1h"}, `invalid --since ""`},
			{"valid then empty", []string{"--since", "1h", "--since", ""}, `invalid --since ""`},
			{"missing value", []string{"--since"}, "--since requires a value"},
		} {
			t.Run(mode.name+"/"+value.name, func(t *testing.T) {
				run(t, append(append([]string{}, mode.args...), value.args...), 2, mode.prefix+" "+value.err, false)
			})
		}
	}
	for _, control := range []struct {
		name string
		args []string
	}{
		{"omitted", nil},
		{"valid", []string{"--since", "1h"}},
		{"repeated valid", []string{"--since", "2h", "--since", "1h"}},
	} {
		t.Run("control/"+control.name, func(t *testing.T) {
			run(t, append([]string{logPath, "ERROR"}, control.args...), 0, "", true)
		})
	}
}

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func writeSectionConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "logc.conf")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOGC_CONFIG", path)
	return path
}

func TestConfigSectionRejectsUnknownHeaders(t *testing.T) {
	for _, section := range []string{"grop.api", "global", "default", "nginx", "group", "Group.api"} {
		for _, test := range []struct {
			name   string
			prefix string
			suffix string
			line   int
		}{
			{name: "empty first", line: 1},
			{name: "before global key", suffix: "lines=3\n", line: 1},
			{name: "before group key", suffix: "path=/srv/api/*.log\n", line: 1},
			{name: "empty before valid section", suffix: "[group.api]\npath=/srv/api/*.log\n", line: 1},
			{name: "later header", prefix: "# comment\n\nlines=7\n[group.api]\npath=/srv/api/*.log\n", suffix: "lines=3\n", line: 6},
		} {
			t.Run(section+"/"+test.name, func(t *testing.T) {
				path := writeSectionConfig(t, test.prefix+"[  "+section+"  ]\n"+test.suffix)
				config, err := loadConfig()
				want := fmt.Sprintf("%s:%d: unknown section %q", path, test.line, section)
				if err == nil || err.Error() != want {
					t.Fatalf("error=%v, want %q", err, want)
				}
				wantLines := defaultConfig().Lines
				if test.prefix != "" {
					wantLines = 7
				}
				if config.Lines != wantLines {
					t.Fatalf("following key changed lines: got %d want %d", config.Lines, wantLines)
				}
			})
		}
	}
}

func TestConfigSectionEmptyGroupStillErrors(t *testing.T) {
	for _, header := range []string{"[group.]", "[ group.   ]"} {
		t.Run(header, func(t *testing.T) {
			path := writeSectionConfig(t, "# comment\n"+header+"\nlines=3\n")
			_, err := loadConfig()
			want := path + ":2: empty group name"
			if err == nil || err.Error() != want {
				t.Fatalf("error=%v, want %q", err, want)
			}
		})
	}
}

func TestConfigSectionMixedFormsAndBlankReset(t *testing.T) {
	for _, reset := range []string{"[]", "[   ]"} {
		t.Run(reset, func(t *testing.T) {
			writeSectionConfig(t, "lines=7\ngroup.api=/srv/api/first.log\n"+
				"[group.api]\ncategory=APP\nmodule=Payment\npath=/srv/api/first.log\npath=/srv/api/second.log\n"+
				"ignore_line=health\nmax_files=9\ngroup.worker=/srv/worker/app.log\n"+
				"[group.api]\npath=/srv/api/third.log\ncategory=web\nmodule=Ingress\n"+
				reset+"\ndefault_log_dir=!\ndefault_log_dir=/srv/logs\nlines=4\n"+
				"[group.worker]\npath=/srv/worker/second.log\n"+reset+"\ncolor=false\n")
			config, err := loadConfig()
			if err != nil {
				t.Fatal(err)
			}
			want := defaultConfig()
			want.Lines = 4
			want.MaxFiles = 9
			want.Color = false
			want.DefaultLogDirs = []string{"/srv/logs"}
			want.IgnoreLines = []string{"health"}
			want.Groups["api"] = []string{"/srv/api/first.log", "/srv/api/second.log", "/srv/api/third.log"}
			want.Groups["worker"] = []string{"/srv/worker/app.log", "/srv/worker/second.log"}
			want.GroupCategories["api"] = "web"
			want.GroupModules["api"] = "Ingress"
			if !reflect.DeepEqual(config, want) {
				t.Fatalf("config=%#v\nwant=%#v", config, want)
			}
		})
	}
}

func TestConfigSectionPreservesGroupNameGrammar(t *testing.T) {
	for _, name := range []string{"api.v2", "api-prod_1", "api worker", "123", "服务"} {
		t.Run(name, func(t *testing.T) {
			writeSectionConfig(t, "group."+name+"=/srv/first.log\n[ group. "+name+" ]\npath=/srv/second.log\n")
			config, err := loadConfig()
			if err != nil {
				t.Fatal(err)
			}
			if want := []string{"/srv/first.log", "/srv/second.log"}; !reflect.DeepEqual(config.Groups[name], want) {
				t.Fatalf("group=%v want=%v", config.Groups[name], want)
			}
		})
	}
}

func TestConfigSectionPathAfterBlankResetIsUnknownKey(t *testing.T) {
	for _, reset := range []string{"[]", "[   ]"} {
		t.Run(reset, func(t *testing.T) {
			path := writeSectionConfig(t, "[group.api]\npath=/srv/api.log\n"+reset+"\npath=/srv/not-a-group.log\n")
			config, err := loadConfig()
			want := path + ":4: unknown key \"path\""
			if err == nil || err.Error() != want {
				t.Fatalf("error=%v want=%q", err, want)
			}
			if !reflect.DeepEqual(config.Groups["api"], []string{"/srv/api.log"}) {
				t.Fatalf("group changed after reset: %v", config.Groups)
			}
		})
	}
}

func TestConfigSectionMissingFileUsesDefaults(t *testing.T) {
	t.Setenv("LOGC_CONFIG", filepath.Join(t.TempDir(), "missing.conf"))
	config, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if want := defaultConfig(); !reflect.DeepEqual(config, want) {
		t.Fatalf("config=%#v want=%#v", config, want)
	}
}

func TestConfigSectionCLI(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "logc")
	buildContext, cancelBuild := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancelBuild()
	if output, err := exec.CommandContext(buildContext, "go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	logPath := filepath.Join(root, "app.log")
	if err := os.WriteFile(logPath, []byte("ERROR must not be emitted\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(t *testing.T, args ...string) (int, string, string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, binary, args...)
		command.Env = append(os.Environ(), "PATH="+root)
		var output, diagnostics bytes.Buffer
		command.Stdout, command.Stderr = &output, &diagnostics
		err := command.Run()
		if ctx.Err() != nil {
			t.Fatalf("CLI did not terminate: %v; stdout=%q stderr=%q", ctx.Err(), &output, &diagnostics)
		}
		code := 0
		if err != nil {
			exit, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatal(err)
			}
			code = exit.ExitCode()
		}
		return code, output.String(), diagnostics.String()
	}
	for _, test := range []struct {
		name string
		args []string
		code int
	}{
		{name: "config show", args: []string{"config", "show"}, code: 1},
		{name: "root default follow", code: 2},
		{name: "target default follow", args: []string{logPath}, code: 2},
		{name: "finite search", args: []string{logPath, "ERROR"}, code: 2},
		{name: "json search", args: []string{logPath, "ERROR", "--json"}, code: 2},
		{name: "watch", args: []string{"watch", "ERROR", logPath}, code: 1},
		{name: "system", args: []string{"system", "ERROR"}, code: 1},
		{name: "docker", args: []string{"docker", "api"}, code: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := writeSectionConfig(t, "[grop.api]\nlines=3\n")
			code, output, diagnostics := run(t, test.args...)
			want := path + ":1: unknown section \"grop.api\""
			if code != test.code || output != "" || !strings.Contains(diagnostics, want) {
				t.Fatalf("code=%d want=%d stdout=%q stderr=%q; want %q", code, test.code, output, diagnostics, want)
			}
		})
	}
	t.Run("generated config round trip", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "generated.conf")
		t.Setenv("LOGC_CONFIG", path)
		if code, output, diagnostics := run(t, "config", "init"); code != 0 {
			t.Fatalf("init code=%d stdout=%q stderr=%q", code, output, diagnostics)
		}
		config, err := loadConfig()
		if err != nil {
			t.Fatal(err)
		}
		want := defaultConfig()
		for _, exclude := range []string{"/var/log/journal/**", "/var/log/audit/**", "/var/log/syslog*", "/var/log/messages*", "/var/log/kern.log*", "/var/log/auth.log*", "/var/log/secure*", "/var/log/dmesg*"} {
			want.Excludes = appendUnique(want.Excludes, exclude)
		}
		if !reflect.DeepEqual(config, want) {
			t.Fatalf("generated config=%#v want=%#v", config, want)
		}
		if code, output, diagnostics := run(t, "config", "show"); code != 0 || diagnostics != "" || !strings.Contains(output, "config="+path+"\n") || !strings.Contains(output, "lines=10\n") {
			t.Fatalf("show code=%d stdout=%q stderr=%q", code, output, diagnostics)
		}
	})
}

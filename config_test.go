package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigGroupsAndIgnoreLines(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "logc.conf")
	body := "group.api=/srv/api/*.log\n[group.worker]\npath=/srv/worker/*.log\nignore_line=.*health.*\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOGC_CONFIG", p)
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Groups["api"]) != 1 || len(cfg.Groups["worker"]) != 1 {
		t.Fatalf("groups: %#v", cfg.Groups)
	}
	if len(cfg.IgnoreLines) != 1 {
		t.Fatalf("ignore lines: %#v", cfg.IgnoreLines)
	}
}

func TestSystemLogExcludesUseLinuxDistribution(t *testing.T) {
	ubuntu := systemLogExcludes("linux", "ID=ubuntu\nID_LIKE=debian\n")
	if !containsString(ubuntu, "/var/log/syslog*") || !containsString(ubuntu, "/var/log/auth.log*") {
		t.Fatalf("ubuntu exclusions missing Debian logs: %#v", ubuntu)
	}
	if containsString(ubuntu, "/var/log/secure*") {
		t.Fatalf("ubuntu should not load RHEL secure log exclusion: %#v", ubuntu)
	}
	for _, pattern := range []string{"/var/log/apt/**", "/var/log/installer/**", "/var/log/cron*", "/var/log/user.log*"} {
		if containsString(ubuntu, pattern) {
			t.Fatalf("ubuntu should not load non-debug system path %q: %#v", pattern, ubuntu)
		}
	}

	centos := systemLogExcludes("linux", "ID=centos\nID_LIKE=\"rhel fedora\"\n")
	if !containsString(centos, "/var/log/messages*") || !containsString(centos, "/var/log/secure*") {
		t.Fatalf("centos exclusions missing RHEL logs: %#v", centos)
	}
	if containsString(centos, "/var/log/syslog*") {
		t.Fatalf("centos should not load Debian syslog exclusion: %#v", centos)
	}
	for _, pattern := range []string{"/var/log/dnf/**", "/var/log/yum.log*", "/var/log/cron*", "/var/log/maillog*"} {
		if containsString(centos, pattern) {
			t.Fatalf("centos should not load non-debug system path %q: %#v", pattern, centos)
		}
	}
}

func TestConfigCanOverrideBuiltInSystemLogExclusion(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "logc.conf")
	body := "exclude=!/var/log/journal/**\nexclude=/srv/platform/**\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOGC_CONFIG", p)
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if containsString(cfg.Excludes, "/var/log/journal/**") {
		t.Fatalf("built-in exclusion was not removed: %#v", cfg.Excludes)
	}
	if !containsString(cfg.Excludes, "/srv/platform/**") {
		t.Fatalf("custom exclusion was not added: %#v", cfg.Excludes)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DefaultLogDirs []string
	Excludes       []string
	IgnoreLines    []string
	Groups         map[string][]string
	Lines          int
	MaxFiles       int
	FlushInterval  time.Duration
	ScanInterval   time.Duration
	MaxBatchLines  int
	MaxBufferLines int
	Recent         time.Duration
	Color          bool
}

func defaultConfig() Config {
	return defaultConfigForPlatform(runtime.GOOS, readOSRelease())
}

func defaultConfigForPlatform(goos, osRelease string) Config {
	return Config{
		DefaultLogDirs: []string{"/var/log", "/opt/var/log", "/opt/log"},
		Excludes:       systemLogExcludes(goos, osRelease),
		Groups: map[string][]string{
			"mysql": {
				"/var/log/mysql/*.log", "/var/log/mysql/*.err",
				"/var/log/mariadb/*.log", "/var/log/mariadb/*.err",
				"/var/log/mysqld.log", "/var/lib/mysql/*.log", "/var/lib/mysql/*.err",
				"/usr/local/var/mysql/*.log", "/usr/local/var/mysql/*.err",
				"/opt/homebrew/var/mysql/*.log", "/opt/homebrew/var/mysql/*.err",
			},
		},
		Lines:          10,
		MaxFiles:       20,
		FlushInterval:  500 * time.Millisecond,
		ScanInterval:   5 * time.Second,
		MaxBatchLines:  10,
		MaxBufferLines: 2000,
		Recent:         24 * time.Hour,
		Color:          true,
	}
}

func readOSRelease() string {
	if runtime.GOOS != "linux" {
		return ""
	}
	b, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return ""
	}
	return string(b)
}

func systemLogExcludes(goos, osRelease string) []string {
	common := []string{
		"/var/log/journal/**", "/var/log/audit/**",
	}
	if goos != "linux" {
		return common
	}

	distros := osReleaseIDs(osRelease)
	debian := []string{
		"/var/log/daemon.log*", "/var/log/syslog*", "/var/log/kern.log*", "/var/log/auth.log*",
		"/var/log/boot.log*", "/var/log/dmesg*",
	}
	rhel := []string{
		"/var/log/messages*", "/var/log/secure*", "/var/log/boot.log*", "/var/log/dmesg*",
	}
	suse := []string{
		"/var/log/messages*", "/var/log/warn*", "/var/log/localmessages*", "/var/log/boot.msg*",
		"/var/log/firewall*",
	}
	arch := []string{"/var/log/Xorg.*.log*"}
	alpine := []string{"/var/log/messages*", "/var/log/warn*"}
	gentoo := []string{"/var/log/messages*", "/var/log/emerge.log*"}

	var selected []string
	switch {
	case distros["debian"] || distros["ubuntu"] || distros["linuxmint"] || distros["raspbian"]:
		selected = debian
	case distros["rhel"] || distros["centos"] || distros["fedora"] || distros["rocky"] || distros["almalinux"] || distros["amzn"]:
		selected = rhel
	case distros["sles"] || distros["opensuse"] || distros["opensuse-leap"] || distros["opensuse-tumbleweed"]:
		selected = suse
	case distros["arch"] || distros["manjaro"]:
		selected = arch
	case distros["alpine"]:
		selected = alpine
	case distros["gentoo"]:
		selected = gentoo
	default:
		selected = append(append(append(append(append([]string{}, debian...), rhel...), suse...), arch...), alpine...)
	}
	for _, pattern := range selected {
		common = appendUnique(common, pattern)
	}
	return common
}

func osReleaseIDs(osRelease string) map[string]bool {
	ids := map[string]bool{}
	for _, line := range strings.Split(osRelease, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || (key != "ID" && key != "ID_LIKE") {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), "\"")
		for _, id := range strings.Fields(strings.ToLower(value)) {
			ids[id] = true
		}
	}
	return ids
}

func configPath() string {
	if p := os.Getenv("LOGC_CONFIG"); p != "" {
		return expandHome(p)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".logc.conf"
	}
	return filepath.Join(home, ".logc.conf")
}

func loadConfig() (Config, error) {
	cfg := defaultConfig()
	p := configPath()
	f, err := os.Open(p)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	lineNo := 0
	sectionGroup := ""
	for s.Scan() {
		lineNo++
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "["), "]"))
			sectionGroup = ""
			if strings.HasPrefix(section, "group.") {
				sectionGroup = strings.TrimSpace(strings.TrimPrefix(section, "group."))
				if sectionGroup == "" {
					return cfg, fmt.Errorf("%s:%d: empty group name", p, lineNo)
				}
			}
			continue
		}

		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return cfg, fmt.Errorf("%s:%d: expected key=value", p, lineNo)
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if sectionGroup != "" && k == "path" {
			cfg.Groups[sectionGroup] = appendUnique(cfg.Groups[sectionGroup], expandHome(v))
			continue
		}
		if strings.HasPrefix(k, "group.") {
			name := strings.TrimSpace(strings.TrimPrefix(k, "group."))
			if name == "" {
				return cfg, fmt.Errorf("%s:%d: empty group name", p, lineNo)
			}
			cfg.Groups[name] = appendUnique(cfg.Groups[name], expandHome(v))
			continue
		}

		switch k {
		case "default_log_dir":
			if v == "!" {
				cfg.DefaultLogDirs = nil
			} else if strings.HasPrefix(v, "!") {
				cfg.DefaultLogDirs = removeString(cfg.DefaultLogDirs, expandHome(strings.TrimPrefix(v, "!")))
			} else {
				cfg.DefaultLogDirs = appendUnique(cfg.DefaultLogDirs, expandHome(v))
			}
		case "exclude":
			if strings.HasPrefix(v, "!") {
				cfg.Excludes = removeString(cfg.Excludes, expandHome(strings.TrimPrefix(v, "!")))
			} else {
				cfg.Excludes = appendUnique(cfg.Excludes, expandHome(v))
			}
		case "ignore_line":
			cfg.IgnoreLines = appendUnique(cfg.IgnoreLines, v)
		case "lines":
			n, e := strconv.Atoi(v)
			if e != nil || n < 1 {
				return cfg, fmt.Errorf("%s:%d: invalid lines", p, lineNo)
			}
			cfg.Lines = n
		case "max_files":
			n, e := strconv.Atoi(v)
			if e != nil || n < 1 {
				return cfg, fmt.Errorf("%s:%d: invalid max_files", p, lineNo)
			}
			cfg.MaxFiles = n
		case "max_batch_lines":
			n, e := strconv.Atoi(v)
			if e != nil || n < 1 {
				return cfg, fmt.Errorf("%s:%d: invalid max_batch_lines", p, lineNo)
			}
			cfg.MaxBatchLines = n
		case "max_buffer_lines":
			n, e := strconv.Atoi(v)
			if e != nil || n < 10 {
				return cfg, fmt.Errorf("%s:%d: invalid max_buffer_lines", p, lineNo)
			}
			cfg.MaxBufferLines = n
		case "flush_interval":
			d, e := time.ParseDuration(v)
			if e != nil || d <= 0 {
				return cfg, fmt.Errorf("%s:%d: invalid flush_interval", p, lineNo)
			}
			cfg.FlushInterval = d
		case "scan_interval":
			d, e := time.ParseDuration(v)
			if e != nil || d <= 0 {
				return cfg, fmt.Errorf("%s:%d: invalid scan_interval", p, lineNo)
			}
			cfg.ScanInterval = d
		case "recent":
			d, e := time.ParseDuration(v)
			if e != nil || d <= 0 {
				return cfg, fmt.Errorf("%s:%d: invalid recent", p, lineNo)
			}
			cfg.Recent = d
		case "color":
			b, e := strconv.ParseBool(v)
			if e != nil {
				return cfg, fmt.Errorf("%s:%d: invalid color", p, lineNo)
			}
			cfg.Color = b
		default:
			return cfg, fmt.Errorf("%s:%d: unknown key %q", p, lineNo, k)
		}
	}
	return cfg, s.Err()
}

func writeDefaultConfig(force bool) error {
	p := configPath()
	if !force {
		if _, err := os.Stat(p); err == nil {
			return fmt.Errorf("config already exists: %s (use --force to overwrite)", p)
		}
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	body := `# logc user configuration
# Keep this small: logc is designed to discover logs automatically.

default_log_dir=/var/log
default_log_dir=/opt/var/log
default_log_dir=/opt/log

# logc loads built-in system-log exclusions for the detected Linux distribution.
# Add exclude= rules here, or prefix an exact built-in pattern with ! to re-include it.
# Run logc config show to inspect the active built-in patterns.
# OS logs stay out of the normal application-log view.
exclude=/var/log/journal/**
exclude=/var/log/audit/**
exclude=/var/log/syslog*
exclude=/var/log/messages*
exclude=/var/log/kern.log*
exclude=/var/log/auth.log*
exclude=/var/log/secure*
exclude=/var/log/dmesg*

# Optional noise filters applied before display/search.
# ignore_line=.*GET /health.*
# ignore_line=.*GET /metrics.*

# Optional memorable names. Both forms are supported:
# logc includes a built-in mysql source for common MySQL and MariaDB file-log paths.
# Run "logc mysql" to follow it, or add your own path below.
# group.mysql=/srv/mysql/log/*.log
# group.api=/srv/api/log/*.log
# [group.payment]
# path=/srv/payment/**/*.log

lines=10
max_files=20
recent=24h
flush_interval=500ms
scan_interval=5s
max_batch_lines=10
max_buffer_lines=2000
color=true
`
	return os.WriteFile(p, []byte(body), 0o644)
}

func printConfig(cfg Config) {
	fmt.Printf("config=%s\n", configPath())
	for _, d := range cfg.DefaultLogDirs {
		fmt.Printf("default_log_dir=%s\n", d)
	}
	for _, e := range cfg.Excludes {
		fmt.Printf("exclude=%s\n", e)
	}
	for _, e := range cfg.IgnoreLines {
		fmt.Printf("ignore_line=%s\n", e)
	}
	names := make([]string, 0, len(cfg.Groups))
	for name := range cfg.Groups {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, p := range cfg.Groups[name] {
			fmt.Printf("group.%s=%s\n", name, p)
		}
	}
	fmt.Printf("lines=%d\nmax_files=%d\nrecent=%s\nflush_interval=%s\nscan_interval=%s\nmax_batch_lines=%d\nmax_buffer_lines=%d\ncolor=%t\n",
		cfg.Lines, cfg.MaxFiles, cfg.Recent, cfg.FlushInterval, cfg.ScanInterval, cfg.MaxBatchLines, cfg.MaxBufferLines, cfg.Color)
}

func appendUnique(xs []string, v string) []string {
	for _, x := range xs {
		if x == v {
			return xs
		}
	}
	return append(xs, v)
}
func removeString(xs []string, v string) []string {
	out := xs[:0]
	for _, x := range xs {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}
func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			if p == "~" {
				return home
			}
			return filepath.Join(home, strings.TrimPrefix(p, "~/"))
		}
	}
	return p
}

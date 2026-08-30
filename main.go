package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var version = "dev"

func usage() {
	fmt.Fprintf(os.Stderr, `logc — one command for local logs

USAGE
  logc                         Discover and follow the latest application logs
  logc TARGET                  Follow a log source (name, path, dir, glob, @process, @PID, :port)
  logc TARGET REGEX            Search that source (regex; searches recent rotated logs too)
  logc REGEX                   Search all default application logs
  logc TARGET REGEX -f         Search existing logs, then keep following matching lines
  logc watch REGEX [TARGET...] Show a real-time aggregated alert view
  logc system [REGEX]          Search/highlight and follow operating-system logs
  logc docker [OPTIONS] NAME   Follow a Docker container's logs
  logc ls [FILTERS]            Discover classified local log sources
  logc where TARGET            Show what a target resolves to

EXAMPLES
  logc api
  logc api ERROR
  logc api 'timeout|reset'
  logc ERROR
  logc api ERROR --since 30m
  logc api ERROR -C 3
  logc api ERROR -f
  logc watch ERROR
  logc watch api 'timeout|reset'
  logc watch ERROR /var/log/nginx '/opt/log/**/*.log' /etc/myapp/app.log
  logc /srv/api/log
  logc /srv/api/a.log /srv/worker/b.log
  logc '/srv/**/logs/*.log'
  logc @nginx
  logc @12345
  logc :8080
  logc docker --tail 100 api
  logc docker --since 30m --timestamps api
  logc system ERROR --since 30m
  logc ls --category web
  logc --category app,web ERROR

SMALL SET OF OPTIONAL FLAGS
  -f                  Keep following after a search
  --since DURATION    Search/view recent time, e.g. 10m, 2h, 7d, today
  -C N                Show N context lines around a match
  -i                  Case-insensitive regex
  -n N                Initial lines per file (default 10)
  --dedup             Collapse consecutive duplicate lines
  --no-color          Disable colors
  --json              Emit one JSON object per log block
  --current           Search only active logs; skip rotated/.gz files
  --category LIST     Filter sources by category, e.g. app,web,network
  --module LIST       Filter configured source modules
  --full              Do not truncate long rows in logc watch
  -m REGEX            Explicit match regex (normally just use the second positional argument)

CONFIG
  ~/.logc.conf
  Override with LOGC_CONFIG=/path/to/logc.conf

  Named sources can be configured as:
    group.api=/srv/api/log/*.log

  or:
    [group.api]
    path=/srv/api/log/*.log
`)
}

type cliOptions struct {
	FollowSet   bool
	Follow      bool
	SinceRaw    string
	Context     int
	IgnoreCase  bool
	Lines       int
	Dedup       bool
	NoColor     bool
	JSON        bool
	CurrentOnly bool
	Full        bool
	Match       string
	Excludes    []string
	Categories  []string
	Modules     []string
	Positionals []string
}

func parseCLI(args []string, cfg Config) (cliOptions, error) {
	o := cliOptions{Lines: cfg.Lines}
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s requires a value", a)
			}
			i++
			return args[i], nil
		}
		switch a {
		case "-f", "--follow":
			o.FollowSet, o.Follow = true, true
		case "--since":
			v, err := next()
			if err != nil {
				return o, err
			}
			o.SinceRaw = v
		case "-C", "--context":
			v, err := next()
			if err != nil {
				return o, err
			}
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return o, fmt.Errorf("invalid context %q", v)
			}
			o.Context = n
		case "-i", "--ignore-case":
			o.IgnoreCase = true
		case "-n", "--lines":
			v, err := next()
			if err != nil {
				return o, err
			}
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return o, fmt.Errorf("invalid lines %q", v)
			}
			o.Lines = n
		case "--dedup":
			o.Dedup = true
		case "--no-color":
			o.NoColor = true
		case "--json":
			o.JSON = true
		case "--current":
			o.CurrentOnly = true
		case "--full":
			o.Full = true
		case "-m", "--match":
			v, err := next()
			if err != nil {
				return o, err
			}
			o.Match = v
		case "--exclude":
			v, err := next()
			if err != nil {
				return o, err
			}
			o.Excludes = append(o.Excludes, v)
		case "--category":
			v, err := next()
			if err != nil {
				return o, err
			}
			o.Categories = append(o.Categories, v)
		case "--module":
			v, err := next()
			if err != nil {
				return o, err
			}
			o.Modules = append(o.Modules, v)
		case "--":
			o.Positionals = append(o.Positionals, args[i+1:]...)
			return o, nil
		case "-h", "--help":
			usage()
			os.Exit(0)
		default:
			if strings.HasPrefix(a, "-") {
				return o, fmt.Errorf("unknown option %s", a)
			}
			o.Positionals = append(o.Positionals, a)
		}
	}
	return o, nil
}

func parseSince(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	if strings.EqualFold(raw, "today") {
		now := time.Now()
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()), nil
	}
	// Friendlier day/week suffixes while preserving Go duration syntax.
	if strings.HasSuffix(raw, "d") {
		n, err := strconv.ParseFloat(strings.TrimSuffix(raw, "d"), 64)
		if err == nil {
			return time.Now().Add(-time.Duration(n * 24 * float64(time.Hour))), nil
		}
	}
	if strings.HasSuffix(raw, "w") {
		n, err := strconv.ParseFloat(strings.TrimSuffix(raw, "w"), 64)
		if err == nil {
			return time.Now().Add(-time.Duration(n * 7 * 24 * float64(time.Hour))), nil
		}
	}
	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		return time.Now().Add(-d), nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid --since %q", raw)
}

func main() { os.Exit(realMain()) }

func realMain() int {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version", "-version":
			fmt.Printf("logc %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
			return 0
		case "help", "--help", "-h":
			usage()
			return 0
		case "config":
			return configCommand(os.Args[2:])
		case "system":
			return systemCommand(os.Args[2:])
		case "watch":
			return watchCommand(os.Args[2:])
		case "docker":
			return dockerCommand(os.Args[2:])
		case "ls", "list":
			return listCommand(os.Args[2:])
		case "where":
			return whereCommand(os.Args[2:])
		}
	}

	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "logc:", err)
		return 2
	}
	opts, err := parseCLI(os.Args[1:], cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "logc:", err)
		return 2
	}
	cfg.Lines = opts.Lines
	cfg.Color = cfg.Color && !opts.NoColor && !opts.JSON
	excludes := append(append([]string(nil), cfg.Excludes...), opts.Excludes...)
	cfg.Excludes = excludes
	out := newPrinter(cfg.Color)
	out.sourceMeta = func(path string) (string, string) { return sourceMetadata(cfg, path) }
	out.json = opts.JSON
	since, err := parseSince(opts.SinceRaw)
	if err != nil {
		out.errorf("%v", err)
		return 2
	}

	// First resolve only active logs so ordinary `logc api` never pulls rotated/.gz files.
	resolved, queryPattern, err := interpretPositionals(cfg, opts.Positionals, opts.Match, false, since)
	if err != nil {
		out.errorf("%v", err)
		return 1
	}
	if queryPattern != "" {
		queryPattern = severityPattern(queryPattern)
	}
	if queryPattern != "" && since.IsZero() {
		since = time.Now().Add(-cfg.Recent)
	}
	// Searches/time-range queries automatically include rotated and .gz logs unless --current is used.
	if !opts.CurrentOnly && (queryPattern != "" || !since.IsZero()) {
		if rr, qq, e := interpretPositionals(cfg, opts.Positionals, opts.Match, true, since); e == nil {
			resolved = rr
			if qq != "" {
				queryPattern = severityPattern(qq)
			}
		}
	}
	for _, warning := range uniqueSorted(resolved.Warnings) {
		out.infof("warning: %s", warning)
	}
	q, err := buildQuery(queryPattern, opts.IgnoreCase, since, opts.Context, opts.Context, opts.Dedup, cfg.IgnoreLines)
	if err != nil {
		out.errorf("%v", err)
		return 2
	}

	if resolved.JournalUnit != "" {
		searchMode := q.Regex != nil || !q.Since.IsZero()
		follow := !searchMode || opts.Follow
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		var runErr error
		if searchMode {
			runErr = runSystemUnitFiltered(ctx, resolved.JournalUnit, cfg.Lines, follow, q, out)
		} else {
			runErr = runSystemUnit(ctx, resolved.JournalUnit, cfg.Lines, follow)
		}
		if runErr != nil && ctx.Err() == nil {
			out.errorf("%v", runErr)
			return 1
		}
		return 0
	}

	paths := uniqueSorted(filterSourcePaths(cfg, resolved.Paths, opts.Categories, opts.Modules))
	if len(paths) > 1000 {
		out.infof("warning: following %d files may increase filesystem polling load", len(paths))
	}
	if len(paths) == 0 {
		out.infof("no application logs found under the configured roots; run 'logc config show' or pass a file/directory")
		return 0
	}

	searchMode := q.Regex != nil || !q.Since.IsZero()
	if searchMode {
		if !opts.CurrentOnly {
			sortHistorical(paths)
		}
		matches := searchPaths(paths, q, out)
		if q.Regex != nil {
			out.infof("%d matching lines", matches)
		}
		if !opts.Follow {
			return 0
		}
		activePaths := nonHistorical(paths)
		if len(activePaths) == 0 {
			out.infof("no active log files to follow")
			return 0
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		fq := q
		fq.Since = time.Time{}
		f := newFollower(cfg, activePaths, excludes, out, fq, true)
		f.defaultDiscovery = resolved.DefaultDiscovery
		f.categories, f.modules = opts.Categories, opts.Modules
		f.run(ctx)
		return 0
	}

	follow := shouldFollow(opts)
	if !follow {
		showSnapshot(paths, cfg.Lines, q, out)
		return 0
	}

	patterns := resolved.Patterns
	if len(patterns) == 0 {
		patterns = paths
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	f := newFollower(cfg, patterns, excludes, out, q, false)
	f.defaultDiscovery = resolved.DefaultDiscovery
	f.categories, f.modules = opts.Categories, opts.Modules
	f.run(ctx)
	return 0
}

func shouldFollow(opts cliOptions) bool {
	return !opts.FollowSet || opts.Follow
}

func interpretPositionals(cfg Config, pos []string, explicitMatch string, includeHistory bool, since time.Time) (ResolvedTarget, string, error) {
	if explicitMatch != "" {
		if len(pos) == 0 {
			r, err := resolveTarget(cfg, "", includeHistory)
			return r, explicitMatch, err
		}
		r, _, err := resolveLeadingTargets(cfg, pos, includeHistory)
		if err != nil {
			return ResolvedTarget{}, "", err
		}
		return r, explicitMatch, nil
	}
	if len(pos) == 0 {
		r, err := resolveTarget(cfg, "", includeHistory && !since.IsZero())
		return r, "", err
	}
	if len(pos) == 1 && looksLikeSearchExpression(pos[0]) {
		r, err := resolveTarget(cfg, "", includeHistory)
		return r, pos[0], err
	}
	// A search-looking token after one or more sources wins over fuzzy source discovery.
	// This makes `logc api ERROR` unambiguously mean search, even if error.log exists.
	for i := 1; i < len(pos); i++ {
		if looksLikeSearchExpression(pos[i]) {
			r, consumed, err := resolveLeadingTargets(cfg, pos[:i], includeHistory)
			if err != nil || consumed != i {
				break
			}
			return r, strings.Join(pos[i:], " "), nil
		}
	}

	combined, consumed, err := resolveLeadingTargets(cfg, pos, includeHistory)
	if consumed == len(pos) && err == nil {
		return combined, "", nil
	}
	if consumed > 0 {
		return combined, strings.Join(pos[consumed:], " "), nil
	}
	if err != nil && (looksLikePath(pos[0]) || strings.HasPrefix(pos[0], "@") || strings.HasPrefix(pos[0], ":")) {
		return ResolvedTarget{}, "", err
	}

	// Nothing looked like a source: make the whole expression a search over default logs.
	r, e := resolveTarget(cfg, "", true)
	if e != nil {
		return ResolvedTarget{}, "", e
	}
	return r, strings.Join(pos, " "), nil
}

func looksLikeSearchExpression(s string) bool {
	l := strings.ToLower(s)
	switch l {
	case "error", "errors", "warn", "warning", "warnings":
		return true
	}
	if strings.ContainsAny(s, "|()^$+{}\\") {
		return true
	}
	return strings.ContainsAny(s, "[]") && !strings.ContainsAny(s, "/\\")
}

func resolveLeadingTargets(cfg Config, pos []string, includeHistory bool) (ResolvedTarget, int, error) {
	combined := ResolvedTarget{Name: strings.Join(pos, ",")}
	for i, token := range pos {
		r, err := resolveTarget(cfg, token, includeHistory)
		if err != nil {
			if i == 0 {
				return ResolvedTarget{}, 0, err
			}
			return combined, i, nil
		}
		if r.JournalUnit != "" {
			if i > 0 || len(pos) > 1 {
				return combined, i, nil
			}
			return r, 1, nil
		}
		combined.Paths = append(combined.Paths, r.Paths...)
		combined.Patterns = append(combined.Patterns, r.Patterns...)
		combined.Warnings = append(combined.Warnings, r.Warnings...)
		combined.DefaultDiscovery = combined.DefaultDiscovery || r.DefaultDiscovery
	}
	combined.Paths = uniqueSorted(combined.Paths)
	combined.Patterns = uniqueSorted(combined.Patterns)
	combined.Warnings = uniqueSorted(combined.Warnings)
	return combined, len(pos), nil
}

func uniqueSorted(xs []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		if x != "" && !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}
func nonHistorical(xs []string) []string {
	var out []string
	for _, x := range xs {
		if !historicalLog(x) {
			out = append(out, x)
		}
	}
	return out
}

func configCommand(args []string) int {
	if len(args) == 0 {
		fmt.Println(configPath())
		return 0
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprintln(os.Stderr, "logc: usage: logc config {init [--force]|path|show}")
		return 0
	case "path":
		fmt.Println(configPath())
		return 0
	case "show":
		cfg, err := loadConfig()
		if err != nil {
			fmt.Fprintln(os.Stderr, "logc:", err)
			return 1
		}
		printConfig(cfg)
		return 0
	case "init":
		if len(args) > 2 || (len(args) == 2 && args[1] != "--force") {
			fmt.Fprintln(os.Stderr, "logc: usage: logc config init [--force]")
			return 2
		}
		force := len(args) == 2
		if err := writeDefaultConfig(force); err != nil {
			fmt.Fprintln(os.Stderr, "logc:", err)
			return 1
		}
		fmt.Printf("created %s\n", configPath())
		return 0
	default:
		fmt.Fprintln(os.Stderr, "logc: usage: logc config {init|path|show}")
		return 2
	}
}

func systemCommand(args []string) int {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "logc:", err)
		return 1
	}
	kernel := false
	filteredArgs := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "--kernel" || arg == "kernel" {
			kernel = true
			continue
		}
		if arg == "-h" || arg == "--help" || arg == "help" {
			fmt.Fprintln(os.Stderr, "logc: usage: logc system [REGEX] [--kernel] [--since DURATION] [-n LINES] [-i] [-C N] [--dedup] [--json]")
			return 0
		}
		filteredArgs = append(filteredArgs, arg)
	}
	opts, err := parseCLI(filteredArgs, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "logc system:", err)
		return 2
	}
	pattern := opts.Match
	if pattern == "" && len(opts.Positionals) > 0 {
		pattern = strings.Join(opts.Positionals, " ")
	}
	pattern = severityPattern(pattern)
	since, err := parseSince(opts.SinceRaw)
	if err != nil {
		fmt.Fprintln(os.Stderr, "logc system:", err)
		return 2
	}
	query, err := buildQuery(pattern, opts.IgnoreCase, since, opts.Context, opts.Context, opts.Dedup, cfg.IgnoreLines)
	if err != nil {
		fmt.Fprintln(os.Stderr, "logc system:", err)
		return 2
	}
	lines := max(opts.Lines, 50)
	out := newPrinter(cfg.Color && !opts.NoColor && !opts.JSON)
	out.sourceMeta = func(string) (string, string) { return "system", "host" }
	out.json = opts.JSON
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err = runSystemLogsFiltered(ctx, lines, kernel, query, out)
	if err != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, "logc system:", err)
		return 1
	}
	return 0
}

func listCommand(args []string) int {
	var categories, modules []string
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--category", "--module":
			if index+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "logc: usage: logc ls [--category LIST] [--module LIST]")
				return 2
			}
			value := args[index+1]
			index++
			if args[index-1] == "--category" {
				categories = append(categories, value)
			} else {
				modules = append(modules, value)
			}
		default:
			fmt.Fprintln(os.Stderr, "logc: usage: logc ls [--category LIST] [--module LIST]")
			return 2
		}
	}
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "logc:", err)
		return 1
	}
	sources, warnings := listSourcesDetailed(cfg)
	for _, warning := range warnings {
		fmt.Fprintln(os.Stderr, "logc: warning:", sanitizeTerminalText(warning))
	}
	wantedCategories, wantedModules := stringSet(categories), stringSet(modules)
	filtered := sources[:0]
	for _, source := range sources {
		if len(wantedCategories) > 0 && !wantedCategories[strings.ToLower(source.Category)] {
			continue
		}
		if len(wantedModules) > 0 && !wantedModules[strings.ToLower(source.Module)] {
			continue
		}
		filtered = append(filtered, source)
	}
	sources = filtered
	if len(sources) == 0 {
		fmt.Println("no application log sources discovered")
		return 0
	}
	fmt.Printf("%-28s %-10s %-14s %-7s %-7s %-10s %s\n", "ID", "CATEGORY", "MODULE", "TYPE", "FILES", "LATEST", "LOCATION")
	for _, s := range sources {
		root := s.Root
		if root == "" && len(s.Paths) > 0 {
			root = s.Paths[0]
		}
		fmt.Printf("%-28s %-10s %-14s %-7s %-7d %-10s %s\n", sanitizeTerminalText(s.ID), sanitizeTerminalText(s.Category), sanitizeTerminalText(s.Module), s.Kind, len(s.Paths), formatAge(s.Latest), sanitizeTerminalText(root))
	}
	return 0
}

func formatAge(timestamp time.Time) string {
	return formatAgeAt(timestamp, time.Now())
}

func formatAgeAt(timestamp, now time.Time) string {
	if timestamp.IsZero() {
		return "-"
	}
	age := now.Sub(timestamp)
	if age < 0 {
		age = 0
	}
	switch {
	case age < time.Minute:
		return fmt.Sprintf("%ds", int(age.Seconds()))
	case age < time.Hour:
		return fmt.Sprintf("%dm", int(age.Minutes()))
	case age < 24*time.Hour:
		return fmt.Sprintf("%dh", int(age.Hours()))
	default:
		return fmt.Sprintf("%dd", int(age.Hours()/24))
	}
}

func whereCommand(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "logc: usage: logc where TARGET")
		return 2
	}
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "logc:", err)
		return 1
	}
	r, err := resolveTarget(cfg, args[0], true)
	if err != nil {
		fmt.Fprintln(os.Stderr, "logc:", err)
		return 1
	}
	if r.JournalUnit != "" {
		fmt.Printf("%s -> systemd:%s\n", args[0], r.JournalUnit)
		return 0
	}
	for _, p := range uniqueSorted(r.Paths) {
		fmt.Println(sanitizeTerminalText(p))
	}
	return 0
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

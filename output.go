package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

type printer struct{ color bool }

var (
	errLevel   = regexp.MustCompile(`(?i)\b(error|err|fatal|panic|critical|severe|exception|traceback)\b`)
	warnLevel  = regexp.MustCompile(`(?i)\b(warn|warning)\b`)
	debugLevel = regexp.MustCompile(`(?i)\b(debug|trace)\b`)
)

func newPrinter(enabled bool) *printer {
	tty := false
	if fi, err := os.Stdout.Stat(); err == nil {
		tty = fi.Mode()&os.ModeCharDevice != 0
	}
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		enabled = false
	}
	return &printer{color: enabled && tty}
}

func (p *printer) header(path, suffix string) string {
	ts := time.Now().Format("15:04:05")
	meta := ts
	if suffix != "" {
		meta += " · " + suffix
	}
	if p.color {
		return fmt.Sprintf("\x1b[1;36m%s\x1b[0m \x1b[2m[%s]\x1b[0m", path, meta)
	}
	return fmt.Sprintf("%s [%s]", path, meta)
}

func (p *printer) decorate(line string) string {
	if !p.color {
		return line
	}
	switch {
	case errLevel.MatchString(line):
		return "\x1b[31m" + line + "\x1b[0m"
	case warnLevel.MatchString(line):
		return "\x1b[33m" + line + "\x1b[0m"
	case debugLevel.MatchString(line):
		return "\x1b[2m" + line + "\x1b[0m"
	default:
		return line
	}
}

func (p *printer) block(path string, lines []string, suffix string) {
	fmt.Println()
	fmt.Println(p.header(path, suffix))
	if len(lines) == 0 {
		if p.color {
			fmt.Println("  \x1b[2m(empty)\x1b[0m")
		} else {
			fmt.Println("  (empty)")
		}
		return
	}
	for _, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		fmt.Printf("  %s\n", p.decorate(line))
	}
}

func (p *printer) infof(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "logg: "+format+"\n", args...)
}
func (p *printer) errorf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "logg: error: "+format+"\n", args...)
}

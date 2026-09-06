package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

type printer struct {
	color      bool
	json       bool
	sourceMeta func(string) (string, string)
	logrotate  func(string) bool
}

var (
	fatalLevel = regexp.MustCompile(`(?i)\b(fatal|panic|critical|severe|exception|traceback)\b`)
	errLevel   = regexp.MustCompile(`(?i)\b(error|err)\b`)
	warnLevel  = regexp.MustCompile(`(?i)\b(warn|warning)\b`)
	infoLevel  = regexp.MustCompile(`(?i)\b(info)\b`)
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
	managedByLogrotate := p.logrotate != nil && p.logrotate(path)
	displayPath := path
	if p.sourceMeta != nil {
		category, module := p.sourceMeta(path)
		if category != "" || module != "" {
			displayPath = fmt.Sprintf("[%s/%s] %s", category, module, path)
		}
	}
	path = sanitizeTerminalText(displayPath)
	if managedByLogrotate {
		if suffix != "" {
			suffix += " · "
		}
		suffix += "logrotate"
	}
	suffix = sanitizeTerminalText(suffix)
	ts := time.Now().Format("15:04:05")
	meta := ts
	if suffix != "" {
		meta += " · " + suffix
	}
	if p.color {
		return fmt.Sprintf("\x1b[1;36m%s\x1b[0m \x1b[2m[shown %s]\x1b[0m", path, meta)
	}
	return fmt.Sprintf("%s [shown %s]", path, meta)
}

func (p *printer) decorate(line string) string {
	line = sanitizeTerminalText(line)
	if !p.color {
		return line
	}
	switch {
	case fatalLevel.MatchString(line):
		return "\x1b[1;35m" + line + "\x1b[0m"
	case errLevel.MatchString(line):
		return "\x1b[1;31m" + line + "\x1b[0m"
	case warnLevel.MatchString(line):
		return "\x1b[33m" + line + "\x1b[0m"
	case infoLevel.MatchString(line):
		return "\x1b[36m" + line + "\x1b[0m"
	case debugLevel.MatchString(line):
		return "\x1b[2;34m" + line + "\x1b[0m"
	default:
		return line
	}
}

func sanitizeTerminalText(text string) string {
	var safe strings.Builder
	safe.Grow(len(text))
	for index := 0; index < len(text); {
		current := text[index]
		if current == 0x1b {
			index++
			if index >= len(text) {
				break
			}
			switch text[index] {
			case '[':
				index++
				for index < len(text) {
					final := text[index]
					index++
					if final >= 0x40 && final <= 0x7e {
						break
					}
				}
			case ']':
				index++
				for index < len(text) {
					if text[index] == 0x07 {
						index++
						break
					}
					if text[index] == 0x1b && index+1 < len(text) && text[index+1] == '\\' {
						index += 2
						break
					}
					index++
				}
			default:
				index++
			}
			continue
		}
		if current < 0x20 {
			if current == '\t' {
				safe.WriteByte(current)
			}
			index++
			continue
		}
		if current == 0x7f {
			index++
			continue
		}
		safe.WriteByte(current)
		index++
	}
	return safe.String()
}

func (p *printer) block(path string, lines []string, suffix string) {
	if p.json {
		category, module := "", ""
		if p.sourceMeta != nil {
			category, module = p.sourceMeta(path)
		}
		_ = json.NewEncoder(os.Stdout).Encode(struct {
			Path      string   `json:"path"`
			Category  string   `json:"category,omitempty"`
			Module    string   `json:"module,omitempty"`
			Logrotate bool     `json:"logrotate,omitempty"`
			Time      string   `json:"time"`
			Suffix    string   `json:"suffix,omitempty"`
			Lines     []string `json:"lines"`
		}{Path: path, Category: category, Module: module, Logrotate: p.logrotate != nil && p.logrotate(path), Time: time.Now().Format(time.RFC3339), Suffix: suffix, Lines: lines})
		return
	}
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
	fmt.Fprintf(os.Stderr, "logc: %s\n", sanitizeTerminalText(fmt.Sprintf(format, args...)))
}
func (p *printer) errorf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "logc: error: %s\n", sanitizeTerminalText(fmt.Sprintf(format, args...)))
}

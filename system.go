package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

func runSystemLogs(ctx context.Context, lines int, kernelOnly bool) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		if kernelOnly {
			if path, err := exec.LookPath("dmesg"); err == nil {
				cmd = exec.CommandContext(ctx, path, "--follow", "--human")
			} else {
				return fmt.Errorf("dmesg not found")
			}
		} else if path, err := exec.LookPath("journalctl"); err == nil {
			cmd = exec.CommandContext(ctx, path, "--follow", "--lines", fmt.Sprint(lines), "--output", "short-iso")
		} else if path, err := exec.LookPath("dmesg"); err == nil {
			cmd = exec.CommandContext(ctx, path, "--follow", "--human")
		} else {
			return fmt.Errorf("neither journalctl nor dmesg found")
		}
	case "darwin":
		if kernelOnly {
			cmd = exec.CommandContext(ctx, "/usr/bin/log", "stream", "--style", "compact", "--predicate", "process == \"kernel\"")
		} else {
			cmd = exec.CommandContext(ctx, "/usr/bin/log", "stream", "--style", "compact")
		}
	default:
		return fmt.Errorf("system log streaming is not supported on %s", runtime.GOOS)
	}
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
	return cmd.Run()
}

func unitCommand(ctx context.Context, unit string, lines int, follow bool, q Query) (*exec.Cmd, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("systemd unit logs are supported on Linux only")
	}
	path, err := exec.LookPath("journalctl")
	if err != nil {
		return nil, fmt.Errorf("journalctl not found")
	}
	args := []string{"-u", unit, "--output", "short-iso", "--no-pager"}
	if !q.Since.IsZero() {
		args = append(args, "--since", q.Since.Format("2006-01-02 15:04:05"))
	} else {
		args = append(args, "--lines", fmt.Sprint(lines))
	}
	if follow {
		args = append(args, "--follow")
	}
	return exec.CommandContext(ctx, path, args...), nil
}

func runSystemUnit(ctx context.Context, unit string, lines int, follow bool) error {
	cmd, err := unitCommand(ctx, unit, lines, follow, Query{})
	if err != nil {
		return err
	}
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
	return cmd.Run()
}

func runSystemUnitFiltered(ctx context.Context, unit string, lines int, follow bool, q Query, out *printer) error {
	cmd, err := unitCommand(ctx, unit, lines, follow, q)
	if err != nil {
		return err
	}
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}

	s := bufio.NewScanner(pipe)
	buf := make([]byte, 64*1024)
	s.Buffer(buf, 2*1024*1024)
	var before []string
	afterRemaining := 0
	lastOutput := ""
	emit := func(line string) {
		if q.Dedup && line == lastOutput {
			return
		}
		out.block("systemd:"+unit, []string{line}, "")
		lastOutput = line
	}
	for s.Scan() {
		line := strings.TrimSuffix(s.Text(), "\r")
		if q.ignored(line) {
			continue
		}
		matched := q.match(line)
		if q.Regex == nil || matched {
			if q.Regex != nil {
				for _, prev := range before {
					emit(prev)
				}
			}
			emit(line)
			if matched {
				afterRemaining = q.After
			}
			before = nil
		} else if afterRemaining > 0 {
			emit(line)
			afterRemaining--
		}
		if q.Before > 0 {
			before = append(before, line)
			if len(before) > q.Before {
				before = before[len(before)-q.Before:]
			}
		}
	}
	scanErr := s.Err()
	waitErr := cmd.Wait()
	if scanErr != nil {
		return scanErr
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return waitErr
}

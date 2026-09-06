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
	cmd, err := systemLogsCommand(ctx, lines, kernelOnly, Query{})
	if err != nil {
		return err
	}
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
	return cmd.Run()
}

func systemLogsCommand(ctx context.Context, lines int, kernelOnly bool, query Query) (*exec.Cmd, error) {
	if query.All && runtime.GOOS != "linux" {
		return nil, fmt.Errorf("--all system-log search requires journald on Linux")
	}
	if query.All && kernelOnly {
		return nil, fmt.Errorf("--all is not supported with kernel-only logs")
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		if kernelOnly {
			if path, err := exec.LookPath("dmesg"); err == nil {
				cmd = exec.CommandContext(ctx, path, "--follow", "--human")
			} else {
				return nil, fmt.Errorf("dmesg not found")
			}
		} else if path, err := exec.LookPath("journalctl"); err == nil {
			args := []string{"--follow", "--output", "short-iso", "--no-pager"}
			if !query.All {
				if query.Since.IsZero() {
					args = append(args, "--lines", fmt.Sprint(lines))
				} else {
					args = append(args, "--since", query.Since.Format("2006-01-02 15:04:05"))
				}
			}
			cmd = exec.CommandContext(ctx, path, args...)
		} else if query.All {
			return nil, fmt.Errorf("journalctl is required for --all system-log search")
		} else if path, err := exec.LookPath("dmesg"); err == nil {
			cmd = exec.CommandContext(ctx, path, "--follow", "--human")
		} else {
			return nil, fmt.Errorf("neither journalctl nor dmesg found")
		}
	case "darwin":
		if kernelOnly {
			cmd = exec.CommandContext(ctx, "/usr/bin/log", "stream", "--style", "compact", "--predicate", "process == \"kernel\"")
		} else {
			cmd = exec.CommandContext(ctx, "/usr/bin/log", "stream", "--style", "compact")
		}
	default:
		return nil, fmt.Errorf("system log streaming is not supported on %s", runtime.GOOS)
	}
	return cmd, nil
}

func runSystemLogsFiltered(ctx context.Context, lines int, kernelOnly bool, query Query, out *printer) error {
	cmd, err := systemLogsCommand(ctx, lines, kernelOnly, query)
	if err != nil {
		return err
	}
	return runFilteredCommand(ctx, cmd, "system", query, out)
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
	if !q.All {
		if !q.Since.IsZero() {
			args = append(args, "--since", q.Since.Format("2006-01-02 15:04:05"))
		} else {
			args = append(args, "--lines", fmt.Sprint(lines))
		}
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
	return runFilteredCommand(ctx, cmd, "systemd:"+unit, q, out)
}

func runFilteredCommand(ctx context.Context, cmd *exec.Cmd, source string, q Query, out *printer) error {
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
	filter := &follower{query: q}
	state := &fileState{}
	headerShown := false
	for s.Scan() {
		line := strings.TrimSuffix(s.Text(), "\r")
		for _, selected := range filter.streamFilter(state, []string{line}) {
			if out.json {
				out.block(source, []string{selected}, "")
				continue
			}
			if !headerShown {
				fmt.Println()
				fmt.Println(out.header(source, "following"))
				headerShown = true
			}
			fmt.Printf("  %s\n", out.decorate(selected))
		}
	}
	if summary, ok := takeStreamDedupSummary(state); ok {
		if out.json {
			out.block(source, []string{summary}, "")
		} else {
			if !headerShown {
				fmt.Println()
				fmt.Println(out.header(source, "following"))
			}
			fmt.Printf("  %s\n", out.decorate(summary))
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

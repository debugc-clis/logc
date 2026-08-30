package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func dockerLogsArgs(args []string) []string {
	dockerArgs := []string{"logs"}
	if !hasDockerFollow(args) {
		dockerArgs = append(dockerArgs, "--follow")
	}
	return append(dockerArgs, args...)
}

func hasDockerFollow(args []string) bool {
	for _, arg := range args {
		if arg == "-f" || arg == "--follow" || strings.HasPrefix(arg, "--follow=") {
			return true
		}
	}
	return false
}

func dockerCommand(args []string) int {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		fmt.Fprintln(os.Stderr, "logc: usage: logc docker [docker logs options] [-m REGEX] [-i] [-C N] [--dedup] [--no-color] [--json] CONTAINER")
		return 0
	}
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "logc: usage: logc docker [docker logs options] CONTAINER")
		return 2
	}
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "logc docker:", err)
		return 1
	}
	dockerInput, view, err := parseDockerViewArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "logc docker:", err)
		return 2
	}
	query, err := buildQuery(view.match, view.ignoreCase, time.Time{}, view.context, view.context, view.dedup, cfg.IgnoreLines)
	if err != nil {
		fmt.Fprintln(os.Stderr, "logc docker:", err)
		return 2
	}
	out := newPrinter(cfg.Color && !view.noColor && !view.json)
	out.sourceMeta = func(string) (string, string) { return "container", dockerInput[len(dockerInput)-1] }
	out.json = view.json

	docker, err := exec.LookPath("docker")
	if err != nil {
		fmt.Fprintln(os.Stderr, "logc docker: docker not found in PATH")
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cmd := exec.CommandContext(ctx, docker, dockerLogsArgs(dockerInput)...)
	cmd.Stdin = os.Stdin
	reader, writer := io.Pipe()
	cmd.Stdout, cmd.Stderr = writer, writer
	done := make(chan error, 1)
	go func() {
		runErr := cmd.Run()
		_ = writer.Close()
		done <- runErr
	}()

	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxCarryBytes)
	filter := &follower{query: query}
	state := &fileState{}
	source := "docker:" + dockerInput[len(dockerInput)-1]
	for scanner.Scan() {
		for _, selected := range filter.streamFilter(state, []string{strings.TrimSuffix(scanner.Text(), "\r")}) {
			if out.json {
				out.block(source, []string{selected}, "")
			} else {
				fmt.Println(out.decorate(selected))
			}
		}
	}
	if summary, ok := takeStreamDedupSummary(state); ok {
		if out.json {
			out.block(source, []string{summary}, "")
		} else {
			fmt.Println(out.decorate(summary))
		}
	}
	runErr := <-done
	if scanErr := scanner.Err(); scanErr != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, "logc docker:", scanErr)
		return 1
	}
	if runErr != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, "logc docker:", runErr)
		return 1
	}
	return 0
}

type dockerViewOptions struct {
	match      string
	ignoreCase bool
	context    int
	dedup      bool
	noColor    bool
	json       bool
}

func parseDockerViewArgs(args []string) ([]string, dockerViewOptions, error) {
	forwarded := make([]string, 0, len(args))
	options := dockerViewOptions{}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		next := func() (string, error) {
			if index+1 >= len(args) {
				return "", fmt.Errorf("%s requires a value", arg)
			}
			index++
			return args[index], nil
		}
		switch arg {
		case "-m", "--match":
			value, err := next()
			if err != nil {
				return nil, options, err
			}
			options.match = severityPattern(value)
		case "-i", "--ignore-case":
			options.ignoreCase = true
		case "-C", "--context":
			value, err := next()
			if err != nil {
				return nil, options, err
			}
			contextLines, err := strconv.Atoi(value)
			if err != nil || contextLines < 0 {
				return nil, options, fmt.Errorf("invalid context %q", value)
			}
			options.context = contextLines
		case "--dedup":
			options.dedup = true
		case "--no-color":
			options.noColor = true
		case "--json":
			options.json = true
		case "--":
			forwarded = append(forwarded, args[index+1:]...)
			return forwarded, options, nil
		default:
			forwarded = append(forwarded, arg)
		}
	}
	if len(forwarded) == 0 {
		return nil, options, fmt.Errorf("container name is required")
	}
	return forwarded, options, nil
}

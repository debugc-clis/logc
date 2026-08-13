package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
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
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "logc: usage: logc docker [docker logs options] CONTAINER")
		return 2
	}

	docker, err := exec.LookPath("docker")
	if err != nil {
		fmt.Fprintln(os.Stderr, "logc docker: docker not found in PATH")
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cmd := exec.CommandContext(ctx, docker, dockerLogsArgs(args)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, "logc docker:", err)
		return 1
	}
	return 0
}

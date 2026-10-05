package main

import (
	"io"
	"os/exec"
)

func stopLogCommand(command *exec.Cmd, reader io.Reader) {
	if closer, ok := reader.(io.Closer); ok {
		_ = closer.Close()
	}
	if command.Process != nil {
		_ = command.Process.Kill()
	}
}

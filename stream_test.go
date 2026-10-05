package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestLogStreamProcess(t *testing.T) {
	mode := os.Getenv("LOGC_TEST_STREAM_PROCESS")
	if mode == "" {
		return
	}
	if mode == "oversized" {
		_, _ = fmt.Fprint(os.Stdout, strings.Repeat("x", maxCarryBytes+128*1024))
		time.Sleep(30 * time.Second)
	} else if mode == "events" {
		for index := 0; index < 500; index++ {
			fmt.Fprintln(os.Stdout, "ERROR timeout")
		}
	} else {
		fmt.Fprintln(os.Stdout, "INFO healthy")
		fmt.Fprintln(os.Stdout, "ERROR timeout")
	}
	os.Exit(0)
}

func logStreamTestCommand(ctx context.Context, mode string) *exec.Cmd {
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLogStreamProcess$")
	command.Env = append(os.Environ(), "LOGC_TEST_STREAM_PROCESS="+mode)
	return command
}

func TestLogCommandsStopOnOversizedLine(t *testing.T) {
	for _, runner := range []struct {
		name string
		run  func(context.Context, *exec.Cmd, string, Query, *printer) error
	}{
		{name: "system", run: runFilteredCommand},
		{name: "docker", run: runDockerLogCommand},
	} {
		t.Run(runner.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out := &printer{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
			err := runner.run(ctx, logStreamTestCommand(ctx, "oversized"), "test", Query{}, out)
			if err == nil || !strings.Contains(err.Error(), "token too long") {
				t.Fatalf("expected oversized-line error, got %v", err)
			}
			if ctx.Err() != nil {
				t.Fatal("command waited for timeout instead of stopping the producer")
			}
		})
	}
}

func TestLogCommandsFilterAndUsePrinterOutput(t *testing.T) {
	for _, runner := range []struct {
		name string
		run  func(context.Context, *exec.Cmd, string, Query, *printer) error
	}{
		{name: "system", run: runFilteredCommand},
		{name: "docker", run: runDockerLogCommand},
	} {
		t.Run(runner.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var output bytes.Buffer
			query, err := buildQuery("ERROR", false, time.Time{}, 0, 0, false, nil)
			if err != nil {
				t.Fatal(err)
			}
			out := &printer{stdout: &output, stderr: &bytes.Buffer{}}
			if err := runner.run(ctx, logStreamTestCommand(ctx, "finite"), "test", query, out); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output.String(), "ERROR timeout") || strings.Contains(output.String(), "INFO healthy") {
				t.Fatalf("unexpected filtered output: %q", output.String())
			}
		})
	}
}

func TestJournalScannerStopsOnOversizedLine(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := logStreamTestCommand(ctx, "oversized")
	reader, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	lines := make(chan string, 1)
	done := make(chan error, 1)
	scanCommandLines(ctx, reader, command, lines, done)
	if err := <-done; err == nil || !strings.Contains(err.Error(), "token too long") {
		t.Fatalf("expected oversized-line error, got %v", err)
	}
	if ctx.Err() != nil {
		t.Fatal("journal scanner waited for timeout")
	}
}

func TestJournalWatchCountsBufferedEventsBeforeExit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var output bytes.Buffer
	query, err := buildQuery("ERROR", false, time.Time{}, 0, 0, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = runJournalAlertCommand(ctx, logStreamTestCommand(ctx, "events"), "systemd:api", defaultConfig(), query, &output, false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "500 events/min") {
		t.Fatalf("missing final buffered events: %s", output.String())
	}
}

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestLogStreamProcess(t *testing.T) {
	mode := os.Getenv("LOGC_TEST_STREAM_PROCESS")
	if mode == "" {
		return
	}
	if mode == "live" || mode == "silent" {
		ready := os.NewFile(3, "ready")
		if mode == "live" {
			fmt.Fprintln(os.Stdout, "ERROR live producer")
		}
		_, _ = ready.Write([]byte("ready\n"))
		_ = ready.Close()
		time.Sleep(30 * time.Second)
	} else if mode == "oversized" {
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

func TestLogCommandsCancelAndReapLiveProducer(t *testing.T) {
	for _, name := range []string{"system", "docker", "journal", "journal-local-reader", "journal-scanner", "journal-scanner-blocked"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			commandCtx := ctx
			cleanupProducer := cancel
			if name == "journal-local-reader" {
				commandCtx, cleanupProducer = context.WithCancel(context.Background())
			}
			defer cleanupProducer()
			mode := "silent"
			if name == "journal-scanner-blocked" {
				mode = "live"
			}
			command := logStreamTestCommand(commandCtx, mode)
			ready, producerReady, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer ready.Close()
			defer producerReady.Close()
			command.ExtraFiles = []*os.File{producerReady}
			result := make(chan error, 1)
			go func() {
				out := &printer{stdout: io.Discard, stderr: io.Discard}
				switch name {
				case "system":
					result <- runFilteredCommand(ctx, command, "system", Query{}, out)
				case "docker":
					result <- runDockerLogCommand(ctx, command, "docker:test", Query{}, out)
				case "journal-scanner", "journal-scanner-blocked":
					reader, err := command.StdoutPipe()
					if err != nil {
						result <- err
						return
					}
					if err := command.Start(); err != nil {
						result <- err
						return
					}
					lines := make(chan string)
					scanDone := make(chan error, 1)
					scanCommandLines(ctx, reader, command, lines, scanDone)
					result <- <-scanDone
				default:
					result <- runJournalAlertCommand(ctx, command, "systemd:test", defaultConfig(), Query{}, io.Discard, false, false, false)
				}
			}()
			join := func() {
				select {
				case <-result:
				case <-time.After(5 * time.Second):
					t.Error("stream worker did not join after cancellation")
				}
			}
			if err := ready.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			handshake := make([]byte, len("ready\n"))
			if _, err := io.ReadFull(ready, handshake); err != nil {
				cancel()
				cleanupProducer()
				join()
				t.Fatalf("producer readiness: %v", err)
			}
			if string(handshake) != "ready\n" {
				cancel()
				cleanupProducer()
				join()
				t.Fatalf("invalid readiness handshake: %q", handshake)
			}
			cancel()
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Errorf("cancellation returned %v, want context.Canceled", err)
				}
				if err := command.Process.Signal(syscall.Signal(0)); !errors.Is(err, os.ErrProcessDone) {
					_ = command.Process.Kill()
					t.Fatalf("returned before reaping immediate producer: %v", err)
				}
			case <-time.After(5 * time.Second):
				cleanupProducer()
				join()
				t.Fatal("cancellation did not stop the stream")
			}
		})
	}
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

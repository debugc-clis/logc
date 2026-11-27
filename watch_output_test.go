package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

type watchTestWriter struct {
	frames       int
	calls        int
	failed       bool
	afterFailure int
	failFrame    int
	short        bool
	cause        error
	onFrame      func(int)
	onFooter     func()
	output       bytes.Buffer
}

func (writer *watchTestWriter) Write(data []byte) (int, error) {
	writer.calls++
	if writer.failed {
		writer.afterFailure++
	}
	if bytes.HasPrefix(data, []byte("logc watch ")) {
		writer.frames++
		if writer.onFrame != nil {
			writer.onFrame(writer.frames)
		}
	}
	if writer.failFrame > 0 && writer.frames >= writer.failFrame {
		writer.failed = true
		if writer.short {
			return len(data) - 1, nil
		}
		return 0, writer.cause
	}
	if bytes.Contains(data, []byte("Refreshes every second")) && writer.onFooter != nil {
		writer.onFooter()
	}
	return writer.output.Write(data)
}

func checkWatchOutputError(t *testing.T, err error, writer *watchTestWriter) {
	t.Helper()
	cause := writer.cause
	if writer.short {
		cause = io.ErrShortWrite
	}
	var typed *watchOutputError
	if !isWatchOutputError(err) || !errors.As(err, &typed) || !errors.Is(err, cause) {
		t.Fatalf("error=%v, want typed dashboard error preserving %v", err, cause)
	}
	if writer.afterFailure != 0 {
		t.Fatalf("%d underlying writes after failure", writer.afterFailure)
	}
}

func TestWatchOutputFileBoundaries(t *testing.T) {
	for _, phase := range []string{"initial", "periodic", "final", "same-write-cancel"} {
		for _, short := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/short=%v", phase, short), func(t *testing.T) {
				root := t.TempDir()
				first := writeWatchTestLog(t, root, "a.log")
				second := writeWatchTestLog(t, root, "b.log")
				cfg := defaultConfig()
				cfg.ScanInterval = time.Hour
				watcher := newAlertWatcher(cfg, []string{first, second}, nil, nil, Query{})
				watcher.rescan(true)
				var files []*os.File
				for _, state := range watcher.states {
					files = append(files, state.File)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
				defer cancel()
				writer := &watchTestWriter{failFrame: 1, short: short, cause: errors.New("sink failed")}
				if phase == "periodic" || phase == "final" {
					writer.failFrame = 2
				}
				if phase == "final" {
					writer.onFooter = cancel
				}
				if phase == "same-write-cancel" {
					writer.onFrame = func(int) { cancel() }
				}
				err := watcher.run(ctx, writer, false, false)
				if phase == "periodic" && ctx.Err() != nil {
					t.Fatalf("periodic failure was only observed after cancellation: %v", ctx.Err())
				}
				checkWatchOutputError(t, err, writer)
				for _, file := range files {
					if file == nil {
						t.Fatal("expected retained descriptor")
					}
					if file.Fd() != ^uintptr(0) {
						t.Error("retained descriptor not closed")
					}
				}
			})
		}
	}
}

func TestWatchOutputHealthyCancel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	watcher := newAlertWatcher(defaultConfig(), nil, nil, nil, Query{})
	writer := &watchTestWriter{onFooter: cancel}
	if err := watcher.run(ctx, writer, false, false); err != nil || writer.frames != 2 {
		t.Fatalf("healthy cancellation err=%v frames=%d", err, writer.frames)
	}
}

func TestWatchOutputHelper(t *testing.T) {
	mode := os.Getenv("LOGC_WATCH_OUTPUT_HELPER")
	if mode == "" {
		return
	}
	ready := os.NewFile(3, "ready")
	_, _ = ready.Write([]byte("ready\n"))
	_ = ready.Close()
	switch mode {
	case "silent":
		time.Sleep(30 * time.Second)
	case "flood":
		for {
			fmt.Fprintln(os.Stdout, "ERROR flooding helper")
		}
	case "exit":
		fmt.Fprintln(os.Stdout, "ERROR exited helper")
	}
	os.Exit(0)
}

func TestWatchOutputJournalBoundaries(t *testing.T) {
	for _, scenario := range []struct{ mode, phase string }{
		{"silent", "initial"}, {"silent", "periodic"}, {"silent", "final"},
		{"flood", "initial"}, {"flood", "periodic"}, {"flood", "final"},
		{"exit", "eof"}, {"exit", "eof-cancel"}, {"silent", "same-write-cancel"},
		{"silent", "healthy"}, {"exit", "healthy"},
	} {
		for _, short := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%s/short=%v", scenario.mode, scenario.phase, short), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
				defer cancel()
				helperCtx, stopHelper := context.WithCancel(context.Background())
				defer stopHelper()
				command := exec.CommandContext(helperCtx, os.Args[0], "-test.run=^TestWatchOutputHelper$")
				command.Env = append(os.Environ(), "LOGC_WATCH_OUTPUT_HELPER="+scenario.mode, "GORACE=atexit_sleep_ms=0")
				ready, producerReady, err := os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				defer ready.Close()
				defer producerReady.Close()
				command.ExtraFiles = []*os.File{producerReady}
				writer := &watchTestWriter{failFrame: 1, short: short, cause: errors.New("journal sink failed")}
				if scenario.phase == "periodic" || scenario.phase == "final" || scenario.phase == "eof" || scenario.phase == "eof-cancel" {
					writer.failFrame = 2
				}
				if scenario.phase == "healthy" {
					writer.failFrame = 0
				}
				if scenario.phase == "final" || (scenario.phase == "healthy" && scenario.mode != "exit") {
					writer.onFooter = cancel
				}
				writer.onFrame = func(frame int) {
					if frame == 2 && scenario.phase == "eof-cancel" {
						cancel()
					}
					if frame != 1 {
						return
					}
					_ = ready.SetReadDeadline(time.Now().Add(3 * time.Second))
					data := make([]byte, 6)
					if _, err := io.ReadFull(ready, data); err != nil {
						t.Errorf("helper readiness: %v", err)
					}
					if scenario.mode == "flood" {
						time.Sleep(100 * time.Millisecond)
					}
					if scenario.phase == "same-write-cancel" {
						cancel()
					}
				}
				result := make(chan error, 1)
				go func() {
					result <- runJournalAlertCommand(ctx, command, "systemd:test", defaultConfig(), Query{}, writer, false, false, false)
				}()
				select {
				case err := <-result:
					if scenario.phase == "healthy" {
						if scenario.mode == "exit" && err != nil {
							t.Fatal(err)
						}
						if scenario.mode != "exit" && !errors.Is(err, context.Canceled) {
							t.Fatalf("healthy cancel: %v", err)
						}
						if writer.frames < 2 || (scenario.mode != "exit" && writer.frames != 2) {
							t.Fatalf("healthy frames=%d", writer.frames)
						}
					} else {
						checkWatchOutputError(t, err, writer)
					}
					if err := command.Process.Signal(syscall.Signal(0)); !errors.Is(err, os.ErrProcessDone) {
						t.Fatalf("helper not reaped: %v", err)
					}
				case <-time.After(5 * time.Second):
					cancel()
					stopHelper()
					select {
					case <-result:
					case <-time.After(2 * time.Second):
						t.Error("journal dashboard did not join after forced helper cleanup")
					}
					t.Fatal("journal dashboard failed to stop promptly")
				}
			})
		}
	}
}

func TestWatchOutputScannerSaturatedCancellation(t *testing.T) {
	helperCtx, stopHelper := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopHelper()
	command := logStreamTestCommand(helperCtx, "events")
	pipe, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		_ = pipe.Close()
		t.Fatal(err)
	}
	scanCtx, cancelScanner := context.WithCancel(context.Background())
	lines := make(chan string, 1)
	done := make(chan error, 1)
	go scanCommandLines(scanCtx, pipe, command, lines, done)
	joined := false
	defer func() {
		cancelScanner()
		stopLogCommand(command, pipe)
		if !joined {
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Error("scanner did not join after cleanup")
			}
		}
	}()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for len(lines) != cap(lines) {
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("scanner did not fill the unconsumed channel")
		}
	}
	cancelScanner()
	select {
	case err := <-done:
		joined = true
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("scanner cancellation: %v", err)
		}
		if err := command.Process.Signal(syscall.Signal(0)); !errors.Is(err, os.ErrProcessDone) {
			t.Fatalf("scanner did not reap helper: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("derived cancellation did not unblock saturated scanner")
	}
}

func TestWatchOutputRenderStopsAfterFailure(t *testing.T) {
	for _, clear := range []bool{false, true} {
		watcher := newAlertWatcher(defaultConfig(), nil, nil, nil, Query{})
		watcher.tracker.add("test.log", "ERROR alert", time.Now())
		writer := &watchTestWriter{failFrame: 1, cause: io.ErrClosedPipe}
		checkWatchOutputError(t, watcher.render(writer, true, clear), writer)
		if clear && writer.calls != 2 || !clear && writer.calls != 1 {
			t.Fatalf("calls=%d clear=%v", writer.calls, clear)
		}
	}
}

func TestWatchOutputFileLifecycle(t *testing.T) {
	root := t.TempDir()
	path := writeWatchTestLog(t, root, "app.log")
	watcher := newAlertWatcher(defaultConfig(), []string{filepath.Join(root, "*.log")}, nil, nil, Query{})
	defer func() {
		for _, state := range watcher.states {
			state.close()
		}
	}()
	watcher.rescan(true)
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("ERROR replacement\n"), 0600); err != nil {
		t.Fatal(err)
	}
	watcher.poll()
	if err := os.WriteFile(path, []byte("ERROR cut\n"), 0600); err != nil {
		t.Fatal(err)
	}
	watcher.poll()
	newPath := writeWatchTestLog(t, root, "new.log")
	watcher.rescan(true)
	file, err := os.OpenFile(newPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.WriteString("ERROR " + strings.Repeat("x", maxCarryBytes+100) + "\n")
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 4; index++ {
		watcher.poll()
	}
	var output bytes.Buffer
	if err := watcher.render(&output, false, false); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"replacement", "ERROR cut", "truncated long line", "2 sources"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("missing %q in %s", expected, output.String())
		}
	}
}

func TestWatchOutputCLI(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "logc")
	buildCtx, cancelBuild := context.WithTimeout(context.Background(), time.Minute)
	defer cancelBuild()
	if output, err := exec.CommandContext(buildCtx, "go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	config := filepath.Join(root, "config")
	if err := os.WriteFile(config, []byte("scan_interval=100ms\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, healthy := range []bool{false, true} {
		t.Run(fmt.Sprintf("healthy=%v", healthy), func(t *testing.T) {
			path := writeWatchTestLog(t, t.TempDir(), "app.log")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary, "watch", "ERROR", path, "--no-color")
			command.Env = append(os.Environ(), "LOGC_CONFIG="+config, "NO_COLOR=1")
			var diagnostics bytes.Buffer
			command.Stderr = &diagnostics
			ready := make(chan struct{})
			if healthy {
				command.Stdout = &followReadyWriter{writer: io.Discard, ready: ready, match: "Press Ctrl+C to stop"}
			} else {
				file, err := os.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				defer file.Close()
				command.Stdout = file
			}
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- command.Wait() }()
			if healthy {
				select {
				case <-ready:
					if err := command.Process.Signal(syscall.SIGTERM); err != nil {
						cancel()
						<-done
						t.Fatal(err)
					}
				case err := <-done:
					t.Fatalf("exited before readiness: %v stderr=%s", err, &diagnostics)
				case <-ctx.Done():
					<-done
					t.Fatalf("readiness timeout: %s", &diagnostics)
				}
			}
			err := <-done
			if ctx.Err() != nil {
				t.Fatalf("watch did not exit promptly: %v stderr=%s", err, &diagnostics)
			}
			if healthy {
				if err != nil {
					t.Fatalf("healthy cancellation: %v stderr=%s", err, &diagnostics)
				}
			} else {
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 1 || !strings.Contains(diagnostics.String(), "dashboard output") || !strings.Contains(diagnostics.String(), "bad file descriptor") {
					t.Fatalf("readonly stdout: %v stderr=%s", err, &diagnostics)
				}
			}
		})
	}
}

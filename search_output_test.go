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
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
)

type searchFailWriter struct {
	bytes.Buffer
	remaining int
	calls     int
	err       error
	short     bool
	failed    bool
	afterFail int
}

func (writer *searchFailWriter) Write(data []byte) (int, error) {
	writer.calls++
	if writer.failed {
		writer.afterFail++
	}
	if len(data) <= writer.remaining {
		writer.remaining -= len(data)
		return writer.Buffer.Write(data)
	}
	written, _ := writer.Buffer.Write(data[:writer.remaining])
	writer.remaining = 0
	writer.failed = true
	if writer.short {
		return written, nil
	}
	return written, writer.err
}

func TestSearchOutputBlockFailures(t *testing.T) {
	cause := errors.New("output unavailable")
	for _, jsonMode := range []bool{false, true} {
		for _, lines := range [][]string{nil, {"ERROR first", "ERROR second"}} {
			var healthy bytes.Buffer
			out := &printer{stdout: &healthy, json: jsonMode, now: func() time.Time { return time.Unix(0, 0) }}
			if err := out.block("app.log", lines, "search"); err != nil {
				t.Fatal(err)
			}
			for _, limit := range []int{0, 1, 5, healthy.Len() - 1} {
				for _, short := range []bool{false, true} {
					writer := &searchFailWriter{remaining: limit, err: cause, short: short}
					out.stdout = writer
					err := out.block("app.log", lines, "search")
					want := cause
					if short {
						want = io.ErrShortWrite
					}
					if !errors.Is(err, want) || writer.String() != healthy.String()[:limit] || writer.afterFail != 0 {
						t.Fatalf("json=%v limit=%d short=%v: err=%v output=%q", jsonMode, limit, short, err, writer.String())
					}
					calls := writer.calls
					writer.remaining = 10000
					writer.failed = false
					if err := out.block("app.log", nil, "retry"); err != nil || writer.calls == calls {
						t.Fatalf("printer retained failure across blocks: %v", err)
					}
				}
			}
		}
	}
}

func TestSearchOutputBatches(t *testing.T) {
	cause := errors.New("sink failed")
	for _, jsonMode := range []bool{false, true} {
		for _, dedup := range []bool{false, true} {
			for _, rows := range []int{1, 256, 257, 513} {
				path := filepath.Join(t.TempDir(), "app.log")
				var body strings.Builder
				for index := 0; index < rows; index++ {
					fmt.Fprintf(&body, "ERROR row %d\n", index)
					if dedup {
						fmt.Fprintf(&body, "ERROR row %d\n", index)
					}
				}
				writeSearchFollowLog(t, path, body.String())
				query := Query{Dedup: dedup}
				var healthy bytes.Buffer
				out := &printer{stdout: &healthy, stderr: io.Discard, json: jsonMode, now: func() time.Time { return time.Unix(0, 0) }}
				count, err := searchPaths([]string{path}, query, out)
				wantCount := rows
				if dedup {
					wantCount *= 2
				}
				if err != nil || count != wantCount {
					t.Fatalf("healthy count=%d err=%v", count, err)
				}
				for _, limit := range []int{0, healthy.Len() / 2, healthy.Len() - 1} {
					writer := &searchFailWriter{remaining: limit, err: cause}
					out.stdout = writer
					_, err := searchPaths([]string{path, filepath.Join(t.TempDir(), "missing.log")}, query, out)
					if !isSearchOutputError(err) || !errors.Is(err, cause) || writer.String() != healthy.String()[:limit] || writer.afterFail != 0 {
						t.Fatalf("json=%v dedup=%v rows=%d limit=%d err=%v", jsonMode, dedup, rows, limit, err)
					}
				}
			}
		}
	}
}

type searchOneLineReader struct{ reads int }

func (reader *searchOneLineReader) Read(data []byte) (int, error) {
	reader.reads++
	return copy(data, "ERROR\n"), nil
}

func TestSearchOutputCallbackAbort(t *testing.T) {
	cause := errors.New("callback stopped")
	for _, query := range []Query{{}, {Regex: regexp.MustCompile("ERROR"), Before: 2, After: 2}} {
		reader := &searchOneLineReader{}
		calls := 0
		_, err := scanLogReaderEach(reader, time.Now(), query, func(string) error { calls++; return cause })
		if !errors.Is(err, cause) || calls != 1 || reader.reads != 1 {
			t.Fatalf("err=%v callbacks=%d reads=%d", err, calls, reader.reads)
		}
	}
}

func TestSearchOutputAbortClosesReader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.log")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	producer := make(chan error, 1)
	go func() {
		file, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			producer <- err
			return
		}
		defer file.Close()
		_, err = file.Write([]byte(strings.Repeat("ERROR\n", 1024*1024)))
		producer <- err
	}()
	cause := errors.New("abort")
	done := make(chan error, 1)
	go func() {
		_, err := scanLogPathEachErr(path, Query{}, func(string) error { return cause })
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, cause) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("scanner did not abort")
	}
	select {
	case err := <-producer:
		if !errors.Is(err, syscall.EPIPE) {
			t.Fatalf("producer did not observe closed reader: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reader remained open after callback abort")
	}
}

func TestSearchOutputDoesNotOpenLaterFile(t *testing.T) {
	root := t.TempDir()
	first, later := filepath.Join(root, "first.log"), filepath.Join(root, "later.log")
	writeSearchFollowLog(t, first, strings.Repeat("ERROR\n", 600))
	if err := syscall.Mkfifo(later, 0o600); err != nil {
		t.Fatal(err)
	}
	cause := errors.New("stop output")
	writer := &searchFailWriter{err: cause}
	done := make(chan error, 1)
	go func() {
		_, err := searchPaths([]string{first, later}, Query{}, &printer{stdout: writer, stderr: io.Discard})
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, cause) || !isSearchOutputError(err) || writer.calls != 1 {
			t.Fatalf("err=%v writes=%d", err, writer.calls)
		}
	case <-time.After(5 * time.Second):
		file, err := os.OpenFile(later, os.O_RDWR|syscall.O_NONBLOCK, 0)
		if err == nil {
			file.WriteString("ERROR later\n")
			file.Close()
		}
		t.Fatal("search opened later FIFO after output failed")
	}
}

func TestSearchOutputFailurePriority(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log.gz")
	corruptSearchStatusGzip(t, path, "ERROR prefix\n")
	cause := errors.New("output lost")
	var diagnostics bytes.Buffer
	_, err := searchPaths([]string{path}, Query{Dedup: true}, &printer{stdout: &searchFailWriter{err: cause}, stderr: &diagnostics})
	if !errors.Is(err, cause) || !isSearchOutputError(err) || diagnostics.Len() != 0 {
		t.Fatalf("err=%v diagnostics=%s", err, &diagnostics)
	}
}

func TestSearchOutputSystemBootstrap(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "app.log")
	writeSearchFollowLog(t, path, "ERROR prefix\n")
	cfg := defaultConfig()
	cfg.SystemLogPaths = []string{path}
	cause := errors.New("system sink failed")
	var diagnostics bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := runSystemFileLogs(ctx, cfg, []string{path}, 10, Query{All: true, Regex: regexp.MustCompile("ERROR")}, &printer{stdout: &searchFailWriter{err: cause}, stderr: &diagnostics})
	if !errors.Is(err, cause) || strings.Contains(diagnostics.String(), "matching lines") {
		t.Fatalf("err=%v diagnostics=%s", err, &diagnostics)
	}
}

func TestSearchOutputCLI(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "logc")
	buildContext, cancelBuild := context.WithTimeout(context.Background(), time.Minute)
	defer cancelBuild()
	if output, err := exec.CommandContext(buildContext, "go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	config := filepath.Join(root, "config")
	writeSearchFollowLog(t, config, "")
	for _, gzipMode := range []bool{false, true} {
		for _, jsonMode := range []bool{false, true} {
			for _, follow := range []bool{false, true} {
				path := filepath.Join(t.TempDir(), "app.log")
				if gzipMode {
					path += ".gz"
				}
				writeSearchFollowLog(t, path, "ERROR prefix\n")
				readonly, err := os.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				args := []string{path, "ERROR", "--all"}
				if jsonMode {
					args = append(args, "--json")
				}
				if follow {
					args = append(args, "-f")
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				cmd := exec.CommandContext(ctx, binary, args...)
				cmd.Env = append(os.Environ(), "LOGC_CONFIG="+config, "NO_COLOR=1")
				var diagnostics bytes.Buffer
				cmd.Stdout, cmd.Stderr = readonly, &diagnostics
				err = cmd.Run()
				readonly.Close()
				cancel()
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 1 {
					t.Fatalf("gzip=%v json=%v follow=%v err=%v stderr=%s", gzipMode, jsonMode, follow, err, &diagnostics)
				}
				text := diagnostics.String()
				if !strings.Contains(text, "bad file descriptor") || strings.Contains(text, "matching lines") || strings.Contains(text, "--current") || strings.Contains(text, "failed to read") || (follow && !strings.Contains(text, "live follow not started")) {
					t.Fatalf("misleading diagnostics: %s", text)
				}
			}
		}
	}
	path := filepath.Join(root, "pipeline.log")
	writeSearchFollowLog(t, path, strings.Repeat("ERROR pipeline\n", 100000))
	for _, jsonMode := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		args := []string{path, "ERROR", "--all"}
		if jsonMode {
			args = append(args, "--json")
		}
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Env = append(os.Environ(), "LOGC_CONFIG="+config)
		reader, writer, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		reader.Close()
		cmd.Stdout = writer
		err = cmd.Run()
		writer.Close()
		cancel()
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("closed pipeline: %v", err)
		}
		status, ok := exit.Sys().(syscall.WaitStatus)
		if !ok || !status.Signaled() || status.Signal() != syscall.SIGPIPE {
			t.Fatalf("SIGPIPE default changed: %v", err)
		}
		ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
		cmd = exec.CommandContext(ctx, binary, args...)
		cmd.Env = append(os.Environ(), "LOGC_CONFIG="+config)
		cmd.Stdout = io.Discard
		if err := cmd.Run(); err != nil {
			t.Fatalf("healthy pipeline: %v", err)
		}
		cancel()
	}
}

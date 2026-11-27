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

type followOutputWriter struct {
	searchFailWriter
	onWrite func()
}

type followReadyWriter struct {
	writer io.Writer
	ready  chan struct{}
	match  string
	text   string
}

func (writer *followReadyWriter) Write(data []byte) (int, error) {
	written, err := writer.writer.Write(data)
	if writer.ready != nil {
		writer.text += string(data[:written])
		if written > 0 && strings.Contains(writer.text, writer.match) {
			close(writer.ready)
			writer.ready = nil
			writer.text = ""
		}
	}
	return written, err
}

func (writer *followOutputWriter) Write(data []byte) (int, error) {
	if writer.onWrite != nil {
		writer.onWrite()
	}
	return writer.searchFailWriter.Write(data)
}

func checkFollowOutputError(t *testing.T, err, cause error, path string) {
	t.Helper()
	var outputErr *fileOutputError
	if !isFileOutputError(err) || !errors.As(err, &outputErr) || !errors.Is(err, cause) || outputErr.Path != path || !strings.Contains(err.Error(), path) {
		t.Fatalf("output error = %v, want path %s and cause %v", err, path, cause)
	}
}

func TestFollowOutputBoundaries(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		for _, short := range []bool{false, true} {
			for _, phase := range []string{"snapshot", "initial", "new", "live", "final-dedup"} {
				t.Run(fmt.Sprintf("json=%v/short=%v/%s", jsonMode, short, phase), func(t *testing.T) {
					root := t.TempDir()
					first, later := filepath.Join(root, "a.log"), filepath.Join(root, "z.log")
					writeSearchFollowLog(t, first, "ERROR first\n")
					writeSearchFollowLog(t, later, "ERROR later\n")
					cause := errors.New("sink failed")
					writer := &searchFailWriter{err: cause, short: short}
					if short {
						cause = io.ErrShortWrite
					}
					out := &printer{stdout: writer, stderr: io.Discard, json: jsonMode}
					cfg := defaultConfig()
					cfg.FlushInterval = time.Millisecond
					cfg.ScanInterval = time.Hour
					f := newFollower(cfg, []string{first, later}, nil, out, Query{}, false)
					defer f.closeStates()
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					defer cancel()
					var err error
					switch phase {
					case "snapshot":
						err = showSnapshot([]string{first, later}, 10, Query{}, out)
					case "initial":
						err = f.run(ctx)
					case "new":
						err = f.rescan()
					default:
						f.skipInitial = true
						if err := f.addPath(first, false); err != nil {
							t.Fatal(err)
						}
						if err := f.addPath(later, false); err != nil {
							t.Fatal(err)
						}
						retained := f.states[first].File
						if phase == "live" {
							file, err := os.OpenFile(first, os.O_APPEND|os.O_WRONLY, 0)
							if err != nil {
								t.Fatal(err)
							}
							_, writeErr := file.WriteString("ERROR appended\n")
							file.Close()
							if writeErr != nil {
								t.Fatal(writeErr)
							}
							f.poll()
						} else {
							f.query.Dedup = true
							f.states[first].DedupSuppressed = 3
							cancel()
						}
						f.states[later].Pending = []string{"never written"}
						err = f.run(ctx)
						if retained.Fd() != ^uintptr(0) {
							t.Fatal("retained descriptor not closed")
						}
					}
					checkFollowOutputError(t, err, cause, first)
					if writer.calls != 1 || writer.afterFail != 0 {
						t.Fatalf("writes=%d, writes after failure=%d", writer.calls, writer.afterFail)
					}
					if phase == "initial" || phase == "new" {
						if _, ok := f.states[later]; ok {
							t.Fatal("opened later source after output failure")
						}
					}
					if phase == "initial" || phase == "live" || phase == "final-dedup" {
						for _, state := range f.states {
							if state.File != nil {
								t.Fatal("run retained an open descriptor")
							}
						}
					}
				})
			}
		}
	}
}

func TestFollowOutputCancellationPriority(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		for _, phase := range []string{"initial", "live", "final-dedup", "system-snapshot", "system-regex", "system-history", "system-live", "system-final-dedup"} {
			t.Run(fmt.Sprintf("json=%v/%s", jsonMode, phase), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "app.log")
				writeSearchFollowLog(t, path, "ERROR initial\n")
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				cause := errors.New("write canceled and failed")
				writer := &followOutputWriter{searchFailWriter: searchFailWriter{err: cause}, onWrite: cancel}
				out := &printer{stdout: writer, stderr: io.Discard, json: jsonMode}
				cfg := defaultConfig()
				cfg.SystemLogPaths = []string{path}
				cfg.FlushInterval = time.Millisecond
				cfg.ScanInterval = time.Hour
				var err error
				if strings.HasPrefix(phase, "system-") && phase != "system-final-dedup" {
					query := Query{}
					if phase == "system-regex" || phase == "system-history" || phase == "system-live" {
						query.Regex = regexp.MustCompile("ERROR")
					}
					query.All = phase == "system-history"
					if phase == "system-live" {
						query.Regex = regexp.MustCompile("APPENDED")
						go func() {
							select {
							case <-ctx.Done():
								return
							case <-time.After(100 * time.Millisecond):
							}
							file, openErr := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
							if openErr == nil {
								file.WriteString("APPENDED\n")
								file.Close()
							}
						}()
					}
					err = runSystemFileLogs(ctx, cfg, []string{path}, 10, query, out)
				} else {
					f := newFollower(cfg, []string{path}, nil, out, Query{}, phase != "initial")
					if phase != "initial" {
						if err := f.addPath(path, false); err != nil {
							t.Fatal(err)
						}
						if strings.Contains(phase, "final-dedup") {
							f.query.Dedup = true
							f.states[path].DedupSuppressed = 2
							cancel()
						} else {
							f.states[path].Pending = []string{"ERROR live"}
						}
					}
					err = f.run(ctx)
				}
				if !errors.Is(err, cause) || !systemCommandFailed(ctx, err) || ctx.Err() == nil || writer.afterFail != 0 {
					t.Fatalf("err=%v ctx=%v writes after failure=%d", err, ctx.Err(), writer.afterFail)
				}
			})
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, err := range []error{nil, context.Canceled, errors.New("input failure")} {
		if systemCommandFailed(ctx, err) {
			t.Fatalf("normal cancellation became fatal: %v", err)
		}
	}
	f := newFollower(defaultConfig(), nil, nil, &printer{stdout: io.Discard, stderr: io.Discard}, Query{}, true)
	if err := f.run(ctx); err != nil {
		t.Fatalf("healthy cancellation: %v", err)
	}
}

func TestFollowOutputInputErrorsContinue(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "app.log")
	writeSearchFollowLog(t, path, "healthy\n")
	var output, diagnostics bytes.Buffer
	out := &printer{stdout: &output, stderr: &diagnostics}
	f := newFollower(defaultConfig(), []string{path}, nil, out, Query{}, false)
	defer f.closeStates()
	if err := f.addPath(filepath.Join(root, "missing.log"), false); err != nil {
		t.Fatal(err)
	}
	if err := f.rescan(); err != nil || !strings.Contains(output.String(), "healthy") || !strings.Contains(diagnostics.String(), "skipping") {
		t.Fatalf("err=%v output=%s diagnostics=%s", err, &output, &diagnostics)
	}
	if err := showSnapshot([]string{filepath.Join(root, "missing.log"), path}, 10, Query{}, out); err != nil {
		t.Fatal(err)
	}
	if isFileOutputError(errors.New("input failure")) {
		t.Fatal("classified input failure as output failure")
	}
}

func TestFollowOutputRescanLifecycle(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		root := t.TempDir()
		first, fresh, later := filepath.Join(root, "a.log"), filepath.Join(root, "b.log"), filepath.Join(root, "z.log")
		writeSearchFollowLog(t, first, "initial\n")
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		cause := errors.New("new file sink failed")
		var initial bytes.Buffer
		out := &printer{stdout: &initial, stderr: io.Discard, json: jsonMode, now: func() time.Time { return time.Unix(0, 0) }}
		if err := out.block(first, []string{"initial"}, ""); err != nil {
			t.Fatal(err)
		}
		writer := &followOutputWriter{searchFailWriter: searchFailWriter{remaining: initial.Len(), err: cause}}
		out.stdout = writer
		cfg := defaultConfig()
		cfg.ScanInterval = time.Millisecond
		cfg.FlushInterval = time.Hour
		f := newFollower(cfg, []string{filepath.Join(root, "*.log")}, nil, out, Query{}, false)
		f.patterns = []string{first}
		var retained *os.File
		writer.onWrite = func() {
			if retained == nil {
				retained = f.states[first].File
				writeSearchFollowLog(t, fresh, "new file\n")
				writeSearchFollowLog(t, later, "never opened\n")
				f.patterns = []string{filepath.Join(root, "*.log")}
			}
			if writer.remaining == 0 {
				cancel()
			}
		}
		err := f.run(ctx)
		cancel()
		checkFollowOutputError(t, err, cause, fresh)
		if retained.Fd() != ^uintptr(0) || writer.afterFail != 0 || f.states[later] != nil {
			t.Fatalf("cleanup/abort failed: retained=%v afterFail=%d later=%v", retained.Fd(), writer.afterFail, f.states[later])
		}
	}
}

func TestFollowOutputDescriptorBoundAndCleanup(t *testing.T) {
	root := t.TempDir()
	f := newFollower(defaultConfig(), nil, nil, &printer{stdout: io.Discard, stderr: io.Discard}, Query{}, true)
	defer f.closeStates()
	var retained []*os.File
	for index := 0; index < maxOpenFollowFiles+3; index++ {
		path := filepath.Join(root, fmt.Sprintf("%03d.log", index))
		writeSearchFollowLog(t, path, "line\n")
		if err := f.addPath(path, false); err != nil {
			t.Fatal(err)
		}
		if file := f.states[path].File; file != nil {
			retained = append(retained, file)
		}
	}
	if len(retained) != maxOpenFollowFiles {
		t.Fatalf("open descriptors=%d, want %d", len(retained), maxOpenFollowFiles)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := f.run(ctx); err != nil {
		t.Fatal(err)
	}
	for _, file := range retained {
		if file.Fd() != ^uintptr(0) {
			t.Fatal("descriptor remained open after cancellation")
		}
	}
}

func TestFollowOutputCLI(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "logc")
	buildCtx, cancelBuild := context.WithTimeout(context.Background(), time.Minute)
	defer cancelBuild()
	if output, err := exec.CommandContext(buildCtx, "go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	config := filepath.Join(root, "config")
	writeSearchFollowLog(t, config, "flush_interval=10ms\nscan_interval=10ms\n")
	for _, jsonMode := range []bool{false, true} {
		for _, mode := range []string{"readonly", "search-append", "sigpipe", "healthy-cancel"} {
			t.Run(fmt.Sprintf("json=%v/%s", jsonMode, mode), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "app.log")
				writeSearchFollowLog(t, path, "INFO initial\n")
				args := []string{path}
				if mode == "search-append" {
					args = append(args, "ERROR", "-f")
				}
				if jsonMode {
					args = append(args, "--json")
				}
				ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, binary, args...)
				cmd.Env = append(os.Environ(), "LOGC_CONFIG="+config, "NO_COLOR=1")
				var diagnostics bytes.Buffer
				cmd.Stderr = &diagnostics
				ready := make(chan struct{})
				if mode == "search-append" {
					cmd.Stderr = &followReadyWriter{writer: &diagnostics, ready: ready, match: "0 matching lines"}
				}
				switch mode {
				case "readonly", "search-append":
					file, err := os.Open(path)
					if err != nil {
						t.Fatal(err)
					}
					defer file.Close()
					cmd.Stdout = file
				case "sigpipe":
					reader, writer, err := os.Pipe()
					if err != nil {
						t.Fatal(err)
					}
					reader.Close()
					defer writer.Close()
					cmd.Stdout = writer
				default:
					cmd.Stdout = &followReadyWriter{writer: io.Discard, ready: ready}
				}
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { done <- cmd.Wait() }()
				if mode == "search-append" || mode == "healthy-cancel" {
					select {
					case <-ready:
					case err := <-done:
						t.Fatalf("exited before readiness: %v stderr=%s", err, &diagnostics)
					case <-ctx.Done():
						<-done
						t.Fatalf("readiness timed out: %s", &diagnostics)
					}
					if mode == "healthy-cancel" {
						if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
							cancel()
							<-done
							t.Fatal(err)
						}
					}
				}
				var err error
				if mode == "search-append" {
					ticker := time.NewTicker(25 * time.Millisecond)
					defer ticker.Stop()
				appendLoop:
					for {
						select {
						case err = <-done:
							break appendLoop
						case <-ctx.Done():
							err = <-done
							break appendLoop
						case <-ticker.C:
							file, openErr := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
							if openErr != nil {
								cancel()
								<-done
								t.Fatal(openErr)
							}
							_, writeErr := file.WriteString("ERROR appended\n")
							closeErr := file.Close()
							if writeErr != nil || closeErr != nil {
								cancel()
								<-done
								t.Fatalf("append: write=%v close=%v", writeErr, closeErr)
							}
						}
					}
				} else {
					err = <-done
				}
				if ctx.Err() != nil {
					t.Fatalf("CLI did not exit promptly: %v stderr=%s", err, &diagnostics)
				}
				if mode == "healthy-cancel" {
					if err != nil {
						t.Fatal(err)
					}
					return
				}
				var exit *exec.ExitError
				if !errors.As(err, &exit) {
					t.Fatalf("expected failed exit: %v", err)
				}
				if mode == "sigpipe" {
					status, ok := exit.Sys().(syscall.WaitStatus)
					if !ok || !status.Signaled() || status.Signal() != syscall.SIGPIPE {
						t.Fatalf("SIGPIPE changed: %v", err)
					}
				} else if exit.ExitCode() != 1 || !strings.Contains(diagnostics.String(), "bad file descriptor") || !strings.Contains(diagnostics.String(), path) {
					t.Fatalf("err=%v diagnostics=%s", err, &diagnostics)
				}
				if mode == "search-append" && !strings.Contains(diagnostics.String(), "failed to write logs") {
					t.Fatalf("failure was not in live follow: %s", &diagnostics)
				}
			})
		}
	}
}

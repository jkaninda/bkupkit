/*
MIT License

Copyright (c) 2026 Jonas Kaninda

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

// Package execx runs external tools (pg_dump, mysqldump, mongorestore...) as
// stream stages, without a shell.
//
// Two rules make streaming safe:
//   - stdout is copied by us; if the consumer fails, the process is killed.
//   - stdin is fed by us; if the producer (download, decryption, checksum)
//     fails, the process is killed BEFORE stdin is closed, so a restore tool
//     never sees a clean EOF and never commits a partial or tampered input.
package execx

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Cmd describes one process.
type Cmd struct {
	Name string
	Args []string
	// Env is added to the current environment.
	Env    []string
	Dir    string
	Stdin  io.Reader // fed to the process; nil = no stdin
	Stdout io.Writer // receives stdout; nil = logged line by line
	Log    *slog.Logger
	// StderrLevel is the level stderr lines are logged at (default Info).
	StderrLevel slog.Level
	// StdoutLevel is the level stdout lines are logged at when Stdout is nil (default Debug).
	StdoutLevel slog.Level
}

// Error is returned when the process fails.
type Error struct {
	Name     string
	ExitCode int
	Stderr   string
	Err      error
}

func (e *Error) Error() string {
	msg := fmt.Sprintf("%s failed", e.Name)
	if e.ExitCode > 0 {
		msg += fmt.Sprintf(" (exit code %d)", e.ExitCode)
	}
	if e.Err != nil && e.ExitCode <= 0 {
		msg += ": " + e.Err.Error()
	}
	if s := strings.TrimSpace(e.Stderr); s != "" {
		msg += ": " + s
	}
	return msg
}

func (e *Error) Unwrap() error { return e.Err }

// tail keeps the last n bytes written.
type tail struct {
	mu  sync.Mutex
	buf []byte
	n   int
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.n {
		t.buf = append([]byte(nil), t.buf[len(t.buf)-t.n:]...)
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

func logLines(r io.Reader, log *slog.Logger, level slog.Level, name string, also io.Writer) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if also != nil {
			_, _ = also.Write([]byte(line + "\n"))
		}
		if log != nil && strings.TrimSpace(line) != "" {
			log.Log(context.Background(), level, line, "tool", name)
		}
	}
	// Drain anything left so the process never blocks on a full pipe.
	_, _ = io.Copy(io.Discard, r)
}

// Run starts the process and waits for it. The first error wins: a producer
// (stdin) or consumer (stdout) failure is returned instead of the resulting
// "signal: killed".
func Run(ctx context.Context, c Cmd) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Env = append(os.Environ(), c.Env...)
	cmd.Dir = c.Dir
	cmd.WaitDelay = 10 * time.Second
	// Own process group: killing it also kills children (a shell's pipeline),
	// which would otherwise keep our pipes open.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	kill := func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.Cancel = func() error { kill(); return nil }

	errTail := &tail{n: 8 * 1024}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stdin io.WriteCloser
	if c.Stdin != nil {
		if stdin, err = cmd.StdinPipe(); err != nil {
			return err
		}
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", c.Name, err)
	}

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
	)
	fail := func(err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		mu.Unlock()
		kill() // kill the process group before anything else happens
		cancel()
	}

	wg.Add(2)
	go func() {
		defer wg.Done()
		logLines(stderr, c.Log, levelOr(c.StderrLevel, slog.LevelInfo), c.Name, errTail)
	}()
	go func() {
		defer wg.Done()
		if c.Stdout == nil {
			logLines(stdout, c.Log, levelOr(c.StdoutLevel, slog.LevelDebug), c.Name, nil)
			return
		}
		if _, err := io.Copy(c.Stdout, stdout); err != nil {
			fail(fmt.Errorf("%s output: %w", c.Name, err))
			_, _ = io.Copy(io.Discard, stdout)
		}
	}()
	stdinDone := make(chan struct{})
	if stdin != nil {
		go func() {
			defer close(stdinDone)
			_, err := io.Copy(stdin, c.Stdin)
			if err != nil && !isPipeClosed(err) {
				// Kill and leave stdin open: the process must never see a
				// clean EOF. Wait closes our end once the process is gone.
				fail(err)
				kill()
				return
			}
			_ = stdin.Close()
		}()
	} else {
		close(stdinDone)
	}

	wg.Wait()
	waitErr := cmd.Wait()
	<-stdinDone
	mu.Lock()
	defer mu.Unlock()
	if firstErr != nil {
		return firstErr
	}
	if waitErr != nil {
		e := &Error{Name: c.Name, Stderr: errTail.String(), Err: waitErr}
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			e.ExitCode = ee.ExitCode()
		}
		if ctx.Err() != nil && e.Err != nil {
			e.Err = errors.Join(context.Cause(ctx), waitErr)
		}
		return e
	}
	return nil
}

func levelOr(l, def slog.Level) slog.Level {
	if l == 0 {
		return def
	}
	return l
}

func isPipeClosed(err error) bool {
	return errors.Is(err, syscall.EPIPE) || errors.Is(err, os.ErrClosed) || errors.Is(err, io.ErrClosedPipe)
}

// Output runs a short command and returns its trimmed stdout.
func Output(ctx context.Context, name string, args []string, env []string) (string, error) {
	var out bytes.Buffer
	err := Run(ctx, Cmd{Name: name, Args: args, Env: env, Stdout: &out})
	return strings.TrimSpace(out.String()), err
}

// PrivateDir creates a 0700 directory under base for secret files and returns
// a cleanup function that removes it.
func PrivateDir(base string) (string, func(), error) {
	if base == "" {
		base = os.TempDir()
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", nil, err
	}
	dir, err := os.MkdirTemp(base, "bkup-")
	if err != nil {
		return "", nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, err
	}
	return dir, func() { _ = os.RemoveAll(dir) }, nil
}

// WriteSecret writes content to dir/name with mode 0600 and returns the path.
func WriteSecret(dir, name, content string) (string, error) {
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		return "", err
	}
	return p, nil
}

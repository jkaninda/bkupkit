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

package execx

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type failingReader struct {
	n   int
	err error
}

func (f *failingReader) Read(p []byte) (int, error) {
	if f.n <= 0 {
		return 0, f.err
	}
	if len(p) > f.n {
		p = p[:f.n]
	}
	for i := range p {
		p[i] = 'x'
	}
	f.n -= len(p)
	return len(p), nil
}

// A restore tool "commits" when it sees EOF on stdin. When the producer fails,
// the tool must be killed before it ever sees EOF.
func TestStdinFailureNeverCommits(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "committed")
	boom := errors.New("integrity check failed")
	err := Run(context.Background(), Cmd{
		Name:  "sh",
		Args:  []string{"-c", "cat > /dev/null && touch " + marker},
		Stdin: &failingReader{n: 1 << 20, err: boom},
	})
	if !errors.Is(err, boom) {
		t.Fatalf("want producer error, got %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("process committed after a failed input")
	}
}

func TestStdoutAndExitCode(t *testing.T) {
	var out bytes.Buffer
	if err := Run(context.Background(), Cmd{Name: "sh", Args: []string{"-c", "printf hello"}, Stdout: &out}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "hello" {
		t.Fatal(out.String())
	}
	err := Run(context.Background(), Cmd{Name: "sh", Args: []string{"-c", "echo bad things >&2; exit 3"}, Stdout: io.Discard})
	var e *Error
	if !errors.As(err, &e) || e.ExitCode != 3 || !strings.Contains(e.Error(), "bad things") {
		t.Fatalf("got %v", err)
	}
}

func TestConsumerFailureKillsProcess(t *testing.T) {
	boom := errors.New("upload failed")
	err := Run(context.Background(), Cmd{Name: "sh", Args: []string{"-c", "yes"}, Stdout: errWriter{boom}})
	if !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
}

type errWriter struct{ err error }

func (e errWriter) Write([]byte) (int, error) { return 0, e.err }

func TestPrivateDir(t *testing.T) {
	dir, cleanup, err := PrivateDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := WriteSecret(dir, "pw", "secret")
	if err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0o600 {
		t.Fatal(st.Mode())
	}
	cleanup()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("not cleaned up")
	}
}

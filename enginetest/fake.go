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

// Package enginetest provides a fake engine for testing bkupkit and tools.
package enginetest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"sync"

	"github.com/jkaninda/bkupkit/config"
	"github.com/jkaninda/bkupkit/engine"
)

// Fake is an engine producing Size deterministic bytes.
type Fake struct {
	Size int64
	// FailAfter makes Dump fail after writing this many bytes (0 = never).
	FailAfter int64
	Native    bool

	mu           sync.Mutex
	DumpSHA      string // sha256 of the last dump
	RestoredSHA  string // sha256 of the last restore input
	RestoredSize int64
	RestoreInfo  engine.ArtifactInfo
}

var _ engine.Engine = (*Fake)(nil)

func (f *Fake) Tool() string                                        { return "fake-bkup" }
func (f *Fake) Kind() string                                        { return "fake" }
func (f *Fake) Settings(r *config.Registry)                         { config.RegisterDatabase(r, "1") }
func (f *Fake) Configure(context.Context, *config.Values) error     { return nil }
func (f *Fake) Ext(engine.Target) string                            { return ".sql" }
func (f *Fake) NativeCompression(engine.Target) bool                { return f.Native }
func (f *Fake) Ping(context.Context, engine.Target) error           { return nil }
func (f *Fake) ServerVersion(context.Context, engine.Target) string { return "1.0" }

func (f *Fake) Targets(_ context.Context, v *config.Values) ([]engine.Target, error) {
	name := v.String(config.DBName)
	if name == "" {
		name = "fakedb"
	}
	return []engine.Target{{Name: name, Values: v}}, nil
}

// Stream is the deterministic content source (a cheap LCG, not compressible to nothing).
type Stream struct {
	N, pos int64
	state  uint64
}

func (s *Stream) Read(p []byte) (int, error) {
	if s.pos >= s.N {
		return 0, io.EOF
	}
	if rem := s.N - s.pos; int64(len(p)) > rem {
		p = p[:rem]
	}
	for i := range p {
		s.state = s.state*6364136223846793005 + 1442695040888963407
		// Mostly text-like bytes so compression has something to do.
		p[i] = "INSERT INTO t VALUES (abcdefghij);\n"[(s.state>>59)%35]
		if s.state>>63 == 1 && i%7 == 0 {
			p[i] = byte(s.state >> 32)
		}
	}
	s.pos += int64(len(p))
	return len(p), nil
}

var errInjected = errors.New("injected dump failure")

// ErrInjected is the error returned by Dump when FailAfter is reached.
func ErrInjected() error { return errInjected }

func (f *Fake) Dump(ctx context.Context, _ engine.Target, w io.Writer) error {
	h := sha256.New()
	src := io.Reader(&Stream{N: f.Size})
	if f.FailAfter > 0 {
		src = io.LimitReader(src, f.FailAfter)
	}
	buf := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := src.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
			if _, werr := w.Write(buf[:n]); werr != nil {
				return werr
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	if f.FailAfter > 0 {
		return errInjected
	}
	f.mu.Lock()
	f.DumpSHA = hex.EncodeToString(h.Sum(nil))
	f.mu.Unlock()
	return nil
}

func (f *Fake) Restore(_ context.Context, _ engine.Target, r io.Reader, info engine.ArtifactInfo) error {
	h := sha256.New()
	n, err := io.Copy(h, r)
	if err != nil {
		return err
	}
	f.mu.Lock()
	f.RestoredSHA, f.RestoredSize, f.RestoreInfo = hex.EncodeToString(h.Sum(nil)), n, info
	f.mu.Unlock()
	return nil
}

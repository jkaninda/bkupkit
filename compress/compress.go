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

// Package compress provides streaming gzip/zstd writers and magic-byte detection.
package compress

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"runtime"
	"strings"

	"github.com/klauspost/compress/zstd"
	"github.com/klauspost/pgzip"
)

// Algo is a compression algorithm.
type Algo string

const (
	None Algo = "none"
	Gzip Algo = "gzip"
	Zstd Algo = "zstd"
)

// Parse maps a setting value to an Algo.
func Parse(s string) (Algo, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "gzip", "gz":
		return Gzip, nil
	case "zstd", "zst":
		return Zstd, nil
	case "none", "off", "false":
		return None, nil
	}
	return "", fmt.Errorf("unknown compression %q", s)
}

// Ext returns the file suffix of a.
func (a Algo) Ext() string {
	switch a {
	case Gzip:
		return ".gz"
	case Zstd:
		return ".zst"
	}
	return ""
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

// NewWriter wraps dst. Close flushes the compressor but does not close dst.
// level 0 uses the default level.
func NewWriter(dst io.Writer, a Algo, level int) (io.WriteCloser, error) {
	switch a {
	case None, "":
		return nopCloser{dst}, nil
	case Gzip:
		if level == 0 {
			level = pgzip.DefaultCompression
		}
		w, err := pgzip.NewWriterLevel(dst, level)
		if err != nil {
			return nil, err
		}
		// 1 MiB blocks, one per CPU (bounded memory: blocks x procs).
		procs := runtime.GOMAXPROCS(0)
		if procs > 8 {
			procs = 8
		}
		if err := w.SetConcurrency(1<<20, procs); err != nil {
			return nil, err
		}
		return w, nil
	case Zstd:
		opts := []zstd.EOption{zstd.WithEncoderConcurrency(min(runtime.GOMAXPROCS(0), 8))}
		if level != 0 {
			opts = append(opts, zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(level)))
		}
		return zstd.NewWriter(dst, opts...)
	}
	return nil, fmt.Errorf("unknown compression %q", a)
}

var (
	gzipMagic = []byte{0x1f, 0x8b}
	zstdMagic = []byte{0x28, 0xb5, 0x2f, 0xfd}
)

// Detect peeks at r and returns its compression and a reader replaying the peeked bytes.
func Detect(r io.Reader) (Algo, io.Reader, error) {
	br := bufio.NewReaderSize(r, 64*1024)
	head, err := br.Peek(4)
	if err != nil && err != io.EOF && err != bufio.ErrBufferFull {
		return None, br, err
	}
	switch {
	case bytes.HasPrefix(head, gzipMagic):
		return Gzip, br, nil
	case bytes.HasPrefix(head, zstdMagic):
		return Zstd, br, nil
	}
	return None, br, nil
}

type readCloser struct {
	io.Reader
	close func()
}

func (r readCloser) Close() error {
	if r.close != nil {
		r.close()
	}
	return nil
}

// NewReader returns a decompressing reader for a.
func NewReader(r io.Reader, a Algo) (io.ReadCloser, error) {
	switch a {
	case None, "":
		return readCloser{Reader: r}, nil
	case Gzip:
		// pgzip reads ahead in parallel; multistream for concatenated members.
		zr, err := pgzip.NewReaderN(r, 1<<20, 4)
		if err != nil {
			return nil, fmt.Errorf("gzip: %w", err)
		}
		return zr, nil
	case Zstd:
		zr, err := zstd.NewReader(r, zstd.WithDecoderConcurrency(2), zstd.WithDecoderLowmem(true))
		if err != nil {
			return nil, fmt.Errorf("zstd: %w", err)
		}
		return readCloser{Reader: zr, close: zr.Close}, nil
	}
	return nil, fmt.Errorf("unknown compression %q", a)
}

// AutoReader detects the compression of r and returns a decompressing reader.
func AutoReader(r io.Reader) (io.ReadCloser, Algo, error) {
	a, br, err := Detect(r)
	if err != nil {
		return nil, None, err
	}
	rc, err := NewReader(br, a)
	return rc, a, err
}

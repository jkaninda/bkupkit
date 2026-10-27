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

package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"path"
	"strings"

	"github.com/jkaninda/bkupkit/compress"
	"github.com/jkaninda/bkupkit/engine"
	"github.com/jkaninda/bkupkit/index"
	"github.com/jkaninda/bkupkit/store"
	"github.com/jkaninda/encryptor"
)

// ErrDecrypt is returned when the artifact cannot be decrypted.
var ErrDecrypt = errors.New("decryption failed")

// ErrChecksum is returned when the stored bytes do not match the recorded sha256.
var ErrChecksum = errors.New("checksum mismatch")

// RestoreOptions configures a restore or verify.
type RestoreOptions struct {
	Engine engine.Engine
	Target engine.Target
	Loc    *store.Location
	Index  *index.Index // optional: provides the expected sha256
	File   string
	Keys   encryptor.Keys
	// VerifyFirst: "true" verifies the whole artifact before restoring, "false"
	// never, "auto" for GPG-encrypted volume archives (GPG authenticates at EOF).
	VerifyFirst string
	Log         *slog.Logger
}

// verifyReader hashes what it reads and, at EOF, fails if the hash differs
// from the expected one. The error replaces io.EOF, so the consumer (and a
// restore process fed through execx) never sees a clean end of input.
type verifyReader struct {
	r        io.Reader
	h        hash.Hash
	n        int64
	expected string
	sum      string
}

func (v *verifyReader) Read(p []byte) (int, error) {
	n, err := v.r.Read(p)
	v.h.Write(p[:n])
	v.n += int64(n)
	if err == io.EOF {
		v.sum = hex.EncodeToString(v.h.Sum(nil))
		if v.expected != "" && !strings.EqualFold(v.sum, v.expected) {
			return n, fmt.Errorf("%w: stored sha256 %s, expected %s", ErrChecksum, v.sum, v.expected)
		}
	}
	return n, err
}

type opened struct {
	data    io.Reader
	closers []func()
	vr      *verifyReader
	raw     *counter
	info    engine.ArtifactInfo
}

func (o *opened) close() {
	for i := len(o.closers) - 1; i >= 0; i-- {
		o.closers[i]()
	}
}

// ValidateFile rejects file names that would escape the storage directory.
func ValidateFile(file string) error {
	if file == "" {
		return errors.New("no file given (use --file or FILE_NAME)")
	}
	for _, seg := range strings.Split(strings.ReplaceAll(file, `\`, "/"), "/") {
		if seg == ".." {
			return fmt.Errorf("file %q must not contain '..'", file)
		}
	}
	return nil
}

type countReader struct {
	r io.Reader
	c *counter
}

func (c countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.c.n += int64(n)
	return n, err
}

func open(ctx context.Context, o RestoreOptions) (*opened, error) {
	if err := ValidateFile(o.File); err != nil {
		return nil, err
	}
	key := o.Loc.Key(strings.TrimPrefix(o.File, "/"))
	expected := ""
	if o.Index != nil {
		if e, err := o.Index.Lookup(ctx, path.Base(o.File)); err == nil && e != nil {
			expected = e.SHA256
		}
	}
	rc, _, err := o.Loc.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("open %s in %s: %w", o.File, o.Loc.Display, err)
	}
	op := &opened{closers: []func(){func() { _ = rc.Close() }}}
	op.vr = &verifyReader{r: rc, h: sha256.New(), expected: expected}

	plain, format, err := encryptor.Decrypt(op.vr, o.Keys)
	if err != nil {
		op.close()
		if errors.Is(err, encryptor.ErrNoKey) {
			return nil, fmt.Errorf("%w: %s is %s-encrypted but no matching key is configured (set %s): %w", ErrDecrypt, o.File, format, keyHint(format), err)
		}
		return nil, fmt.Errorf("%w: %s: %w", ErrDecrypt, o.File, err)
	}
	op.info = engine.ArtifactInfo{File: path.Base(o.File), Encryption: format.String(), Compression: string(compress.None)}
	if format != encryptor.FormatNone {
		plain = decryptErrReader{plain}
	}
	data := plain
	if !o.Engine.NativeCompression(o.Target) {
		dc, algo, err := compress.AutoReader(plain)
		if err != nil {
			op.close()
			if errors.Is(err, ErrDecrypt) || errors.Is(err, ErrChecksum) {
				return nil, err
			}
			return nil, fmt.Errorf("decompress %s: %w", o.File, err)
		}
		op.closers = append(op.closers, func() { _ = dc.Close() })
		op.info.Compression = string(algo)
		data = dc
	}
	op.raw = &counter{}
	// The engine only sees EOF after every integrity check passed, so it can
	// never commit data whose authentication fails at the end.
	op.data = countReader{r: &sealedReader{r: data, drain: []io.Reader{plain, op.vr}}, c: op.raw}
	return op, nil
}

// decryptErrReader marks read errors of the decrypted stream (GPG MDC, age
// chunk authentication) as decryption failures, keeping checksum errors as is.
type decryptErrReader struct{ r io.Reader }

func (d decryptErrReader) Read(p []byte) (int, error) {
	n, err := d.r.Read(p)
	if err != nil && err != io.EOF && !errors.Is(err, ErrChecksum) && !errors.Is(err, ErrDecrypt) {
		err = fmt.Errorf("%w: %w", ErrDecrypt, err)
	}
	return n, err
}

// sealedReader returns io.EOF only after draining the lower layers (the rest
// of the decrypted stream, then the raw bytes through the sha256 check), so
// integrity errors replace the EOF instead of arriving after it.
type sealedReader struct {
	r     io.Reader
	drain []io.Reader
	err   error
}

func (s *sealedReader) Read(p []byte) (int, error) {
	if s.err != nil {
		return 0, s.err
	}
	n, err := s.r.Read(p)
	if err == io.EOF {
		for _, d := range s.drain {
			if _, derr := io.Copy(io.Discard, d); derr != nil {
				s.err = derr
				return n, derr
			}
		}
		s.err = io.EOF
	} else if err != nil {
		s.err = err
	}
	return n, err
}

func keyHint(f encryptor.Format) string {
	if f == encryptor.FormatAge {
		return "AGE_IDENTITY, AGE_IDENTITY_FILE or AGE_PASSPHRASE"
	}
	return "GPG_PASSPHRASE or GPG_PRIVATE_KEY"
}

// VerifyResult is the outcome of Verify.
type VerifyResult struct {
	File            string `json:"file"`
	Size            int64  `json:"size"`
	RawSize         int64  `json:"raw_size"`
	SHA256          string `json:"sha256"`
	ChecksumChecked bool   `json:"checksum_checked"`
	Encryption      string `json:"encryption"`
	Compression     string `json:"compression"`
}

// Verify streams the artifact through download, checksum, decryption and
// decompression without restoring it.
func Verify(ctx context.Context, o RestoreOptions) (*VerifyResult, error) {
	op, err := open(ctx, o)
	if err != nil {
		return nil, err
	}
	defer op.close()
	if _, err := io.Copy(io.Discard, op.data); err != nil {
		return nil, fmt.Errorf("verify %s: %w", o.File, err)
	}
	// Decompression may stop before the ciphertext's end: drain to reach the checksum.
	if _, err := io.Copy(io.Discard, op.vr); err != nil {
		return nil, fmt.Errorf("verify %s: %w", o.File, err)
	}
	return &VerifyResult{
		File: op.info.File, Size: op.vr.n, RawSize: op.raw.n, SHA256: op.vr.sum,
		ChecksumChecked: op.vr.expected != "", Encryption: op.info.Encryption, Compression: op.info.Compression,
	}, nil
}

// Restore streams the artifact into the engine.
func Restore(ctx context.Context, o RestoreOptions) error {
	log := o.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	op, err := open(ctx, o)
	if err != nil {
		return err
	}
	verify := o.VerifyFirst == "true"
	if o.VerifyFirst == "auto" {
		// GPG authenticates only at EOF; volumes have no transaction to roll back.
		verify = o.Engine.Kind() == "volume" && op.info.Encryption == encryptor.FormatGPG.String()
		if rv, ok := o.Engine.(engine.RestoreVerifier); ok && rv.VerifyBeforeRestore(o.Target, op.info) {
			verify = true
		}
	}
	if verify {
		op.close()
		log.Info("Verifying the backup before restoring", "file", o.File)
		vr, err := Verify(ctx, o)
		if err != nil {
			return err
		}
		log.Info("Backup verified", "size", vr.Size, "checksum_checked", vr.ChecksumChecked)
		if op, err = open(ctx, o); err != nil {
			return err
		}
	}
	defer op.close()
	log.Info("Restore started", "file", o.File, "database", o.Target.Name, "encryption", op.info.Encryption, "compression", op.info.Compression)
	if err := o.Engine.Restore(ctx, o.Target, op.data, op.info); err != nil {
		return err
	}
	// Read whatever the engine left so integrity checks (GPG MDC, sha256) run.
	if n, err := io.Copy(io.Discard, op.data); err != nil {
		return fmt.Errorf("restore completed but the artifact failed its integrity check: %w", err)
	} else if n > 0 {
		log.Warn("The restore tool stopped before the end of the backup", "unread_bytes", n)
	}
	if _, err := io.Copy(io.Discard, op.vr); err != nil {
		return fmt.Errorf("restore completed but the artifact failed its integrity check: %w", err)
	}
	return nil
}

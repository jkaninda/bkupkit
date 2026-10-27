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

// Package pipeline streams backups and restores end to end:
//
//	backup:  engine.Dump → compress → encrypt → sha256 → storage.Put
//	restore: storage.Get → sha256 check → decrypt → decompress → engine.Restore
//
// Nothing is buffered in memory beyond fixed-size buffers and nothing is
// written to local disk.
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
	"time"

	"github.com/jkaninda/bkupkit/compress"
	"github.com/jkaninda/bkupkit/engine"
	"github.com/jkaninda/bkupkit/index"
	"github.com/jkaninda/bkupkit/naming"
	"github.com/jkaninda/bkupkit/store"
	"github.com/jkaninda/encryptor"
	gostorage "github.com/jkaninda/go-storage"
)

// Stage errors, for exit codes and messages.
var (
	ErrDump    = errors.New("dump failed")
	ErrUpload  = errors.New("upload failed")
	ErrEncrypt = errors.New("encryption failed")
)

// BackupOptions configures one backup.
type BackupOptions struct {
	Engine      engine.Engine
	Target      engine.Target
	Loc         *store.Location
	Index       *index.Index // nil = no index
	Compression compress.Algo
	Level       int
	Encrypter   encryptor.Encrypter // nil = no encryption
	// BackupName is the artifact base name. FixedName uses it verbatim without a timestamp.
	BackupName  string
	FixedName   bool
	Infix       string
	ToolVersion string
	Log         *slog.Logger
	Now         func() time.Time
}

// BackupResult describes a stored backup.
type BackupResult struct {
	File          string        `json:"file"`
	Key           string        `json:"key"`
	Location      string        `json:"location"`
	Storage       string        `json:"storage"`
	Database      string        `json:"database"`
	BackupName    string        `json:"backup_name"`
	Size          int64         `json:"size"`
	RawSize       int64         `json:"raw_size"`
	SHA256        string        `json:"sha256"`
	ETag          string        `json:"etag,omitempty"`
	Compression   string        `json:"compression"`
	Encryption    string        `json:"encryption"`
	ServerVersion string        `json:"server_version,omitempty"`
	StartedAt     time.Time     `json:"started_at"`
	CompletedAt   time.Time     `json:"completed_at"`
	Duration      time.Duration `json:"-"`
	DurationMS    int64         `json:"duration_ms"`
	IndexError    string        `json:"index_error,omitempty"`
}

type counter struct {
	w io.Writer
	n int64
}

func (c *counter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

type hashWriter struct {
	w io.Writer
	h hash.Hash
	n int64
}

func (h *hashWriter) Write(p []byte) (int, error) {
	n, err := h.w.Write(p)
	h.h.Write(p[:n])
	h.n += int64(n)
	return n, err
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// Backup runs one backup. The artifact only becomes visible in storage when the
// dump completed successfully; on any failure no object is left behind.
func Backup(ctx context.Context, o BackupOptions) (*BackupResult, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	log := o.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	t := o.Target
	start := o.Now()

	comp := o.Compression
	if o.Engine.NativeCompression(t) {
		comp = compress.None
	}
	encFormat := encryptor.FormatNone
	if o.Encrypter != nil {
		encFormat = o.Encrypter.Format()
	}
	// Names use local time (TZ), as in 1.x.
	spec := naming.Spec{
		Base: o.BackupName, Infix: o.Infix, Time: start, Fixed: o.FixedName,
		Ext: o.Engine.Ext(t), Suffix: comp.Ext() + encFormat.Ext(),
	}
	file, err := freeName(ctx, o.Loc, spec, o.FixedName)
	if err != nil {
		return nil, err
	}
	key := o.Loc.Key(file)
	log.Info("Backup started", "database", t.Name, "storage", o.Loc.Kind, "compression", string(comp), "encryption", encFormat.String())

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	pr, pw := io.Pipe()
	stored := &hashWriter{w: pw, h: sha256.New()}
	raw := &counter{}

	// The writer chain is built inside the goroutine: encrypters write their
	// header on Wrap, which would block until the upload starts reading.
	dumpErr := make(chan error, 1)
	go func() {
		err := func() error {
			var encW io.WriteCloser = nopWriteCloser{stored}
			if o.Encrypter != nil {
				var err error
				if encW, err = o.Encrypter.Wrap(stored); err != nil {
					return fmt.Errorf("%w: %w", ErrEncrypt, err)
				}
			}
			compW, err := compress.NewWriter(encW, comp, o.Level)
			if err != nil {
				return err
			}
			raw.w = compW
			if err := o.Engine.Dump(ctx, t, raw); err != nil {
				return err
			}
			if err := compW.Close(); err != nil {
				return err
			}
			return encW.Close()
		}()
		if err != nil {
			_ = pw.CloseWithError(err)
		} else {
			_ = pw.Close()
		}
		dumpErr <- err
	}()

	item, putErr := o.Loc.Put(ctx, key, pr, &gostorage.PutOptions{ContentType: "application/octet-stream", Size: -1})
	if putErr != nil {
		cancel()
		_ = pr.CloseWithError(putErr)
	}
	dErr := <-dumpErr
	switch {
	case dErr != nil:
		if putErr == nil {
			// Defensive: never keep an object whose dump failed.
			_ = o.Loc.Remove(context.WithoutCancel(ctx), key)
		}
		if errors.Is(dErr, ErrEncrypt) {
			return nil, dErr
		}
		return nil, fmt.Errorf("%w: %s: %w", ErrDump, t.Name, dErr)
	case putErr != nil:
		return nil, fmt.Errorf("%w: %s to %s: %w", ErrUpload, file, o.Loc.Display, putErr)
	}

	end := o.Now()
	res := &BackupResult{
		File: file, Key: key, Location: o.Loc.Display, Storage: o.Loc.Kind, Database: t.Name, BackupName: o.BackupName,
		Size: stored.n, RawSize: raw.n, SHA256: hex.EncodeToString(stored.h.Sum(nil)), ETag: item.ETag,
		Compression: string(comp), Encryption: encFormat.String(),
		StartedAt: start.UTC(), CompletedAt: end.UTC(), Duration: end.Sub(start), DurationMS: end.Sub(start).Milliseconds(),
	}
	if o.Engine.NativeCompression(t) {
		res.Compression = "native"
	}
	if o.Index != nil {
		res.ServerVersion = o.Engine.ServerVersion(ctx, t)
		e := index.Entry{
			File: file, Key: key, BackupName: o.BackupName, DB: t.Name, Engine: o.Engine.Kind(),
			ServerVersion: res.ServerVersion, Tool: o.Engine.Tool(), ToolVersion: o.ToolVersion,
			Status: index.StatusSuccess, StartedAt: res.StartedAt, CompletedAt: res.CompletedAt, DurationMS: res.DurationMS,
			Size: res.Size, RawSize: res.RawSize, SHA256: res.SHA256, ETag: item.ETag, VersionID: item.VersionID,
			Storage: o.Loc.Kind, Compression: res.Compression, Encryption: res.Encryption,
		}
		if o.Encrypter != nil {
			e.KeyFingerprints = o.Encrypter.Fingerprints()
		}
		if err := o.Index.Record(ctx, e); err != nil {
			// The backup itself is safe; reconcile repairs the index later.
			res.IndexError = err.Error()
			log.Warn("Backup stored but the index could not be updated (run reconcile)", "error", err)
		}
	}
	return res, nil
}

// freeName returns an artifact name that does not exist yet (a counter is
// added when two backups land in the same second).
func freeName(ctx context.Context, loc *store.Location, spec naming.Spec, fixed bool) (string, error) {
	for n := 0; n < 100; n++ {
		name := spec.Build(n)
		if fixed && n == 0 {
			return name, nil // custom names overwrite, as in 1.x
		}
		_, err := loc.Stat(ctx, loc.Key(name))
		if errors.Is(err, gostorage.ErrNotFound) {
			return name, nil
		}
		if err != nil {
			return "", fmt.Errorf("check %s: %w", name, err)
		}
	}
	return "", errors.New("could not find a free artifact name")
}

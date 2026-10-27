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

// Package index keeps the backup history of one storage path in a JSON index
// file, with one .meta.json sidecar per backup as the source of truth.
//
// Write order (crash-safe): artifact → sidecar → index entry. Deletion order:
// artifact → sidecar → index entry. "Already gone" counts as success, so any
// interrupted run can be repeated, and Reconcile rebuilds the index from the
// sidecars when it drifts.
//
// The index is updated with optimistic concurrency (conditional writes) and
// falls back to a lock file on backends without them (SFTP, FTP).
package index

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"sort"
	"time"

	"github.com/jkaninda/bkupkit/naming"
	"github.com/jkaninda/bkupkit/store"
	gostorage "github.com/jkaninda/go-storage"
)

// Version is the index file schema version.
const Version = 1

// Entry statuses.
const (
	StatusSuccess = "success"
	StatusDeleted = "deleted"
	StatusMissing = "missing"
)

// TombstoneTTL is how long deleted/missing entries stay in the index.
var TombstoneTTL = 30 * 24 * time.Hour

// ErrCorrupt is returned when the index file cannot be parsed.
var ErrCorrupt = errors.New("index file is corrupted")

// maxIndexSize bounds the index file read into memory.
const maxIndexSize = 32 << 20

// Entry describes one backup.
type Entry struct {
	File            string     `json:"file"`
	Key             string     `json:"key"`
	BackupName      string     `json:"backup_name"`
	DB              string     `json:"db,omitempty"`
	Engine          string     `json:"engine,omitempty"`
	ServerVersion   string     `json:"server_version,omitempty"`
	Tool            string     `json:"tool,omitempty"`
	ToolVersion     string     `json:"tool_version,omitempty"`
	Status          string     `json:"status"`
	StartedAt       time.Time  `json:"started_at"`
	CompletedAt     time.Time  `json:"completed_at"`
	DurationMS      int64      `json:"duration_ms"`
	Size            int64      `json:"size"`
	RawSize         int64      `json:"raw_size,omitempty"`
	SHA256          string     `json:"sha256,omitempty"`
	ETag            string     `json:"etag,omitempty"`
	VersionID       string     `json:"version_id,omitempty"`
	Storage         string     `json:"storage,omitempty"`
	Compression     string     `json:"compression,omitempty"`
	Encryption      string     `json:"encryption,omitempty"`
	KeyFingerprints []string   `json:"key_fingerprints,omitempty"`
	Index           string     `json:"index,omitempty"`
	Adopted         bool       `json:"adopted,omitempty"`
	DeletedAt       *time.Time `json:"deleted_at,omitempty"`
	DeletedReason   string     `json:"deleted_reason,omitempty"`
}

// File is the index file content.
type File struct {
	Version   int       `json:"version"`
	Tool      string    `json:"tool"`
	Name      string    `json:"name"`
	UpdatedAt time.Time `json:"updated_at"`
	Entries   []Entry   `json:"entries"`
}

// Find returns the entry for file, or nil.
func (f *File) Find(file string) *Entry {
	for i := range f.Entries {
		if f.Entries[i].File == file {
			return &f.Entries[i]
		}
	}
	return nil
}

// Upsert adds e or replaces the entry with the same file.
func (f *File) Upsert(e Entry) {
	if cur := f.Find(e.File); cur != nil {
		*cur = e
		return
	}
	f.Entries = append(f.Entries, e)
}

// Live returns success entries, newest first, optionally for one backup name.
func (f *File) Live(backupName string) []Entry {
	var out []Entry
	for _, e := range f.Entries {
		if e.Status == StatusSuccess && (backupName == "" || e.BackupName == backupName) {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CompletedAt.After(out[j].CompletedAt) })
	return out
}

func (f *File) compact(now time.Time) {
	kept := f.Entries[:0]
	for _, e := range f.Entries {
		if e.DeletedAt != nil && now.Sub(*e.DeletedAt) > TombstoneTTL {
			continue
		}
		kept = append(kept, e)
	}
	f.Entries = kept
}

// Index is the backup index of one location.
type Index struct {
	Loc  *store.Location
	Name string
	Tool string
	Log  *slog.Logger
	// Owner identifies this process in lock files.
	Owner string
	// Now is overridable for tests.
	Now func() time.Time
}

// New returns an index handle.
func New(loc *store.Location, name, tool string, log *slog.Logger) *Index {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Index{Loc: loc, Name: name, Tool: tool, Log: log, Owner: fmt.Sprintf("%s-%d", tool, rand.Int64()), Now: time.Now}
}

func (ix *Index) key() string { return ix.Loc.Key(ix.Name) }

// SidecarKey returns the sidecar key of an artifact file.
func (ix *Index) SidecarKey(file string) string { return ix.Loc.Key(file + naming.SidecarSuffix) }

// Load reads the index. A missing index yields an empty file and exists=false.
func (ix *Index) Load(ctx context.Context) (f *File, etag string, exists bool, err error) {
	return ix.loadKey(ctx, ix.key(), ix.Name)
}

func (ix *Index) loadKey(ctx context.Context, key, name string) (*File, string, bool, error) {
	empty := &File{Version: Version, Tool: ix.Tool, Name: name}
	rc, item, err := ix.Loc.Get(ctx, key)
	if errors.Is(err, gostorage.ErrNotFound) {
		return empty, "", false, nil
	}
	if err != nil {
		return nil, "", false, fmt.Errorf("read index %s: %w", name, err)
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(io.LimitReader(rc, maxIndexSize+1))
	if err != nil {
		return nil, "", false, fmt.Errorf("read index %s: %w", name, err)
	}
	if len(b) > maxIndexSize {
		return nil, "", false, fmt.Errorf("index %s is larger than %d bytes", name, maxIndexSize)
	}
	f := &File{}
	if err := json.Unmarshal(b, f); err != nil {
		return nil, "", false, fmt.Errorf("index %s: %w (run reconcile to rebuild it): %w", name, ErrCorrupt, err)
	}
	if f.Version > Version {
		return nil, "", false, fmt.Errorf("index %s has version %d, this tool supports %d", name, f.Version, Version)
	}
	f.Name = name
	return f, item.ETag, true, nil
}

func (ix *Index) encode(f *File) ([]byte, error) {
	f.Version = Version
	f.Tool = ix.Tool
	f.Name = ix.Name
	f.UpdatedAt = ix.Now().UTC()
	f.compact(ix.Now())
	return json.MarshalIndent(f, "", "  ")
}

var putOpts = &gostorage.PutOptions{ContentType: "application/json"}

// Update applies fn to the current index and writes it back atomically,
// retrying on concurrent modification.
func (ix *Index) Update(ctx context.Context, fn func(*File) error) error {
	const attempts = 10
	for attempt := 0; attempt < attempts; attempt++ {
		f, etag, exists, err := ix.Load(ctx)
		if err != nil {
			return err
		}
		if exists && etag == "" {
			break // the backend gives no version token: lock instead
		}
		if err := fn(f); err != nil {
			return err
		}
		b, err := ix.encode(f)
		if err != nil {
			return err
		}
		cond := gostorage.Condition{IfNoneMatch: !exists, IfMatch: etag}
		_, err = ix.Loc.PutIf(ctx, ix.key(), bytes.NewReader(b), cond, putOpts)
		if err == nil {
			return nil
		}
		if errors.Is(err, gostorage.ErrConditionNotSupported) {
			break
		}
		if !errors.Is(err, gostorage.ErrPreconditionFailed) {
			return fmt.Errorf("write index %s: %w", ix.Name, err)
		}
		if attempt == attempts-1 {
			return fmt.Errorf("write index %s: too many concurrent updates: %w", ix.Name, err)
		}
		ix.Log.Debug("index changed concurrently, retrying", "index", ix.Name, "attempt", attempt+1)
		if err := sleep(ctx, backoff(attempt)); err != nil {
			return err
		}
	}
	return ix.withLock(ctx, func() error {
		f, _, _, err := ix.Load(ctx)
		if err != nil {
			return err
		}
		if err := fn(f); err != nil {
			return err
		}
		b, err := ix.encode(f)
		if err != nil {
			return err
		}
		if _, err := ix.Loc.Put(ctx, ix.key(), bytes.NewReader(b), putOpts); err != nil {
			return fmt.Errorf("write index %s: %w", ix.Name, err)
		}
		return nil
	})
}

func backoff(attempt int) time.Duration {
	d := time.Duration(50*(1<<attempt)) * time.Millisecond
	if d > 2*time.Second {
		d = 2 * time.Second
	}
	return d/2 + time.Duration(rand.Int64N(int64(d/2)+1))
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// WriteSidecar stores e next to its artifact.
func (ix *Index) WriteSidecar(ctx context.Context, e Entry) error {
	e.Index = ix.Name
	b, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	_, err = ix.Loc.Put(ctx, ix.SidecarKey(e.File), bytes.NewReader(b), putOpts)
	return err
}

// ReadSidecar returns the sidecar of file.
func (ix *Index) ReadSidecar(ctx context.Context, file string) (*Entry, error) {
	rc, _, err := ix.Loc.Get(ctx, ix.SidecarKey(file))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	e := &Entry{}
	if err := json.NewDecoder(io.LimitReader(rc, 1<<20)).Decode(e); err != nil {
		return nil, fmt.Errorf("sidecar of %s: %w", file, err)
	}
	return e, nil
}

// Record writes the sidecar, then adds the entry to the index.
func (ix *Index) Record(ctx context.Context, e Entry) error {
	e.Index = ix.Name
	if err := ix.WriteSidecar(ctx, e); err != nil {
		return fmt.Errorf("write sidecar: %w", err)
	}
	return ix.Update(ctx, func(f *File) error {
		f.Upsert(e)
		return nil
	})
}

// Lookup returns what is known about file: the index entry, else its sidecar.
func (ix *Index) Lookup(ctx context.Context, file string) (*Entry, error) {
	f, _, _, err := ix.Load(ctx)
	if err == nil {
		if e := f.Find(file); e != nil {
			return e, nil
		}
	}
	return ix.ReadSidecar(ctx, file)
}

// removeArtifact deletes an artifact and its sidecar; missing objects are fine.
// It reports whether the artifact existed.
func (ix *Index) removeArtifact(ctx context.Context, key, file string) (bool, error) {
	existed := true
	if err := ix.Loc.Remove(ctx, key); err != nil {
		if !errors.Is(err, gostorage.ErrNotFound) {
			return false, fmt.Errorf("delete %s: %w", file, err)
		}
		existed = false
	}
	if err := ix.Loc.Remove(ctx, ix.SidecarKey(file)); err != nil && !errors.Is(err, gostorage.ErrNotFound) {
		ix.Log.Warn("could not delete sidecar", "file", file, "error", err)
	}
	return existed, nil
}

// Delete removes one backup and marks it deleted in the index.
func (ix *Index) Delete(ctx context.Context, file, reason string) error {
	key := ix.Loc.Key(file)
	existed, err := ix.removeArtifact(ctx, key, file)
	if err != nil {
		return err
	}
	known := false
	err = ix.Update(ctx, func(f *File) error {
		e := f.Find(file)
		if e == nil {
			return nil
		}
		known = true
		now := ix.Now().UTC()
		e.Status, e.DeletedAt, e.DeletedReason = StatusDeleted, &now, reason
		return nil
	})
	if err != nil {
		return err
	}
	if !existed && !known {
		return fmt.Errorf("%s: %w", file, gostorage.ErrNotFound)
	}
	return nil
}

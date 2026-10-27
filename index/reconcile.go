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

package index

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/jkaninda/bkupkit/naming"
	gostorage "github.com/jkaninda/go-storage"
)

// ReconcileReport summarises a reconcile run.
type ReconcileReport struct {
	Added    []string // entries rebuilt from sidecars
	Missing  []string // entries whose artifact is gone
	Imported int      // entries moved from another index
}

// Reconcile repairs the index from the storage listing and the sidecars:
// sidecars written for this index whose artifact exists are (re)added, and
// success entries whose artifact is gone are marked missing. With fromIndex,
// the entries of that index are moved into this one first, their sidecars are
// repointed and the old index file is deleted (renaming an index).
func (ix *Index) Reconcile(ctx context.Context, fromIndex string, dryRun bool) (*ReconcileReport, error) {
	rep := &ReconcileReport{}
	imported, err := ix.importFrom(ctx, fromIndex)
	if err != nil {
		return nil, err
	}
	rep.Imported = len(imported)

	present, sidecars, err := ix.scan(ctx)
	if err != nil {
		return nil, err
	}
	cur, _, _, err := ix.Load(ctx)
	if err != nil && !errors.Is(err, ErrCorrupt) {
		return nil, err
	}
	if cur == nil {
		cur = &File{}
	}
	known := map[string]bool{}
	for _, e := range cur.Entries {
		known[e.File] = true
	}
	for _, e := range imported {
		known[e.File] = true
	}
	rebuilt := ix.fromSidecars(ctx, sidecars, present, known, fromIndex)
	for _, e := range rebuilt {
		rep.Added = append(rep.Added, e.File)
	}

	if dryRun {
		for _, list := range [][]Entry{cur.Entries, imported} {
			for _, e := range list {
				if e.Status == StatusSuccess && !present[e.File] {
					rep.Missing = append(rep.Missing, e.File)
				}
			}
		}
		return rep, nil
	}

	repair := func(f *File) error {
		for _, list := range [][]Entry{imported, rebuilt} {
			for _, e := range list {
				if f.Find(e.File) == nil {
					f.Entries = append(f.Entries, e)
				}
			}
		}
		rep.Missing = rep.Missing[:0]
		now := ix.Now().UTC()
		for i := range f.Entries {
			e := &f.Entries[i]
			if e.Status == StatusSuccess && !present[e.File] {
				e.Status, e.DeletedAt, e.DeletedReason = StatusMissing, &now, "missing at reconcile"
				rep.Missing = append(rep.Missing, e.File)
			}
		}
		return nil
	}
	if err := ix.Update(ctx, repair); err != nil {
		if !errors.Is(err, ErrCorrupt) {
			return nil, err
		}
		if err := ix.overwrite(ctx, repair); err != nil {
			return nil, err
		}
	}
	if len(imported) > 0 {
		ix.finishRename(ctx, fromIndex, imported, present)
	}
	return rep, nil
}

// importFrom returns the entries of another index in the same directory.
func (ix *Index) importFrom(ctx context.Context, fromIndex string) ([]Entry, error) {
	if fromIndex == "" || fromIndex == ix.Name {
		return nil, nil
	}
	old, _, exists, err := ix.loadKey(ctx, ix.Loc.Key(fromIndex), fromIndex)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("index %s not found in %s", fromIndex, ix.Loc.Display)
	}
	return old.Entries, nil
}

// scan lists the directory: which files exist, and which artifacts have sidecars.
func (ix *Index) scan(ctx context.Context) (map[string]bool, []string, error) {
	items, err := ix.Loc.ListDir(ctx, ix.Loc.Dir)
	if err != nil {
		return nil, nil, fmt.Errorf("list %s: %w", ix.Loc.Display, err)
	}
	present := map[string]bool{}
	var sidecars []string
	for _, it := range items {
		file := path.Base(it.Key)
		present[file] = true
		if strings.HasSuffix(file, naming.SidecarSuffix) {
			sidecars = append(sidecars, strings.TrimSuffix(file, naming.SidecarSuffix))
		}
	}
	return present, sidecars, nil
}

// fromSidecars returns entries for sidecars of this index (or fromIndex) whose
// artifact exists and that the index does not know yet.
func (ix *Index) fromSidecars(ctx context.Context, sidecars []string, present, known map[string]bool, fromIndex string) []Entry {
	var out []Entry
	for _, file := range sidecars {
		if known[file] || !present[file] {
			continue
		}
		e, err := ix.ReadSidecar(ctx, file)
		if err != nil {
			ix.Log.Warn("skipping unreadable sidecar", "file", file, "error", err)
			continue
		}
		if e.Index != ix.Name && (fromIndex == "" || e.Index != fromIndex) {
			continue
		}
		out = append(out, *e)
	}
	return out
}

// overwrite replaces a corrupted index under the lock.
func (ix *Index) overwrite(ctx context.Context, repair func(*File) error) error {
	f := &File{}
	if err := repair(f); err != nil {
		return err
	}
	b, err := ix.encode(f)
	if err != nil {
		return err
	}
	return ix.withLock(ctx, func() error {
		_, err := ix.Loc.Put(ctx, ix.key(), bytes.NewReader(b), putOpts)
		return err
	})
}

// finishRename points the moved entries' sidecars at this index and deletes the old index.
func (ix *Index) finishRename(ctx context.Context, fromIndex string, imported []Entry, present map[string]bool) {
	for _, e := range imported {
		if !present[e.File] || !present[e.File+naming.SidecarSuffix] {
			continue
		}
		if err := ix.WriteSidecar(ctx, e); err != nil {
			ix.Log.Warn("could not update sidecar", "file", e.File, "error", err)
		}
	}
	if err := ix.Loc.Remove(ctx, ix.Loc.Key(fromIndex)); err != nil && !errors.Is(err, gostorage.ErrNotFound) {
		ix.Log.Warn("could not delete the old index", "index", fromIndex, "error", err)
	}
}

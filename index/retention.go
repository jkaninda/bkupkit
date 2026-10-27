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
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/jkaninda/bkupkit/naming"
	"github.com/jkaninda/bkupkit/store"
	gostorage "github.com/jkaninda/go-storage"
)

// Policy selects which backups retention removes.
type Policy struct {
	Days  int // delete backups older than Days (0 = no age limit)
	Count int // keep only the newest Count (0 = no count limit)
	// BackupName limits retention to one backup name ("" = every entry of the index).
	BackupName  string
	DryRun      bool
	AdoptLegacy bool
}

// Active reports whether the policy deletes anything at all.
func (p Policy) Active() bool { return p.Days > 0 || p.Count > 0 }

// select returns the entries to delete. items must be sorted newest first; the
// newest is always kept.
func selectExpired[T any](items []T, when func(T) time.Time, p Policy, now time.Time) []T {
	var out []T
	cutoff := now.AddDate(0, 0, -p.Days)
	for i, it := range items {
		if i == 0 {
			continue
		}
		if (p.Count > 0 && i >= p.Count) || (p.Days > 0 && when(it).Before(cutoff)) {
			out = append(out, it)
		}
	}
	return out
}

// Prune applies p to the index and returns the entries it deleted (or would delete on a dry run).
func (ix *Index) Prune(ctx context.Context, p Policy) ([]Entry, error) {
	if !p.Active() {
		return nil, nil
	}
	if p.AdoptLegacy && p.BackupName != "" {
		if n, err := ix.Adopt(ctx, p.BackupName, p.DryRun); err != nil {
			return nil, err
		} else if n > 0 {
			ix.Log.Info("adopted pre-index backups", "count", n, "index", ix.Name)
		}
	}
	f, _, _, err := ix.Load(ctx)
	if err != nil {
		return nil, err
	}
	expired := selectExpired(f.Live(p.BackupName), func(e Entry) time.Time { return e.CompletedAt }, p, ix.Now())
	if p.DryRun || len(expired) == 0 {
		return expired, nil
	}
	type outcome struct {
		file    string
		missing bool
	}
	var done []outcome
	var deleted []Entry
	var errs []error
	for _, e := range expired {
		existed, err := ix.removeArtifact(ctx, e.Key, e.File)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		done = append(done, outcome{e.File, !existed})
		deleted = append(deleted, e)
		ix.Log.Info("Deleted old backup", "file", e.File, "completed_at", e.CompletedAt.Format(time.RFC3339))
	}
	if len(done) > 0 {
		now := ix.Now().UTC()
		err := ix.Update(ctx, func(f *File) error {
			for _, d := range done {
				if e := f.Find(d.file); e != nil {
					e.Status, e.DeletedAt, e.DeletedReason = StatusDeleted, &now, "retention"
					if d.missing {
						e.Status, e.DeletedReason = StatusMissing, "missing at retention"
					}
				}
			}
			return nil
		})
		if err != nil {
			errs = append(errs, err)
		}
	}
	return deleted, errors.Join(errs...)
}

// Adopt imports artifacts of backupName that are in the storage directory but
// not in the index (backups made before the index existed).
func (ix *Index) Adopt(ctx context.Context, backupName string, dryRun bool) (int, error) {
	items, err := ix.Loc.ListDir(ctx, ix.Loc.Dir)
	if err != nil {
		return 0, fmt.Errorf("list %s: %w", ix.Loc.Display, err)
	}
	f, _, _, err := ix.Load(ctx)
	if err != nil {
		return 0, err
	}
	m := naming.NewMatcher(backupName)
	var add []Entry
	for _, it := range items {
		file := path.Base(it.Key)
		if !m.Match(file) || f.Find(file) != nil {
			continue
		}
		t, ok := naming.TimeOf(file)
		if !ok {
			t = it.ModifiedTime
		}
		add = append(add, Entry{
			File: file, Key: ix.Loc.Key(file), BackupName: backupName, Tool: ix.Tool,
			Status: StatusSuccess, StartedAt: t, CompletedAt: t, Size: it.Size, ETag: it.ETag,
			Storage: ix.Loc.Kind, Encryption: encryptionOf(file), Adopted: true,
		})
	}
	if dryRun || len(add) == 0 {
		return len(add), nil
	}
	return len(add), ix.Update(ctx, func(f *File) error {
		for _, e := range add {
			if f.Find(e.File) == nil {
				f.Entries = append(f.Entries, e)
			}
		}
		return nil
	})
}

func encryptionOf(file string) string {
	switch {
	case strings.HasSuffix(file, ".gpg"):
		return "gpg"
	case strings.HasSuffix(file, ".age"):
		return "age"
	}
	return "none"
}

// PrunePattern is retention without an index: only files matching the naming
// pattern of p.BackupName directly inside loc.Dir are considered. atRoot
// tells whether loc.Dir is the root of a shared backend (bucket root); that is
// refused unless allowRoot is set.
func PrunePattern(ctx context.Context, loc *store.Location, p Policy, atRoot, allowRoot bool, log func(file string)) ([]gostorage.Item, error) {
	if !p.Active() {
		return nil, nil
	}
	if p.BackupName == "" {
		return nil, errors.New("retention needs a backup name")
	}
	if atRoot && !allowRoot {
		return nil, errors.New("refusing retention at the storage root without an index; set REMOTE_PATH, enable BACKUP_INDEX or set BACKUP_RETENTION_ALLOW_ROOT=true")
	}
	items, err := loc.ListDir(ctx, loc.Dir)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", loc.Display, err)
	}
	m := naming.NewMatcher(p.BackupName)
	var mine []gostorage.Item
	for _, it := range items {
		if m.Match(path.Base(it.Key)) {
			mine = append(mine, it)
		}
	}
	when := func(it gostorage.Item) time.Time {
		if t, ok := naming.TimeOf(path.Base(it.Key)); ok {
			return t
		}
		return it.ModifiedTime
	}
	sort.SliceStable(mine, func(i, j int) bool { return when(mine[i]).After(when(mine[j])) })
	expired := selectExpired(mine, when, p, time.Now())
	if p.DryRun {
		return expired, nil
	}
	var errs []error
	for _, it := range expired {
		if err := loc.Remove(ctx, it.Key); err != nil && !errors.Is(err, gostorage.ErrNotFound) {
			errs = append(errs, err)
			continue
		}
		_ = loc.Remove(ctx, it.Key+naming.SidecarSuffix)
		if log != nil {
			log(path.Base(it.Key))
		}
	}
	return expired, errors.Join(errs...)
}

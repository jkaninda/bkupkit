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

package index_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jkaninda/bkupkit/config"
	"github.com/jkaninda/bkupkit/index"
	"github.com/jkaninda/bkupkit/store"
)

func loc(t *testing.T, dir string) *store.Location {
	t.Helper()
	r := config.NewRegistry()
	config.RegisterCommon(r)
	v, err := r.Resolve(config.ResolveOptions{Env: config.MapEnv{"STORAGE": "local", "STORAGE_PATH": dir, "REMOTE_PATH": "b"}})
	if err != nil {
		t.Fatal(err)
	}
	l, err := store.Open(v)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func touch(t *testing.T, dir, file string) {
	t.Helper()
	p := filepath.Join(dir, "b", file)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func entry(file, name string, at time.Time) index.Entry {
	return index.Entry{File: file, Key: "b/" + file, BackupName: name, Status: index.StatusSuccess, CompletedAt: at}
}

func TestConcurrentRecordLosesNothing(t *testing.T) {
	dir := t.TempDir()
	l := loc(t, dir)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ix := index.New(l, "x-metadata.json", "t", nil)
			f := fmt.Sprintf("db_20260101_0000%02d.sql.gz", i)
			touch(t, dir, f)
			if err := ix.Record(context.Background(), entry(f, "db", time.Now())); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	f, _, _, err := index.New(l, "x-metadata.json", "t", nil).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Entries) != 12 {
		t.Fatalf("got %d entries, want 12", len(f.Entries))
	}
}

func TestRetentionOnlyTouchesOwnEntries(t *testing.T) {
	dir := t.TempDir()
	l := loc(t, dir)
	ctx := context.Background()
	shop := index.New(l, "shop-metadata.json", "t", nil)
	blog := index.New(l, "blog-metadata.json", "t", nil)
	now := time.Now()
	for i := 0; i < 5; i++ {
		f := fmt.Sprintf("shop_2026010%d_000000.sql.gz", i+1)
		touch(t, dir, f)
		_ = shop.Record(ctx, entry(f, "shop", now.AddDate(0, 0, -10+i)))
		g := fmt.Sprintf("blog_2026010%d_000000.sql.gz", i+1)
		touch(t, dir, g)
		_ = blog.Record(ctx, entry(g, "blog", now.AddDate(0, 0, -10+i)))
	}
	touch(t, dir, "unrelated.txt")
	touch(t, dir, "shop_old_manual.sql.gz")

	deleted, err := shop.Prune(ctx, index.Policy{Count: 2, BackupName: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 3 {
		t.Fatalf("deleted %d, want 3", len(deleted))
	}
	for _, keep := range []string{"unrelated.txt", "shop_old_manual.sql.gz", "blog_20260101_000000.sql.gz", "shop_20260105_000000.sql.gz", "shop_20260104_000000.sql.gz"} {
		if _, err := os.Stat(filepath.Join(dir, "b", keep)); err != nil {
			t.Errorf("%s was deleted", keep)
		}
	}
	// Days policy never deletes the newest backup, even when everything is old.
	deleted, _ = shop.Prune(ctx, index.Policy{Days: 1, BackupName: "shop"})
	if len(deleted) != 1 {
		t.Fatalf("days: deleted %d, want 1 (newest kept)", len(deleted))
	}
}

func TestReconcileRebuildsAndRenames(t *testing.T) {
	dir := t.TempDir()
	l := loc(t, dir)
	ctx := context.Background()
	old := index.New(l, "old-metadata.json", "t", nil)
	for i := 0; i < 3; i++ {
		f := fmt.Sprintf("db_2026010%d_000000.sql.gz", i+1)
		touch(t, dir, f)
		if err := old.Record(ctx, entry(f, "db", time.Now())); err != nil {
			t.Fatal(err)
		}
	}
	// Rename: move entries into the new index, old file goes away.
	nw := index.New(l, "new-metadata.json", "t", nil)
	rep, err := nw.Reconcile(ctx, "old-metadata.json", false)
	if err != nil || rep.Imported != 3 {
		t.Fatalf("%+v %v", rep, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "b", "old-metadata.json")); !os.IsNotExist(err) {
		t.Fatal("old index not removed")
	}
	// Corrupt the index and delete one artifact: reconcile rebuilds from sidecars.
	_ = os.WriteFile(filepath.Join(dir, "b", "new-metadata.json"), []byte("{broken"), 0o644)
	_ = os.Remove(filepath.Join(dir, "b", "db_20260101_000000.sql.gz"))
	if _, err := nw.Reconcile(ctx, "", false); err != nil {
		t.Fatal(err)
	}
	f, _, _, err := nw.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(f.Live("")); got != 2 {
		t.Fatalf("rebuilt %d live entries, want 2", got)
	}
	// A foreign index's sidecars are never adopted.
	other := index.New(l, "other-metadata.json", "t", nil)
	if _, err := other.Reconcile(ctx, "", false); err != nil {
		t.Fatal(err)
	}
	if f, _, _, _ := other.Load(ctx); len(f.Live("")) != 0 {
		t.Fatal("foreign sidecars adopted")
	}
}

func TestAdoptLegacy(t *testing.T) {
	dir := t.TempDir()
	l := loc(t, dir)
	ctx := context.Background()
	for _, f := range []string{"shop_20250101_000000.sql.gz", "shop_20250102_000000.sql.gz.gpg", "shopx_20250101_000000.sql.gz", "notes.txt"} {
		touch(t, dir, f)
	}
	ix := index.New(l, "shop-metadata.json", "t", nil)
	n, err := ix.Adopt(ctx, "shop", false)
	if err != nil || n != 2 {
		t.Fatalf("adopted %d, %v", n, err)
	}
}

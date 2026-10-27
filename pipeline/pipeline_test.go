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

package pipeline_test

import (
	"context"

	"errors"
	"filippo.io/age"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jkaninda/bkupkit/compress"
	"github.com/jkaninda/bkupkit/config"
	"github.com/jkaninda/bkupkit/engine"
	"github.com/jkaninda/bkupkit/enginetest"
	"github.com/jkaninda/bkupkit/index"
	"github.com/jkaninda/bkupkit/pipeline"
	"github.com/jkaninda/bkupkit/store"
	"github.com/jkaninda/encryptor"
)

func localLoc(t *testing.T, dir, remote string) *store.Location {
	t.Helper()
	r := config.NewRegistry()
	config.RegisterCommon(r)
	v, err := r.Resolve(config.ResolveOptions{Env: config.MapEnv{"STORAGE": "local", "STORAGE_PATH": dir, "REMOTE_PATH": remote}})
	if err != nil {
		t.Fatal(err)
	}
	loc, err := store.Open(v)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = loc.Close() })
	return loc
}

func TestRoundTrips(t *testing.T) {
	gpgPass, _ := encryptor.GPGPassphrase([]byte("pw"))
	agePass, _ := encryptor.AgePassphrase([]byte("pw"))
	cases := []struct {
		name string
		comp compress.Algo
		enc  encryptor.Encrypter
		keys encryptor.Keys
		ext  string
	}{
		{"plain", compress.None, nil, encryptor.Keys{}, ".sql"},
		{"gzip", compress.Gzip, nil, encryptor.Keys{}, ".sql.gz"},
		{"zstd-age", compress.Zstd, agePass, encryptor.Keys{AgePassphrases: [][]byte{[]byte("pw")}}, ".sql.zst.age"},
		{"gzip-gpg", compress.Gzip, gpgPass, encryptor.Keys{GPGPassphrases: [][]byte{[]byte("pw")}}, ".sql.gz.gpg"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			loc := localLoc(t, dir, "backups/shop")
			ix := index.New(loc, "fake-metadata.json", "fake-bkup", nil)
			fe := &enginetest.Fake{Size: 3<<20 + 17}
			tgt := engine.Target{Name: "shop"}
			res, err := pipeline.Backup(context.Background(), pipeline.BackupOptions{
				Engine: fe, Target: tgt, Loc: loc, Index: ix, Compression: c.comp, Encrypter: c.enc, BackupName: "shop",
			})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(res.File, c.ext) || res.RawSize != fe.Size {
				t.Fatalf("file %s raw %d", res.File, res.RawSize)
			}
			if _, err := os.Stat(filepath.Join(dir, "backups/shop", res.File+".meta.json")); err != nil {
				t.Fatal("sidecar missing:", err)
			}
			err = pipeline.Restore(context.Background(), pipeline.RestoreOptions{Engine: fe, Target: tgt, Loc: loc, Index: ix, File: res.File, Keys: c.keys, VerifyFirst: "auto"})
			if err != nil {
				t.Fatal(err)
			}
			if fe.RestoredSHA != fe.DumpSHA || fe.RestoredSize != fe.Size {
				t.Fatalf("restored %d bytes sha %s, want %s", fe.RestoredSize, fe.RestoredSHA, fe.DumpSHA)
			}
			vr, err := pipeline.Verify(context.Background(), pipeline.RestoreOptions{Engine: fe, Target: tgt, Loc: loc, Index: ix, File: res.File, Keys: c.keys})
			if err != nil || !vr.ChecksumChecked || vr.SHA256 != res.SHA256 {
				t.Fatalf("verify: %+v %v", vr, err)
			}
		})
	}
}

func TestFailedDumpLeavesNothing(t *testing.T) {
	dir := t.TempDir()
	loc := localLoc(t, dir, "b")
	ix := index.New(loc, "fake-metadata.json", "fake-bkup", nil)
	fe := &enginetest.Fake{Size: 10 << 20, FailAfter: 5 << 20}
	_, err := pipeline.Backup(context.Background(), pipeline.BackupOptions{Engine: fe, Target: engine.Target{Name: "x"}, Loc: loc, Index: ix, Compression: compress.Gzip, BackupName: "x"})
	if !errors.Is(err, pipeline.ErrDump) {
		t.Fatalf("want ErrDump, got %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "b"))
	for _, e := range entries {
		t.Errorf("left behind: %s", e.Name())
	}
}

func TestChecksumMismatchFailsRestore(t *testing.T) {
	dir := t.TempDir()
	loc := localLoc(t, dir, "")
	ix := index.New(loc, "fake-metadata.json", "fake-bkup", nil)
	fe := &enginetest.Fake{Size: 1 << 20}
	res, err := pipeline.Backup(context.Background(), pipeline.BackupOptions{Engine: fe, Target: engine.Target{Name: "x"}, Loc: loc, Index: ix, Compression: compress.None, BackupName: "x"})
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, res.File)
	b, _ := os.ReadFile(p)
	b[len(b)/2] ^= 0xff
	_ = os.WriteFile(p, b, 0o644)
	err = pipeline.Restore(context.Background(), pipeline.RestoreOptions{Engine: fe, Target: engine.Target{Name: "x"}, Loc: loc, Index: ix, File: res.File})
	if !errors.Is(err, pipeline.ErrChecksum) {
		t.Fatalf("want checksum error, got %v", err)
	}
}

// TestStreamingMemory backs up 256 MiB and checks the heap never holds a
// meaningful fraction of it and that no temp files are written.
func TestStreamingMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("large stream")
	}
	dir := t.TempDir()
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	loc := localLoc(t, dir, "")
	// A recipient key, not a passphrase: age's scrypt KDF alone allocates
	// 256 MiB once, which would hide what this test measures.
	id, _ := age.GenerateX25519Identity()
	enc, err := encryptor.AgeRecipients([]string{id.Recipient().String()})
	if err != nil {
		t.Fatal(err)
	}
	fe := &enginetest.Fake{Size: 256 << 20}

	var peak atomic.Uint64
	stop := make(chan struct{})
	go func() {
		var ms runtime.MemStats
		for {
			select {
			case <-stop:
				return
			case <-time.After(20 * time.Millisecond):
				// Live heap after a collection: retained data, not garbage.
				runtime.GC()
				runtime.ReadMemStats(&ms)
				if ms.HeapAlloc > peak.Load() {
					peak.Store(ms.HeapAlloc)
				}
			}
		}
	}()
	_, err = pipeline.Backup(context.Background(), pipeline.BackupOptions{Engine: fe, Target: engine.Target{Name: "big"}, Loc: loc, Compression: compress.Zstd, Encrypter: enc, BackupName: "big"})
	close(stop)
	if err != nil {
		t.Fatal(err)
	}
	if p := peak.Load(); p > 64<<20 {
		t.Fatalf("peak heap %d MiB for a 256 MiB stream: not streaming", p>>20)
	}
	if ents, _ := os.ReadDir(tmp); len(ents) > 0 {
		t.Fatalf("temp files written: %v", ents)
	}
	t.Logf("peak heap %d MiB", peak.Load()>>20)
}

// Integrity failures must reach the engine as a read error, never after a
// clean EOF (an engine commits at EOF). The fake engine only records a
// restore when it reads to EOF without error.
func TestIntegrityErrorBeforeEOF(t *testing.T) {
	gpg, _ := encryptor.GPGPassphrase([]byte("pw"))
	for _, c := range []struct {
		name string
		enc  encryptor.Encrypter
		comp compress.Algo
		want error
	}{
		{"gpg-tail", gpg, compress.Gzip, pipeline.ErrDecrypt},
		{"checksum", nil, compress.None, pipeline.ErrChecksum},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			loc := localLoc(t, dir, "")
			ix := index.New(loc, "fake-metadata.json", "fake-bkup", nil)
			fe := &enginetest.Fake{Size: 2 << 20}
			tgt := engine.Target{Name: "x"}
			res, err := pipeline.Backup(context.Background(), pipeline.BackupOptions{Engine: fe, Target: tgt, Loc: loc, Index: ix, Compression: c.comp, Encrypter: c.enc, BackupName: "x"})
			if err != nil {
				t.Fatal(err)
			}
			p := filepath.Join(dir, res.File)
			b, _ := os.ReadFile(p)
			b[len(b)-10] ^= 0x01 // in the MDC / last bytes
			_ = os.WriteFile(p, b, 0o644)
			err = pipeline.Restore(context.Background(), pipeline.RestoreOptions{
				Engine: fe, Target: tgt, Loc: loc, Index: ix, File: res.File,
				Keys: encryptor.Keys{GPGPassphrases: [][]byte{[]byte("pw")}}, VerifyFirst: "false",
			})
			if !errors.Is(err, c.want) && !errors.Is(err, pipeline.ErrChecksum) {
				t.Fatalf("want %v, got %v", c.want, err)
			}
			if fe.RestoredSHA != "" {
				t.Fatal("the engine saw a clean EOF before the integrity error")
			}
		})
	}
}

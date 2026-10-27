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

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jkaninda/bkupkit/enginetest"
)

func run(t *testing.T, fe *enginetest.Fake, args ...string) (string, int) {
	t.Helper()
	var out bytes.Buffer
	a := &App{Engine: fe, Version: "test", Out: &out}
	code := a.Run(args)
	return out.String(), code
}

// Miabi (internal/services/backup/backup.go) parses the artifact name from the
// output with these regexes and takes the LAST match.
var miabiDB = regexp.MustCompile(`[\w.\-]+\.(?:sql|archive)\.gz(?:\.gpg)?`)

func TestMiabiContract(t *testing.T) {
	store := t.TempDir()
	t.Setenv("STORAGE_PATH", store)
	t.Setenv("DB_NAME", "shop")
	t.Setenv("GPG_PASSPHRASE", "pw")
	t.Setenv("HEALTH_FILE", filepath.Join(t.TempDir(), "h.json"))
	t.Setenv("TEMPLATES_DIR", "")
	fe := &enginetest.Fake{Size: 1 << 20}

	// Three runs with retention so old file names are printed before the new one.
	t.Setenv("BACKUP_RETENTION_COUNT", "1")
	var out string
	for i := 0; i < 3; i++ {
		var code int
		out, code = run(t, fe, "backup", "-d", "shop")
		if code != 0 {
			t.Fatalf("exit %d\n%s", code, out)
		}
	}
	m := miabiDB.FindAllString(out, -1)
	if len(m) == 0 {
		t.Fatalf("no artifact name in output:\n%s", out)
	}
	last := m[len(m)-1]
	if !strings.HasSuffix(last, ".sql.gz.gpg") {
		t.Fatalf("last match %q", last)
	}
	if _, err := os.Stat(filepath.Join(store, last)); err != nil {
		t.Fatalf("last match %q is not the stored artifact: %v\n%s", last, err, out)
	}
	ents, _ := os.ReadDir(store)
	var arts []string
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".gpg") {
			arts = append(arts, e.Name())
		}
	}
	if len(arts) != 1 {
		t.Fatalf("retention kept %v", arts)
	}

	// Restore with Miabi's argv: restore -d <db> -f <file>.
	out, code := run(t, fe, "restore", "-d", "shop", "-f", last)
	if code != 0 {
		t.Fatalf("restore exit %d\n%s", code, out)
	}
	if fe.RestoredSHA != fe.DumpSHA {
		t.Fatal("restored data differs")
	}

	// delete updates the index and removes the sidecar.
	if out, code := run(t, fe, "delete", "-d", "shop", "-f", last); code != 0 {
		t.Fatalf("delete exit %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(store, last+".meta.json")); !os.IsNotExist(err) {
		t.Fatal("sidecar left behind")
	}
}

func TestExitCodesAndResultFile(t *testing.T) {
	t.Setenv("STORAGE_PATH", t.TempDir())
	t.Setenv("TEMPLATES_DIR", "")
	if _, code := run(t, &enginetest.Fake{Size: 10}, "backup", "--storage", "dropbox"); code != ExitConfig {
		t.Fatalf("bad storage: exit %d", code)
	}
	if _, code := run(t, &enginetest.Fake{Size: 10}, "restore", "-f", "missing.sql.gz"); code != ExitStorage {
		t.Fatalf("missing file: exit %d", code)
	}
	t.Setenv("ENCRYPTION", "age")
	if _, code := run(t, &enginetest.Fake{Size: 10}, "backup"); code != ExitCrypto {
		t.Fatalf("encryption without keys must fail closed: exit %d", code)
	}
	t.Setenv("ENCRYPTION", "")
	rf := filepath.Join(t.TempDir(), "r.json")
	if out, code := run(t, &enginetest.Fake{Size: 10, FailAfter: 5}, "backup", "--result-file", rf); code != ExitFailure {
		t.Fatalf("dump failure: exit %d\n%s", code, out)
	}
	var r RunResult
	b, _ := os.ReadFile(rf)
	if err := json.Unmarshal(b, &r); err != nil || r.Success || len(r.Errors) != 1 {
		t.Fatalf("result file: %s", b)
	}
}

func TestIndexNameFlag(t *testing.T) {
	store := t.TempDir()
	t.Setenv("STORAGE_PATH", store)
	t.Setenv("TEMPLATES_DIR", "")
	fe := &enginetest.Fake{Size: 100}
	if out, code := run(t, fe, "backup", "-d", "shop", "--index-name", "{engine}-prod-metadata.json"); code != 0 {
		t.Fatal(out)
	}
	if _, err := os.Stat(filepath.Join(store, "fake-prod-metadata.json")); err != nil {
		t.Fatal(err)
	}
	if _, code := run(t, fe, "backup", "-d", "shop", "--index-name", "info.json"); code != ExitConfig {
		t.Fatalf("reserved name accepted: %d", code)
	}
}

func TestAbsoluteLocalFileAndQuotedBools(t *testing.T) {
	store := t.TempDir()
	t.Setenv("STORAGE_PATH", store)
	t.Setenv("TEMPLATES_DIR", "")
	t.Setenv("AWS_DISABLE_SSL", `"false"`) // compose list syntax
	t.Setenv("STORAGE", "Local")
	fe := &enginetest.Fake{Size: 1000}
	out, code := run(t, fe, "backup", "-d", "shop")
	if code != 0 {
		t.Fatal(out)
	}
	ents, _ := os.ReadDir(store)
	var file string
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".sql.gz") {
			file = e.Name()
		}
	}
	if out, code := run(t, fe, "restore", "-d", "shop", "-f", filepath.Join(store, file)); code != 0 {
		t.Fatalf("absolute path restore: %d\n%s", code, out)
	}
	if _, code := run(t, fe, "restore", "-d", "shop", "-f", "/etc/passwd"); code != ExitConfig {
		t.Fatalf("path outside STORAGE_PATH: exit %d", code)
	}
}

func TestCompletedLineCarriesBytesAndSHA(t *testing.T) {
	t.Setenv("STORAGE_PATH", t.TempDir())
	t.Setenv("TEMPLATES_DIR", "")
	out, code := run(t, &enginetest.Fake{Size: 4096}, "backup", "-d", "shop")
	if code != 0 {
		t.Fatal(out)
	}
	if !regexp.MustCompile(`msg="Backup completed".* bytes=\d+ sha256=[0-9a-f]{64}`).MatchString(out) {
		t.Fatalf("completed line lacks bytes/sha256:\n%s", out)
	}
}

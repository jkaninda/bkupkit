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

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

func testRegistry() *Registry {
	r := NewRegistry()
	RegisterCommon(r)
	RegisterDatabase(r, "5432")
	return r
}

func TestPrecedenceAndAliases(t *testing.T) {
	r := testRegistry()
	env := MapEnv{
		"S3_PATH":           "/old",
		"AWS_ACCESS_KEY":    "new-key",
		"ACCESS_KEY":        "old-key",
		"BUCKET_NAME":       "legacy-bucket",
		"FTP_HOST_NAME":     "ftp.example",
		"SSH_HOST":          "sftp.example",
		"SSH_IDENTIFY_FILE": "/keys/id",
		"DB_USERNAME":       "compat-user",
		"AWS_REGION":        "",
	}
	v, err := r.Resolve(ResolveOptions{Env: env})
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		RemotePath:  "/old",
		S3AccessKey: "new-key", // canonical beats alias
		S3Bucket:    "legacy-bucket",
		FTPHost:     "ftp.example", // FTP_HOST_NAME fallback (1.x validated the old name)
		DBUser:      "compat-user", // DB_USERNAME is still read
		S3Region:    "us-east-1",   // default; empty env is ignored
		Storage:     "local",
	}
	for k, want := range cases {
		if got := v.String(k); got != want {
			t.Errorf("%s = %q, want %q (source %s)", k, got, want, v.Source(k))
		}
	}
	if len(v.Warnings) == 0 {
		t.Error("expected deprecation warnings for old names")
	}
}

func TestStorageEnvHonouredWhenFlagNotSet(t *testing.T) {
	// 1.x bug: the --storage flag default ("local") always won over STORAGE.
	r := testRegistry()
	root := &cobra.Command{Use: "x"}
	root.AddCommand(&cobra.Command{Use: "backup", RunE: func(*cobra.Command, []string) error { return nil }})
	r.BindFlags(root)
	_ = root.PersistentFlags().Parse(nil)
	v, err := r.Resolve(ResolveOptions{Env: MapEnv{"STORAGE": "S3"}, Flags: root.PersistentFlags()})
	if err != nil {
		t.Fatal(err)
	}
	if v.String(Storage) != "s3" {
		t.Fatalf("STORAGE = %q", v.String(Storage))
	}
	_ = root.PersistentFlags().Parse([]string{"--storage", "azure"})
	v, _ = r.Resolve(ResolveOptions{Env: MapEnv{"STORAGE": "s3"}, Flags: root.PersistentFlags()})
	if v.String(Storage) != "azure" {
		t.Fatalf("flag should win, got %q", v.String(Storage))
	}
}

func TestInvalidEnumAndSecretFile(t *testing.T) {
	r := testRegistry()
	if _, err := r.Resolve(ResolveOptions{Env: MapEnv{"STORAGE": "dropbox"}}); err == nil {
		t.Fatal("unknown storage must be an error, not a silent fallback to local")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "pw")
	if err := os.WriteFile(p, []byte("s3cr$t\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	v, err := r.Resolve(ResolveOptions{Env: MapEnv{"DB_PASSWORD_FILE": p}})
	if err != nil {
		t.Fatal(err)
	}
	if v.String(DBPassword) != "s3cr$t" {
		t.Fatalf("got %q", v.String(DBPassword))
	}
	for _, l := range v.Dump(false) {
		if l.Key == DBPassword && l.Value != "********" {
			t.Fatal("secret not redacted")
		}
	}
}

func TestForJob(t *testing.T) {
	r := testRegistry()
	v, err := r.Resolve(ResolveOptions{Env: MapEnv{"DB_HOST": "base", "DB_HOST_SHOP": "shop-host", "REMOTE_PATH": "/a"}})
	if err != nil {
		t.Fatal(err)
	}
	j, err := v.ForJob("shop", map[string]string{DBName: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	if j.String(DBHost) != "shop-host" || j.String(DBName) != "shop" || j.String(RemotePath) != "/a" {
		t.Fatalf("job values: host=%q name=%q path=%q", j.String(DBHost), j.String(DBName), j.String(RemotePath))
	}
	j2, _ := v.ForJob("blog", map[string]string{DBName: "blog", RemotePath: "/b"})
	if j2.String(DBHost) != "base" || j2.String(RemotePath) != "/b" {
		t.Fatal("job overrides leaked between jobs")
	}
}

func TestParseHelpers(t *testing.T) {
	if n, _ := ParseSize("64MiB"); n != 64<<20 {
		t.Fatal(n)
	}
	if d, _ := ParseDuration("2d"); d.Hours() != 48 {
		t.Fatal(d)
	}
	if b, _ := ParseBool("ON"); !b {
		t.Fatal("ON")
	}
	got := ExpandEnv("a=${A} b=${B:-dflt} c=$C", func(k string) (string, bool) {
		if k == "A" {
			return "1", true
		}
		return "", false
	})
	if got != "a=1 b=dflt c=$C" {
		t.Fatal(got)
	}
}

func TestLegacyYAML(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	_ = os.WriteFile(p, []byte(`cronExpression: "@daily"
backupRescueMode: false
databases:
  - host: db1
    port: 5433
    name: shop
    user: u
    password: ${PW}
    path: /s3/shop
  - name: blog
`), 0o600)
	f, err := LoadFile(p, MapEnv{"PW": "p@$$"})
	if err != nil {
		t.Fatal(err)
	}
	jobs := f.AllJobs()
	if len(jobs) != 2 || jobs[0].Overrides()[DBPassword] != "p@$$" || jobs[0].Overrides()[RemotePath] != "/s3/shop" {
		t.Fatalf("%+v", jobs)
	}
	if f.Env[CronExpression] != "@daily" {
		t.Fatal("cronExpression not mapped")
	}
}

func TestSSHNamesAreNotRead(t *testing.T) {
	v, err := testRegistry().Resolve(ResolveOptions{Env: MapEnv{"SSH_HOST": "old", "SSH_HOST_NAME": "old", "SSH_REMOTE_PATH": "/old"}})
	if err != nil {
		t.Fatal(err)
	}
	if v.Has(SFTPHost) || v.Has(RemotePath) {
		t.Fatalf("SSH_* names must not be read: host=%q path=%q", v.String(SFTPHost), v.String(RemotePath))
	}
}

func TestDBUserNames(t *testing.T) {
	r := testRegistry()
	v, _ := r.Resolve(ResolveOptions{Env: MapEnv{"DB_USER": "new", "DB_USERNAME": "old", "TARGET_DB_USERNAME": "tu"}})
	if v.String(DBUser) != "new" || v.String(TargetDBUser) != "tu" {
		t.Fatalf("DB_USER=%q TARGET_DB_USER=%q", v.String(DBUser), v.String(TargetDBUser))
	}
	// DB_USERNAME (what Miabi sets) is accepted without a deprecation warning.
	v, _ = r.Resolve(ResolveOptions{Env: MapEnv{"DB_USERNAME": "old"}})
	if v.String(DBUser) != "old" || len(v.Warnings) != 0 {
		t.Fatalf("DB_USERNAME: %q warnings=%v", v.String(DBUser), v.Warnings)
	}
}

func TestForJobOlderNames(t *testing.T) {
	r := testRegistry()
	v, _ := r.Resolve(ResolveOptions{Env: MapEnv{"DB_USERNAME_SHOP": "shop-user", "BLOG_DB_USERNAME": "blog-user", "DB_USER_WIKI": "wiki-user"}})
	for job, want := range map[string]string{"shop": "shop-user", "blog": "blog-user", "wiki": "wiki-user"} {
		j, err := v.ForJob(job, nil)
		if err != nil || j.String(DBUser) != want {
			t.Errorf("%s: DB_USER=%q err=%v, want %q", job, j.String(DBUser), err, want)
		}
	}
}

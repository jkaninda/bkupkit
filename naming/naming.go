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

// Package naming builds and recognises artifact file names:
//
//	<name>[_<infix>]_<YYYYMMDD_HHMMSS>[_<n>]<ext>[.gz|.zst][.gpg|.age]
//
// Names use only [A-Za-z0-9_.-] so that consumers can match them with simple
// regular expressions (Miabi relies on this).
package naming

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// TimeLayout is the timestamp layout in artifact names.
const TimeLayout = "20060102_150405"

// Sanitize replaces characters outside [A-Za-z0-9_.-] with '-'.
func Sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), ".")
}

// Spec describes an artifact name.
type Spec struct {
	Base  string    // backup or database name
	Infix string    // optional ("schema", "tables_3")
	Time  time.Time // timestamp
	Ext   string    // ".sql", ".tar", ".archive.gz", ...
	// Suffix is appended after Ext: compression then encryption (".gz.gpg").
	Suffix string
	// Fixed, when true, uses Base as the full name without timestamp (custom names).
	Fixed bool
}

// Build returns the file name; n > 0 adds a collision counter.
func (s Spec) Build(n int) string {
	if s.Fixed {
		name := Sanitize(s.Base)
		if n > 0 {
			name = fmt.Sprintf("%s_%d", name, n)
		}
		return name + s.Ext + s.Suffix
	}
	parts := []string{Sanitize(s.Base)}
	if s.Infix != "" {
		parts = append(parts, Sanitize(s.Infix))
	}
	parts = append(parts, s.Time.Format(TimeLayout))
	if n > 0 {
		parts = append(parts, fmt.Sprint(n))
	}
	return strings.Join(parts, "_") + s.Ext + s.Suffix
}

// KnownExts are the artifact extensions (before compression/encryption) the tools produce.
var KnownExts = []string{".sql", ".archive", ".tar", ".dump"}

// Matcher recognises artifacts belonging to one backup name.
type Matcher struct{ re *regexp.Regexp }

// NewMatcher matches "<base>_[<infix>_]<timestamp>[_n]<ext...>" for base.
func NewMatcher(base string) Matcher {
	q := regexp.QuoteMeta(Sanitize(base))
	// The infix is limited to the known ones so that base "my" never matches
	// "my_db_<timestamp>".
	re := regexp.MustCompile(`^` + q + `_(?:schema_|data_|tables_\d+_)?\d{8}_\d{6}(?:_\d+)?\.(?:sql|archive|tar|dump)(?:\.gz|\.zst)?(?:\.gpg|\.age)?$`)
	return Matcher{re}
}

// Match reports whether file (a base name) is an artifact of this backup name.
func (m Matcher) Match(file string) bool { return m.re.MatchString(file) }

// IsArtifact reports whether file looks like any tool artifact (not a sidecar,
// index, lock or partial upload).
func IsArtifact(file string) bool {
	if strings.HasSuffix(file, ".json") || strings.HasSuffix(file, ".lock") || strings.HasSuffix(file, ".partial") {
		return false
	}
	f := strings.TrimSuffix(strings.TrimSuffix(file, ".gpg"), ".age")
	f = strings.TrimSuffix(strings.TrimSuffix(f, ".gz"), ".zst")
	for _, e := range KnownExts {
		if strings.HasSuffix(f, e) {
			return true
		}
	}
	return false
}

// TimeOf extracts the timestamp from an artifact name, if present.
func TimeOf(file string) (time.Time, bool) {
	m := tsRe.FindStringSubmatch(file)
	if m == nil {
		return time.Time{}, false
	}
	t, err := time.Parse(TimeLayout, m[1])
	return t, err == nil
}

var tsRe = regexp.MustCompile(`_(\d{8}_\d{6})(?:_\d+)?\.`)

// SidecarSuffix is appended to an artifact key to get its metadata sidecar.
const SidecarSuffix = ".meta.json"

// reservedIndexNames may never be used as index file names.
var reservedIndexNames = map[string]bool{"info.json": true}

var indexNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+\.json$`)

// ExpandIndexName replaces {tool} {engine} {name} {db} and validates the result.
func ExpandIndexName(tmpl string, vars map[string]string) (string, error) {
	name := tmpl
	for k, v := range vars {
		name = strings.ReplaceAll(name, "{"+k+"}", v)
	}
	return name, ValidateIndexName(name)
}

// ValidateIndexName rejects names that could collide with artifacts, sidecars,
// locks, partial uploads or Miabi's info.json.
func ValidateIndexName(name string) error {
	switch {
	case strings.ContainsAny(name, "{}"):
		return fmt.Errorf("index name %q: unknown placeholder (use {tool} {engine} {name} {db})", name)
	case strings.Contains(name, "/") || strings.Contains(name, `\`) || strings.Contains(name, ".."):
		return fmt.Errorf("index name %q: must be a file name, not a path", name)
	case !indexNameRe.MatchString(name):
		return fmt.Errorf("index name %q: use only letters, digits, '_', '-', '.' and end in .json", name)
	case reservedIndexNames[strings.ToLower(name)]:
		return fmt.Errorf("index name %q is reserved", name)
	case strings.HasSuffix(name, SidecarSuffix):
		return fmt.Errorf("index name %q: must not end in %s", name, SidecarSuffix)
	}
	stem := strings.TrimSuffix(name, ".json")
	for _, e := range []string{".sql", ".gz", ".zst", ".archive", ".tar", ".dump", ".gpg", ".age", ".lock", ".partial"} {
		if strings.HasSuffix(stem, e) {
			return fmt.Errorf("index name %q: must not look like a backup file (%s)", name, e)
		}
	}
	return nil
}

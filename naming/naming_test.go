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

package naming

import (
	"regexp"
	"testing"
	"time"
)

func TestBuildAndMatch(t *testing.T) {
	ts := time.Date(2026, 10, 27, 3, 15, 0, 0, time.UTC)
	s := Spec{Base: "shop", Time: ts, Ext: ".sql", Suffix: ".gz.gpg"}
	if got := s.Build(0); got != "shop_20261027_031500.sql.gz.gpg" {
		t.Fatal(got)
	}
	if got := s.Build(2); got != "shop_20261027_031500_2.sql.gz.gpg" {
		t.Fatal(got)
	}
	m := NewMatcher("shop")
	for _, f := range []string{"shop_20261027_031500.sql.gz.gpg", "shop_schema_20261027_031500.sql", "shop_tables_3_20261027_031500.sql.gz", "shop_20261027_031500_1.archive.gz.age"} {
		if !m.Match(f) {
			t.Errorf("should match %s", f)
		}
	}
	for _, f := range []string{"shop_db_20261027_031500.sql.gz", "shopx_20261027_031500.sql.gz", "shop_20261027_031500.sql.gz.meta.json", "shop-metadata.json", "other.sql.gz"} {
		if m.Match(f) {
			t.Errorf("must not match %s", f)
		}
	}
	if tm, ok := TimeOf("shop_20261027_031500.sql.gz"); !ok || !tm.Equal(ts) {
		t.Fatal(tm)
	}
}

// Miabi takes the LAST match of these regexes in the output; index and sidecar
// names must never produce a different match.
var miabiRes = []*regexp.Regexp{
	regexp.MustCompile(`[\w.\-]+\.(?:sql|archive)\.gz(?:\.gpg)?`),
	regexp.MustCompile(`[\w.\-]+\.tar\.gz(?:\.gpg)?`),
}

func TestIndexNames(t *testing.T) {
	ok := []string{"pg-bkup-metadata.json", "postgres-shop-prod-metadata.json", "x.json"}
	for _, n := range ok {
		if err := ValidateIndexName(n); err != nil {
			t.Errorf("%s: %v", n, err)
		}
		for _, re := range miabiRes {
			if re.MatchString(n) {
				t.Errorf("index name %s matches a Miabi artifact regex", n)
			}
		}
	}
	bad := []string{"info.json", "INFO.JSON", "../x.json", "a/b.json", "x.sql.gz.json", "x.gpg.json", "x.meta.json", "x.txt", "x y.json", "{bogus}.json", "x.lock.json"}
	for _, n := range bad {
		if err := ValidateIndexName(n); err == nil {
			t.Errorf("%s should be rejected", n)
		}
	}
	got, err := ExpandIndexName("{engine}-{name}-metadata.json", map[string]string{"engine": "postgres", "name": "shop"})
	if err != nil || got != "postgres-shop-metadata.json" {
		t.Fatal(got, err)
	}
}

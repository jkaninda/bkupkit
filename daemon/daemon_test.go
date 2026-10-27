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

package daemon

import (
	"context"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	for _, e := range []string{"0 1 * * *", "*/10 * * * * *", "@daily", "@every 1h"} {
		if _, err := Parse(e); err != nil {
			t.Errorf("%s: %v", e, err)
		}
	}
	if _, err := Parse("not cron"); err == nil {
		t.Fatal("invalid expression accepted")
	}
}

func TestCheck(t *testing.T) {
	now := time.Now()
	ok := State{StartedAt: now.Add(-time.Hour), LastSuccess: now.Add(-time.Hour), LastStatus: "success", Interval: 3600}
	if err := Check(ok, 0, now); err != nil {
		t.Fatal(err)
	}
	if err := Check(State{LastStatus: "failure", LastError: "boom"}, 0, now); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatal("failed run must be unhealthy")
	}
	stale := State{StartedAt: now.Add(-5 * time.Hour), LastSuccess: now.Add(-5 * time.Hour), LastStatus: "success", Interval: 3600}
	if err := Check(stale, 0, now); err == nil {
		t.Fatal("stale success must be unhealthy")
	}
	fresh := State{StartedAt: now.Add(-time.Minute), Interval: 86400}
	if err := Check(fresh, 0, now); err != nil {
		t.Fatal("before the first run the daemon is healthy:", err)
	}
}

func TestDaemonKeepsRunningAfterFailures(t *testing.T) {
	var runs atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	health := filepath.Join(t.TempDir(), "h.json")
	done := make(chan error, 1)
	go func() {
		done <- Start(ctx, Options{
			Schedule:   "@every 1s",
			HealthFile: health,
			Run: func(context.Context) error {
				if runs.Add(1) == 1 {
					return errors.New("first run fails")
				}
				return nil
			},
		})
	}()
	deadline := time.Now().Add(10 * time.Second)
	for runs.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if runs.Load() < 2 {
		t.Fatal("daemon stopped after a failed run")
	}
	st, err := ReadState(health)
	if err != nil || st.LastStatus != "success" {
		t.Fatalf("%+v %v", st, err)
	}
}

func TestMetrics(t *testing.T) {
	m := NewMetrics()
	m.Observe("shop", true, 2*time.Second, 1024)
	m.Observe("shop", false, time.Second, 0)
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{`bkup_last_size_bytes{target="shop"} 1024`, `bkup_failures_total{target="shop"} 1`, `bkup_runs_total{status="success"} 1`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in\n%s", want, body)
		}
	}
}

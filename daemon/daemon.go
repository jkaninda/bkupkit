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

// Package daemon runs backups on a cron schedule. A failed run is logged,
// recorded and notified; the daemon keeps running. Overlapping runs are
// skipped, SIGTERM/SIGINT (via the caller's context) stop it cleanly, and a
// state file feeds the `health` command and the /healthz endpoint.
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

// Parser accepts 5 or 6 (seconds) fields and descriptors (@daily, @every 1h).
var Parser = cron.NewParser(cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// Parse validates a cron expression.
func Parse(expr string) (cron.Schedule, error) {
	s, err := Parser.Parse(strings.TrimSpace(expr))
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression %q: %w", expr, err)
	}
	return s, nil
}

// Interval estimates the period of a schedule.
func Interval(s cron.Schedule, from time.Time) time.Duration {
	a := s.Next(from)
	return s.Next(a).Sub(a)
}

// State is persisted after every run.
type State struct {
	StartedAt   time.Time `json:"started_at"`
	LastRun     time.Time `json:"last_run,omitempty"`
	LastSuccess time.Time `json:"last_success,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
	LastStatus  string    `json:"last_status,omitempty"` // success | failure
	NextRun     time.Time `json:"next_run,omitempty"`
	Interval    float64   `json:"interval_seconds,omitempty"`
	Schedule    string    `json:"schedule,omitempty"`
}

// WriteState stores s atomically.
func WriteState(path string, s State) error {
	if path == "" {
		return nil
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ReadState loads the state file.
func ReadState(path string) (State, error) {
	var s State
	b, err := os.ReadFile(path)
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(b, &s)
}

// Check evaluates health: the last run must have succeeded and the last
// success (or daemon start, before the first run) must be recent enough.
func Check(s State, maxAge time.Duration, now time.Time) error {
	if maxAge <= 0 {
		maxAge = 2 * time.Duration(s.Interval*float64(time.Second))
		if maxAge <= 0 {
			maxAge = 48 * time.Hour
		}
		maxAge += 10 * time.Minute
	}
	if s.LastStatus == "failure" {
		return fmt.Errorf("last run failed at %s: %s", s.LastRun.Format(time.RFC3339), s.LastError)
	}
	ref := s.LastSuccess
	if ref.IsZero() {
		ref = s.StartedAt
	}
	if age := now.Sub(ref); age > maxAge {
		return fmt.Errorf("no successful run for %s (max %s)", age.Round(time.Second), maxAge)
	}
	return nil
}

// Options configures a daemon.
type Options struct {
	Schedule    string
	RunOnStart  bool
	HealthFile  string
	MetricsAddr string
	Metrics     *Metrics
	Log         *slog.Logger
	// Run performs one backup run; a non-nil error marks the run failed.
	Run func(ctx context.Context) error
}

// Start blocks until ctx is cancelled, then waits for a running job to finish
// (up to its own cancellation through ctx).
func Start(ctx context.Context, o Options) error {
	sched, err := Parse(o.Schedule)
	if err != nil {
		return err
	}
	log := o.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if o.Metrics == nil {
		o.Metrics = NewMetrics()
	}
	var mu sync.Mutex
	state := State{StartedAt: time.Now().UTC(), Schedule: o.Schedule, Interval: Interval(sched, time.Now()).Seconds()}
	state.NextRun = sched.Next(time.Now())
	_ = WriteState(o.HealthFile, state)

	var srv *http.Server
	if o.MetricsAddr != "" {
		mux := http.NewServeMux()
		mux.Handle("/metrics", o.Metrics)
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
			mu.Lock()
			s := state
			mu.Unlock()
			if err := Check(s, 0, time.Now()); err != nil {
				http.Error(w, err.Error(), http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write([]byte("ok\n"))
		})
		ln, err := net.Listen("tcp", o.MetricsAddr)
		if err != nil {
			return fmt.Errorf("metrics listener: %w", err)
		}
		srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
		go func() { _ = srv.Serve(ln) }()
		log.Info("Serving metrics", "addr", ln.Addr().String())
	}

	job := func() {
		start := time.Now()
		err := o.Run(ctx)
		mu.Lock()
		state.LastRun = start.UTC()
		if err != nil {
			state.LastStatus, state.LastError = "failure", err.Error()
		} else {
			state.LastStatus, state.LastError, state.LastSuccess = "success", "", time.Now().UTC()
		}
		state.NextRun = sched.Next(time.Now())
		s := state
		mu.Unlock()
		if werr := WriteState(o.HealthFile, s); werr != nil {
			log.Warn("Could not write health state", "error", werr)
		}
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Error("Scheduled backup failed; waiting for the next run", "error", err, "next_run", s.NextRun.Format(time.RFC3339))
		} else {
			log.Info("Next backup scheduled", "next_run", s.NextRun.Format(time.RFC3339))
		}
	}

	c := cron.New(cron.WithParser(Parser), cron.WithChain(
		cron.Recover(cronLogger{log}),
		cron.SkipIfStillRunning(cronLogger{log}),
	))
	if _, err := c.AddFunc(o.Schedule, job); err != nil {
		return err
	}
	log.Info("Backup daemon started", "schedule", o.Schedule, "next_run", state.NextRun.Format(time.RFC3339))
	c.Start()
	if o.RunOnStart {
		go c.Entries()[0].WrappedJob.Run()
	}
	<-ctx.Done()
	log.Info("Stopping backup daemon")
	stopped := c.Stop()
	select {
	case <-stopped.Done():
	case <-time.After(5 * time.Minute):
		log.Warn("A backup was still running at shutdown")
	}
	if srv != nil {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}
	return nil
}

type cronLogger struct{ l *slog.Logger }

func (c cronLogger) Info(msg string, kv ...any) {
	if msg == "skip" {
		c.l.Warn("Previous backup still running; skipping this run")
		return
	}
	c.l.Debug("cron: "+msg, kv...)
}

func (c cronLogger) Error(err error, msg string, kv ...any) {
	c.l.Error("cron: "+msg, append(kv, "error", err)...)
}

// Metrics is a tiny Prometheus text-format registry (no dependency).
type Metrics struct {
	mu      sync.Mutex
	targets map[string]*targetMetrics
	runs    map[string]float64
}

type targetMetrics struct {
	lastRun, lastSuccess, duration, size float64
	failures                             float64
}

// NewMetrics returns an empty registry.
func NewMetrics() *Metrics {
	return &Metrics{targets: map[string]*targetMetrics{}, runs: map[string]float64{}}
}

// Observe records one target's backup.
func (m *Metrics) Observe(target string, success bool, d time.Duration, size int64) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.targets[target]
	if t == nil {
		t = &targetMetrics{}
		m.targets[target] = t
	}
	now := float64(time.Now().Unix())
	t.lastRun, t.duration = now, d.Seconds()
	if success {
		t.lastSuccess, t.size = now, float64(size)
		m.runs["success"]++
	} else {
		t.failures++
		m.runs["failure"]++
	}
}

func (m *Metrics) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	names := make([]string, 0, len(m.targets))
	for k := range m.targets {
		names = append(names, k)
	}
	sort.Strings(names)
	write := func(name, help, typ string, get func(*targetMetrics) float64) {
		_, _ = fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, typ)
		for _, n := range names {
			_, _ = fmt.Fprintf(w, "%s{target=%q} %g\n", name, n, get(m.targets[n]))
		}
	}
	write("bkup_last_run_timestamp_seconds", "Time of the last backup attempt.", "gauge", func(t *targetMetrics) float64 { return t.lastRun })
	write("bkup_last_success_timestamp_seconds", "Time of the last successful backup.", "gauge", func(t *targetMetrics) float64 { return t.lastSuccess })
	write("bkup_last_duration_seconds", "Duration of the last backup.", "gauge", func(t *targetMetrics) float64 { return t.duration })
	write("bkup_last_size_bytes", "Stored size of the last successful backup.", "gauge", func(t *targetMetrics) float64 { return t.size })
	write("bkup_failures_total", "Failed backups.", "counter", func(t *targetMetrics) float64 { return t.failures })
	_, _ = fmt.Fprintf(w, "# HELP bkup_runs_total Backups by status.\n# TYPE bkup_runs_total counter\n")
	for _, s := range []string{"success", "failure"} {
		_, _ = fmt.Fprintf(w, "bkup_runs_total{status=%q} %g\n", s, m.runs[s])
	}
}

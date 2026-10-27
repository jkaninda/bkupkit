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
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jkaninda/bkupkit/compress"
	"github.com/jkaninda/bkupkit/config"
	"github.com/jkaninda/bkupkit/crypt"
	"github.com/jkaninda/bkupkit/daemon"
	"github.com/jkaninda/bkupkit/engine"
	"github.com/jkaninda/bkupkit/index"
	"github.com/jkaninda/bkupkit/logx"
	"github.com/jkaninda/bkupkit/naming"
	"github.com/jkaninda/bkupkit/notify"
	"github.com/jkaninda/bkupkit/pipeline"
	"github.com/jkaninda/bkupkit/store"
	gostorage "github.com/jkaninda/go-storage"
	"github.com/spf13/cobra"
)

// RunResult is written to --result-file.
type RunResult struct {
	Tool    string                   `json:"tool"`
	Version string                   `json:"version"`
	Command string                   `json:"command"`
	Success bool                     `json:"success"`
	Backups []*pipeline.BackupResult `json:"backups,omitempty"`
	Pruned  []string                 `json:"pruned,omitempty"`
	Errors  []string                 `json:"errors,omitempty"`
}

func (a *App) backupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "backup",
		Short: "Back up now, or on a schedule with --cron-expression",
		Example: "  " + a.Engine.Tool() + " backup --storage s3 --" + a.remoteFlag() + " backups\n" +
			"  " + a.Engine.Tool() + " backup --cron-expression '@daily'",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := a.load(cmd)
			if err != nil {
				return err
			}
			jobs, schedule, err := s.jobs()
			if err != nil {
				return withCode(ExitConfig, err)
			}
			if schedule != "" {
				if _, err := daemon.Parse(schedule); err != nil {
					return withCode(ExitConfig, err)
				}
				metrics := daemon.NewMetrics()
				return daemon.Start(cmd.Context(), daemon.Options{
					Schedule:    schedule,
					RunOnStart:  s.v.Bool(config.RunOnStart),
					HealthFile:  s.v.String(config.HealthFile),
					MetricsAddr: s.v.String(config.MetricsAddr),
					Metrics:     metrics,
					Log:         s.log,
					Run: func(ctx context.Context) error {
						_, err := s.runBackups(ctx, jobs, metrics)
						return err
					},
				})
			}
			res, err := s.runBackups(cmd.Context(), jobs, nil)
			if werr := writeResult(s.v.String(config.ResultFile), res); werr != nil {
				s.log.Warn("Could not write the result file", "error", werr)
			}
			return err
		},
	}
}

// jobs returns the per-job configurations and the daemon schedule.
func (s *session) jobs() ([]*config.Values, string, error) {
	schedule := s.v.String(config.CronExpression)
	if s.file == nil || len(s.file.AllJobs()) == 0 {
		return []*config.Values{s.v}, schedule, nil
	}
	var out []*config.Values
	for _, j := range s.file.AllJobs() {
		jv, err := s.v.ForJob(j.JobName(), j.Overrides())
		if err != nil {
			return nil, "", err
		}
		if js := jv.String(config.CronExpression); js != "" && schedule != "" && js != schedule {
			s.log.Warn("Per-job cron expressions are not supported; the global schedule is used", "job", j.JobName(), "job_schedule", js, "schedule", schedule)
		} else if schedule == "" && js != "" {
			schedule = js
		}
		out = append(out, jv)
	}
	return out, schedule, nil
}

// runBackups backs up every target of every job. A failing target does not
// stop the others; the joined error is returned at the end.
func (s *session) runBackups(ctx context.Context, jobs []*config.Values, metrics *daemon.Metrics) (*RunResult, error) {
	res := &RunResult{Tool: s.app.Engine.Tool(), Version: s.app.Version, Command: "backup"}
	notifier := notify.New(s.v, s.log)
	notifier.Start(ctx)
	var errs []error
	for _, jv := range jobs {
		targets, err := s.app.Engine.Targets(ctx, jv)
		if err != nil {
			errs = append(errs, withCode(ExitConfig, err))
			s.fail(ctx, notifier, jv.String(config.DBName), err, metrics, 0)
			continue
		}
		for _, t := range targets {
			if t.Values == nil {
				t.Values = jv
			}
			start := time.Now()
			br, pruned, err := s.backupTarget(ctx, t, len(targets) > 1)
			if err != nil {
				errs = append(errs, err)
				s.fail(ctx, notifier, t.Name, err, metrics, time.Since(start))
				continue
			}
			res.Backups = append(res.Backups, br)
			res.Pruned = append(res.Pruned, pruned...)
			metrics.Observe(t.Name, true, br.Duration, br.Size)
			notifier.Send(ctx, notify.Event{
				Success: true, Tool: s.app.Engine.Tool(), ToolVersion: s.app.Version, Engine: s.app.Engine.Kind(),
				Database: t.Name, File: br.File, Duration: br.Duration.Round(time.Millisecond).String(), DurationMS: br.DurationMS,
				Storage: br.Storage, BackupLocation: br.Location + "/" + br.File, BackupSize: logx.HumanSize(br.Size), Size: br.Size,
				SHA256: br.SHA256, Encryption: br.Encryption, Pruned: pruned,
				StartTime: br.StartedAt.Local().Format(s.v.String(config.TimeFormat)),
			})
			// Keep this the last line of a successful target: consumers take
			// the artifact name from the last file name in the output.
			s.log.Info("Backup completed", "database", t.Name, "file", br.File, "size", logx.HumanSize(br.Size),
				"bytes", br.Size, "sha256", br.SHA256,
				"duration", br.Duration.Round(time.Millisecond).String(), "storage", br.Storage, "location", br.Location)
		}
	}
	err := errors.Join(errs...)
	res.Success = err == nil
	for _, e := range errs {
		res.Errors = append(res.Errors, e.Error())
	}
	if len(errs) == 1 {
		err = errs[0]
	}
	return res, err
}

func (s *session) fail(ctx context.Context, n *notify.Notifier, name string, err error, m *daemon.Metrics, d time.Duration) {
	s.log.Error("Backup failed", "database", name, "error", err)
	m.Observe(name, false, d, 0)
	n.Send(ctx, notify.Event{Success: false, Tool: s.app.Engine.Tool(), ToolVersion: s.app.Version, Engine: s.app.Engine.Kind(), Database: name, Error: err.Error()})
}

// backupName returns the artifact base name for t and whether it is fixed.
func backupName(t engine.Target, multi bool) (string, bool) {
	v := t.Values
	if c := v.String(config.CustomName); c != "" {
		if multi {
			return c + "_" + t.Name, true
		}
		return c, true
	}
	name := v.String(config.BackupName)
	switch {
	case name == "":
		name = t.Name
	case multi && t.Name != "":
		name = name + "_" + t.Name
	}
	if name == "" {
		name = "backup"
	}
	return naming.Sanitize(name), false
}

// openIndex returns the index for t, or nil when disabled.
func openIndex(e engine.Engine, loc *store.Location, v *config.Values, name, db string, log *slog.Logger) (*index.Index, error) {
	if !v.Bool(config.IndexEnabled) {
		return nil, nil
	}
	ixName, err := naming.ExpandIndexName(v.String(config.IndexName), map[string]string{
		"tool": e.Tool(), "engine": e.Kind(), "name": name, "db": naming.Sanitize(db),
	})
	if err != nil {
		return nil, withCode(ExitConfig, err)
	}
	return index.New(loc, ixName, e.Tool(), log), nil
}

func (s *session) backupTarget(ctx context.Context, t engine.Target, multi bool) (*pipeline.BackupResult, []string, error) {
	v := t.Values
	if d := v.Duration(config.BackupTimeout); d > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d)
		defer cancel()
	}
	loc, err := store.Open(v)
	if err != nil {
		return nil, nil, withCode(ExitStorage, err)
	}
	defer func() { _ = loc.Close() }()
	enc, err := crypt.Encrypter(v)
	if err != nil {
		return nil, nil, withCode(ExitCrypto, err)
	}
	comp := compress.None
	if !v.Bool(config.DisableCompression) {
		if comp, err = compress.Parse(v.String(config.Compression)); err != nil {
			return nil, nil, withCode(ExitConfig, err)
		}
	}
	if err := s.app.Engine.Ping(ctx, t); err != nil {
		return nil, nil, withCode(ExitConnection, err)
	}
	name, fixed := backupName(t, multi)
	ix, err := openIndex(s.app.Engine, loc, v, name, t.Name, s.log)
	if err != nil {
		return nil, nil, err
	}
	infix := ""
	if ns, ok := s.app.Engine.(engine.NameSuffixer); ok {
		infix = ns.NameInfix(t)
	}
	br, err := pipeline.Backup(ctx, pipeline.BackupOptions{
		Engine: s.app.Engine, Target: t, Loc: loc, Index: ix,
		Compression: comp, Level: v.Int(config.CompressionLevel), Encrypter: enc,
		BackupName: name, FixedName: fixed, Infix: infix, ToolVersion: s.app.Version, Log: s.log,
	})
	if err != nil {
		return nil, nil, err
	}
	s.replicate(ctx, v, loc, br, name, t)
	pruned := s.retention(ctx, v, loc, ix, name, false)
	return br, pruned, nil
}

// retention applies the retention policy and returns the deleted file names.
// Failures are warnings: the new backup is safe.
func (s *session) retention(ctx context.Context, v *config.Values, loc *store.Location, ix *index.Index, name string, dryRun bool) []string {
	p := index.Policy{
		Days: v.Int(config.RetentionDays), Count: v.Int(config.RetentionCount), BackupName: name,
		DryRun: dryRun, AdoptLegacy: v.Bool(config.RetentionAdoptLegacy),
	}
	if !p.Active() {
		return nil
	}
	var files []string
	if ix != nil {
		deleted, err := ix.Prune(ctx, p)
		for _, e := range deleted {
			files = append(files, e.File)
		}
		if err != nil {
			s.log.Warn("Retention failed", "error", err)
		}
		return files
	}
	atRoot := strings.Trim(v.String(config.RemotePath), "/") == "" && loc.Kind != "local"
	items, err := index.PrunePattern(ctx, loc, p, atRoot, v.Bool(config.RetentionAllowRoot), func(f string) {
		s.log.Info("Deleted old backup", "file", f)
	})
	for _, it := range items {
		files = append(files, it.Key)
	}
	if err != nil {
		s.log.Warn("Retention failed", "error", err)
	}
	return files
}

// replicate copies the new backup to the extra storages of the config file.
func (s *session) replicate(ctx context.Context, v *config.Values, src *store.Location, br *pipeline.BackupResult, name string, t engine.Target) {
	if s.file == nil || len(s.file.Storages) == 0 {
		return
	}
	for i, extra := range s.file.Storages {
		ev, err := v.ForJob(fmt.Sprintf("storage-%d", i+1), upper(extra))
		if err != nil {
			s.log.Warn("Invalid extra storage", "storage", i+1, "error", err)
			continue
		}
		dst, err := store.Open(ev)
		if err != nil {
			s.log.Warn("Could not open extra storage", "storage", i+1, "error", err)
			continue
		}
		err = func() error {
			defer func() { _ = dst.Close() }()
			rc, _, err := src.Get(ctx, br.Key)
			if err != nil {
				return err
			}
			defer func() { _ = rc.Close() }()
			item, err := dst.Put(ctx, dst.Key(br.File), rc, &gostorage.PutOptions{ContentType: "application/octet-stream", Size: br.Size})
			if err != nil {
				return err
			}
			if ix, _ := openIndex(s.app.Engine, dst, ev, name, t.Name, s.log); ix != nil {
				e := index.Entry{
					File: br.File, Key: dst.Key(br.File), BackupName: name, DB: t.Name, Engine: s.app.Engine.Kind(),
					Tool: s.app.Engine.Tool(), ToolVersion: s.app.Version, Status: index.StatusSuccess,
					StartedAt: br.StartedAt, CompletedAt: br.CompletedAt, DurationMS: br.DurationMS, Size: br.Size, RawSize: br.RawSize,
					SHA256: br.SHA256, ETag: item.ETag, Storage: dst.Kind, Compression: br.Compression, Encryption: br.Encryption,
				}
				if err := ix.Record(ctx, e); err != nil {
					s.log.Warn("Copy stored but its index could not be updated", "storage", dst.Display, "error", err)
				}
				s.retention(ctx, ev, dst, ix, name, false)
			} else {
				s.retention(ctx, ev, dst, nil, name, false)
			}
			s.log.Info("Backup copied", "storage", dst.Kind, "location", dst.Display)
			return nil
		}()
		if err != nil {
			s.log.Warn("Could not copy the backup to an extra storage", "storage", dst.Display, "error", err)
		}
	}
}

func upper(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[strings.ToUpper(k)] = v
	}
	return out
}

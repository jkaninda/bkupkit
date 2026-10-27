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
	"path"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jkaninda/bkupkit/config"
	"github.com/jkaninda/bkupkit/crypt"
	"github.com/jkaninda/bkupkit/daemon"
	"github.com/jkaninda/bkupkit/engine"
	"github.com/jkaninda/bkupkit/index"
	"github.com/jkaninda/bkupkit/logx"
	"github.com/jkaninda/bkupkit/naming"
	"github.com/jkaninda/bkupkit/pipeline"
	"github.com/jkaninda/bkupkit/store"
	gostorage "github.com/jkaninda/go-storage"
	"github.com/spf13/cobra"
)

// localFile maps an absolute file path on local storage (1.x accepted
// `-f /backup/x.sql.gz`) to a key relative to STORAGE_PATH.
func localFile(loc *store.Location, v *config.Values, file string) (string, error) {
	if loc.Kind != "local" || !strings.HasPrefix(file, "/") {
		return file, nil
	}
	base := filepath.Clean(v.String(config.StoragePath))
	if rel, ok := strings.CutPrefix(filepath.Clean(file), base+"/"); ok {
		loc.Dir = ""
		return rel, nil
	}
	return "", fmt.Errorf("%s is outside STORAGE_PATH (%s)", file, base)
}

// targetLocation opens the storage and index for single-target commands.
func (s *session) targetLocation(ctx context.Context) (engine.Target, *store.Location, *index.Index, error) {
	t, err := s.singleTarget(ctx)
	if err != nil {
		return t, nil, nil, err
	}
	loc, err := store.Open(t.Values)
	if err != nil {
		return t, nil, nil, withCode(ExitStorage, err)
	}
	name, _ := backupName(t, false)
	ix, err := openIndex(s.app.Engine, loc, t.Values, name, t.Name, s.log)
	if err != nil {
		_ = loc.Close()
		return t, nil, nil, err
	}
	return t, loc, ix, nil
}

func (s *session) restoreOptions(ctx context.Context, cmd string) (pipeline.RestoreOptions, func(), error) {
	t, loc, ix, err := s.targetLocation(ctx)
	if err != nil {
		return pipeline.RestoreOptions{}, nil, err
	}
	keys, err := crypt.Keys(t.Values)
	if err != nil {
		_ = loc.Close()
		return pipeline.RestoreOptions{}, nil, withCode(ExitCrypto, err)
	}
	file, err := localFile(loc, t.Values, t.Values.String(config.FileName))
	if err == nil {
		err = pipeline.ValidateFile(file)
	}
	if err != nil {
		_ = loc.Close()
		return pipeline.RestoreOptions{}, nil, withCode(ExitConfig, fmt.Errorf("%s: %w", cmd, err))
	}
	return pipeline.RestoreOptions{
		Engine: s.app.Engine, Target: t, Loc: loc, Index: ix, File: file, Keys: keys,
		VerifyFirst: t.Values.String(config.RestoreVerifyFirst), Log: s.log,
	}, func() { _ = loc.Close() }, nil
}

func (a *App) restoreCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "restore",
		Short:   "Restore a backup (streamed: download, decrypt, decompress, restore)",
		Example: "  " + a.Engine.Tool() + " restore --storage s3 --" + a.remoteFlag() + " backups --file <file>",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := a.load(cmd)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			if d := s.v.Duration(config.RestoreTimeout); d > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, d)
				defer cancel()
			}
			o, done, err := s.restoreOptions(ctx, "restore")
			if err != nil {
				return err
			}
			defer done()
			if err := a.Engine.Ping(ctx, o.Target); err != nil {
				return withCode(ExitConnection, err)
			}
			start := time.Now()
			if err := pipeline.Restore(ctx, o); err != nil {
				s.log.Error("Restore failed", "file", o.File, "error", err)
				return err
			}
			s.log.Info("Restore completed", "database", o.Target.Name, "file", o.File, "duration", time.Since(start).Round(time.Millisecond).String())
			return nil
		},
	}
}

func (a *App) verifyCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:   "verify",
		Short: "Check that a backup downloads, decrypts and decompresses, and matches its checksum",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := a.load(cmd)
			if err != nil {
				return err
			}
			o, done, err := s.restoreOptions(cmd.Context(), "verify")
			if err != nil {
				return err
			}
			defer done()
			r, err := pipeline.Verify(cmd.Context(), o)
			if err != nil {
				s.log.Error("Verification failed", "file", o.File, "error", err)
				return err
			}
			if asJSON {
				return writeJSON(a.Out, r)
			}
			s.log.Info("Backup verified", "file", r.File, "size", logx.HumanSize(r.Size), "raw_size", logx.HumanSize(r.RawSize),
				"sha256", r.SHA256, "checksum_checked", r.ChecksumChecked, "encryption", r.Encryption, "compression", r.Compression)
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "Print the result as JSON")
	return c
}

func (a *App) listCmd() *cobra.Command {
	var asJSON, all, scan bool
	c := &cobra.Command{
		Use:   "list",
		Short: "List backups from the index (or the storage with --storage-scan)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := a.load(cmd)
			if err != nil {
				return err
			}
			_, loc, ix, err := s.targetLocation(cmd.Context())
			if err != nil {
				return err
			}
			defer func() { _ = loc.Close() }()
			if scan || ix == nil {
				items, err := loc.ListDir(cmd.Context(), loc.Dir)
				if err != nil {
					return withCode(ExitStorage, err)
				}
				var arts []gostorage.Item
				for _, it := range items {
					if naming.IsArtifact(path.Base(it.Key)) {
						arts = append(arts, it)
					}
				}
				if asJSON {
					return writeJSON(a.Out, arts)
				}
				w := tabwriter.NewWriter(a.Out, 0, 4, 2, ' ', 0)
				_, _ = fmt.Fprintln(w, "FILE\tSIZE\tMODIFIED")
				for _, it := range arts {
					_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", path.Base(it.Key), logx.HumanSize(it.Size), it.ModifiedTime.Local().Format(time.RFC3339))
				}
				return w.Flush()
			}
			f, _, _, err := ix.Load(cmd.Context())
			if err != nil {
				return withCode(ExitStorage, err)
			}
			entries := f.Entries
			if !all {
				entries = f.Live("")
			}
			if asJSON {
				return writeJSON(a.Out, entries)
			}
			w := tabwriter.NewWriter(a.Out, 0, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "FILE\tSIZE\tCOMPLETED\tSTATUS\tENCRYPTION")
			for _, e := range entries {
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", e.File, logx.HumanSize(e.Size), e.CompletedAt.Local().Format(time.RFC3339), e.Status, e.Encryption)
			}
			return w.Flush()
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "Print JSON")
	c.Flags().BoolVar(&all, "all", false, "Include deleted and missing entries")
	c.Flags().BoolVar(&scan, "storage-scan", false, "List artifacts in the storage instead of the index")
	return c
}

func (a *App) deleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete",
		Short: "Delete one backup, its sidecar and its index entry",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := a.load(cmd)
			if err != nil {
				return err
			}
			t, loc, ix, err := s.targetLocation(cmd.Context())
			if err != nil {
				return err
			}
			defer func() { _ = loc.Close() }()
			file, err := localFile(loc, t.Values, t.Values.String(config.FileName))
			if err == nil {
				err = pipeline.ValidateFile(file)
			}
			if err != nil {
				return withCode(ExitConfig, err)
			}
			if ix != nil {
				err = ix.Delete(cmd.Context(), file, "manual")
			} else {
				err = loc.Remove(cmd.Context(), loc.Key(file))
				if err == nil {
					_ = loc.Remove(cmd.Context(), loc.Key(file+naming.SidecarSuffix))
				}
			}
			if err != nil {
				return withCode(ExitStorage, err)
			}
			s.log.Info("Backup deleted", "file", file, "location", loc.Display)
			return nil
		},
	}
}

func (a *App) pruneCmd() *cobra.Command {
	var dryRun bool
	c := &cobra.Command{
		Use:   "prune",
		Short: "Apply the retention policy (BACKUP_RETENTION_DAYS / BACKUP_RETENTION_COUNT)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := a.load(cmd)
			if err != nil {
				return err
			}
			t, loc, ix, err := s.targetLocation(cmd.Context())
			if err != nil {
				return err
			}
			defer func() { _ = loc.Close() }()
			if t.Values.Int(config.RetentionDays) <= 0 && t.Values.Int(config.RetentionCount) <= 0 {
				return withCode(ExitConfig, errors.New("set BACKUP_RETENTION_DAYS and/or BACKUP_RETENTION_COUNT"))
			}
			name, _ := backupName(t, false)
			files := s.retention(cmd.Context(), t.Values, loc, ix, name, dryRun)
			verb := "Deleted"
			if dryRun {
				verb = "Would delete"
			}
			for _, f := range files {
				_, _ = fmt.Fprintf(a.Out, "%s %s\n", verb, f)
			}
			s.log.Info("Retention applied", "deleted", len(files), "dry_run", dryRun)
			return nil
		},
	}
	c.Flags().BoolVar(&dryRun, "dry-run", false, "Only show what would be deleted")
	return c
}

func (a *App) reconcileCmd() *cobra.Command {
	var dryRun bool
	var from string
	c := &cobra.Command{
		Use:   "reconcile",
		Short: "Rebuild or repair the backup index from the storage and the sidecars",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := a.load(cmd)
			if err != nil {
				return err
			}
			_, loc, ix, err := s.targetLocation(cmd.Context())
			if err != nil {
				return err
			}
			defer func() { _ = loc.Close() }()
			if ix == nil {
				return withCode(ExitConfig, errors.New("the index is disabled (BACKUP_INDEX=false)"))
			}
			if from != "" {
				if err := naming.ValidateIndexName(from); err != nil {
					return withCode(ExitConfig, err)
				}
			}
			rep, err := ix.Reconcile(cmd.Context(), from, dryRun)
			if err != nil {
				return withCode(ExitStorage, err)
			}
			s.log.Info("Index reconciled", "index", ix.Name, "added", len(rep.Added), "missing", len(rep.Missing), "imported", rep.Imported, "dry_run", dryRun)
			return nil
		},
	}
	c.Flags().BoolVar(&dryRun, "dry-run", false, "Only report what would change")
	c.Flags().StringVar(&from, "from-index", "", "Move the entries of this index into the current one (index rename)")
	return c
}

func (a *App) configCmd() *cobra.Command {
	var all, docs bool
	c := &cobra.Command{
		Use:   "config",
		Short: "Show the resolved configuration (secrets redacted) or the env reference",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if docs {
				config.WriteMarkdown(a.Out, a.Registry())
				return nil
			}
			reg := a.Registry()
			v, err := reg.Resolve(config.ResolveOptions{Flags: cmd.Flags()})
			for _, w := range v.Warnings {
				_, _ = fmt.Fprintln(a.Out, "warning:", w)
			}
			w := tabwriter.NewWriter(a.Out, 0, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "KEY\tVALUE\tSOURCE")
			for _, l := range v.Dump(all) {
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", l.Key, l.Value, l.Source)
			}
			_ = w.Flush()
			if err != nil {
				return withCode(ExitConfig, err)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&all, "all", false, "Include unset settings")
	c.Flags().BoolVar(&docs, "env-docs", false, "Print the environment variable reference as Markdown")
	return c
}

func (a *App) healthCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "health",
		Short: "Exit 0 when the backup daemon is healthy (for Docker HEALTHCHECK)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := a.Registry().Resolve(config.ResolveOptions{Flags: cmd.Flags()})
			if err != nil {
				return withCode(ExitConfig, err)
			}
			st, err := daemon.ReadState(v.String(config.HealthFile))
			if err != nil {
				return fmt.Errorf("no daemon state (is the backup running with --cron-expression?): %w", err)
			}
			if err := daemon.Check(st, v.Duration(config.HealthMaxAge), time.Now()); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(a.Out, "healthy")
			return nil
		},
	}
}

func (a *App) versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  cobra.NoArgs,
		Run: func(*cobra.Command, []string) {
			_, _ = fmt.Fprintf(a.Out, "%s version %s\n", a.Engine.Tool(), a.Version)
		},
	}
}

func (a *App) migrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Copy a database directly into the target database (TARGET_DB_*), streamed",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := a.load(cmd)
			if err != nil {
				return err
			}
			m := a.Engine.(engine.Migrator)
			pairs, err := m.MigrateTargets(cmd.Context(), s.v)
			if err != nil {
				return withCode(ExitConfig, err)
			}
			var errs []error
			for _, p := range pairs {
				start := time.Now()
				s.log.Info("Migration started", "source", p[0].Name, "target", p[1].Name)
				if err := m.Migrate(cmd.Context(), p[0], p[1]); err != nil {
					s.log.Error("Migration failed", "source", p[0].Name, "error", err)
					errs = append(errs, err)
					continue
				}
				s.log.Info("Migration completed", "source", p[0].Name, "target", p[1].Name, "duration", time.Since(start).Round(time.Millisecond).String())
			}
			return errors.Join(errs...)
		},
	}
}

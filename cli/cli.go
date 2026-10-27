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

// Package cli builds the command line shared by every tool:
//
//	backup restore verify list delete prune reconcile config health version [migrate]
//
// A tool provides an engine.Engine and calls App.Main.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/jkaninda/bkupkit/config"
	"github.com/jkaninda/bkupkit/engine"
	"github.com/jkaninda/bkupkit/logx"
	"github.com/jkaninda/bkupkit/pipeline"
	gostorage "github.com/jkaninda/go-storage"
	"github.com/spf13/cobra"
)

// Exit codes. Every failure is non-zero; the codes only refine the cause.
const (
	ExitOK         = 0
	ExitFailure    = 1
	ExitConfig     = 2
	ExitConnection = 3
	ExitStorage    = 4
	ExitCrypto     = 5
)

// ExitError carries an exit code.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string { return e.Err.Error() }
func (e *ExitError) Unwrap() error { return e.Err }

func withCode(code int, err error) error {
	if err == nil {
		return nil
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		return err
	}
	return &ExitError{Code: code, Err: err}
}

// codeOf maps an error to an exit code.
func codeOf(err error) int {
	var ee *ExitError
	switch {
	case err == nil:
		return ExitOK
	case errors.As(err, &ee):
		return ee.Code
	case errors.Is(err, pipeline.ErrEncrypt), errors.Is(err, pipeline.ErrDecrypt):
		return ExitCrypto
	case errors.Is(err, pipeline.ErrUpload), errors.Is(err, gostorage.ErrNotFound):
		return ExitStorage
	}
	return ExitFailure
}

// App is a tool's command line.
type App struct {
	Engine  engine.Engine
	Version string
	// Short is the root command description.
	Short string
	// Extend may add tool-specific commands.
	Extend func(root *cobra.Command, a *App)

	// Out receives logs and command output (default os.Stdout).
	Out io.Writer

	reg *config.Registry
}

// Main runs the CLI and exits the process.
func (a *App) Main() {
	os.Exit(a.Run(os.Args[1:]))
}

// Run executes the CLI with args and returns the exit code.
func (a *App) Run(args []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	root := a.Command()
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	if err != nil {
		code := codeOf(err)
		fmt.Fprintln(os.Stderr, "Error:", err)
		return code
	}
	return ExitOK
}

// Registry returns the settings registry (for docs generation and tests).
func (a *App) Registry() *config.Registry {
	if a.reg == nil {
		a.reg = config.NewRegistry()
		config.RegisterCommon(a.reg)
		a.Engine.Settings(a.reg)
	}
	return a.reg
}

// remoteFlag is the long flag bound to REMOTE_PATH (tools may rebind it).
func (a *App) remoteFlag() string {
	if s := a.Registry().Get(config.RemotePath); s != nil && s.Flag != "" {
		return s.Flag
	}
	return "path"
}

// Command builds the cobra command tree.
func (a *App) Command() *cobra.Command {
	if a.Out == nil {
		a.Out = os.Stdout
	}
	tool := a.Engine.Tool()
	root := &cobra.Command{
		Use:           tool,
		Short:         a.Short,
		Version:       a.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(
		a.backupCmd(), a.restoreCmd(), a.verifyCmd(), a.listCmd(), a.deleteCmd(),
		a.pruneCmd(), a.reconcileCmd(), a.configCmd(), a.healthCmd(), a.versionCmd(),
	)
	if _, ok := a.Engine.(engine.Migrator); ok {
		root.AddCommand(a.migrateCmd())
	}
	if a.Extend != nil {
		a.Extend(root, a)
	}
	a.Registry().BindFlags(root)
	return root
}

// session is the resolved context of one command.
type session struct {
	app  *App
	v    *config.Values
	file *config.File
	log  *slog.Logger
}

// load resolves configuration (flags, env, YAML) and sets up logging.
func (a *App) load(cmd *cobra.Command) (*session, error) {
	reg := a.Registry()
	v, err := reg.Resolve(config.ResolveOptions{Flags: cmd.Flags()})
	if err != nil {
		return nil, withCode(ExitConfig, err)
	}
	var file *config.File
	if path := config.FindFile(v.String(config.ConfigFile)); path != "" {
		if file, err = config.LoadFile(path, nil); err != nil {
			return nil, withCode(ExitConfig, err)
		}
		if v, err = reg.Resolve(config.ResolveOptions{Flags: cmd.Flags(), File: file.Env}); err != nil {
			return nil, withCode(ExitConfig, err)
		}
	}
	v.Command = cmd.Name()
	log := logx.New(a.Out, v.String(config.LogFormat), v.String(config.LogLevel))
	slog.SetDefault(log)
	for _, w := range v.Warnings {
		log.Warn(w)
	}
	if file != nil {
		log.Info("Using configuration file", "file", config.FindFile(v.String(config.ConfigFile)))
	}
	if err := a.Engine.Configure(cmd.Context(), v); err != nil {
		return nil, withCode(ExitConfig, err)
	}
	return &session{app: a, v: v, file: file, log: log}, nil
}

// singleTarget returns the one target of a restore/verify/delete.
func (s *session) singleTarget(ctx context.Context) (engine.Target, error) {
	ts, err := s.app.Engine.Targets(ctx, s.v)
	if err != nil {
		return engine.Target{}, withCode(ExitConfig, err)
	}
	if len(ts) == 0 {
		return engine.Target{Name: "", Values: s.v}, nil
	}
	return ts[0], nil
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// writeResult stores the run result for orchestrators (--result-file).
func writeResult(path string, v any) error {
	if path == "" {
		return nil
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

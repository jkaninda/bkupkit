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

// Package engine defines the contract between bkupkit and a backup tool.
//
// A tool (pg-bkup, mysql-bkup, ...) only knows how to talk to its data source:
// list what can be backed up, write a raw dump to a writer and replay a dump
// from a reader. Compression, encryption, storage, naming, the backup index,
// retention, notifications and scheduling are handled by bkupkit.
package engine

import (
	"context"
	"io"

	"github.com/jkaninda/bkupkit/config"
)

// Target is one unit of backup: a database, or a volume.
type Target struct {
	// Name is the database or volume name. It is used for the artifact name
	// (unless a backup name is configured) and in logs and notifications.
	Name string
	// Values holds the resolved configuration for this target (multi-database
	// configs give each target its own host, credentials, path...).
	Values *config.Values
}

// ArtifactInfo describes a backup being restored, after decryption and
// decompression have been set up by the pipeline.
type ArtifactInfo struct {
	// File is the artifact file name (base name).
	File string
	// Encryption is "none", "gpg" or "age".
	Encryption string
	// Compression is the compression the pipeline removed: "none", "gzip" or "zstd".
	// It is "none" for engines with native compression (the stream is passed through).
	Compression string
}

// Engine is implemented by every backup tool.
type Engine interface {
	// Tool is the program name, e.g. "pg-bkup". Used for {tool} and the default index name.
	Tool() string
	// Kind is the engine kind, e.g. "postgres", "mysql", "mariadb", "mongodb", "libsql", "volume".
	Kind() string
	// Settings returns the engine-specific settings (DB_HOST, ...). They are added
	// to the common registry; an engine may also override common settings' flags.
	Settings(r *config.Registry)
	// Configure validates the resolved configuration before anything runs.
	Configure(ctx context.Context, v *config.Values) error
	// Targets returns what a backup run covers for the given configuration
	// (one database by default; all databases with --all-databases; ...).
	Targets(ctx context.Context, v *config.Values) ([]Target, error)
	// Ext returns the artifact extension produced by Dump for t, without the
	// pipeline compression or encryption suffix (".sql", ".tar", ".dump").
	// Engines with native compression include it (".archive.gz").
	Ext(t Target) string
	// NativeCompression reports whether Dump output is already compressed by the
	// engine itself. The pipeline then neither compresses nor decompresses it.
	NativeCompression(t Target) bool
	// Ping checks connectivity and credentials.
	Ping(ctx context.Context, t Target) error
	// ServerVersion returns the data source version for the index ("" if unknown).
	ServerVersion(ctx context.Context, t Target) string
	// Dump writes the raw backup of t to w. It must return only after the dump
	// has fully completed (external processes exited successfully).
	Dump(ctx context.Context, t Target, w io.Writer) error
	// Restore replays the backup read from r into t. If reading r fails, the
	// engine must abort without committing (kill the restore process).
	Restore(ctx context.Context, t Target, r io.Reader, info ArtifactInfo) error
}

// Migrator is implemented by engines that can copy a source directly into a
// target (dump | restore, no intermediate file).
type Migrator interface {
	// Migrate copies source into the target described by target values.
	Migrate(ctx context.Context, source Target, target Target) error
	// MigrateTargets returns source/target pairs for a migrate run.
	MigrateTargets(ctx context.Context, v *config.Values) ([][2]Target, error)
}

// RestoreVerifier is implemented by engines that want the whole artifact
// verified before Restore starts (RESTORE_VERIFY_FIRST=auto), e.g. because the
// restore is destructive before it finishes.
type RestoreVerifier interface {
	VerifyBeforeRestore(t Target, info ArtifactInfo) bool
}

// NameSuffixer is implemented by engines that add an infix to artifact names
// (pg-bkup "schema", "tables_3"). The infix goes between the name and timestamp.
type NameSuffixer interface {
	NameInfix(t Target) string
}

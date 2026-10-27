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
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// FileJob is one entry of a YAML config file. The named fields are the legacy
// pg-bkup / mysql-bkup format; Env can set any other setting by key.
type FileJob struct {
	Name           string            `yaml:"name"`
	Host           string            `yaml:"host"`
	Port           string            `yaml:"port"`
	User           string            `yaml:"user"`
	Username       string            `yaml:"username"`
	Password       string            `yaml:"password"`
	Path           string            `yaml:"path"`
	SSLMode        string            `yaml:"sslMode"`
	AuthDatabase   string            `yaml:"authDatabase"`
	Source         string            `yaml:"source"` // volume-bkup: directory to back up
	BackupName     string            `yaml:"backupName"`
	CronExpression string            `yaml:"cronExpression"`
	RetentionDays  *int              `yaml:"retentionDays"`
	RetentionCount *int              `yaml:"retentionCount"`
	Storage        string            `yaml:"storage"`
	Env            map[string]string `yaml:"env"`
}

// File is a parsed YAML config file.
type File struct {
	Env              map[string]string `yaml:"env"`
	CronExpression   string            `yaml:"cronExpression"`
	BackupRescueMode bool              `yaml:"backupRescueMode"`
	Databases        []FileJob         `yaml:"databases"`
	Volumes          []FileJob         `yaml:"volumes"`
	Jobs             []FileJob         `yaml:"jobs"`
	// Storages lists extra storage targets; each map holds settings (STORAGE,
	// REMOTE_PATH, AWS_..., ...) that override the main storage for a copy.
	Storages []map[string]string `yaml:"storages"`
}

// DefaultConfigFiles are tried when BACKUP_CONFIG_FILE is not set.
var DefaultConfigFiles = []string{"/config/config.yaml", "/config/config.yml"}

var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:-([^}]*))?\}`)

// ExpandEnv replaces ${VAR} and ${VAR:-default}; a bare $ is left alone so
// passwords containing $ survive.
func ExpandEnv(s string, lookup func(string) (string, bool)) string {
	return envRef.ReplaceAllStringFunc(s, func(m string) string {
		sub := envRef.FindStringSubmatch(m)
		if v, ok := lookup(sub[1]); ok {
			return v
		}
		return sub[3]
	})
}

// LoadFile reads and parses path, expanding ${VAR} references.
func LoadFile(path string, env Env) (*File, error) {
	if env == nil {
		env = OSEnv{}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}
	text := ExpandEnv(string(b), env.Lookup)
	f := &File{}
	if err := yaml.Unmarshal([]byte(text), f); err != nil {
		return nil, fmt.Errorf("parse config file %s: %w", path, err)
	}
	if f.CronExpression != "" {
		if f.Env == nil {
			f.Env = map[string]string{}
		}
		if _, ok := f.Env[CronExpression]; !ok {
			f.Env[CronExpression] = f.CronExpression
		}
	}
	return f, nil
}

// FindFile returns the configured file, or the first default that exists.
func FindFile(explicit string) string {
	if explicit != "" {
		return explicit
	}
	for _, p := range DefaultConfigFiles {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// AllJobs returns databases, volumes and jobs together.
func (f *File) AllJobs() []FileJob {
	out := make([]FileJob, 0, len(f.Databases)+len(f.Volumes)+len(f.Jobs))
	out = append(out, f.Databases...)
	out = append(out, f.Volumes...)
	out = append(out, f.Jobs...)
	return out
}

// Overrides converts a job entry into setting overrides.
func (j FileJob) Overrides() map[string]string {
	m := map[string]string{}
	set := func(k, v string) {
		if strings.TrimSpace(v) != "" {
			m[k] = v
		}
	}
	set(DBName, j.Name)
	set(DBHost, j.Host)
	set(DBPort, j.Port)
	set(DBUsername, j.User)
	set(DBUsername, j.Username)
	set(DBPassword, j.Password)
	set(RemotePath, j.Path)
	set(DBSSLMode, j.SSLMode)
	set("DB_AUTH_DATABASE", j.AuthDatabase)
	set("VOLUME_PATH", j.Source)
	set(BackupName, j.BackupName)
	set(CronExpression, j.CronExpression)
	set(Storage, j.Storage)
	if j.RetentionDays != nil {
		m[RetentionDays] = strconv.Itoa(*j.RetentionDays)
	}
	if j.RetentionCount != nil {
		m[RetentionCount] = strconv.Itoa(*j.RetentionCount)
	}
	for k, v := range j.Env {
		m[strings.ToUpper(k)] = v
	}
	return m
}

// JobName is the identifier of a job (its name, backup name or source).
func (j FileJob) JobName() string {
	for _, s := range []string{j.Name, j.BackupName, j.Source} {
		if s != "" {
			return s
		}
	}
	return "job"
}

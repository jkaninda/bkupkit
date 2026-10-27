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

// Package config resolves bkupkit and tool settings from flags, environment
// variables (with deprecated fallbacks and _FILE secrets), a YAML file and
// defaults, from one declarative registry.
//
// Resolution order for every setting:
//
//	flag (only when set on the command line)
//	> per-job override (YAML job entry, then KEY_<JOB> / <JOB>_KEY env vars)
//	> KEY, KEY_FILE
//	> aliases in order (ALIAS, ALIAS_FILE)
//	> YAML file "env" section
//	> default
package config

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Kind is the value type of a setting.
type Kind int

const (
	String Kind = iota
	Bool
	Int
	Duration
	Size
	List
)

func (k Kind) String() string {
	switch k {
	case Bool:
		return "bool"
	case Int:
		return "int"
	case Duration:
		return "duration"
	case Size:
		return "size"
	case List:
		return "list"
	default:
		return "string"
	}
}

// Setting declares one configuration value.
type Setting struct {
	Key string
	// Aliases are older names, tried in order after Key. Using one logs a
	// deprecation warning.
	Aliases []string
	// Compat are alternative names accepted silently (standard names such as
	// AWS_ACCESS_KEY_ID), tried after Aliases.
	Compat  []string
	Default string
	Kind    Kind
	// Secret values are redacted in output and can be read from KEY_FILE.
	Secret bool
	// Enum restricts the value (compared case-insensitively, stored lower-case).
	Enum []string
	// Flag is the long flag name bound to this setting ("" = none).
	Flag  string
	Short string
	// FlagAliases are extra hidden long flags bound to the same setting.
	FlagAliases []string
	// Commands lists the subcommands the flag is added to; empty means a
	// persistent flag on the root command.
	Commands []string
	Group    string
	Usage    string
}

// Registry holds all settings of a tool.
type Registry struct {
	order []string
	m     map[string]*Setting
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{m: map[string]*Setting{}} }

// Add registers settings. Adding a key twice replaces the earlier definition.
func (r *Registry) Add(settings ...Setting) {
	for _, s := range settings {
		if _, ok := r.m[s.Key]; !ok {
			r.order = append(r.order, s.Key)
		}
		r.m[s.Key] = &s
	}
}

// Get returns the setting for key, or nil.
func (r *Registry) Get(key string) *Setting { return r.m[key] }

// Override changes an existing setting in place (e.g. a tool rebinding a flag).
func (r *Registry) Override(key string, fn func(*Setting)) {
	if s := r.m[key]; s != nil {
		fn(s)
	}
}

// Remove deletes a setting.
func (r *Registry) Remove(key string) {
	delete(r.m, key)
	for i, k := range r.order {
		if k == key {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
}

// All returns settings in registration order.
func (r *Registry) All() []*Setting {
	out := make([]*Setting, 0, len(r.order))
	for _, k := range r.order {
		out = append(out, r.m[k])
	}
	return out
}

// BindFlags adds the flags of all settings to root (persistent) or to the
// named subcommands.
func (r *Registry) BindFlags(root *cobra.Command) {
	cmds := map[string]*cobra.Command{}
	for _, c := range root.Commands() {
		cmds[c.Name()] = c
	}
	for _, s := range r.All() {
		if s.Flag == "" {
			continue
		}
		var sets []*pflag.FlagSet
		if len(s.Commands) == 0 {
			sets = append(sets, root.PersistentFlags())
		} else {
			for _, name := range s.Commands {
				if c, ok := cmds[name]; ok {
					sets = append(sets, c.Flags())
				}
			}
		}
		usage := s.Usage
		if usage == "" {
			usage = s.Key
		}
		usage += " (env " + s.Key + ")"
		for _, fs := range sets {
			addFlag(fs, s, s.Flag, s.Short, usage, false)
			for _, a := range s.FlagAliases {
				addFlag(fs, s, a, "", usage, true)
			}
		}
	}
}

func addFlag(fs *pflag.FlagSet, s *Setting, name, short, usage string, hidden bool) {
	if fs.Lookup(name) != nil {
		return
	}
	if short != "" && fs.ShorthandLookup(short) != nil {
		short = ""
	}
	if s.Kind == Bool {
		fs.BoolP(name, short, false, usage)
	} else {
		fs.StringP(name, short, "", usage)
	}
	if hidden {
		_ = fs.MarkHidden(name)
	}
}

// Env abstracts the process environment (tests inject their own).
type Env interface {
	Lookup(key string) (string, bool)
	ReadFile(path string) ([]byte, error)
}

// OSEnv reads the real environment.
type OSEnv struct{}

// Lookup returns a non-empty trimmed environment variable.
func (OSEnv) Lookup(key string) (string, bool) {
	v, ok := os.LookupEnv(key)
	v = strings.TrimSpace(v)
	return v, ok && v != ""
}

// ReadFile reads a file.
func (OSEnv) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }

// MapEnv is an in-memory Env for tests.
type MapEnv map[string]string

// Lookup returns a non-empty value.
func (m MapEnv) Lookup(key string) (string, bool) {
	v := strings.TrimSpace(m[key])
	return v, v != ""
}

// ReadFile reads a real file.
func (m MapEnv) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }

type entry struct {
	value  string
	source string
}

// Values is a resolved configuration.
type Values struct {
	reg   *Registry
	env   Env
	flags *pflag.FlagSet
	file  map[string]string
	vals  map[string]entry
	Job   string
	// Command is the running subcommand ("backup", "restore", ...), set by the CLI.
	Command  string
	Warnings []string
}

// ResolveOptions are the inputs of Resolve.
type ResolveOptions struct {
	Env   Env            // default OSEnv
	Flags *pflag.FlagSet // the executing command's flags (may be nil)
	// File holds global values from the YAML config file ("env" section).
	File map[string]string
}

// Resolve computes every setting's value. Invalid values are reported together.
func (r *Registry) Resolve(o ResolveOptions) (*Values, error) {
	if o.Env == nil {
		o.Env = OSEnv{}
	}
	v := &Values{reg: r, env: o.Env, flags: o.Flags, file: o.File, vals: map[string]entry{}}
	var errs []string
	for _, s := range r.All() {
		val, src, err := v.resolveOne(s, nil, "")
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		v.vals[s.Key] = entry{val, src}
	}
	if len(errs) > 0 {
		return v, fmt.Errorf("invalid configuration:\n  %s", strings.Join(errs, "\n  "))
	}
	return v, nil
}

// ForJob returns a copy of v with per-job values applied: explicit overrides
// (from a YAML job entry) and KEY_<JOB> / <JOB>_KEY environment variables.
func (v *Values) ForJob(job string, overrides map[string]string) (*Values, error) {
	nv := &Values{reg: v.reg, env: v.env, flags: v.flags, file: v.file, vals: map[string]entry{}, Job: job, Command: v.Command}
	var errs []string
	for _, s := range v.reg.All() {
		val, src, err := nv.resolveOne(s, overrides, job)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		nv.vals[s.Key] = entry{val, src}
	}
	// Keep values set programmatically on the parent.
	for k, e := range v.vals {
		if e.source == "set" {
			if cur := nv.vals[k]; cur.source == "default" {
				nv.vals[k] = e
			}
		}
	}
	if len(errs) > 0 {
		return nv, fmt.Errorf("invalid configuration for %q:\n  %s", job, strings.Join(errs, "\n  "))
	}
	return nv, nil
}

func jobKey(job string) string {
	var b strings.Builder
	for _, c := range strings.ToUpper(job) {
		if (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			b.WriteRune(c)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func (v *Values) resolveOne(s *Setting, overrides map[string]string, job string) (string, string, error) {
	raw, src, err := v.lookup(s, overrides, job)
	if err != nil {
		return "", "", err
	}
	norm, err := normalize(s, raw)
	if err != nil {
		return "", "", fmt.Errorf("%s (from %s): %w", s.Key, src, err)
	}
	return norm, src, nil
}

func (v *Values) lookup(s *Setting, overrides map[string]string, job string) (string, string, error) {
	if v.flags != nil && s.Flag != "" {
		for _, name := range append([]string{s.Flag}, s.FlagAliases...) {
			if f := v.flags.Lookup(name); f != nil && f.Changed {
				return f.Value.String(), "flag --" + name, nil
			}
		}
	}
	if job != "" {
		if val, ok := overrides[s.Key]; ok && strings.TrimSpace(val) != "" {
			return strings.TrimSpace(val), "config file (" + job + ")", nil
		}
		jk := jobKey(job)
		for _, name := range []string{s.Key + "_" + jk, jk + "_" + s.Key} {
			if val, ok := v.env.Lookup(name); ok {
				return val, "env " + name, nil
			}
		}
	}
	if val, src, ok, err := v.fromEnv(s, s.Key); ok || err != nil {
		return val, src, err
	}
	for _, a := range s.Aliases {
		if val, src, ok, err := v.fromEnv(s, a); ok || err != nil {
			if ok {
				v.warn(fmt.Sprintf("%s is deprecated, use %s", a, s.Key))
			}
			return val, src, err
		}
	}
	for _, a := range s.Compat {
		if val, src, ok, err := v.fromEnv(s, a); ok || err != nil {
			return val, src, err
		}
	}
	if val, ok := v.file[s.Key]; ok && strings.TrimSpace(val) != "" {
		return strings.TrimSpace(val), "config file", nil
	}
	return s.Default, "default", nil
}

func (v *Values) fromEnv(s *Setting, name string) (string, string, bool, error) {
	if val, ok := v.env.Lookup(name); ok {
		return val, "env " + name, true, nil
	}
	if s.Secret {
		if path, ok := v.env.Lookup(name + "_FILE"); ok {
			b, err := v.env.ReadFile(path)
			if err != nil {
				return "", "", false, fmt.Errorf("%s_FILE: %w", name, err)
			}
			return strings.TrimRight(string(b), "\r\n"), "file " + name + "_FILE", true, nil
		}
	}
	return "", "", false, nil
}

func (v *Values) warn(msg string) {
	for _, w := range v.Warnings {
		if w == msg {
			return
		}
	}
	v.Warnings = append(v.Warnings, msg)
}

func normalize(s *Setting, raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	// Compose list syntax (- KEY="false") passes the quotes through; accept
	// them for typed values. Free strings and secrets are left untouched.
	if s.Kind != String || len(s.Enum) > 0 {
		raw = unquote(raw)
	}
	switch s.Kind {
	case Bool:
		b, err := ParseBool(raw)
		if err != nil {
			return "", err
		}
		return strconv.FormatBool(b), nil
	case Int:
		if _, err := strconv.Atoi(raw); err != nil {
			return "", fmt.Errorf("not an integer: %q", raw)
		}
	case Duration:
		if _, err := ParseDuration(raw); err != nil {
			return "", err
		}
	case Size:
		if _, err := ParseSize(raw); err != nil {
			return "", err
		}
	}
	if len(s.Enum) > 0 {
		low := strings.ToLower(raw)
		for _, e := range s.Enum {
			if e == low {
				return low, nil
			}
		}
		return "", fmt.Errorf("must be one of %s, got %q", strings.Join(s.Enum, "|"), raw)
	}
	return raw, nil
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return strings.TrimSpace(s[1 : len(s)-1])
	}
	return s
}

// ParseBool accepts 1/true/yes/on/y and 0/false/no/off/n (case-insensitive).
func ParseBool(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "y", "on":
		return true, nil
	case "0", "false", "no", "n", "off", "":
		return false, nil
	}
	return false, fmt.Errorf("not a boolean: %q", s)
}

// ParseDuration accepts Go durations, plain seconds and a "d" (days) suffix.
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if n, err := strconv.Atoi(s); err == nil {
		return time.Duration(n) * time.Second, nil
	}
	if strings.HasSuffix(s, "d") {
		if n, err := strconv.Atoi(strings.TrimSuffix(s, "d")); err == nil {
			return time.Duration(n) * 24 * time.Hour, nil
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("not a duration: %q", s)
	}
	return d, nil
}

// ParseSize accepts bytes or a K/M/G/T suffix (binary units, optional "i"/"B").
func ParseSize(s string) (int64, error) {
	t := strings.ToUpper(strings.TrimSpace(s))
	t = strings.TrimSuffix(strings.TrimSuffix(t, "B"), "I")
	mult := int64(1)
	if t != "" {
		switch t[len(t)-1] {
		case 'K':
			mult = 1 << 10
		case 'M':
			mult = 1 << 20
		case 'G':
			mult = 1 << 30
		case 'T':
			mult = 1 << 40
		}
		if mult > 1 {
			t = t[:len(t)-1]
		}
	}
	n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("not a size: %q", s)
	}
	return n * mult, nil
}

// Registry returns the registry v was resolved from.
func (v *Values) Registry() *Registry { return v.reg }

// String returns the value of key ("" if unset or unknown).
func (v *Values) String(key string) string { return v.vals[key].value }

// Has reports whether key has a non-empty value.
func (v *Values) Has(key string) bool { return v.vals[key].value != "" }

// IsSet reports whether key was set explicitly (not from its default).
func (v *Values) IsSet(key string) bool {
	e, ok := v.vals[key]
	return ok && e.source != "default" && e.value != ""
}

// Source returns where key's value came from.
func (v *Values) Source(key string) string { return v.vals[key].source }

// Bool returns key as a bool.
func (v *Values) Bool(key string) bool {
	b, _ := ParseBool(v.vals[key].value)
	return b
}

// Int returns key as an int (0 if unset).
func (v *Values) Int(key string) int {
	n, _ := strconv.Atoi(v.vals[key].value)
	return n
}

// Duration returns key as a duration (0 if unset).
func (v *Values) Duration(key string) time.Duration {
	if v.vals[key].value == "" {
		return 0
	}
	d, _ := ParseDuration(v.vals[key].value)
	return d
}

// Size returns key in bytes (0 if unset).
func (v *Values) Size(key string) int64 {
	if v.vals[key].value == "" {
		return 0
	}
	n, _ := ParseSize(v.vals[key].value)
	return n
}

// List splits key on commas and newlines, trimming blanks.
func (v *Values) List(key string) []string { return SplitList(v.vals[key].value) }

// SplitList splits on commas and newlines and drops empty items.
func SplitList(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' }) {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// Set assigns a value programmatically.
func (v *Values) Set(key, value string) {
	v.vals[key] = entry{value, "set"}
}

// Require returns one error naming every missing key.
func (v *Values) Require(keys ...string) error {
	var missing []string
	for _, k := range keys {
		if !v.Has(k) {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required configuration: %s", strings.Join(missing, ", "))
	}
	return nil
}

// Line is one row of Dump output.
type Line struct {
	Key, Value, Source, Group string
}

// Dump returns all non-empty values with secrets redacted, sorted by group and key.
func (v *Values) Dump(includeEmpty bool) []Line {
	var out []Line
	for _, s := range v.reg.All() {
		e := v.vals[s.Key]
		if e.value == "" && !includeEmpty {
			continue
		}
		val := e.value
		if s.Secret && val != "" {
			val = "********"
		}
		out = append(out, Line{s.Key, val, e.source, s.Group})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Group < out[j].Group })
	return out
}

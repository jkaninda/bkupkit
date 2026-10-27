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
	"io"
	"strings"
)

// WriteMarkdown writes the environment variable reference of r as Markdown
// tables, one per group, in registration order.
func WriteMarkdown(w io.Writer, r *Registry) {
	var groups []string
	byGroup := map[string][]*Setting{}
	for _, s := range r.All() {
		if _, ok := byGroup[s.Group]; !ok {
			groups = append(groups, s.Group)
		}
		byGroup[s.Group] = append(byGroup[s.Group], s)
	}
	for _, g := range groups {
		title := g
		if title == "" {
			title = "other"
		}
		_, _ = fmt.Fprintf(w, "### %s\n\n| Variable | Flag | Default | Fallbacks | Description |\n|---|---|---|---|---|\n", title)
		for _, s := range byGroup[g] {
			flag := ""
			if s.Flag != "" {
				flag = "`--" + s.Flag + "`"
				if s.Short != "" {
					flag += " `-" + s.Short + "`"
				}
			}
			var fb []string
			for _, a := range s.Aliases {
				fb = append(fb, "`"+a+"` (deprecated)")
			}
			for _, a := range s.Compat {
				fb = append(fb, "`"+a+"`")
			}
			if s.Secret {
				fb = append(fb, "`"+s.Key+"_FILE`")
			}
			def := s.Default
			if def != "" {
				def = "`" + def + "`"
			}
			usage := s.Usage
			if len(s.Enum) > 0 {
				usage += " (" + strings.Join(s.Enum, ", ") + ")"
			}
			_, _ = fmt.Fprintf(w, "| `%s` | %s | %s | %s | %s |\n", s.Key, flag, def, strings.Join(fb, ", "), strings.ReplaceAll(usage, "|", "\\|"))
		}
		_, _ = fmt.Fprintln(w)
	}
}

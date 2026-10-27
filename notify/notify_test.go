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

package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jkaninda/bkupkit/config"
)

type capture struct {
	mu   sync.Mutex
	reqs map[string][]byte
	hdr  map[string]http.Header
}

func (c *capture) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.reqs[r.URL.Path] = b
		c.hdr[r.URL.Path] = r.Header.Clone()
		c.mu.Unlock()
	})
}

func values(t *testing.T, env config.MapEnv) *config.Values {
	t.Helper()
	r := config.NewRegistry()
	config.RegisterCommon(r)
	v, err := r.Resolve(config.ResolveOptions{Env: env})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestChannels(t *testing.T) {
	c := &capture{reqs: map[string][]byte{}, hdr: map[string]http.Header{}}
	srv := httptest.NewServer(c.handler())
	defer srv.Close()
	v := values(t, config.MapEnv{
		"WEBHOOK_URL":          srv.URL + "/hook",
		"WEBHOOK_SECRET":       "s",
		"SLACK_WEBHOOK_URL":    srv.URL + "/slack",
		"NTFY_URL":             srv.URL + "/ntfy",
		"TG_TOKEN":             "tok",
		"TG_CHAT_ID":           "42",
		"HEALTHCHECK_PING_URL": srv.URL + "/ping",
		"TEMPLATES_DIR":        t.TempDir(),
	})
	n := New(v, nil)
	n.telegramAPI = srv.URL
	n.Send(context.Background(), Event{Success: false, Database: "shop<&>", Error: "dump failed"})

	var slack map[string]string
	_ = json.Unmarshal(c.reqs["/slack"], &slack)
	if !strings.Contains(slack["text"], "shop<&>") {
		t.Errorf("slack text must not be HTML-escaped: %q", slack["text"])
	}
	var tg map[string]string
	_ = json.Unmarshal(c.reqs["/bottok/sendMessage"], &tg)
	if tg["chat_id"] != "42" || !strings.Contains(tg["text"], "dump failed") {
		t.Errorf("telegram: %v", tg)
	}
	mac := hmac.New(sha256.New, []byte("s"))
	mac.Write(c.reqs["/hook"])
	if got := c.hdr["/hook"].Get("X-Bkup-Signature"); got != "sha256="+hex.EncodeToString(mac.Sum(nil)) {
		t.Errorf("bad webhook signature %q", got)
	}
	if c.hdr["/ntfy"].Get("Priority") != "high" {
		t.Error("ntfy failure priority")
	}
	if _, ok := c.reqs["/ping/fail"]; !ok {
		t.Error("healthcheck /fail not pinged")
	}
}

func TestNotifyOn(t *testing.T) {
	c := &capture{reqs: map[string][]byte{}, hdr: map[string]http.Header{}}
	srv := httptest.NewServer(c.handler())
	defer srv.Close()
	n := New(values(t, config.MapEnv{"WEBHOOK_URL": srv.URL + "/hook", "NOTIFY_ON": "failure"}), nil)
	n.Send(context.Background(), Event{Success: true, Database: "x"})
	if _, ok := c.reqs["/hook"]; ok {
		t.Fatal("success notified with NOTIFY_ON=failure")
	}
}

func TestPosta(t *testing.T) {
	c := &capture{reqs: map[string][]byte{}, hdr: map[string]http.Header{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.handler().ServeHTTP(w, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"em_1","status":"queued"}`))
	}))
	defer srv.Close()

	// Built-in HTML, MAIL_* fallbacks, workspace header.
	n := New(values(t, config.MapEnv{
		"POSTA_URL": srv.URL + "/", "POSTA_API_KEY": "psk_test", "POSTA_WORKSPACE_ID": "7",
		"MAIL_FROM": `"Backups <b@example.com>"`, "MAIL_TO": "ops@example.com,dba@example.com", "TEMPLATES_DIR": "",
	}), nil)
	n.Send(context.Background(), Event{Success: true, Tool: "pg-bkup", Database: "shop", File: "shop_1.sql.gz", BackupSize: "1.0 MiB"})
	var req map[string]any
	if err := json.Unmarshal(c.reqs["/api/v1/emails/send"], &req); err != nil {
		t.Fatalf("no send request: %v %v", err, c.reqs)
	}
	if req["from"] != "Backups <b@example.com>" || len(req["to"].([]any)) != 2 || !strings.Contains(req["html"].(string), "shop") || req["text"] == "" {
		t.Errorf("send body: %v", req)
	}
	h := c.hdr["/api/v1/emails/send"]
	if h.Get("Authorization") != "Bearer psk_test" || h.Get("X-Posta-Workspace-Id") != "7" {
		t.Errorf("headers: %v", h)
	}

	// Stored templates: failure template with the event as data.
	n = New(values(t, config.MapEnv{
		"POSTA_URL": srv.URL, "POSTA_API_KEY": "psk_test", "POSTA_FROM": "b@example.com", "POSTA_TO": "ops@example.com",
		"POSTA_TEMPLATE": "backup-ok", "POSTA_TEMPLATE_ERROR": "backup-failed", "POSTA_LANGUAGE": "fr",
	}), nil)
	n.Send(context.Background(), Event{Success: false, Tool: "pg-bkup", Database: "shop", Error: "dump failed"})
	req = nil
	_ = json.Unmarshal(c.reqs["/api/v1/emails/send-template"], &req)
	data, _ := req["template_data"].(map[string]any)
	if req["template"] != "backup-failed" || req["language"] != "fr" || data["error"] != "dump failed" || data["database"] != "shop" {
		t.Errorf("template body: %v", req)
	}
}

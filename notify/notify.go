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

// Package notify sends run notifications: email (SMTP or Posta), Telegram, Slack, Discord,
// ntfy, a generic signed JSON webhook and healthchecks.io-style pings.
// Delivery failures are logged and never fail a backup.
package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	htmltemplate "html/template"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	texttemplate "text/template"
	"time"

	"github.com/goposta/posta-go"
	"github.com/jkaninda/bkupkit/config"
	"gopkg.in/mail.v2"
)

//go:embed templates/*.tmpl
var builtin embed.FS

// Event is the data passed to templates and webhooks. Field names keep the
// 1.x template variables (Database, DatabaseName, BackupLocation, ...).
type Event struct {
	Success         bool     `json:"success"`
	Tool            string   `json:"tool"`
	ToolVersion     string   `json:"tool_version"`
	Engine          string   `json:"engine"`
	Database        string   `json:"database"`
	DatabaseName    string   `json:"-"`
	File            string   `json:"file,omitempty"`
	Duration        string   `json:"duration,omitempty"`
	DurationMS      int64    `json:"duration_ms,omitempty"`
	Storage         string   `json:"storage,omitempty"`
	BackupLocation  string   `json:"location,omitempty"`
	BackupSize      string   `json:"-"`
	Size            int64    `json:"size,omitempty"`
	SHA256          string   `json:"sha256,omitempty"`
	Encryption      string   `json:"encryption,omitempty"`
	BackupReference string   `json:"reference,omitempty"`
	Error           string   `json:"error,omitempty"`
	StartTime       string   `json:"-"`
	EndTime         string   `json:"-"`
	Time            string   `json:"time"`
	Pruned          []string `json:"pruned,omitempty"`
}

// Notifier delivers events according to the configuration.
type Notifier struct {
	v      *config.Values
	log    *slog.Logger
	client *http.Client
	// Telegram API base, overridable in tests.
	telegramAPI string
}

// New returns a notifier for v.
func New(v *config.Values, log *slog.Logger) *Notifier {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if v.IsSet(config.MailSkipTLS) {
		log.Warn("MAIL_SKIP_TLS is deprecated; true now means skip certificate verification (1.x inverted it), use MAIL_INSECURE_SKIP_VERIFY")
	}
	return &Notifier{v: v, log: log, client: &http.Client{Timeout: 30 * time.Second}, telegramAPI: "https://api.telegram.org"}
}

// Start pings the healthcheck URL's /start endpoint.
func (n *Notifier) Start(ctx context.Context) {
	if u := n.v.String(config.HealthcheckPingURL); u != "" {
		n.post(ctx, "healthcheck", strings.TrimRight(u, "/")+"/start", "text/plain", nil, nil)
	}
}

func (n *Notifier) wants(success bool) bool {
	switch n.v.String(config.NotifyOn) {
	case "never":
		return false
	case "failure":
		return !success
	case "success":
		return success
	}
	return true
}

// Send delivers ev to every configured channel.
func (n *Notifier) Send(ctx context.Context, ev Event) {
	ev.DatabaseName = ev.Database
	if ev.BackupReference == "" {
		ev.BackupReference = n.v.String(config.BackupReference)
	}
	layout := n.v.String(config.TimeFormat)
	now := time.Now()
	if ev.EndTime == "" {
		ev.EndTime = now.Format(layout)
	}
	ev.Time = now.UTC().Format(time.RFC3339)

	if u := n.v.String(config.HealthcheckPingURL); u != "" {
		u = strings.TrimRight(u, "/")
		if !ev.Success {
			u += "/fail"
		}
		n.post(ctx, "healthcheck", u, "text/plain", []byte(ev.Error), nil)
	}
	if !n.wants(ev.Success) {
		return
	}
	if n.v.Has(config.MailHost) && n.v.Has(config.MailTo) {
		n.email(ev)
	}
	if n.v.Has(config.PostaURL) && n.v.Has(config.PostaAPIKey) && len(n.postaRecipients()) > 0 {
		n.posta(ctx, ev)
	}
	if n.v.Has(config.TelegramToken) && n.v.Has(config.TelegramChatID) {
		if msg, ok := n.text("telegram", ev); ok {
			body, _ := json.Marshal(map[string]string{"chat_id": n.v.String(config.TelegramChatID), "text": msg})
			n.post(ctx, "telegram", fmt.Sprintf("%s/bot%s/sendMessage", n.telegramAPI, n.v.String(config.TelegramToken)), "application/json", body, nil)
		}
	}
	if u := n.v.String(config.SlackWebhookURL); u != "" {
		if msg, ok := n.text("slack", ev); ok {
			body, _ := json.Marshal(map[string]string{"text": msg})
			n.post(ctx, "slack", u, "application/json", body, nil)
		}
	}
	if u := n.v.String(config.DiscordWebhookURL); u != "" {
		if msg, ok := n.text("discord", ev); ok {
			if len(msg) > 2000 {
				msg = msg[:1997] + "..."
			}
			body, _ := json.Marshal(map[string]string{"content": msg})
			n.post(ctx, "discord", u, "application/json", body, nil)
		}
	}
	if u := n.v.String(config.NtfyURL); u != "" {
		if msg, ok := n.text("ntfy", ev); ok {
			h := map[string]string{"Title": firstLine(msg), "Tags": "white_check_mark"}
			if !ev.Success {
				h["Priority"], h["Tags"] = "high", "rotating_light"
			}
			if t := n.v.String(config.NtfyToken); t != "" {
				h["Authorization"] = "Bearer " + t
			}
			n.post(ctx, "ntfy", u, "text/plain", []byte(msg), h)
		}
	}
	if u := n.v.String(config.WebhookURL); u != "" {
		body, _ := json.Marshal(ev)
		h := map[string]string{}
		if s := n.v.String(config.WebhookSecret); s != "" {
			mac := hmac.New(sha256.New, []byte(s))
			mac.Write(body)
			h["X-Bkup-Signature"] = "sha256=" + hex.EncodeToString(mac.Sum(nil))
		}
		n.post(ctx, "webhook", u, "application/json", body, h)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// load returns a template source: a custom file from TEMPLATES_DIR (first of
// names that exists), else the built-in one.
func (n *Notifier) load(builtinName string, names ...string) (string, error) {
	dir := n.v.String(config.TemplatesDir)
	for _, name := range names {
		if dir == "" {
			break
		}
		if b, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			return string(b), nil
		}
	}
	b, err := builtin.ReadFile("templates/" + builtinName)
	return string(b), err
}

// text renders the plain-text message for a channel.
func (n *Notifier) text(channel string, ev Event) (string, bool) {
	suffix := ""
	if !ev.Success {
		suffix = "-error"
	}
	src, err := n.load("text"+suffix+".tmpl", channel+suffix+".tmpl", "text"+suffix+".tmpl", "telegram"+suffix+".tmpl")
	if err != nil {
		n.log.Error("notification template", "channel", channel, "error", err)
		return "", false
	}
	t, err := texttemplate.New(channel).Parse(src)
	if err != nil {
		n.log.Error("notification template is invalid", "channel", channel, "error", err)
		return "", false
	}
	var b bytes.Buffer
	if err := t.Execute(&b, ev); err != nil {
		n.log.Error("notification template failed", "channel", channel, "error", err)
		return "", false
	}
	return strings.TrimSpace(b.String()), true
}

// emailContent renders the subject and HTML body shared by SMTP and Posta.
func (n *Notifier) emailContent(ev Event) (subject, html string, ok bool) {
	name, subject := "email.tmpl", fmt.Sprintf("✅ Backup successful – %s", ev.Database)
	if !ev.Success {
		name, subject = "email-error.tmpl", fmt.Sprintf("🔴 Backup failed – %s", ev.Database)
	}
	src, err := n.load(name, name)
	if err != nil {
		n.log.Error("email template", "error", err)
		return "", "", false
	}
	t, err := htmltemplate.New("email").Parse(src)
	if err != nil {
		n.log.Error("email template is invalid", "error", err)
		return "", "", false
	}
	var body bytes.Buffer
	if err := t.Execute(&body, ev); err != nil {
		n.log.Error("email template failed", "error", err)
		return "", "", false
	}
	return subject, body.String(), true
}

func (n *Notifier) email(ev Event) {
	subject, html, ok := n.emailContent(ev)
	if !ok {
		return
	}
	m := mail.NewMessage()
	m.SetHeader("From", strings.Trim(n.v.String(config.MailFrom), `"`))
	m.SetHeader("To", n.v.List(config.MailTo)...)
	m.SetHeader("Subject", subject)
	m.SetBody("text/html", html)

	d := mail.NewDialer(n.v.String(config.MailHost), n.v.Int(config.MailPort), n.v.String(config.MailUsername), n.v.String(config.MailPassword))
	insecure := n.v.Bool(config.MailInsecure) || n.v.Bool(config.MailSkipTLS)
	d.TLSConfig = &tls.Config{ServerName: n.v.String(config.MailHost), InsecureSkipVerify: insecure} //nolint:gosec // opt-in
	switch n.v.String(config.MailTLS) {
	case "tls":
		d.SSL = true
	case "none":
		d.StartTLSPolicy = mail.NoStartTLS
	default:
		d.StartTLSPolicy = mail.OpportunisticStartTLS
	}
	d.Timeout = 30 * time.Second
	if err := d.DialAndSend(m); err != nil {
		n.log.Error("Could not send email notification", "error", err)
		return
	}
	n.log.Info("Email notification sent")
}

// postaRecipients returns POSTA_TO, else MAIL_TO.
func (n *Notifier) postaRecipients() []string {
	if to := n.v.List(config.PostaTo); len(to) > 0 {
		return to
	}
	return n.v.List(config.MailTo)
}

// posta sends the notification through the Posta API: a stored Posta template
// when POSTA_TEMPLATE is set (the event is the template data), otherwise the
// built-in HTML email plus its plain-text version.
func (n *Notifier) posta(ctx context.Context, ev Event) {
	opts := []posta.Option{posta.WithTimeout(30 * time.Second), posta.WithUserAgent("bkupkit/" + ev.Tool)}
	if id := n.v.Int(config.PostaWorkspaceID); id > 0 {
		opts = append(opts, posta.WithWorkspace(uint(id)))
	}
	c := posta.New(strings.TrimRight(n.v.String(config.PostaURL), "/"), n.v.String(config.PostaAPIKey), opts...).
		WithContext(context.WithoutCancel(ctx))
	from := n.v.String(config.PostaFrom)
	if from == "" {
		from = strings.Trim(n.v.String(config.MailFrom), `"`)
	}
	to := n.postaRecipients()

	tmpl := n.v.String(config.PostaTemplate)
	if !ev.Success && n.v.Has(config.PostaTemplateError) {
		tmpl = n.v.String(config.PostaTemplateError)
	}
	var (
		res *posta.SendResponse
		err error
	)
	if tmpl != "" {
		data := map[string]any{}
		b, _ := json.Marshal(ev)
		_ = json.Unmarshal(b, &data)
		if ev.BackupSize != "" {
			data["backup_size"] = ev.BackupSize
		}
		res, err = c.Emails.SendTemplate(&posta.SendTemplateEmailRequest{
			Template: tmpl, Language: n.v.String(config.PostaLanguage), From: from, To: to, TemplateData: data,
		})
	} else {
		subject, html, ok := n.emailContent(ev)
		if !ok {
			return
		}
		text, _ := n.text("posta", ev)
		res, err = c.Emails.Send(&posta.SendEmailRequest{From: from, To: to, Subject: subject, HTML: html, Text: text})
	}
	if err != nil {
		n.log.Error("Could not send Posta notification", "error", redact(err.Error(), n.v.String(config.PostaAPIKey)))
		return
	}
	n.log.Info("Posta notification sent", "id", res.ID, "status", res.Status)
}

func (n *Notifier) post(ctx context.Context, channel, url, contentType string, body []byte, headers map[string]string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		n.log.Error("notification request", "channel", channel, "error", err)
		return
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", "bkupkit")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := n.client.Do(req)
	if err != nil {
		n.log.Error("Could not send notification", "channel", channel, "error", redact(err.Error(), n.v.String(config.TelegramToken)))
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		n.log.Error("Notification rejected", "channel", channel, "status", resp.StatusCode, "response", strings.TrimSpace(string(b)))
		return
	}
	n.log.Debug("Notification sent", "channel", channel)
}

func redact(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "****")
}

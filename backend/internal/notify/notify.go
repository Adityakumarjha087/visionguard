// Package notify delivers incident notifications over email (SMTP) and Telegram.
// Both channels are disabled unless configured; the log channel is always on.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/smtp"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"visionguard/backend/internal/config"
	"visionguard/backend/internal/domain"
)

var sentTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "vg_notifications_total", Help: "Notifications by channel and outcome"}, []string{"channel", "status"})

func init() { prometheus.MustRegister(sentTotal) }

// Channel delivers one incident notification.
type Channel interface {
	Name() string
	Enabled() bool
	Send(ctx context.Context, inc domain.Incident) error
}

// Dispatcher routes incidents to channels according to the rule's notify flags.
type Dispatcher struct {
	Email     Channel
	Telegram  Channel
	PublicURL string
	timeout   time.Duration
}

// NewDispatcher builds channels from config.
func NewDispatcher(cfg config.Config) *Dispatcher {
	return &Dispatcher{
		Email:     &EmailChannel{Cfg: cfg.SMTP, PublicURL: cfg.PublicURL, send: smtp.SendMail},
		Telegram:  &TelegramChannel{Cfg: cfg.Telegram, PublicURL: cfg.PublicURL, HTTP: &http.Client{Timeout: 10 * time.Second}},
		PublicURL: cfg.PublicURL,
		timeout:   15 * time.Second,
	}
}

// Notify sends asynchronously; failures are logged and counted, never propagated.
func (d *Dispatcher) Notify(inc domain.Incident) {
	slog.Info("incident", "id", inc.ID, "camera", inc.CameraName, "rule", inc.RuleName, "severity", inc.Severity,
		"message", inc.Message)
	var targets []Channel
	if inc.NotifyEmail && d.Email != nil {
		targets = append(targets, d.Email)
	}
	if inc.NotifyTelegram && d.Telegram != nil {
		targets = append(targets, d.Telegram)
	}
	for _, ch := range targets {
		if !ch.Enabled() {
			sentTotal.WithLabelValues(ch.Name(), "disabled").Inc()
			continue
		}
		go func(ch Channel) {
			ctx, cancel := context.WithTimeout(context.Background(), d.timeout)
			defer cancel()
			if err := ch.Send(ctx, inc); err != nil {
				sentTotal.WithLabelValues(ch.Name(), "error").Inc()
				slog.Warn("notification failed", "channel", ch.Name(), "incident", inc.ID, "err", err)
				return
			}
			sentTotal.WithLabelValues(ch.Name(), "ok").Inc()
		}(ch)
	}
}

func subject(inc domain.Incident) string {
	return fmt.Sprintf("[VisionGuard][%s] %s on %s", strings.ToUpper(inc.Severity), inc.RuleName, inc.CameraName)
}

func body(inc domain.Incident, publicURL string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\nCamera: %s\nRule: %s (%s)\nTime: %s\n", inc.Message, inc.CameraName, inc.RuleName,
		inc.RuleType, inc.OccurredAt.UTC().Format(time.RFC3339))
	if inc.Description != nil {
		fmt.Fprintf(&b, "\n%s\n", *inc.Description)
	}
	fmt.Fprintf(&b, "\nOpen: %s/incidents?id=%s\n", strings.TrimRight(publicURL, "/"), inc.ID)
	return b.String()
}

// SendMailFunc matches net/smtp.SendMail (injectable for tests).
type SendMailFunc func(addr string, a smtp.Auth, from string, to []string, msg []byte) error

// EmailChannel sends plain-text email over SMTP.
type EmailChannel struct {
	Cfg       config.SMTPConfig
	PublicURL string
	send      SendMailFunc
}

// NewEmailChannel builds an email channel with a custom sender.
func NewEmailChannel(cfg config.SMTPConfig, publicURL string, send SendMailFunc) *EmailChannel {
	return &EmailChannel{Cfg: cfg, PublicURL: publicURL, send: send}
}

func (e *EmailChannel) Name() string  { return "email" }
func (e *EmailChannel) Enabled() bool { return e.Cfg.Host != "" && len(e.Cfg.To) > 0 }

func (e *EmailChannel) Send(_ context.Context, inc domain.Incident) error {
	var auth smtp.Auth
	if e.Cfg.Username != "" {
		auth = smtp.PlainAuth("", e.Cfg.Username, e.Cfg.Password, e.Cfg.Host)
	}
	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s",
		e.Cfg.From, strings.Join(e.Cfg.To, ", "), subject(inc), body(inc, e.PublicURL))
	return e.send(fmt.Sprintf("%s:%d", e.Cfg.Host, e.Cfg.Port), auth, e.Cfg.From, e.Cfg.To, []byte(msg))
}

// TelegramChannel posts to the Bot API sendMessage endpoint.
type TelegramChannel struct {
	Cfg       config.TelegramConfig
	PublicURL string
	HTTP      *http.Client
}

func (t *TelegramChannel) Name() string  { return "telegram" }
func (t *TelegramChannel) Enabled() bool { return t.Cfg.BotToken != "" && t.Cfg.ChatID != "" }

func (t *TelegramChannel) Send(ctx context.Context, inc domain.Incident) error {
	payload, _ := json.Marshal(map[string]any{
		"chat_id": t.Cfg.ChatID, "text": subject(inc) + "\n\n" + body(inc, t.PublicURL), "disable_web_page_preview": true,
	})
	url := fmt.Sprintf("%s/bot%s/sendMessage", strings.TrimRight(t.Cfg.BaseURL, "/"), t.Cfg.BotToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("telegram returned %d", resp.StatusCode)
	}
	return nil
}

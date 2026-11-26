package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/smtp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"visionguard/backend/internal/config"
	"visionguard/backend/internal/domain"
)

func incident() domain.Incident {
	d := "A worker without a helmet."
	return domain.Incident{ID: "i1", CameraName: "Dock", RuleName: "Helmet", RuleType: "missing_ppe", Severity: "high",
		Message: "worker #3 without helmet", OccurredAt: time.Unix(0, 0), Description: &d, NotifyEmail: true, NotifyTelegram: true}
}

func TestEmailChannelFormatsMessage(t *testing.T) {
	var gotAddr, gotMsg string
	var gotTo []string
	ch := NewEmailChannel(config.SMTPConfig{Host: "smtp.test", Port: 2525, Username: "u", Password: "p", From: "vg@test", To: []string{"ops@test"}},
		"http://ui", func(addr string, _ smtp.Auth, _ string, to []string, msg []byte) error {
			gotAddr, gotTo, gotMsg = addr, to, string(msg)
			return nil
		})
	assert.True(t, ch.Enabled())
	require.NoError(t, ch.Send(context.Background(), incident()))
	assert.Equal(t, "smtp.test:2525", gotAddr)
	assert.Equal(t, []string{"ops@test"}, gotTo)
	assert.Contains(t, gotMsg, "Subject: [VisionGuard][HIGH] Helmet on Dock")
	assert.Contains(t, gotMsg, "http://ui/incidents?id=i1")
	assert.False(t, NewEmailChannel(config.SMTPConfig{}, "", nil).Enabled())
}

func TestTelegramChannel(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/botTOKEN/sendMessage", r.URL.Path)
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["chat_id"] == "bad" {
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()
	ch := &TelegramChannel{Cfg: config.TelegramConfig{BotToken: "TOKEN", ChatID: "42", BaseURL: srv.URL}, HTTP: srv.Client()}
	require.NoError(t, ch.Send(context.Background(), incident()))
	assert.Equal(t, "42", body["chat_id"])
	assert.True(t, strings.Contains(body["text"].(string), "worker #3"))
	ch.Cfg.ChatID = "bad"
	assert.Error(t, ch.Send(context.Background(), incident()))
	assert.False(t, (&TelegramChannel{}).Enabled())
}

type fakeChannel struct {
	mu      sync.Mutex
	name    string
	enabled bool
	sent    int
}

func (f *fakeChannel) Name() string  { return f.name }
func (f *fakeChannel) Enabled() bool { return f.enabled }
func (f *fakeChannel) Send(context.Context, domain.Incident) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent++
	return nil
}
func (f *fakeChannel) count() int { f.mu.Lock(); defer f.mu.Unlock(); return f.sent }

func TestDispatcherHonoursRuleFlags(t *testing.T) {
	email := &fakeChannel{name: "email", enabled: true}
	tg := &fakeChannel{name: "telegram", enabled: false}
	d := NewDispatcher(config.Config{PublicURL: "http://ui"})
	d.Email, d.Telegram = email, tg
	inc := incident()
	d.Notify(inc)
	inc.NotifyEmail = false
	d.Notify(inc)
	assert.Eventually(t, func() bool { return email.count() == 1 }, time.Second, 5*time.Millisecond)
	assert.Equal(t, 0, tg.count())
}

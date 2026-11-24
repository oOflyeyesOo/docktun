package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type failedNotificationTransport struct{}

func (failedNotificationTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("fixture transport unavailable")
}

func TestTelegramFailureDoesNotPersistOrReturnBotToken(t *testing.T) {
	h := NewNotifications(t.TempDir())
	const token = "123456:private-fixture-token"
	cfg := notifConfig{Type: "telegram", TelegramBotToken: token, TelegramChatID: "fixture-chat"}
	if err := h.saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	original := http.DefaultTransport
	http.DefaultTransport = failedNotificationTransport{}
	t.Cleanup(func() { http.DefaultTransport = original })
	w := httptest.NewRecorder()
	h.Test(w, httptest.NewRequest(http.MethodPost, "/api/notifications/test", nil))
	if w.Code != http.StatusBadGateway || strings.Contains(w.Body.String(), token) {
		t.Fatalf("unsafe failure response: %d %s", w.Code, w.Body.String())
	}
	saved, err := h.readConfig()
	if err != nil || strings.Contains(saved.LastStatus, token) || saved.TelegramBotToken != token {
		t.Fatalf("unsafe status or changed credentials: status=%q err=%v", saved.LastStatus, err)
	}
	// Existing statuses from older versions are sanitized when displayed too.
	saved.LastStatus = "Error: Post https://api.telegram.org/bot" + token + "/sendMessage: unavailable"
	if err := h.saveConfig(saved); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	h.Get(w, httptest.NewRequest(http.MethodGet, "/api/notifications", nil))
	if strings.Contains(w.Body.String(), token) || !strings.Contains(w.Body.String(), "[redacted]") {
		t.Fatalf("legacy delivery status exposes token: %s", w.Body.String())
	}
}

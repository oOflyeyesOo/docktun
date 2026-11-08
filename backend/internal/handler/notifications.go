package handler

import (
	"bytes"
	"docktun/internal/service"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Notifications struct {
	dataPath string
	mu       sync.Mutex
	queue    *service.AlertQueue
	wake     chan struct{}
}

func NewNotifications(dataPath string) *Notifications {
	return &Notifications{dataPath: dataPath}
}

type notifConfig struct {
	Enabled                bool   `json:"enabled"`
	Type                   string `json:"type"` // "discord", "telegram", "slack", "ntfy", "webhook"
	WebhookURL             string `json:"webhook_url"`
	TelegramBotToken       string `json:"telegram_bot_token,omitempty"`
	TelegramChatID         string `json:"telegram_chat_id,omitempty"`
	NotifyTunnelDown       bool   `json:"notify_tunnel_down"`
	NotifyContainerStop    bool   `json:"notify_container_stop"`
	NotifyDeployFinish     bool   `json:"notify_deploy_finish"`
	NotifyHighUsage        bool   `json:"notify_high_usage"`
	CpuThresholdPercent    int    `json:"cpu_threshold_percent"`
	MemoryThresholdPercent int    `json:"memory_threshold_percent"`
	LastTestedAt           string `json:"last_tested_at,omitempty"`
	LastStatus             string `json:"last_status,omitempty"`
}

func (h *Notifications) configFile() string {
	return filepath.Join(h.dataPath, "notifications.json")
}

func (h *Notifications) Get(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile(h.configFile())
	if err != nil {
		def := notifConfig{
			Enabled:                false,
			Type:                   "discord",
			NotifyTunnelDown:       true,
			NotifyContainerStop:    true,
			NotifyDeployFinish:     true,
			NotifyHighUsage:        true,
			CpuThresholdPercent:    85,
			MemoryThresholdPercent: 85,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(def)
		return
	}

	var cfg notifConfig
	if err := json.Unmarshal(data, &cfg); err == nil {
		if cfg.CpuThresholdPercent == 0 {
			cfg.CpuThresholdPercent = 85
		}
		if cfg.MemoryThresholdPercent == 0 {
			cfg.MemoryThresholdPercent = 85
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(cfg)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

func (h *Notifications) Update(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var cfg notifConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	if cfg.CpuThresholdPercent <= 0 {
		cfg.CpuThresholdPercent = 85
	}
	if cfg.MemoryThresholdPercent <= 0 {
		cfg.MemoryThresholdPercent = 85
	}
	if cfg.Type == "" {
		cfg.Type = "discord"
	}

	if err := h.saveConfig(cfg); err != nil {
		jsonError(w, "could not save notification settings", 500)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *Notifications) Test(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile(h.configFile())
	if err != nil {
		http.Error(w, `{"error":"no notification configuration saved yet"}`, http.StatusBadRequest)
		return
	}

	var cfg notifConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		http.Error(w, `{"error":"corrupted notification configuration"}`, http.StatusBadRequest)
		return
	}

	targetURL := strings.TrimSpace(cfg.WebhookURL)
	var payload []byte
	contentType := "application/json"
	reqHeaders := make(map[string]string)

	nowStr := time.Now().Format("2006-01-02 15:04:05 MST")

	switch strings.ToLower(cfg.Type) {
	case "telegram":
		if cfg.TelegramBotToken != "" && cfg.TelegramChatID != "" {
			targetURL = fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", strings.TrimSpace(cfg.TelegramBotToken))
			payload, _ = json.Marshal(map[string]interface{}{
				"chat_id":    strings.TrimSpace(cfg.TelegramChatID),
				"text":       fmt.Sprintf("🟢 <b>DockTun Alert Test</b>\nYour DockTun notification channel is connected and operational.\n<i>Timestamp: %s</i>", nowStr),
				"parse_mode": "HTML",
			})
		} else if targetURL != "" {
			payload, _ = json.Marshal(map[string]interface{}{
				"text":       fmt.Sprintf("🟢 <b>DockTun Alert Test</b>\nYour DockTun notification channel is connected.\n<i>Timestamp: %s</i>", nowStr),
				"parse_mode": "HTML",
			})
		} else {
			http.Error(w, `{"error":"Telegram Bot Token and Chat ID (or webhook URL) are required"}`, http.StatusBadRequest)
			return
		}

	case "slack":
		if targetURL == "" {
			http.Error(w, `{"error":"Slack Incoming Webhook URL is required"}`, http.StatusBadRequest)
			return
		}
		payload, _ = json.Marshal(map[string]interface{}{
			"text": fmt.Sprintf("🟢 *DockTun Alert Test*\nYour DockTun notification channel is operational.\n*Time*: `%s`", nowStr),
		})

	case "ntfy":
		if targetURL == "" {
			http.Error(w, `{"error":"Ntfy topic URL is required (e.g. https://ntfy.sh/my_topic)"}`, http.StatusBadRequest)
			return
		}
		contentType = "text/plain"
		payload = []byte(fmt.Sprintf("🟢 DockTun alert test received successfully at %s", nowStr))
		reqHeaders["Title"] = "DockTun Alert Test"
		reqHeaders["Priority"] = "default"
		reqHeaders["Tags"] = "white_check_mark,bell"

	case "discord":
		fallthrough
	default:
		if targetURL == "" {
			http.Error(w, `{"error":"Webhook URL is required"}`, http.StatusBadRequest)
			return
		}

		if strings.ToLower(cfg.Type) == "discord" {
			payload, _ = json.Marshal(map[string]interface{}{
				"username": "DockTun Monitor",
				"embeds": []map[string]interface{}{
					{
						"title":       "🟢 DockTun Alert Test",
						"description": "Your DockTun notification channel is verified and active.",
						"color":       3066993, // Green
						"fields": []map[string]interface{}{
							{"name": "Status", "value": "Operational", "inline": true},
							{"name": "Trigger", "value": "Manual Test", "inline": true},
							{"name": "Timestamp", "value": nowStr, "inline": false},
						},
						"footer": map[string]interface{}{
							"text": "DockTun • Tunnels & Containers Dashboard",
						},
					},
				},
			})
		} else {
			// Generic Webhook
			payload, _ = json.Marshal(map[string]interface{}{
				"event":     "alert_test",
				"title":     "DockTun Alert Test",
				"message":   "🟢 DockTun test notification delivered successfully.",
				"timestamp": time.Now().Format(time.RFC3339),
				"source":    "DockTun",
			})
		}
	}

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(r.Context(), "POST", targetURL, bytes.NewReader(payload))
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"failed to build request: %s"}`, err.Error()), http.StatusBadRequest)
		return
	}
	req.Header.Set("Content-Type", contentType)
	for k, v := range reqHeaders {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if resp != nil {
		defer resp.Body.Close()
	}
	if err != nil || resp.StatusCode >= 400 {
		errMsg := "Webhook request failed"
		if err != nil {
			errMsg = err.Error()
		} else if resp != nil {
			errMsg = fmt.Sprintf("HTTP %d from provider", resp.StatusCode)
		}

		cfg.LastTestedAt = time.Now().Format(time.RFC3339)
		cfg.LastStatus = fmt.Sprintf("Error: %s", errMsg)
		h.recordDeliveryStatus(cfg.LastTestedAt, cfg.LastStatus)

		http.Error(w, fmt.Sprintf(`{"error":"Webhook delivery failed: %s"}`, errMsg), http.StatusBadGateway)
		return
	}
	cfg.LastTestedAt = time.Now().Format(time.RFC3339)
	cfg.LastStatus = "Sent successfully (HTTP 200)"
	h.recordDeliveryStatus(cfg.LastTestedAt, cfg.LastStatus)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":     "sent",
		"type":       cfg.Type,
		"timestamp":  cfg.LastTestedAt,
		"status_msg": cfg.LastStatus,
	})
}

// Caller holds the settings lock. Replace atomically so monitors never read a
// truncated JSON file; retain newer settings while updating delivery status.
func (h *Notifications) saveConfig(cfg notifConfig) error {
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(h.dataPath, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(h.dataPath, ".notifications-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(raw); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), h.configFile())
}
func (h *Notifications) recordDeliveryStatus(at, status string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	cfg, err := h.readConfig()
	if err != nil {
		return
	}
	cfg.LastTestedAt = at
	cfg.LastStatus = status
	_ = h.saveConfig(cfg)
}

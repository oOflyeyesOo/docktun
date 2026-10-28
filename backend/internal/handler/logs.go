package handler

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"docktun/internal/ws"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{}

type Logs struct {
	hub     *ws.Hub
	tickets map[string]time.Time
	mu      sync.Mutex
}

func NewLogs(hub *ws.Hub) *Logs {
	return &Logs{hub: hub, tickets: make(map[string]time.Time)}
}

func (h *Logs) IssueTicket(w http.ResponseWriter, r *http.Request) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		http.Error(w, `{"error":"unable to create WebSocket ticket"}`, http.StatusInternalServerError)
		return
	}
	ticket := fmt.Sprintf("%x", raw)
	h.mu.Lock()
	now := time.Now()
	for existing, expires := range h.tickets {
		if !expires.After(now) {
			delete(h.tickets, existing)
		}
	}
	h.tickets[ticket] = time.Now().Add(60 * time.Second)
	h.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"ticket": ticket})
}

func (h *Logs) Stream(w http.ResponseWriter, r *http.Request) {
	ticket := r.URL.Query().Get("ticket")
	if ticket == "" {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	h.mu.Lock()
	expires, ok := h.tickets[ticket]
	if ok {
		delete(h.tickets, ticket)
	}
	h.mu.Unlock()
	if !ok || time.Now().After(expires) {
		http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	client := &ws.Client{Send: make(chan []byte, 256)}
	h.hub.Register <- client
	defer func() { h.hub.Unregister <- client }()

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case msg, ok := <-client.Send:
				if !ok {
					conn.WriteMessage(websocket.CloseMessage, []byte{})
					return
				}
				conn.WriteMessage(websocket.TextMessage, msg)
			case <-ticker.C:
				conn.WriteMessage(websocket.PingMessage, nil)
			}
		}
	}()

	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}
}

func (h *Logs) History(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil {
			limit = v
		}
	}

	entries := h.hub.History(limit)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"logs":  entries,
		"count": len(entries),
	})
}

package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"docktun/internal/ws"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
)

func TestLogsRejectsOversizedClientMessage(t *testing.T) {
	hub := ws.NewHub()
	go hub.Run()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute))}).SignedString([]byte("test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(NewLogs(hub, "test-secret").Stream))
	defer server.Close()
	connection, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"?token="+token, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if err := connection.WriteMessage(websocket.TextMessage, make([]byte, 8192)); err != nil {
		t.Fatal(err)
	}
	connection.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, _, err = connection.ReadMessage()
	if !websocket.IsCloseError(err, websocket.CloseMessageTooBig) {
		t.Fatalf("expected size-limit close, got %v", err)
	}
}

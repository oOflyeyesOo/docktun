package handler

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"docktun/internal/ws"
)

func TestLogsIssueTicketIsSingleUse(t *testing.T) {
	h := NewLogs(ws.NewHub())
	issue := httptest.NewRecorder()
	h.IssueTicket(issue, httptest.NewRequest("POST", "/api/logs/ws-ticket", nil))
	if issue.Code != 200 {
		t.Fatalf("issue ticket status = %d", issue.Code)
	}
	var response struct {
		Ticket string `json:"ticket"`
	}
	if err := json.Unmarshal(issue.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Ticket == "" {
		t.Fatal("ticket response has no ticket")
	}

	first := httptest.NewRecorder()
	h.Stream(first, httptest.NewRequest("GET", "/api/logs/ws?ticket="+response.Ticket, nil))
	if first.Code != 400 {
		t.Fatalf("first ticket use should reach WebSocket upgrade, status = %d", first.Code)
	}
	second := httptest.NewRecorder()
	h.Stream(second, httptest.NewRequest("GET", "/api/logs/ws?ticket="+response.Ticket, nil))
	if second.Code != 401 {
		t.Fatalf("ticket reuse status = %d", second.Code)
	}
}

package service

import (
	"net/http"
	"strings"
	"testing"
)

func TestAccessPolicyFailuresCannotReportProtection(t *testing.T) {
	for _, update := range []bool{false, true} {
		api := dnsFixture(t, func(r *http.Request) (int, string) {
			if strings.HasSuffix(r.URL.Path, "/access/apps") {
				return 200, `{"success":true,"result":[{"id":"app123","domain":"app.example.com"}]}`
			}
			if strings.HasSuffix(r.URL.Path, "/app123") && r.Method == "PUT" {
				return 200, `{"success":true}`
			}
			if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/policies") {
				if update {
					return 200, `{"success":true,"result":[{"id":"policy","name":"DockTun Zero Trust Policy"}]}`
				}
				return 200, `{"success":true,"result":[]}`
			}
			if r.Method == "POST" || r.Method == "PUT" {
				return 403, `{"success":false,"errors":[{"message":"denied"}]}`
			}
			t.Fatalf("unexpected request %s %s", r.Method, r.URL)
			return 500, ""
		})
		if _, err := api.SyncZeroTrustAccess(ZeroTrustPolicyOptions{Hostname: "app.example.com", AuthType: "email", Emails: "owner@example.com"}); err == nil {
			t.Fatal("failed Access policy accepted", update)
		}
	}
}
func TestEmptyAccessRulesDoNotFallBackToEveryone(t *testing.T) {
	api := dnsFixture(t, func(r *http.Request) (int, string) { t.Fatal("empty allowlist reached provider"); return 500, "" })
	if _, e := api.SyncZeroTrustAccess(ZeroTrustPolicyOptions{Hostname: "app.example.com", AuthType: "email"}); e == nil {
		t.Fatal("empty allowlist permitted")
	}
}

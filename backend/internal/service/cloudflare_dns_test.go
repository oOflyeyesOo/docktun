package service

import (
	"docktun/internal/db"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

type dnsTransport func(*http.Request) (*http.Response, error)

func (f dnsTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func dnsFixture(t *testing.T, handle func(*http.Request) (int, string)) *CloudflareAPI {
	t.Helper()
	database, err := db.New(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	api := NewCloudflareAPI(database)
	if err := api.SaveCredentials("test-user-api-token", "account123", "zone123"); err != nil {
		t.Fatal(err)
	}
	api.client = &http.Client{Transport: dnsTransport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer test-user-api-token" {
			t.Fatal("missing authorization")
		}
		code, body := handle(r)
		return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	return api
}

func TestDNSReplacementUsesOneBatchAndPreservesOtherRecords(t *testing.T) {
	var batchCount int
	api := dnsFixture(t, func(r *http.Request) (int, string) {
		if strings.HasSuffix(r.URL.Path, "/zones") {
			return 200, `{"success":true,"result":[{"id":"zone123","name":"example.com"}]}`
		}
		if r.Method == http.MethodGet {
			if r.URL.Query().Get("name") != "app.example.com" {
				t.Fatal("wrong hostname filter")
			}
			if r.URL.Query().Get("page") == "1" {
				return 200, `{"success":true,"result":[{"id":"a","type":"A","name":"app.example.com","content":"192.0.2.1"},{"id":"txt","type":"TXT","name":"app.example.com","content":"verification"}],"result_info":{"total_pages":2}}`
			}
			return 200, `{"success":true,"result":[{"id":"aaaa","type":"AAAA","name":"app.example.com","content":"2001:db8::1"}],"result_info":{"total_pages":2}}`
		}
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/dns_records/batch") {
			t.Fatalf("separate destructive write: %s %s", r.Method, r.URL)
		}
		batchCount++
		var payload struct {
			Deletes []struct {
				ID string `json:"id"`
			} `json:"deletes"`
			Posts []cfDNSRecord `json:"posts"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Deletes) != 2 || payload.Deletes[0].ID != "a" || payload.Deletes[1].ID != "aaaa" {
			t.Fatalf("wrong records deleted: %+v", payload)
		}
		if len(payload.Posts) != 1 || payload.Posts[0].Content != "tunnel123.cfargotunnel.com" || !payload.Posts[0].Proxied {
			t.Fatalf("wrong replacement: %+v", payload)
		}
		return 200, `{"success":true,"result":{"posts":[{"id":"new"}]}}`
	})
	result, err := api.ForceReplaceDNSRecord("app.example.com", "tunnel123")
	if err != nil {
		t.Fatal(err)
	}
	if batchCount != 1 || !result.Success || len(result.DeletedRecords) != 2 {
		t.Fatalf("bad result: %+v", result)
	}
}

func TestDNSFailuresNeverFallBackToIndividualDeletes(t *testing.T) {
	for _, failure := range []string{"query-auth", "query-json", "query-records", "unexpected-host", "batch-auth", "batch-api", "batch-json"} {
		for _, sync := range []bool{false, true} {
			t.Run(failure+fmtBool(sync), func(t *testing.T) {
				writes := 0
				api := dnsFixture(t, func(r *http.Request) (int, string) {
					if strings.HasSuffix(r.URL.Path, "/zones") {
						return 200, `{"success":true,"result":[{"id":"zone123"}]}`
					}
					if r.Method == http.MethodGet {
						switch failure {
						case "query-auth":
							return 403, `{"success":false,"errors":[{"code":10000,"message":"Authentication failed"}]}`
						case "query-json":
							return 200, `not-json`
						case "query-records":
							return 200, `{"success":true,"result":{}}`
						case "unexpected-host":
							return 200, `{"success":true,"result":[{"id":"other","name":"other.example.com","type":"A"}]}`
						}
						return 200, `{"success":true,"result":[{"id":"old","name":"app.example.com","type":"A","content":"192.0.2.1"}]}`
					}
					writes++
					if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/batch") {
						t.Fatal("nontransactional fallback attempted")
					}
					switch failure {
					case "batch-auth":
						return 403, `{"success":false,"errors":[{"message":"Authentication failed"}]}`
					case "batch-api":
						return 200, `{"success":false,"errors":[{"message":"conflict"}]}`
					default:
						return 502, `upstream unavailable`
					}
				})
				var err error
				if sync {
					err = api.SyncDNSRecord("app.example.com", "tunnel123")
				} else {
					_, err = api.ForceReplaceDNSRecord("app.example.com", "tunnel123")
				}
				if err == nil {
					t.Fatal("failure reported as success")
				}
				if strings.Contains(failure, "auth") && !strings.Contains(err.Error(), "Zone:DNS:Edit") {
					t.Fatalf("missing permission guidance: %v", err)
				}
				if strings.HasPrefix(failure, "query") || failure == "unexpected-host" {
					if writes != 0 {
						t.Fatal("wrote despite invalid query")
					}
				} else if writes != 1 {
					t.Fatalf("unexpected retries: %d", writes)
				}
			})
		}
	}
}
func fmtBool(v bool) string {
	if v {
		return "-sync"
	}
	return "-force"
}

func TestDNSAlreadyCorrectDoesNotRewrite(t *testing.T) {
	api := dnsFixture(t, func(r *http.Request) (int, string) {
		if r.Method != http.MethodGet {
			t.Fatal("already-correct record rewritten")
		}
		if strings.HasSuffix(r.URL.Path, "/zones") {
			return 200, `{"success":true,"result":[{"id":"zone123"}]}`
		}
		return 200, `{"success":true,"result":[{"id":"old","name":"app.example.com","type":"CNAME","content":"tunnel123.cfargotunnel.com","proxied":true}]}`
	})
	if err := api.SyncDNSRecord("app.example.com", "tunnel123"); err != nil {
		t.Fatal(err)
	}
}

func TestConnectorTokenDetectionUsesFieldsNotPrefix(t *testing.T) {
	for _, raw := range []string{`{"a":"account","s":"c2VjcmV0","t":"tunnel"}`, `{"t":"tunnel","s":"c2VjcmV0","a":"account"}`} {
		if !isConnectorToken(base64.StdEncoding.EncodeToString([]byte(raw))) {
			t.Fatal("connector token field order was significant")
		}
	}
	for _, token := range []string{"eyJh-opaque-user-api-token", "eyJhbGciOiJIUzI1NiJ9.payload.signature", base64.StdEncoding.EncodeToString([]byte(`{"a":"other"}`)), "opaque-api-token"} {
		if isConnectorToken(token) {
			t.Fatal("non-connector token rejected")
		}
	}
}

func TestDNSZoneReadFailureUsesExplicitZoneAndKeepsPermissionDetails(t *testing.T) {
	api := dnsFixture(t, func(r *http.Request) (int, string) {
		if strings.HasSuffix(r.URL.Path, "/zones") {
			return 403, `{"success":false,"errors":[{"message":"Authentication failed"}]}`
		}
		if !strings.Contains(r.URL.Path, "/zones/zone123/") {
			t.Fatal("explicit Zone ID ignored")
		}
		if r.Method == http.MethodGet {
			return 200, `{"success":true,"result":[]}`
		}
		return 200, `{"success":true,"result":{}}`
	})
	if err := api.SyncDNSRecord("app.example.com", "tunnel123"); err != nil {
		t.Fatal(err)
	}
	_, err := api.FindZoneID("test-user-api-token", "app.example.com")
	if err == nil || !strings.Contains(err.Error(), "Zone:Zone:Read") {
		t.Fatalf("zone permissions lost: %v", err)
	}
}

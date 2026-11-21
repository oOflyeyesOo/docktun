package middleware

import (
	"bytes"
	chimw "github.com/go-chi/chi/v5/middleware"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestLoggerRedactsQueryAndPreservesHandlerRequest(t *testing.T) {
	var output bytes.Buffer
	oldLogger := chimw.DefaultLogger
	chimw.DefaultLogger = chimw.RequestLogger(&chimw.DefaultLogFormatter{Logger: log.New(&output, "", 0), NoColor: true})
	defer func() { chimw.DefaultLogger = oldLogger }()
	request := httptest.NewRequest("GET", "/api/logs/ws?token=secret-access-token&query=private-value", nil)
	handler := RequestLogger(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("token") != "secret-access-token" {
			t.Error("authentication token lost")
		}
		if !strings.Contains(r.RequestURI, "secret-access-token") {
			t.Error("original request changed")
		}
		w.WriteHeader(204)
	}))
	handler.ServeHTTP(httptest.NewRecorder(), request)
	if strings.Contains(output.String(), "secret-access-token") || strings.Contains(output.String(), "private-value") {
		t.Fatal("query leaked to request log")
	}
	if !strings.Contains(output.String(), "/api/logs/ws") {
		t.Fatalf("missing request path in log: %s", output.String())
	}
}

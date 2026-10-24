package middleware

import (
	"github.com/golang-jwt/jwt/v5"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestVerifyClaims(t *testing.T) {
	for _, tc := range []struct {
		name   string
		claims jwt.MapClaims
		method jwt.SigningMethod
		want   int
	}{
		{"valid", jwt.MapClaims{"sub": 1, "username": "owner", "exp": time.Now().Add(time.Hour).Unix()}, jwt.SigningMethodHS256, 200},
		{"missing identity", jwt.MapClaims{"exp": time.Now().Add(time.Hour).Unix()}, jwt.SigningMethodHS256, 401},
		{"wrong subject", jwt.MapClaims{"sub": "1", "username": "owner", "exp": time.Now().Add(time.Hour).Unix()}, jwt.SigningMethodHS256, 401},
		{"missing expiry", jwt.MapClaims{"sub": 1, "username": "owner"}, jwt.SigningMethodHS256, 401},
		{"wrong algorithm", jwt.MapClaims{"sub": 1, "username": "owner", "exp": time.Now().Add(time.Hour).Unix()}, jwt.SigningMethodHS384, 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token, _ := jwt.NewWithClaims(tc.method, tc.claims).SignedString([]byte("secret"))
			r := httptest.NewRequest("POST", "/", nil)
			r.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			NewAuth("secret").Verify(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })).ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("got %d want %d", w.Code, tc.want)
			}
		})
	}
}

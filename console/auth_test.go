package console

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOperatorSessionAndBearerBoundary(t *testing.T) {
	t.Setenv("FAB_CONSOLE_TOKEN", "test-operator-token")
	t.Setenv("FAB_CONSOLE_TOKEN_FILE", "")
	m := New(nil)
	handler := m.consoleAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	call := func(method, path, body, origin, bearer string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://console.example"+path, strings.NewReader(body))
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := call("GET", "/api/catalog", "", "", "", nil); w.Code != 401 || !strings.Contains(w.Header().Get("Content-Type"), "json") {
		t.Fatal("missing structured auth failure", w.Code)
	}
	if w := call("GET", "/healthz", "", "", "", nil); w.Code != 200 {
		t.Fatal("health requires credentials")
	}
	if w := call("POST", "/api/auth/session", `{"token":"wrong"}`, "http://console.example", "", nil); w.Code != 401 {
		t.Fatal("bad token admitted")
	}
	if w := call("POST", "/api/auth/session", `{"token":"test-operator-token"}`, "https://other.example", "", nil); w.Code != 403 {
		t.Fatal("cross-origin login admitted")
	}
	signed := call("POST", "/api/auth/session", `{"token":"test-operator-token"}`, "http://console.example", "", nil)
	if signed.Code != 200 || len(signed.Result().Cookies()) != 1 {
		t.Fatal("login failed", signed.Code)
	}
	cookie := signed.Result().Cookies()[0]
	if !cookie.HttpOnly || cookie.Path != "/api/" || cookie.SameSite != http.SameSiteStrictMode || cookie.MaxAge != 28800 {
		t.Fatal("unsafe cookie attributes")
	}
	for _, path := range []string{"/api/catalog", "/.well-known/fabricate"} {
		if w := call("GET", path, "", "", "", cookie); w.Code != 204 {
			t.Fatal("cookie read failed", path, w.Code)
		}
	}
	for _, origin := range []string{"", "https://other.example"} {
		if w := call("POST", "/api/sessions", `{}`, origin, "", cookie); w.Code != 403 {
			t.Fatal("cross-origin mutation admitted")
		}
	}
	if w := call("POST", "/api/sessions", `{}`, "http://console.example", "", cookie); w.Code != 204 {
		t.Fatal("same-origin mutation denied")
	}
	if w := call("POST", "/api/sessions", `{}`, "", "test-operator-token", nil); w.Code != 204 {
		t.Fatal("nonbrowser bearer client changed")
	}
	if w := call("DELETE", "/api/auth/session", "", "http://console.example", "", cookie); w.Code != 204 || w.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("logout did not clear cookie")
	}
	if err := m.Configure(Config{PublicURL: "https://console.example"}); err != nil {
		t.Fatal(err)
	}
	signed = call("POST", "/api/auth/session", `{"token":"test-operator-token"}`, "https://console.example", "", nil)
	if signed.Code != 200 || !signed.Result().Cookies()[0].Secure {
		t.Fatal("hosted session cookie is not Secure")
	}
}
func TestSessionTamperingExpirationAndRotation(t *testing.T) {
	now := time.Now()
	value, err := signedConsoleSession("operator", "http://console.example", now)
	if err != nil {
		t.Fatal(err)
	}
	request := func(value string) *http.Request {
		r := httptest.NewRequest("GET", "http://console.example/api/catalog", nil)
		r.AddCookie(&http.Cookie{Name: consoleCookie, Value: value})
		return r
	}
	if validConsoleSession(request(value), "operator", "http://different.example", now) {
		t.Fatal("cookie crossed deployment origin")
	}
	if !validConsoleSession(request(value), "operator", "http://console.example", now) {
		t.Fatal("valid session denied")
	}
	if validConsoleSession(request(value+"x"), "operator", "http://console.example", now) || validConsoleSession(request(value), "rotated", "http://console.example", now) || validConsoleSession(request(value), "operator", "http://console.example", now.Add(9*time.Hour)) {
		t.Fatal("invalid session admitted")
	}
}
func TestMountedOperatorSecretAndInvalidConfiguration(t *testing.T) {
	t.Setenv("FAB_CONSOLE_TOKEN", "")
	file := filepath.Join(t.TempDir(), "operator")
	if err := os.WriteFile(file, []byte("test-file-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAB_CONSOLE_TOKEN_FILE", file)
	token, err := ControlTokenFromEnvironment()
	if err != nil || token != "test-file-token" {
		t.Fatal("mounted secret not loaded")
	}
	t.Setenv("FAB_CONSOLE_TOKEN", "conflict")
	if _, err := ControlTokenFromEnvironment(); err == nil {
		t.Fatal("ambiguous secrets accepted")
	}
	t.Setenv("FAB_CONSOLE_TOKEN", "")
	t.Setenv("FAB_CONSOLE_TOKEN_FILE", file+"-absent")
	handler := New(nil).Handler()
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "http://console.example/api/catalog", nil))
	if w.Code != 503 {
		t.Fatal("bad secret configuration failed open")
	}
	var body map[string]any
	if json.Unmarshal(w.Body.Bytes(), &body) != nil {
		t.Fatal("invalid error envelope")
	}
}

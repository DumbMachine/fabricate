package requestlog

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestMiddlewarePersistsPayloadAndRedactsSecrets(t *testing.T) {
	t.Setenv("FAB_LOG_DIR", t.TempDir())
	log, err := New("acme-gmail")
	if err != nil {
		t.Fatal(err)
	}
	handler := log.Middleware("support-mail", http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		_, _ = io.ReadAll(request.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Set-Cookie", "secret-cookie")
		_, _ = io.WriteString(w, `{"id":"msg-1","access_token":"must-not-leak"}`)
	}))
	request := httptest.NewRequest(http.MethodPost, "https://gmail.googleapis.com/gmail/v1/users/me/messages/send?q=hello", strings.NewReader(`{"raw":"email-payload","client_secret":"must-not-leak"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer must-not-leak")
	handler.ServeHTTP(httptest.NewRecorder(), request)
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(log.Path())
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, expected := range []string{`"service":"support-mail"`, `"raw":"email-payload"`, `"id":"msg-1"`, `"status":200`, `[REDACTED]`} {
		if !strings.Contains(text, expected) {
			t.Fatalf("log missing %q: %s", expected, text)
		}
	}
	if strings.Contains(text, "must-not-leak") || strings.Contains(text, "secret-cookie") {
		t.Fatalf("log leaked a secret: %s", text)
	}
	paths, err := Find("acme-gmail")
	if err != nil || len(paths) != 1 || paths[0] != log.Path() {
		t.Fatalf("find = %v, %v", paths, err)
	}
}

func TestFindRejectsPathTraversal(t *testing.T) {
	t.Setenv("FAB_LOG_DIR", t.TempDir())
	if _, err := Find("../outside"); err == nil {
		t.Fatal("expected invalid environment name error")
	}
}

package console

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const consoleCookie = "fabricate_session"
const consoleSessionLifetime = 8 * time.Hour

// ControlTokenFromEnvironment supports an operator-mounted secret as well as
// the existing API credential. Neither value is returned by the console API.
func ControlTokenFromEnvironment() (string, error) {
	token, file := strings.TrimSpace(os.Getenv("FAB_CONSOLE_TOKEN")), strings.TrimSpace(os.Getenv("FAB_CONSOLE_TOKEN_FILE"))
	if token != "" && file != "" {
		return "", fmt.Errorf("set only one of FAB_CONSOLE_TOKEN or FAB_CONSOLE_TOKEN_FILE")
	}
	if file != "" {
		raw, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("read console operator token file: %w", err)
		}
		token = strings.TrimSpace(string(raw))
		if token == "" {
			return "", fmt.Errorf("console operator token file is empty")
		}
	}
	if len(token) > 4096 || strings.ContainsAny(token, "\r\n") {
		return "", fmt.Errorf("invalid console operator token")
	}
	return token, nil
}

type consoleClaim struct {
	Expires  int64  `json:"exp"`
	Nonce    string `json:"nonce"`
	Audience string `json:"aud"`
}

func signedConsoleSession(token, origin string, now time.Time) (string, error) {
	nonce := make([]byte, 24)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	raw, _ := json.Marshal(consoleClaim{now.Add(consoleSessionLifetime).Unix(), base64.RawURLEncoding.EncodeToString(nonce), origin})
	payload := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte("fabricate-console-session-v1:" + payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}
func validConsoleSession(r *http.Request, token, origin string, now time.Time) bool {
	cookie, err := r.Cookie(consoleCookie)
	if err != nil || len(cookie.Value) > 1024 {
		return false
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 2 {
		return false
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte("fabricate-console-session-v1:" + parts[0]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	var claim consoleClaim
	return json.Unmarshal(raw, &claim) == nil && claim.Nonce != "" && claim.Audience == origin && claim.Expires > now.Unix() && claim.Expires <= now.Add(consoleSessionLifetime).Unix()
}
func validConsoleBearer(r *http.Request, token string) bool {
	return token != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) == 1
}
func (m *Manager) consoleOrigin(r *http.Request) string {
	if base := m.configuredBase(); base != "" {
		u, _ := url.Parse(base)
		return u.Scheme + "://" + u.Host
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
func (m *Manager) sameConsoleOrigin(r *http.Request) bool {
	return r.Header.Get("Origin") == m.consoleOrigin(r)
}
func (m *Manager) sessionCookie(r *http.Request, value string, maxAge int) *http.Cookie {
	return &http.Cookie{Name: consoleCookie, Value: value, Path: "/api/", HttpOnly: true, Secure: strings.HasPrefix(m.consoleOrigin(r), "https://"), SameSite: http.SameSiteStrictMode, MaxAge: maxAge}
}
func (m *Manager) consoleAuth(next http.Handler) http.Handler {
	token, configErr := ControlTokenFromEnvironment()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if configErr != nil {
			writeError(w, http.StatusServiceUnavailable, fmt.Errorf("console authentication is misconfigured"))
			return
		}
		if r.URL.Path == "/healthz" && (r.Method == "GET" || r.Method == "HEAD") {
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, 200, map[string]string{"status": "ok"})
			return
		}
		if r.URL.Path == "/api/auth/session" {
			w.Header().Set("Cache-Control", "no-store")
			switch r.Method {
			case "GET":
				writeJSON(w, 200, map[string]bool{"required": token != "", "authenticated": token == "" || validConsoleBearer(r, token) || validConsoleSession(r, token, m.consoleOrigin(r), time.Now())})
			case "POST":
				if !m.sameConsoleOrigin(r) {
					writeError(w, 403, fmt.Errorf("same-origin sign-in required"))
					return
				}
				var input struct {
					Token string `json:"token"`
				}
				decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
				decoder.DisallowUnknownFields()
				if decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF {
					writeError(w, 400, fmt.Errorf("invalid sign-in request"))
					return
				}
				if token != "" && subtle.ConstantTimeCompare([]byte(input.Token), []byte(token)) != 1 {
					writeError(w, 401, fmt.Errorf("operator token is incorrect"))
					return
				}
				if token != "" {
					value, err := signedConsoleSession(token, m.consoleOrigin(r), time.Now())
					if err != nil {
						writeError(w, 500, fmt.Errorf("could not create console session"))
						return
					}
					http.SetCookie(w, m.sessionCookie(r, value, int(consoleSessionLifetime.Seconds())))
				}
				writeJSON(w, 200, map[string]bool{"required": token != "", "authenticated": true})
			case "DELETE":
				if !m.sameConsoleOrigin(r) {
					writeError(w, 403, fmt.Errorf("same-origin sign-out required"))
					return
				}
				http.SetCookie(w, m.sessionCookie(r, "", -1))
				w.WriteHeader(204)
			default:
				w.Header().Set("Allow", "GET, POST, DELETE")
				writeError(w, 405, fmt.Errorf("method not allowed"))
			}
			return
		}
		protected := strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/.well-known/fabricate" || r.URL.Path == "/mcp"
		if protected {
			w.Header().Set("Cache-Control", "no-store")
		}
		if r.URL.Path == "/mcp" && r.Header.Get("Origin") != "" && !m.sameConsoleOrigin(r) {
			writeError(w, 403, fmt.Errorf("untrusted MCP origin"))
			return
		}
		p := principal{Admin: true, Project: "default"}
		if token != "" && protected && !validConsoleBearer(r, token) {
			if r.Header.Get("Authorization") != "" {
				var ok bool
				p, ok = m.authenticateProject(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
				if !ok || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
					w.Header().Set("WWW-Authenticate", "Bearer")
					writeError(w, 401, fmt.Errorf("invalid or expired control credential"))
					return
				}
			} else {
				if r.URL.Path == "/mcp" || !validConsoleSession(r, token, m.consoleOrigin(r), time.Now()) {
					w.Header().Set("WWW-Authenticate", "Bearer")
					writeError(w, 401, fmt.Errorf("sign in to the Fabricate console"))
					return
				}
				if r.Method != "GET" && r.Method != "HEAD" && !m.sameConsoleOrigin(r) {
					writeError(w, 403, fmt.Errorf("same-origin request required"))
					return
				}
			}
		}
		r = r.WithContext(withActor(r.Context(), p))
		next.ServeHTTP(w, r)
	})
}

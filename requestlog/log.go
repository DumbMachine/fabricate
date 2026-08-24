// Package requestlog owns durable, redacted HTTP request traces for Fabricate
// environments. Logs outlive the ephemeral service state they describe.
package requestlog

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const bodyLimit = 1 << 20

var environmentPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

type Log struct {
	Environment string
	RunID       string
	path        string
	file        *os.File
	mu          sync.Mutex
}

type Entry struct {
	Timestamp   time.Time  `json:"timestamp"`
	Environment string     `json:"environment"`
	RunID       string     `json:"runId"`
	Service     string     `json:"service"`
	Host        string     `json:"host"`
	Method      string     `json:"method"`
	Path        string     `json:"path"`
	Query       url.Values `json:"query,omitempty"`
	Request     Message    `json:"request"`
	Response    Message    `json:"response"`
	Status      int        `json:"status"`
	DurationMS  float64    `json:"durationMs"`
	Error       string     `json:"error,omitempty"`
}

type Message struct {
	Headers   map[string][]string `json:"headers,omitempty"`
	Body      any                 `json:"body,omitempty"`
	Bytes     int                 `json:"bytes"`
	Truncated bool                `json:"truncated,omitempty"`
}

func New(environment string) (*Log, error) {
	if !environmentPattern.MatchString(environment) {
		return nil, fmt.Errorf("request log: invalid environment name %q", environment)
	}
	root, err := Root()
	if err != nil {
		return nil, err
	}
	runID, err := newRunID()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(root, environment, runID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("request log: create run directory: %w", err)
	}
	path := filepath.Join(dir, "requests.jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("request log: create %s: %w", path, err)
	}
	return &Log{Environment: environment, RunID: runID, path: path, file: file}, nil
}

func Root() (string, error) {
	if root := strings.TrimSpace(os.Getenv("FAB_LOG_DIR")); root != "" {
		return filepath.Abs(root)
	}
	config, err := os.UserConfigDir()
	if err != nil || config == "" {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return "", fmt.Errorf("request log: locate config directory: %w", err)
		}
		config = filepath.Join(home, ".config")
	}
	return filepath.Join(config, "fab", "logs"), nil
}

func (l *Log) Path() string { return l.path }

func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

func (l *Log) Middleware(service string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		started := time.Now()
		requestCapture := &capturedReadCloser{ReadCloser: request.Body}
		if request.Body != nil {
			request.Body = requestCapture
		}
		responseCapture := &capturedResponseWriter{ResponseWriter: w}
		next.ServeHTTP(responseCapture, request)
		status := responseCapture.status
		if status == 0 {
			status = http.StatusOK
		}
		l.Record(service, request, EntryInput{
			Started: started, Status: status,
			RequestBody: requestCapture.buffer.Bytes(), RequestBytes: requestCapture.bytes, RequestTruncated: requestCapture.truncated,
			ResponseBody: responseCapture.buffer.Bytes(), ResponseBytes: responseCapture.bytes, ResponseTruncated: responseCapture.truncated,
			ResponseHeaders: w.Header(),
		})
	})
}

type EntryInput struct {
	Started           time.Time
	Status            int
	RequestBody       []byte
	RequestBytes      int
	RequestTruncated  bool
	ResponseBody      []byte
	ResponseBytes     int
	ResponseTruncated bool
	ResponseHeaders   http.Header
	Error             string
}

func (l *Log) Record(service string, request *http.Request, input EntryInput) {
	if l == nil || request == nil {
		return
	}
	entry := Entry{
		Timestamp: input.Started.UTC(), Environment: l.Environment, RunID: l.RunID,
		Service: service, Host: request.Host, Method: request.Method, Path: request.URL.Path,
		Query: redactValues(request.URL.Query()), Status: input.Status,
		DurationMS: float64(time.Since(input.Started).Microseconds()) / 1000, Error: input.Error,
		Request: Message{
			Headers: redactHeaders(request.Header), Body: decodeBody(request.Header.Get("Content-Type"), input.RequestBody),
			Bytes: input.RequestBytes, Truncated: input.RequestTruncated,
		},
		Response: Message{
			Headers: redactHeaders(input.ResponseHeaders), Body: decodeBody(input.ResponseHeaders.Get("Content-Type"), input.ResponseBody),
			Bytes: input.ResponseBytes, Truncated: input.ResponseTruncated,
		},
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return
	}
	_, _ = l.file.Write(append(encoded, '\n'))
	_ = l.file.Sync()
}

func Find(environment string) ([]string, error) {
	if !environmentPattern.MatchString(environment) {
		return nil, fmt.Errorf("request log: invalid environment name %q", environment)
	}
	root, err := Root()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(root, environment)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("request log: list %s: %w", dir, err)
	}
	var paths []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name(), "requests.jsonl")
		if _, err := os.Stat(path); err == nil {
			paths = append(paths, path)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(paths)))
	return paths, nil
}

type capturedReadCloser struct {
	io.ReadCloser
	buffer    bytes.Buffer
	bytes     int
	truncated bool
}

func (c *capturedReadCloser) Read(p []byte) (int, error) {
	if c.ReadCloser == nil {
		return 0, io.EOF
	}
	n, err := c.ReadCloser.Read(p)
	c.bytes += n
	c.capture(p[:n])
	return n, err
}

func (c *capturedReadCloser) capture(data []byte) {
	remaining := bodyLimit - c.buffer.Len()
	if remaining > 0 {
		c.buffer.Write(data[:min(remaining, len(data))])
	}
	if len(data) > remaining {
		c.truncated = true
	}
}

type capturedResponseWriter struct {
	http.ResponseWriter
	status    int
	buffer    bytes.Buffer
	bytes     int
	truncated bool
}

func (w *capturedResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *capturedResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.bytes += len(data)
	remaining := bodyLimit - w.buffer.Len()
	if remaining > 0 {
		w.buffer.Write(data[:min(remaining, len(data))])
	}
	if len(data) > remaining {
		w.truncated = true
	}
	return w.ResponseWriter.Write(data)
}

func redactHeaders(header http.Header) map[string][]string {
	out := make(map[string][]string)
	for key, values := range header {
		if sensitiveKey(key) {
			out[key] = []string{"[REDACTED]"}
		} else {
			out[key] = append([]string(nil), values...)
		}
	}
	return out
}

func redactValues(values url.Values) url.Values {
	out := make(url.Values, len(values))
	for key, items := range values {
		if sensitiveKey(key) {
			out[key] = []string{"[REDACTED]"}
		} else {
			out[key] = append([]string(nil), items...)
		}
	}
	return out
}

func decodeBody(contentType string, body []byte) any {
	if len(body) == 0 {
		return nil
	}
	mediaType, _, _ := mime.ParseMediaType(contentType)
	switch mediaType {
	case "application/json", "application/problem+json":
		var value any
		if json.Unmarshal(body, &value) == nil {
			return redactValue(value)
		}
	case "application/x-www-form-urlencoded":
		if values, err := url.ParseQuery(string(body)); err == nil {
			return redactValues(values)
		}
	}
	if utf8.Valid(body) {
		return string(body)
	}
	return map[string]string{"base64": base64.StdEncoding.EncodeToString(body)}
}

func redactValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			if sensitiveKey(key) {
				out[key] = "[REDACTED]"
			} else {
				out[key] = redactValue(item)
			}
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = redactValue(item)
		}
		return out
	default:
		return value
	}
}

func sensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "-", "_"), " ", "_"))
	if normalized == "token" || normalized == "authorization" || normalized == "cookie" || normalized == "set_cookie" || normalized == "password" {
		return true
	}
	for _, fragment := range []string{"access_token", "refresh_token", "client_secret", "api_key", "private_key"} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

func newRunID() (string, error) {
	random := make([]byte, 4)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("request log: generate run id: %w", err)
	}
	return time.Now().UTC().Format("20060102T150405.000000000Z") + "-" + hex.EncodeToString(random), nil
}

package specserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ExerciseAllOperations hits every compiled operation without auth (401) and
// with a synthetic token (not a router 404). Git-required GitHub routes should
// already have been dropped from the compiled spec.
func ExerciseAllOperations(t *testing.T, handler http.Handler, spec *CompiledSpec, token string) {
	t.Helper()
	if spec == nil || spec.Index == nil {
		t.Fatal("compiled spec is required")
	}
	ops := spec.Index.Operations()
	if len(ops) < 4 {
		t.Fatalf("compiled operation count = %d, want the full vendor catalog", len(ops))
	}
	for _, op := range ops {
		if op.OperationID == "" {
			t.Fatalf("%s %s has no operationId", op.Method, op.Path)
		}
		path := InstantiatePath(op.Path)
		name := op.Method + " " + op.Path
		t.Run(name, func(t *testing.T) {
			rt, _, ok := spec.Index.find(op.Method, path)
			if !ok {
				t.Fatalf("index missed %s %s (instantiated %s)", op.Method, op.Path, path)
			}
			unauthorized := doExercise(t, handler, op.Method, path, "", "")
			if unauthorized.Code != http.StatusUnauthorized {
				t.Fatalf("unauthorized = %d %s", unauthorized.Code, unauthorized.Body.String())
			}
			authed := doExercise(t, handler, op.Method, path, "{}", token)
			if authed.Code == http.StatusMethodNotAllowed {
				t.Fatalf("method not allowed for %s %s", op.Method, path)
			}
			if authed.Code >= 500 {
				t.Fatalf("%s %s = %d %s", op.Method, path, authed.Code, authed.Body.String())
			}
			if authed.Code == http.StatusNotFound && !rt.item {
				t.Fatalf("router missed %s %s: %s", op.Method, path, authed.Body.String())
			}
		})
	}
}

func doExercise(t *testing.T, handler http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" && method != http.MethodGet && method != http.MethodHead && method != http.MethodDelete && method != http.MethodOptions {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Api-Key", token)
		req.Header.Set("X-Figma-Token", token)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

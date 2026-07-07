package fly

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dumbmachine/fabricate/mockd/mock"
)

const fixtureJSON = `{
  "org": "cd-org",
  "apps": [{
    "id":"web","name":"web","status":"deployed","network":"default",
    "machines":[{"id":"m-web-1","name":"web-1","state":"started","region":"iad"}],
    "certificates":[
      {"id":"cert-1","hostname":"app.example.com","configured":false,"dns_configured":false,
       "validation_errors":["A record does not point to the app"],"expires_in_days":30},
      {"id":"cert-2","hostname":"api.example.com","configured":true,"dns_configured":true,
       "validation_errors":[],"expires_in_days":7}
    ]
  }]
}`

func setup(t *testing.T) *mock.Service {
	t.Helper()
	svc := New()
	if err := svc.Init(":memory:", []byte(fixtureJSON)); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return svc
}

func do(t *testing.T, svc *mock.Service, method, path, body string) (int, []byte) {
	t.Helper()
	rec := httptest.NewRecorder()
	svc.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec.Code, rec.Body.Bytes()
}

func TestAppsScopedByOrg(t *testing.T) {
	svc := setup(t)

	code, body := do(t, svc, "GET", "/v1/apps?org_slug=cd-org", "")
	if code != 200 || !strings.Contains(string(body), `"total_apps":1`) {
		t.Fatalf("apps = %d %s", code, body)
	}

	code, body = do(t, svc, "GET", "/v1/apps?org_slug=someone-else", "")
	if code != 200 || !strings.Contains(string(body), `"total_apps":0`) {
		t.Fatalf("foreign org apps = %d %s", code, body)
	}
}

func TestCertificateCheckMaterializesExpiry(t *testing.T) {
	svc := setup(t)

	code, body := do(t, svc, "GET", "/v1/apps/web/certificates/api.example.com/check", "")
	if code != 200 {
		t.Fatalf("check status %d", code)
	}
	var check struct {
		DNSConfigured    bool     `json:"dns_configured"`
		ValidationErrors []string `json:"validation_errors"`
		ExpiresAt        string   `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &check); err != nil {
		t.Fatalf("decode check: %v (%s)", err, body)
	}
	expiresAt, err := time.Parse(time.RFC3339, check.ExpiresAt)
	if err != nil {
		t.Fatalf("expires_at %q: %v", check.ExpiresAt, err)
	}
	daysToExpiry := time.Until(expiresAt).Hours() / 24
	if daysToExpiry < 6 || daysToExpiry > 8 {
		t.Fatalf("expires_at %s is %.1f days out, want ~7", check.ExpiresAt, daysToExpiry)
	}
}

func TestMachineCreateAndCertificateLifecycle(t *testing.T) {
	svc := setup(t)

	code, _ := do(t, svc, "POST", "/v1/apps/web/machines", `{"region":"fra"}`)
	if code != 200 {
		t.Fatalf("create machine status %d", code)
	}
	_, body := do(t, svc, "GET", "/v1/apps/web/machines", "")
	var machines []struct {
		ID string `json:"id"`
	}
	json.Unmarshal(body, &machines)
	if len(machines) != 2 {
		t.Fatalf("machines = %d, want 2", len(machines))
	}

	// Re-adding a failing hostname returns a healthy certificate.
	code, _ = do(t, svc, "POST", "/v1/apps/web/certificates", `{"hostname":"app.example.com"}`)
	if code != 201 {
		t.Fatalf("create certificate status %d", code)
	}
	_, body = do(t, svc, "GET", "/v1/apps/web/certificates/app.example.com/check", "")
	if !strings.Contains(string(body), `"validation_errors":[]`) || !strings.Contains(string(body), `"dns_configured":true`) {
		t.Fatalf("check after re-add = %s", body)
	}

	code, _ = do(t, svc, "DELETE", "/v1/apps/web/certificates/api.example.com", "")
	if code != 200 {
		t.Fatalf("delete certificate status %d", code)
	}
	_, body = do(t, svc, "GET", "/v1/apps/web/certificates", "")
	if strings.Contains(string(body), "api.example.com") {
		t.Fatalf("certificates after delete = %s", body)
	}
}

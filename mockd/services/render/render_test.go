package render

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dumbmachine/fabricate/mockd/mock"
)

const fixtureJSON = `{
  "owner": {"id":"tea-cd","name":"Cloud Doctor","email":"team@example.com","type":"team"},
  "services": [{
    "id":"srv-web","name":"web","type":"web_service",
    "customDomains":[{"id":"dom-1","name":"app.example.com","verificationStatus":"unverified"}],
    "envVars":[{"key":"STRIPE_SECRET_KEY","value":"sk_live_x"},{"key":"LOG_LEVEL","value":"info"}]
  }],
  "postgres": [{
    "id":"pg-open","name":"prod-db","plan":"pro_4gb",
    "ipAllowList":[{"cidrBlock":"0.0.0.0/0","description":"everywhere"}],
    "highAvailabilityEnabled":false
  }],
  "keyValue": [{
    "id":"kv-open","name":"cache","plan":"starter",
    "ipAllowList":[{"cidrBlock":"0.0.0.0/0","description":"everywhere"}]
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

func TestOwnersAndServiceLists(t *testing.T) {
	svc := setup(t)

	code, body := do(t, svc, "GET", "/v1/owners?limit=100", "")
	if code != 200 || !strings.Contains(string(body), `"tea-cd"`) {
		t.Fatalf("owners = %d %s", code, body)
	}

	code, body = do(t, svc, "GET", "/v1/services?limit=100", "")
	if code != 200 {
		t.Fatalf("services status %d", code)
	}
	var services []struct {
		Service struct {
			ID string `json:"id"`
		} `json:"service"`
		Cursor string `json:"cursor"`
	}
	if err := json.Unmarshal(body, &services); err != nil {
		t.Fatalf("decode services: %v (%s)", err, body)
	}
	if len(services) != 1 || services[0].Service.ID != "srv-web" || services[0].Cursor == "" {
		t.Fatalf("services = %+v", services)
	}
}

func TestCustomDomainVerifyTakesEffect(t *testing.T) {
	svc := setup(t)

	code, _ := do(t, svc, "POST", "/v1/services/srv-web/custom-domains/dom-1/verify", "")
	if code != 200 {
		t.Fatalf("verify status %d", code)
	}
	_, body := do(t, svc, "GET", "/v1/services/srv-web/custom-domains?limit=100", "")
	if !strings.Contains(string(body), `"verified"`) || strings.Contains(string(body), `"unverified"`) {
		t.Fatalf("domains after verify = %s", body)
	}
}

func TestEnvVarDeleteTakesEffect(t *testing.T) {
	svc := setup(t)

	code, _ := do(t, svc, "DELETE", "/v1/services/srv-web/env-vars/STRIPE_SECRET_KEY", "")
	if code != 204 {
		t.Fatalf("delete env var status %d", code)
	}
	_, body := do(t, svc, "GET", "/v1/services/srv-web/env-vars?limit=100", "")
	if strings.Contains(string(body), "STRIPE_SECRET_KEY") || !strings.Contains(string(body), "LOG_LEVEL") {
		t.Fatalf("env vars after delete = %s", body)
	}
}

func TestPostgresAndKeyValuePatchTakeEffect(t *testing.T) {
	svc := setup(t)

	code, _ := do(t, svc, "PATCH", "/v1/postgres/pg-open",
		`{"ipAllowList":[{"cidrBlock":"203.0.113.0/24","description":"office"}],"highAvailabilityEnabled":true}`)
	if code != 200 {
		t.Fatalf("patch postgres status %d", code)
	}
	_, body := do(t, svc, "GET", "/v1/postgres?limit=100", "")
	if strings.Contains(string(body), "0.0.0.0/0") || !strings.Contains(string(body), `"highAvailabilityEnabled":true`) {
		t.Fatalf("postgres after patch = %s", body)
	}

	code, _ = do(t, svc, "PATCH", "/v1/key-value/kv-open", `{"ipAllowList":[]}`)
	if code != 200 {
		t.Fatalf("patch key value status %d", code)
	}
	_, body = do(t, svc, "GET", "/v1/key-value?limit=100", "")
	if strings.Contains(string(body), "0.0.0.0/0") {
		t.Fatalf("key value after patch = %s", body)
	}
}

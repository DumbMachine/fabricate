package supabase

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dumbmachine/fabricate/mockd/mock"
)

const fixtureJSON = `{
  "projects": [{
    "id": "refweb0000000000001",
    "organization_id": "org-cd",
    "name": "web",
    "region": "us-east-1",
    "status": "ACTIVE_HEALTHY",
    "lints": [
      {"name":"rls_disabled_in_public","level":"ERROR","metadata":{"schema":"public","name":"users","type":"table"}},
      {"name":"security_definer_view","level":"WARN","metadata":{"schema":"public","name":"admin_view","type":"view"}}
    ],
    "auth": {"mailer_autoconfirm": true, "external_anonymous_users_enabled": true, "mailer_otp_exp": 86400},
    "network": {"entitlement":"allowed","config":{"dbAllowedCidrs":["0.0.0.0/0"],"dbAllowedCidrsV6":["::/0"]},"status":"applied"},
    "ssl": {"currentConfig":{"database":false},"appliedSuccessfully":true},
    "legacyApiKeys": {"enabled": true},
    "backups": {"pitr_enabled": false, "walg_enabled": false}
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

func lintNames(t *testing.T, svc *mock.Service) []string {
	t.Helper()
	code, body := do(t, svc, "GET", "/v1/projects/refweb0000000000001/advisors/security", "")
	if code != 200 {
		t.Fatalf("advisors status %d", code)
	}
	var response struct {
		Lints []struct {
			Name string `json:"name"`
		} `json:"lints"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode advisors: %v (%s)", err, body)
	}
	names := make([]string, 0, len(response.Lints))
	for _, l := range response.Lints {
		names = append(names, l.Name)
	}
	return names
}

func TestListProjectsAndAdvisors(t *testing.T) {
	svc := setup(t)

	code, body := do(t, svc, "GET", "/v1/projects", "")
	if code != 200 {
		t.Fatalf("projects status %d", code)
	}
	var projects []struct {
		ID             string `json:"id"`
		OrganizationID string `json:"organization_id"`
		Name           string `json:"name"`
	}
	if err := json.Unmarshal(body, &projects); err != nil {
		t.Fatalf("decode projects: %v (%s)", err, body)
	}
	if len(projects) != 1 || projects[0].Name != "web" || projects[0].OrganizationID != "org-cd" {
		t.Fatalf("projects = %+v", projects)
	}

	if names := lintNames(t, svc); len(names) != 2 {
		t.Fatalf("lints = %v, want 2", names)
	}

	code, _ = do(t, svc, "GET", "/v1/projects/missing/advisors/security", "")
	if code != 404 {
		t.Fatalf("missing project advisors status %d, want 404", code)
	}
}

func TestAuthConfigPatchTakesEffect(t *testing.T) {
	svc := setup(t)

	code, _ := do(t, svc, "PATCH", "/v1/projects/refweb0000000000001/config/auth",
		`{"mailer_autoconfirm": false, "mailer_otp_exp": 3600}`)
	if code != 200 {
		t.Fatalf("patch auth status %d", code)
	}

	code, body := do(t, svc, "GET", "/v1/projects/refweb0000000000001/config/auth", "")
	if code != 200 {
		t.Fatalf("get auth status %d", code)
	}
	var auth struct {
		MailerAutoconfirm             bool `json:"mailer_autoconfirm"`
		ExternalAnonymousUsersEnabled bool `json:"external_anonymous_users_enabled"`
		MailerOtpExp                  int  `json:"mailer_otp_exp"`
	}
	json.Unmarshal(body, &auth)
	if auth.MailerAutoconfirm || auth.MailerOtpExp != 3600 {
		t.Fatalf("auth after patch = %+v", auth)
	}
	if !auth.ExternalAnonymousUsersEnabled {
		t.Fatalf("untouched field external_anonymous_users_enabled should stay true")
	}
}

func TestNetworkSslAndLegacyKeyWrites(t *testing.T) {
	svc := setup(t)

	code, body := do(t, svc, "POST", "/v1/projects/refweb0000000000001/network-restrictions/apply",
		`{"dbAllowedCidrs":["203.0.113.0/24"],"dbAllowedCidrsV6":[]}`)
	if code != 201 {
		t.Fatalf("apply network status %d (%s)", code, body)
	}
	code, body = do(t, svc, "GET", "/v1/projects/refweb0000000000001/network-restrictions", "")
	var network struct {
		Config struct {
			DbAllowedCidrs []string `json:"dbAllowedCidrs"`
		} `json:"config"`
	}
	json.Unmarshal(body, &network)
	if code != 200 || len(network.Config.DbAllowedCidrs) != 1 || network.Config.DbAllowedCidrs[0] != "203.0.113.0/24" {
		t.Fatalf("network after apply = %s", body)
	}

	code, _ = do(t, svc, "PUT", "/v1/projects/refweb0000000000001/ssl-enforcement",
		`{"requestedConfig":{"database":true}}`)
	if code != 200 {
		t.Fatalf("put ssl status %d", code)
	}
	_, body = do(t, svc, "GET", "/v1/projects/refweb0000000000001/ssl-enforcement", "")
	if !strings.Contains(string(body), `"database":true`) {
		t.Fatalf("ssl after put = %s", body)
	}

	code, _ = do(t, svc, "PUT", "/v1/projects/refweb0000000000001/api-keys/legacy", `{"enabled":false}`)
	if code != 200 {
		t.Fatalf("put legacy keys status %d", code)
	}
	_, body = do(t, svc, "GET", "/v1/projects/refweb0000000000001/api-keys/legacy", "")
	if !strings.Contains(string(body), `"enabled":false`) {
		t.Fatalf("legacy keys after put = %s", body)
	}
}

func TestDatabaseQueryClearsMatchingLints(t *testing.T) {
	svc := setup(t)

	code, _ := do(t, svc, "POST", "/v1/projects/refweb0000000000001/database/query",
		`{"query":"ALTER TABLE public.users ENABLE ROW LEVEL SECURITY;"}`)
	if code != 201 {
		t.Fatalf("query status %d", code)
	}
	names := lintNames(t, svc)
	if len(names) != 1 || names[0] != "security_definer_view" {
		t.Fatalf("lints after RLS fix = %v, want [security_definer_view]", names)
	}

	// A query about an unrelated table must not clear anything.
	do(t, svc, "POST", "/v1/projects/refweb0000000000001/database/query",
		`{"query":"ALTER TABLE public.orders ENABLE ROW LEVEL SECURITY;"}`)
	if names := lintNames(t, svc); len(names) != 1 {
		t.Fatalf("unrelated fix cleared lints: %v", names)
	}

	do(t, svc, "POST", "/v1/projects/refweb0000000000001/database/query",
		`{"query":"CREATE OR REPLACE VIEW public.admin_view WITH (security_invoker = true) AS SELECT 1;"}`)
	if names := lintNames(t, svc); len(names) != 0 {
		t.Fatalf("lints after view fix = %v, want none", names)
	}
}

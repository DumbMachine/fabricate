package cloudflare

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dumbmachine/fabricate/mockd/mock"
)

const fixtureJSON = `{
  "accounts": [{"id":"acc-1","name":"Acme","enforceTwofactor":false}],
  "zones": [{
    "id":"zone-1","name":"insecure.example","plan":"free",
    "settings":{"ssl":"flexible","always_use_https":"off","min_tls_version":"1.0"},
    "dnssecStatus":"disabled",
    "universalSslEnabled":true,
    "phaseRules":{}
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

func envelopeResult(t *testing.T, body []byte) json.RawMessage {
	t.Helper()
	var envelope struct {
		Success bool            `json:"success"`
		Result  json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode envelope: %v (%s)", err, body)
	}
	if !envelope.Success {
		t.Fatalf("success=false: %s", body)
	}
	return envelope.Result
}

func TestVerifyTokenAndListZones(t *testing.T) {
	svc := setup(t)

	code, body := do(t, svc, "GET", "/user/tokens/verify", "")
	if code != 200 {
		t.Fatalf("verify status %d", code)
	}
	var verify struct {
		Status string `json:"status"`
	}
	json.Unmarshal(envelopeResult(t, body), &verify)
	if verify.Status != "active" {
		t.Fatalf("token status %q", verify.Status)
	}

	code, body = do(t, svc, "GET", "/zones?page=1&per_page=50", "")
	if code != 200 {
		t.Fatalf("zones status %d", code)
	}
	var zones []struct {
		Name string `json:"name"`
		Plan struct {
			LegacyID string `json:"legacy_id"`
		} `json:"plan"`
	}
	json.Unmarshal(envelopeResult(t, body), &zones)
	if len(zones) != 1 || zones[0].Name != "insecure.example" || zones[0].Plan.LegacyID != "free" {
		t.Fatalf("zones = %+v", zones)
	}
}

func TestMissingPhaseEntrypoint404(t *testing.T) {
	svc := setup(t)
	code, _ := do(t, svc, "GET", "/zones/zone-1/rulesets/phases/http_ratelimit/entrypoint", "")
	if code != 404 {
		t.Fatalf("status %d, want 404", code)
	}
}

// The fix loop: PATCH the ssl setting, and the next bulk settings read reflects it.
func TestPatchSetting_MutatesState(t *testing.T) {
	svc := setup(t)

	sslValue := func() string {
		_, body := do(t, svc, "GET", "/zones/zone-1/settings", "")
		var settings []struct {
			ID    string          `json:"id"`
			Value json.RawMessage `json:"value"`
		}
		json.Unmarshal(envelopeResult(t, body), &settings)
		for _, setting := range settings {
			if setting.ID == "ssl" {
				return string(setting.Value)
			}
		}
		return ""
	}

	if got := sslValue(); got != `"flexible"` {
		t.Fatalf("initial ssl = %s", got)
	}
	code, _ := do(t, svc, "PATCH", "/zones/zone-1/settings/ssl", `{"value":"strict"}`)
	if code != 200 {
		t.Fatalf("patch status %d", code)
	}
	if got := sslValue(); got != `"strict"` {
		t.Fatalf("ssl after patch = %s (patch must persist)", got)
	}
}

func TestPutEntrypointThenGet(t *testing.T) {
	svc := setup(t)
	code, _ := do(t, svc, "PUT", "/zones/zone-1/rulesets/phases/http_ratelimit/entrypoint",
		`{"rules":[{"enabled":true,"action":"block","expression":"true"}]}`)
	if code != 200 {
		t.Fatalf("put status %d", code)
	}
	code, body := do(t, svc, "GET", "/zones/zone-1/rulesets/phases/http_ratelimit/entrypoint", "")
	if code != 200 {
		t.Fatalf("get status %d", code)
	}
	var entrypoint struct {
		Rules []struct {
			Enabled bool `json:"enabled"`
		} `json:"rules"`
	}
	json.Unmarshal(envelopeResult(t, body), &entrypoint)
	if len(entrypoint.Rules) != 1 || !entrypoint.Rules[0].Enabled {
		t.Fatalf("entrypoint rules = %+v", entrypoint.Rules)
	}
}

func TestPatchAccount2fa(t *testing.T) {
	svc := setup(t)
	code, _ := do(t, svc, "PATCH", "/accounts/acc-1", `{"settings":{"enforce_twofactor":true}}`)
	if code != 200 {
		t.Fatalf("patch status %d", code)
	}
	_, body := do(t, svc, "GET", "/accounts", "")
	var accounts []struct {
		Settings struct {
			EnforceTwofactor bool `json:"enforce_twofactor"`
		} `json:"settings"`
	}
	json.Unmarshal(envelopeResult(t, body), &accounts)
	if len(accounts) != 1 || !accounts[0].Settings.EnforceTwofactor {
		t.Fatalf("accounts after patch = %+v", accounts)
	}
}

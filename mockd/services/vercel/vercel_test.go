package vercel

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dumbmachine/fabricate/mockd/mock"
)

const fixtureJSON = `{
  "user": {"id":"user-1","username":"neeraj"},
  "teams": [{"id":"team-1","slug":"acme","name":"Acme","samlConfigured":true,"samlEnforced":false}],
  "projects": [{
    "id":"prj-web","name":"web",
    "ssoProtection":null,"passwordProtection":null,
    "gitForkProtection":false,"directoryListing":true,"protectedSourcemaps":false,
    "deploymentExpiration":null,
    "env":[{"id":"env-1","key":"STRIPE_SECRET_KEY","type":"plain","target":["production"]}]
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

func firstProject(t *testing.T, svc *mock.Service) map[string]json.RawMessage {
	t.Helper()
	code, body := do(t, svc, "GET", "/v10/projects?limit=100&teamId=team-1", "")
	if code != 200 {
		t.Fatalf("projects status %d", code)
	}
	var response struct {
		Projects []map[string]json.RawMessage `json:"projects"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode projects: %v (%s)", err, body)
	}
	if len(response.Projects) != 1 {
		t.Fatalf("projects = %d, want 1", len(response.Projects))
	}
	return response.Projects[0]
}

func TestUserAndTeam(t *testing.T) {
	svc := setup(t)

	code, body := do(t, svc, "GET", "/v2/user", "")
	if code != 200 {
		t.Fatalf("user status %d", code)
	}
	var user struct {
		User struct {
			Username string `json:"username"`
		} `json:"user"`
	}
	json.Unmarshal(body, &user)
	if user.User.Username != "neeraj" {
		t.Fatalf("username %q", user.User.Username)
	}

	code, body = do(t, svc, "GET", "/v2/teams/team-1", "")
	if code != 200 {
		t.Fatalf("team status %d", code)
	}
	var team struct {
		Saml struct {
			Enforced   bool            `json:"enforced"`
			Connection json.RawMessage `json:"connection"`
		} `json:"saml"`
	}
	json.Unmarshal(body, &team)
	if team.Saml.Enforced || len(team.Saml.Connection) == 0 {
		t.Fatalf("saml = %+v (want configured, not enforced)", team.Saml)
	}
}

// The fix loop: PATCH the project's fork protection and the next list reflects it.
func TestPatchProject_MutatesState(t *testing.T) {
	svc := setup(t)

	if got := string(firstProject(t, svc)["gitForkProtection"]); got != "false" {
		t.Fatalf("initial gitForkProtection = %s", got)
	}

	code, _ := do(t, svc, "PATCH", "/v9/projects/prj-web", `{"gitForkProtection":true,"directoryListing":false}`)
	if code != 200 {
		t.Fatalf("patch status %d", code)
	}

	project := firstProject(t, svc)
	if string(project["gitForkProtection"]) != "true" || string(project["directoryListing"]) != "false" {
		t.Fatalf("after patch = fork %s, listing %s (patch must persist)",
			project["gitForkProtection"], project["directoryListing"])
	}
}

func TestPatchEnvType(t *testing.T) {
	svc := setup(t)

	code, _ := do(t, svc, "PATCH", "/v9/projects/prj-web/env/env-1", `{"type":"sensitive"}`)
	if code != 200 {
		t.Fatalf("patch env status %d", code)
	}

	_, body := do(t, svc, "GET", "/v9/projects/prj-web/env?teamId=team-1", "")
	var response struct {
		Envs []struct {
			Key  string `json:"key"`
			Type string `json:"type"`
		} `json:"envs"`
	}
	json.Unmarshal(body, &response)
	if len(response.Envs) != 1 || response.Envs[0].Type != "sensitive" {
		t.Fatalf("envs after patch = %+v", response.Envs)
	}
}

func TestUnknownProject404(t *testing.T) {
	svc := setup(t)
	code, _ := do(t, svc, "GET", "/v9/projects/nope/env", "")
	if code != 404 {
		t.Fatalf("status %d, want 404", code)
	}
}

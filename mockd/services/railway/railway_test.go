package railway

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dumbmachine/fabricate/mockd/mock"
)

const fixtureJSON = `{
  "me": {"id":"user-cd","name":"Cloud Doctor","email":"cd@example.com"},
  "workspaces": [{"id":"ws-cd","name":"acme-core","enforceTwoFactor":false,
    "members":[{"id":"user-cd","email":"cd@example.com","role":"ADMIN"},
               {"id":"user-2","email":"two@example.com","role":"ADMIN"}]}],
  "projects": [{
    "id":"prj-1","name":"storefront",
    "environments":[{"id":"env-prod","name":"production"}],
    "services":[
      {"id":"svc-postgres","name":"Postgres",
       "tcpProxies":[{"id":"proxy-1","proxyPort":32001,"applicationPort":5432,"domain":"roundhouse.proxy.rlwy.net"}],
       "variables":{}},
      {"id":"svc-web","name":"web","tcpProxies":[],
       "variables":{"STRIPE_SECRET_KEY":"sk_live_x","PORT":"3000"}}
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

func gql(t *testing.T, svc *mock.Service, query string, variables map[string]any) (int, []byte) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	rec := httptest.NewRecorder()
	svc.ServeHTTP(rec, httptest.NewRequest("POST", "/graphql/v2", strings.NewReader(string(body))))
	return rec.Code, rec.Body.Bytes()
}

func TestMeAndProjectTree(t *testing.T) {
	svc := setup(t)

	code, body := gql(t, svc, "query { me { id name email } }", nil)
	if code != 200 || !strings.Contains(string(body), `"user-cd"`) {
		t.Fatalf("me = %d %s", code, body)
	}

	_, body = gql(t, svc, `query { projects { edges { node { id name
    services { edges { node { id name } } }
    environments { edges { node { id name } } } } } } }`, nil)
	if !strings.Contains(string(body), `"svc-postgres"`) || !strings.Contains(string(body), `"env-prod"`) {
		t.Fatalf("projects = %s", body)
	}
}

func TestTcpProxyQueryAndDeleteMutation(t *testing.T) {
	svc := setup(t)

	proxiesQuery := `query($environmentId: String!, $serviceId: String!) {
    tcpProxies(environmentId: $environmentId, serviceId: $serviceId) { id proxyPort applicationPort domain } }`
	proxyVariables := map[string]any{"environmentId": "env-prod", "serviceId": "svc-postgres"}

	_, body := gql(t, svc, proxiesQuery, proxyVariables)
	if !strings.Contains(string(body), `"proxy-1"`) {
		t.Fatalf("tcpProxies = %s", body)
	}

	_, body = gql(t, svc, `mutation($id: String!) { tcpProxyDelete(id: $id) }`,
		map[string]any{"id": "proxy-1"})
	if !strings.Contains(string(body), `"tcpProxyDelete":true`) {
		t.Fatalf("tcpProxyDelete = %s", body)
	}

	_, body = gql(t, svc, proxiesQuery, proxyVariables)
	if strings.Contains(string(body), `"proxy-1"`) {
		t.Fatalf("proxy still present after delete: %s", body)
	}
}

func TestVariablesQueryAndDeleteMutation(t *testing.T) {
	svc := setup(t)

	variablesQuery := `query($projectId: String!, $environmentId: String!, $serviceId: String!) {
    variables(projectId: $projectId, environmentId: $environmentId, serviceId: $serviceId) }`
	queryVariables := map[string]any{
		"projectId": "prj-1", "environmentId": "env-prod", "serviceId": "svc-web",
	}

	_, body := gql(t, svc, variablesQuery, queryVariables)
	if !strings.Contains(string(body), "STRIPE_SECRET_KEY") {
		t.Fatalf("variables = %s", body)
	}

	_, body = gql(t, svc, `mutation($input: VariableDeleteInput!) { variableDelete(input: $input) }`,
		map[string]any{"input": map[string]any{
			"projectId": "prj-1", "environmentId": "env-prod",
			"serviceId": "svc-web", "name": "STRIPE_SECRET_KEY",
		}})
	if !strings.Contains(string(body), `"variableDelete":true`) {
		t.Fatalf("variableDelete = %s", body)
	}

	_, body = gql(t, svc, variablesQuery, queryVariables)
	if strings.Contains(string(body), "STRIPE_SECRET_KEY") || !strings.Contains(string(body), "PORT") {
		t.Fatalf("variables after delete = %s", body)
	}
}

func TestWorkspacesAndMembers(t *testing.T) {
	svc := setup(t)

	_, body := gql(t, svc, "query { workspaces { id name enforceTwoFactor } }", nil)
	if !strings.Contains(string(body), `"enforceTwoFactor":false`) {
		t.Fatalf("workspaces = %s", body)
	}

	_, body = gql(t, svc, "query { workspaces { id name members { id email role } } }", nil)
	if !strings.Contains(string(body), `"role":"ADMIN"`) {
		t.Fatalf("members = %s", body)
	}
}

package linear

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dumbmachine/fabricate/mockd/mock"
)

const fixtureJSON = `{
  "viewer": {"id":"usr-1","name":"Val Ops","email":"val@acme.dev"},
  "teams": [{"id":"team-eng","key":"ENG","name":"Engineering"}],
  "users": [
    {"id":"usr-1","name":"Val Ops","email":"val@acme.dev"},
    {"id":"usr-2","name":"Kim Rivera","email":"kim@acme.dev"}
  ],
  "workflowStates": [
    {"id":"st-backlog","teamId":"team-eng","name":"Backlog","type":"backlog","position":0},
    {"id":"st-progress","teamId":"team-eng","name":"In Progress","type":"started","position":2},
    {"id":"st-done","teamId":"team-eng","name":"Done","type":"completed","position":4}
  ],
  "issues": [
    {"id":"iss-101","teamId":"team-eng","number":101,"title":"Checkout 500s under load",
     "description":"Spikes at 100 rps.","priority":1,"stateId":"st-progress",
     "assigneeId":"usr-2","createdAt":"2026-06-20T10:00:00Z"},
    {"id":"iss-102","teamId":"team-eng","number":102,"title":"Dark mode flickers",
     "priority":3,"stateId":"st-backlog","createdAt":"2026-06-21T09:00:00Z"}
  ],
  "comments": [
    {"id":"cmt-1","issueId":"iss-101","userId":"usr-2","body":"Repro'd on staging.",
     "createdAt":"2026-06-20T12:00:00Z"}
  ]
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
	svc.ServeHTTP(rec, httptest.NewRequest("POST", "/graphql", strings.NewReader(string(body))))
	return rec.Code, rec.Body.Bytes()
}

func TestViewerTeamsAndStates(t *testing.T) {
	svc := setup(t)

	code, body := gql(t, svc, "query { viewer { id name email } }", nil)
	if code != 200 || !strings.Contains(string(body), `"val@acme.dev"`) {
		t.Fatalf("viewer = %d %s", code, body)
	}

	_, body = gql(t, svc, "query { teams { nodes { id key name } } }", nil)
	if !strings.Contains(string(body), `"ENG"`) {
		t.Fatalf("teams = %s", body)
	}

	_, body = gql(t, svc, "query { workflowStates { nodes { id name type } } }", nil)
	if !strings.Contains(string(body), `"st-progress"`) || !strings.Contains(string(body), `"started"`) {
		t.Fatalf("workflowStates = %s", body)
	}
}

func TestIssuesQueryAndFilters(t *testing.T) {
	svc := setup(t)

	_, body := gql(t, svc, "query { issues { nodes { id identifier title state { name } assignee { name } } } }", nil)
	if !strings.Contains(string(body), `"ENG-101"`) || !strings.Contains(string(body), `"ENG-102"`) {
		t.Fatalf("issues = %s", body)
	}

	// state name filter
	_, body = gql(t, svc, "query($filter: IssueFilter) { issues(filter: $filter) { nodes { id } } }",
		map[string]any{"filter": map[string]any{"state": map[string]any{"name": map[string]any{"eq": "In Progress"}}}})
	if !strings.Contains(string(body), "iss-101") || strings.Contains(string(body), "iss-102") {
		t.Fatalf("state filter = %s", body)
	}

	// unassigned filter
	_, body = gql(t, svc, "query($filter: IssueFilter) { issues(filter: $filter) { nodes { id } } }",
		map[string]any{"filter": map[string]any{"assignee": map[string]any{"null": true}}})
	if !strings.Contains(string(body), "iss-102") || strings.Contains(string(body), "iss-101") {
		t.Fatalf("assignee null filter = %s", body)
	}

	// priority filter (urgent+high = lte 2)
	_, body = gql(t, svc, "query($filter: IssueFilter) { issues(filter: $filter) { nodes { id } } }",
		map[string]any{"filter": map[string]any{"priority": map[string]any{"lte": 2}}})
	if !strings.Contains(string(body), "iss-101") || strings.Contains(string(body), "iss-102") {
		t.Fatalf("priority filter = %s", body)
	}
}

func TestSingleIssueByIdOrIdentifier(t *testing.T) {
	svc := setup(t)

	_, body := gql(t, svc, "query($id: String!) { issue(id: $id) { id identifier comments { nodes { body } } } }",
		map[string]any{"id": "iss-101"})
	if !strings.Contains(string(body), "Repro'd on staging.") {
		t.Fatalf("issue by id = %s", body)
	}

	_, body = gql(t, svc, "query($id: String!) { issue(id: $id) { id title } }",
		map[string]any{"id": "ENG-102"})
	if !strings.Contains(string(body), "Dark mode flickers") {
		t.Fatalf("issue by identifier = %s", body)
	}
}

func TestIssueCreateAssignsNextIdentifier(t *testing.T) {
	svc := setup(t)

	_, body := gql(t, svc, "mutation($input: IssueCreateInput!) { issueCreate(input: $input) { success issue { id identifier state { name } } } }",
		map[string]any{"input": map[string]any{
			"teamId": "team-eng", "title": "Rate-limit the webhook endpoint", "priority": 2,
		}})
	if !strings.Contains(string(body), `"success":true`) || !strings.Contains(string(body), `"ENG-103"`) {
		t.Fatalf("issueCreate = %s", body)
	}
	// Default state = lowest-position (Backlog).
	if !strings.Contains(string(body), `"Backlog"`) {
		t.Fatalf("issueCreate default state = %s", body)
	}

	// Visible on the next list.
	_, body = gql(t, svc, "query { issues { nodes { identifier } } }", nil)
	if !strings.Contains(string(body), `"ENG-103"`) {
		t.Fatalf("created issue missing from list = %s", body)
	}
}

func TestIssueUpdateMovesState(t *testing.T) {
	svc := setup(t)

	_, body := gql(t, svc, "mutation($id: String!, $input: IssueUpdateInput!) { issueUpdate(id: $id, input: $input) { success issue { state { name } assignee { name } } } }",
		map[string]any{"id": "ENG-102", "input": map[string]any{"stateId": "st-progress", "assigneeId": "usr-1"}})
	if !strings.Contains(string(body), `"In Progress"`) || !strings.Contains(string(body), `"Val Ops"`) {
		t.Fatalf("issueUpdate = %s", body)
	}

	_, body = gql(t, svc, "mutation($id: String!, $input: IssueUpdateInput!) { issueUpdate(id: $id, input: $input) { success issue { id } } }",
		map[string]any{"id": "iss-102", "input": map[string]any{"stateId": "st-nope"}})
	if !strings.Contains(string(body), "unknown stateId") {
		t.Fatalf("bad stateId = %s", body)
	}
}

func TestCommentCreatePostsAsViewer(t *testing.T) {
	svc := setup(t)

	_, body := gql(t, svc, "mutation($input: CommentCreateInput!) { commentCreate(input: $input) { success comment { id body } } }",
		map[string]any{"input": map[string]any{"issueId": "iss-101", "body": "Fix shipped in #4821."}})
	if !strings.Contains(string(body), `"success":true`) {
		t.Fatalf("commentCreate = %s", body)
	}

	_, body = gql(t, svc, "query($id: String!) { issue(id: $id) { comments { nodes { body user { name } } } } }",
		map[string]any{"id": "iss-101"})
	if !strings.Contains(string(body), "Fix shipped in #4821.") || !strings.Contains(string(body), `"Val Ops"`) {
		t.Fatalf("comment not visible on issue = %s", body)
	}
}

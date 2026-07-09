package googleplay

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dumbmachine/fabricate/mockd/mock"
)

const fixtureJSON = `{
  "apps": [
    {"packageName":"com.acme.shopping","displayName":"Acme Shopping"},
    {"packageName":"com.acme.fit","displayName":"Acme Fitness"}
  ],
  "reviews": [
    {"reviewId":"r1","packageName":"com.acme.shopping","author":"A","text":"crash on login","starRating":1,"language":"en","device":"Pixel 7","appVersion":"4.2.1","lastModified":1780000001},
    {"reviewId":"r2","packageName":"com.acme.shopping","author":"B","text":"checkout broken","starRating":2,"language":"en","device":"Pixel 6","appVersion":"4.2.1","lastModified":1780000002},
    {"reviewId":"r3","packageName":"com.acme.shopping","author":"C","text":"love it","starRating":5,"language":"en","device":"Pixel 8","appVersion":"4.2.1","lastModified":1780000003,"developerReply":"thanks!","replyModified":1780000100}
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

func do(t *testing.T, svc *mock.Service, method, path, body string) (int, []byte) {
	t.Helper()
	rec := httptest.NewRecorder()
	svc.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec.Code, rec.Body.Bytes()
}

func tally(t *testing.T, body []byte) (total, answered int) {
	t.Helper()
	var resp struct {
		Reviews []struct {
			Comments []map[string]json.RawMessage `json:"comments"`
		} `json:"reviews"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode reviews: %v (%s)", err, body)
	}
	for _, r := range resp.Reviews {
		total++
		for _, c := range r.Comments {
			if _, ok := c["developerComment"]; ok {
				answered++
			}
		}
	}
	return total, answered
}

func TestAppsSearch(t *testing.T) {
	svc := setup(t)
	code, body := do(t, svc, "GET", "/v1beta1/apps:search?pageSize=50", "")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	var resp struct {
		Apps []struct {
			Name        string `json:"name"`
			PackageName string `json:"packageName"`
			DisplayName string `json:"displayName"`
		} `json:"apps"`
	}
	json.Unmarshal(body, &resp)
	if len(resp.Apps) != 2 || resp.Apps[0].Name != "apps/com.acme.fit" {
		t.Fatalf("apps:search = %+v (want 2, sorted, name=apps/<pkg>)", resp.Apps)
	}
	// The real App resource carries name, packageName, and displayName —
	// the official client reads packageName directly (see e2e/sdk).
	if resp.Apps[0].PackageName != "com.acme.fit" {
		t.Fatalf("apps:search[0].packageName = %q (want com.acme.fit)", resp.Apps[0].PackageName)
	}
}

// The core stateful behavior: replying to a review actually flips it to
// answered on the next read.
func TestReply_MutatesBacklog(t *testing.T) {
	svc := setup(t)
	const list = "/androidpublisher/v3/applications/com.acme.shopping/reviews?maxResults=100"

	if total, answered := tallyGet(t, svc, list); total != 3 || answered != 1 {
		t.Fatalf("initial = %d reviews, %d answered; want 3, 1", total, answered)
	}

	code, _ := do(t, svc, "POST", "/androidpublisher/v3/applications/com.acme.shopping/reviews/r1:reply", `{"replyText":"Sorry — a fix for the login crash is rolling out in 4.2.2."}`)
	if code != 200 {
		t.Fatalf("reply status %d", code)
	}

	// state changed: r1 is now answered too.
	if total, answered := tallyGet(t, svc, list); total != 3 || answered != 2 {
		t.Fatalf("after reply = %d reviews, %d answered; want 3, 2 (reply must persist)", total, answered)
	}
}

func tallyGet(t *testing.T, svc *mock.Service, path string) (int, int) {
	t.Helper()
	code, body := do(t, svc, "GET", path, "")
	if code != 200 {
		t.Fatalf("list status %d", code)
	}
	return tally(t, body)
}

func TestReply_UnknownReview404(t *testing.T) {
	svc := setup(t)
	code, _ := do(t, svc, "POST", "/androidpublisher/v3/applications/com.acme.shopping/reviews/nope:reply", `{"replyText":"x"}`)
	if code != 404 {
		t.Fatalf("status %d, want 404", code)
	}
}

func TestReviews_Paginate(t *testing.T) {
	svc := setup(t)
	base := "/androidpublisher/v3/applications/com.acme.shopping/reviews"
	code, body := do(t, svc, "GET", base+"?maxResults=2", "")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	var p1 struct {
		Reviews         []json.RawMessage `json:"reviews"`
		TokenPagination struct {
			NextPageToken string `json:"nextPageToken"`
		} `json:"tokenPagination"`
	}
	json.Unmarshal(body, &p1)
	if len(p1.Reviews) != 2 || p1.TokenPagination.NextPageToken != "2" {
		t.Fatalf("page1 = %d reviews, token %q; want 2, \"2\"", len(p1.Reviews), p1.TokenPagination.NextPageToken)
	}
	_, body2 := do(t, svc, "GET", base+"?maxResults=2&token=2", "")
	var p2 struct {
		Reviews         []json.RawMessage `json:"reviews"`
		TokenPagination struct {
			NextPageToken string `json:"nextPageToken"`
		} `json:"tokenPagination"`
	}
	json.Unmarshal(body2, &p2)
	if len(p2.Reviews) != 1 || p2.TokenPagination.NextPageToken != "" {
		t.Fatalf("page2 = %d reviews, token %q; want 1, \"\"", len(p2.Reviews), p2.TokenPagination.NextPageToken)
	}
}

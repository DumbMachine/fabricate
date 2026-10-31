package metaads_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	httpengine "github.com/dumbmachine/fabricate/engine/http"
	proxyengine "github.com/dumbmachine/fabricate/engine/http/proxy"
	"github.com/dumbmachine/fabricate/resources/meta-ads"
)

func TestBaselinesReadWriteIsolationAndProxy(t *testing.T) {
	resource := metaads.NewResource()
	docs, err := resource.ScenarioDocuments()
	if err != nil {
		t.Fatal(err)
	}
	for _, doc := range docs {
		t.Run(doc.ID, func(t *testing.T) {
			ctx := context.Background()
			if err := resource.Scenarios().Validate(ctx, doc); err != nil {
				t.Fatal(err)
			}
			var state struct {
				Accounts []struct {
					ID string `json:"id"`
				}
				Adsets []struct {
					ID          string `json:"id"`
					DailyBudget string `json:"daily_budget"`
				}
			}
			if err = json.Unmarshal(doc.State, &state); err != nil {
				t.Fatal(err)
			}
			a, err := httpengine.StartService(ctx, "primary", filepath.Join(t.TempDir(), "a"), resource, doc, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close(ctx)
			b, err := httpengine.StartService(ctx, "control", filepath.Join(t.TempDir(), "b"), resource, doc, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close(ctx)
			account, adset := state.Accounts[0].ID, state.Adsets[0].ID
			call := func(service *httpengine.Service, method, path, body, token string) (int, map[string]any) {
				t.Helper()
				request, _ := http.NewRequest(method, service.URL+"/v23.0"+path, strings.NewReader(body))
				request.Header.Set("Authorization", "Bearer "+token)
				request.Header.Set("Content-Type", "application/json")
				response, err := http.DefaultClient.Do(request)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				raw, _ := io.ReadAll(response.Body)
				var value map[string]any
				_ = json.Unmarshal(raw, &value)
				return response.StatusCode, value
			}
			if status, initial := call(a, "GET", "/"+adset, "", a.Token); status != 200 || initial["daily_budget"] != state.Adsets[0].DailyBudget {
				t.Fatal(status, initial)
			}
			if status, _ := call(a, "POST", "/"+adset, `{"daily_budget":6400}`, a.Token); status != 200 {
				t.Fatal(status)
			}
			if status, after := call(a, "GET", "/"+adset, "", a.Token); status != 200 || after["daily_budget"] != "6400" {
				t.Fatal(status, after)
			}
			if status, control := call(b, "GET", "/"+adset, "", b.Token); status != 200 || control["daily_budget"] != state.Adsets[0].DailyBudget {
				t.Fatal("cross-instance state leak", status, control)
			}
			if status, _ := call(b, "GET", "/"+adset, "", a.Token); status != 401 {
				t.Fatal("cross-instance token admitted", status)
			}
			for _, test := range []struct {
				method, path, body string
				status             int
			}{
				{"POST", "/" + adset, `{"daily_budget":-1}`, 400},
				{"POST", "/" + adset, `{"daily_budget":6400,"status":"PAUSED"}`, 400},
				{"POST", "/" + account, `{"daily_budget":6400}`, 404},
				{"GET", "/" + account + "/ads", "", 404},
				{"GET", "/" + account + "/adsets?fields=targeting", "", 400},
				{"GET", "/" + account + "/insights?level=campaign&time_range=%7B%7D", "", 400},
				{"GET", "/" + account + "/insights?level=adset&time_range=%7B%7D", "", 400},
			} {
				if status, _ := call(a, test.method, test.path, test.body, a.Token); status != test.status {
					t.Fatalf("%s %s = %d, want %d", test.method, test.path, status, test.status)
				}
			}
			window := url.QueryEscape(`{"since":"2026-10-01","until":"2026-10-01"}`)
			if status, value := call(a, "GET", "/"+account+"/insights?level=adset&time_range="+window, "", a.Token); status != 200 {
				t.Fatal(status, value)
			} else if doc.ID == "meta-ads.food-publisher.v1" && len(value["data"].([]any)) != 3 {
				t.Fatal("date filtering failed", value)
			}
			dumped, err := resource.Scenarios().Dump(ctx, a.State.DB(), doc.Metadata())
			if err != nil {
				t.Fatal(err)
			}
			if err = resource.Scenarios().Validate(ctx, dumped); err != nil {
				t.Fatal(err)
			}
			proxy, err := proxyengine.Start(filepath.Join(t.TempDir(), "proxy"), []proxyengine.Route{{Host: "graph.facebook.com", PathPrefix: "/", Target: a.URL, Token: a.Token}}, nil, proxyengine.Options{RejectUnknown: true})
			if err != nil {
				t.Fatal(err)
			}
			defer proxy.Close(ctx)
			command := exec.Command("curl", "--silent", "--show-error", "--fail", "--noproxy", "", "--proxy", proxy.URL, "--cacert", proxy.CAPath, "https://graph.facebook.com/v23.0/"+adset)
			raw, err := command.Output()
			if err != nil || !strings.Contains(string(raw), `"daily_budget":"6400"`) {
				t.Fatalf("normal-host proxy read: %v %s", err, raw)
			}
		})
	}
}

func TestInvalidRelationshipsFailBeforeLoading(t *testing.T) {
	r := metaads.NewResource()
	doc, err := r.Scenario("meta-ads.minimal.v1")
	if err != nil {
		t.Fatal(err)
	}
	var state map[string][]map[string]any
	_ = json.Unmarshal(doc.State, &state)
	state["adsets"][0]["campaign_id"] = "absent"
	doc.State, _ = json.Marshal(state)
	if err = r.Scenarios().Validate(context.Background(), doc); err == nil {
		t.Fatal("dangling campaign accepted")
	}
	_ = fmt.Sprint(err)
}

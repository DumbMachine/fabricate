package all

import (
	"bytes"
	"context"
	"encoding/json"
	httpengine "github.com/dumbmachine/fabricate/engine/http"
	proxyengine "github.com/dumbmachine/fabricate/engine/http/proxy"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSocialPublishingDirectAndProxy(t *testing.T) {
	for _, id := range []string{"linkedin", "x", "instagram"} {
		t.Run(id, func(t *testing.T) {
			for instance := 0; instance < 2; instance++ {
				ctx := context.Background()
				r, _ := Registry().Get(id)
				doc, e := r.Scenario(id + ".acme-social.v1")
				if e != nil {
					t.Fatal(e)
				}
				svc, e := httpengine.StartService(ctx, id, filepath.Join(t.TempDir(), "svc"), r, doc, nil)
				if e != nil {
					t.Fatal(e)
				}
				defer svc.Close(ctx)
				call := func(method, path string, v any) (int, http.Header, map[string]any) {
					var b []byte
					if v != nil {
						b, _ = json.Marshal(v)
					}
					req, _ := http.NewRequest(method, svc.URL+path, bytes.NewReader(b))
					req.Header.Set("Authorization", "Bearer "+svc.Token)
					req.Header.Set("Content-Type", "application/json")
					res, e := http.DefaultClient.Do(req)
					if e != nil {
						t.Fatal(e)
					}
					defer res.Body.Close()
					raw, _ := io.ReadAll(res.Body)
					m := map[string]any{}
					json.Unmarshal(raw, &m)
					return res.StatusCode, res.Header, m
				}
				status, _, _ := call("GET", "/unsupported", nil)
				if status != 404 {
					t.Fatalf("unsupported=%d", status)
				}
				req, _ := http.NewRequest("GET", svc.URL+"/unsupported", nil)
				res, e := http.DefaultClient.Do(req)
				if e != nil {
					t.Fatal(e)
				}
				res.Body.Close()
				if res.StatusCode != 401 {
					t.Fatal("missing auth admitted")
				}
				var postID, read string
				switch id {
				case "linkedin":
					status, h, _ := call("POST", "/rest/posts", map[string]any{"author": "urn:li:organization:104812345", "commentary": "A measured launch.", "visibility": "PUBLIC", "distribution": map[string]any{"feedDistribution": "MAIN_FEED", "targetEntities": []any{}, "thirdPartyDistributionChannels": []any{}}, "lifecycleState": "PUBLISHED", "isReshareDisabledByAuthor": false})
					if status != 201 {
						t.Fatalf("create=%d", status)
					}
					postID = h.Get("x-restli-id")
					read = "/rest/posts/" + url.PathEscape(postID)
				case "x":
					status, _, m := call("POST", "/2/tweets", map[string]any{"text": "A measured launch."})
					if status != 201 {
						t.Fatalf("create=%d", status)
					}
					postID = m["data"].(map[string]any)["id"].(string)
					read = "/2/tweets/" + postID
					status, _, _ = call("POST", "/2/tweets", map[string]any{"text": ""})
					if status != 400 {
						t.Fatal("empty copy accepted")
					}
				case "instagram":
					status, _, m := call("POST", "/v25.0/17841400000000001/media", map[string]any{"image_url": "https://images.example/launch.jpg", "caption": "A measured launch."})
					if status != 200 {
						t.Fatalf("container=%d", status)
					}
					container := m["id"].(string)
					status, _, m = call("POST", "/v25.0/17841400000000001/media_publish", map[string]any{"creation_id": container})
					if status != 200 {
						t.Fatalf("publish=%d", status)
					}
					postID = m["id"].(string)
					read = "/v25.0/" + postID
					status, _, _ = call("POST", "/v25.0/17841400000000001/media_publish", map[string]any{"creation_id": container})
					if status != 400 {
						t.Fatal("duplicate publication accepted")
					}
				}
				status, _, m := call("GET", read, nil)
				if status != 200 || len(m) == 0 {
					t.Fatal("readback failed")
				}
				dump, e := r.Scenarios().Dump(ctx, svc.State.DB(), doc.Metadata())
				if e != nil {
					t.Fatal(e)
				}
				if e = r.Scenarios().Validate(ctx, dump); e != nil {
					t.Fatal(e)
				}
				host := r.Descriptor().ProviderHosts[0]
				p, e := proxyengine.Start(filepath.Join(t.TempDir(), "proxy"), []proxyengine.Route{{Host: host, PathPrefix: "/", Target: svc.URL, Token: svc.Token}}, nil, proxyengine.Options{RejectUnknown: true})
				if e != nil {
					t.Fatal(e)
				}
				defer p.Close(ctx)
				cmd := exec.Command("curl", "--silent", "--show-error", "--fail", "--noproxy", "", "--proxy", p.URL, "--cacert", p.CAPath, "https://"+host+read)
				raw, e := cmd.Output()
				if e != nil {
					t.Fatalf("normal-host curl: %v", e)
				}
				if !bytes.Contains(raw, []byte(postID)) {
					t.Fatal("proxy readback mismatch")
				}
			}
		})
	}
}

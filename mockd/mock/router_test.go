package mock

import (
	"reflect"
	"testing"
)

func TestRouter_CustomMethodsAndColonIDs(t *testing.T) {
	var got string
	mk := func(name string) Handler { return func(c *Ctx) error { got = name; return nil } }

	var rt router
	rt.handle("GET", "/v1beta1/apps:search", mk("search"))
	rt.handle("GET", "/androidpublisher/v3/applications/{packageName}/reviews", mk("list"))
	rt.handle("GET", "/androidpublisher/v3/applications/{packageName}/reviews/{reviewId}", mk("get"))
	rt.handle("POST", "/androidpublisher/v3/applications/{packageName}/reviews/{reviewId}:reply", mk("reply"))

	cases := []struct {
		method, path, want string
		params             map[string]string
	}{
		{"GET", "/v1beta1/apps:search", "search", map[string]string{}},
		{"GET", "/androidpublisher/v3/applications/com.acme.shopping/reviews", "list",
			map[string]string{"packageName": "com.acme.shopping"}},
		// review id with internal colons stays intact on a verb-less route
		{"GET", "/androidpublisher/v3/applications/com.acme.shopping/reviews/gp:AOq:Xy", "get",
			map[string]string{"packageName": "com.acme.shopping", "reviewId": "gp:AOq:Xy"}},
		// the :reply custom method strips only the trailing verb, keeping the colon id
		{"POST", "/androidpublisher/v3/applications/com.acme.shopping/reviews/gp:AOq:Xy:reply", "reply",
			map[string]string{"packageName": "com.acme.shopping", "reviewId": "gp:AOq:Xy"}},
	}
	for _, tc := range cases {
		got = ""
		h, params, ok := rt.match(tc.method, tc.path)
		if !ok {
			t.Fatalf("%s %s: no match", tc.method, tc.path)
		}
		_ = h(&Ctx{})
		if got != tc.want {
			t.Errorf("%s %s → route %q, want %q", tc.method, tc.path, got, tc.want)
		}
		if !reflect.DeepEqual(params, tc.params) {
			t.Errorf("%s %s → params %v, want %v", tc.method, tc.path, params, tc.params)
		}
	}

	// a GET on the reply path must NOT match (wrong method), and an unknown path misses.
	if _, _, ok := rt.match("GET", "/nope"); ok {
		t.Error("unexpected match for /nope")
	}
}

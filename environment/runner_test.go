package environment

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/all"
)

func TestHostPrefixNormalizesSharedHosts(t *testing.T) {
	descriptor := httpresource.Descriptor{HostPrefixes: map[string]string{
		"www.zohoapis.com":   "books",
		"www.googleapis.com": "/gmail/",
	}}
	if got := hostPrefix(descriptor, "www.zohoapis.com"); got != "/books/" {
		t.Fatalf("zoho prefix = %q", got)
	}
	if got := hostPrefix(descriptor, "www.googleapis.com"); got != "/gmail/" {
		t.Fatalf("gmail prefix = %q", got)
	}
	if got := hostPrefix(descriptor, "api.razorpay.com"); got != "/" {
		t.Fatalf("default prefix = %q", got)
	}
}

func TestRuntimeServesAcmeGmailThroughTransparentProxy(t *testing.T) {
	t.Setenv("FAB_LOG_DIR", t.TempDir())
	registry := all.Registry()
	spec, err := Parse([]byte(`apiVersion: fabricate.dev/v1alpha1
kind: Environment
metadata: {name: acme-gmail}
services:
  support-mail: {resource: gmail, scenario: gmail.acme-corp.v1}
`))
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := Start(context.Background(), spec, registry, true)
	if err != nil {
		t.Fatal(err)
	}
	stateDir := runtime.StateDir
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	client := runtimeProxyClient(t, runtime)

	tokenResponse, err := client.Post("https://oauth2.googleapis.com/token", "application/x-www-form-urlencoded",
		strings.NewReader("grant_type=refresh_token&refresh_token=existing"))
	if err != nil {
		t.Fatal(err)
	}
	tokenBody, _ := io.ReadAll(tokenResponse.Body)
	tokenResponse.Body.Close()
	if !strings.Contains(string(tokenBody), runtime.Services["support-mail"].Token) {
		t.Fatalf("OAuth token does not match service token: %s", tokenBody)
	}

	request, _ := http.NewRequest(http.MethodGet, "https://gmail.googleapis.com/gmail/v1/users/me/profile", nil)
	request.Header.Set("Authorization", "Bearer anything")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `"messagesTotal":28`) {
		t.Fatalf("profile = %d %s", response.StatusCode, body)
	}
	shared, _ := http.NewRequest(http.MethodGet, "https://www.googleapis.com/gmail/v1/users/me/profile", nil)
	sharedResponse, err := client.Do(shared)
	if err != nil {
		t.Fatal(err)
	}
	sharedBody, _ := io.ReadAll(sharedResponse.Body)
	sharedResponse.Body.Close()
	if sharedResponse.StatusCode != http.StatusOK || !strings.Contains(string(sharedBody), `"messagesTotal":28`) {
		t.Fatalf("shared-host profile = %d %s", sharedResponse.StatusCode, sharedBody)
	}
	logPath := runtime.Requests.Path()
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Fatalf("state directory still exists: %v", err)
	}
	logBody, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("request log was not retained: %v", err)
	}
	if !strings.Contains(string(logBody), `"messagesTotal":28`) || strings.Contains(string(logBody), runtime.Services["support-mail"].Token) {
		t.Fatalf("request log missing payload or leaked token: %s", logBody)
	}
}

func TestCheckedInEnvironmentsStart(t *testing.T) {
	matches, err := filepath.Glob(filepath.Join("..", "environments", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatal("no checked-in environment manifests")
	}
	registry := all.Registry()
	for _, path := range matches {
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Setenv("FAB_LOG_DIR", t.TempDir())
			spec, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			runtime, err := Start(context.Background(), spec, registry, false)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = runtime.Close(context.Background()) })
			if got, want := len(runtime.Services), len(spec.Services); got != want {
				t.Fatalf("started %d services, want %d", got, want)
			}
			for name, service := range runtime.Services {
				if service.URL == "" || service.Token == "" {
					t.Fatalf("service %q missing URL or token", name)
				}
			}
		})
	}
}

func TestRuntimeServesAcmeSupportDeskAcrossServices(t *testing.T) {
	t.Setenv("FAB_LOG_DIR", t.TempDir())
	spec, err := Load(filepath.Join("..", "environments", "acme-support-desk.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := Start(context.Background(), spec, all.Registry(), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	for _, name := range []string{"support-mail", "inbox", "board", "crm"} {
		if runtime.Services[name] == nil {
			t.Fatalf("missing service %q (%d started)", name, len(runtime.Services))
		}
	}
	client := runtimeProxyClient(t, runtime)
	token := func(name string) string { return runtime.Services[name].Token }

	mail := authorizedGET(t, client, "https://gmail.googleapis.com/gmail/v1/users/me/messages/msg-0001", token("support-mail"))
	assertContains(t, mail, "gmail INV-4812", "INV-4812", "dana@northwind.example")

	inbox := authorizedGET(t, client, "https://api.intercom.io/conversations/101", token("inbox"))
	assertContains(t, inbox, "intercom INV-4812", "INV-4812", "contact-dana")

	ssoInbox := authorizedGET(t, client, "https://api.intercom.io/conversations/102", token("inbox"))
	assertContains(t, ssoInbox, "intercom Contoso", "Contoso", "contoso-eu")

	board := authorizedGET(t, client, "https://app.asana.com/api/1.0/tasks/task-double-charge", token("board"))
	assertContains(t, board, "asana INV-4812", "INV-4812", "Northwind")

	ssoTask := authorizedGET(t, client, "https://app.asana.com/api/1.0/tasks/task-sso", token("board"))
	assertContains(t, ssoTask, "asana Contoso", "contoso-eu")

	stories := authorizedGET(t, client, "https://app.asana.com/api/1.0/tasks/task-sso/stories", token("board"))
	assertContains(t, stories, "asana Mei Chen comment", "mei.chen@contoso.example")

	deal := authorizedGET(t, client, "https://api.hubapi.com/crm/v3/objects/deals/301", token("crm"))
	assertContains(t, deal, "hubspot INV-4812", "INV-4812", "Northwind")

	mei := authorizedGET(t, client, "https://api.hubapi.com/crm/v3/objects/contacts/103", token("crm"))
	assertContains(t, mei, "hubspot Mei Chen", "mei.chen@contoso.example")

	listed := authorizedGET(t, http.DefaultClient, runtime.Services["support-mail"].URL+"/gmail/v1/users/me/messages?q=INV-4812", token("support-mail"))
	assertContains(t, listed, "direct Gmail search", "msg-0001")
}

func TestRuntimeServesAcmeBillingOps(t *testing.T) {
	t.Setenv("FAB_LOG_DIR", t.TempDir())
	spec, err := Load(filepath.Join("..", "environments", "acme-billing-ops.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := Start(context.Background(), spec, all.Registry(), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	for _, name := range []string{"support-mail", "inbox", "crm"} {
		if runtime.Services[name] == nil {
			t.Fatalf("missing service %q (%d started)", name, len(runtime.Services))
		}
	}
	client := runtimeProxyClient(t, runtime)
	mail := authorizedGET(t, client, "https://gmail.googleapis.com/gmail/v1/users/me/messages/msg-0001", runtime.Services["support-mail"].Token)
	inbox := authorizedGET(t, client, "https://api.intercom.io/conversations/101", runtime.Services["inbox"].Token)
	deal := authorizedGET(t, client, "https://api.hubapi.com/crm/v3/objects/deals/301", runtime.Services["crm"].Token)
	assertContains(t, mail, "billing-ops gmail", "INV-4812")
	assertContains(t, inbox, "billing-ops intercom", "INV-4812")
	assertContains(t, deal, "billing-ops hubspot", "INV-4812")
}

func TestRuntimeServesAcmeGoodsShop(t *testing.T) {
	t.Setenv("FAB_LOG_DIR", t.TempDir())
	spec, err := Load(filepath.Join("..", "environments", "acme-goods-shop.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := Start(context.Background(), spec, all.Registry(), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	client := runtimeProxyClient(t, runtime)
	token := func(name string) string { return runtime.Services[name].Token }

	order := authorizedGET(t, client, "https://acme-goods.myshopify.com/admin/api/2024-10/orders/10482.json", token("shopify"))
	assertContains(t, order, "shopify 10482", "10482", "priya@fernworks.example", "AG-TEE-02")

	payment := authorizedGET(t, client, "https://api.razorpay.com/v1/payments/pay_Acme10482", token("razorpay"))
	assertContains(t, payment, "razorpay 10482", "pay_Acme10482", "309700")

	shipment := authorizedGET(t, client, "https://apiv2.shiprocket.in/v1/external/courier/track/awb/SR10482AWB", token("shiprocket"))
	assertContains(t, shipment, "shiprocket 10482", "SR10482AWB", "Delhivery")

	salesOrder := authorizedGET(t, client, "https://www.zohoapis.com/inventory/v1/salesorders/so-10482?organization_id=org-acme", token("zohoinventory"))
	assertContains(t, salesOrder, "inventory 10482", "SO-10482", "AG-MUG-01")

	invoice := authorizedGET(t, client, "https://www.zohoapis.com/books/v3/invoices/INV-10482?organization_id=org-acme", token("zohobooks"))
	assertContains(t, invoice, "books 10482", "INV-10482")
}

func TestRuntimeServesAcmeCommerceAgency(t *testing.T) {
	t.Setenv("FAB_LOG_DIR", t.TempDir())
	spec, err := Load(filepath.Join("..", "environments", "acme-commerce-agency.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := Start(context.Background(), spec, all.Registry(), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	client := runtimeProxyClient(t, runtime)
	token := runtime.Services["shopify"].Token

	goods := authorizedGET(t, client, "https://acme-goods.myshopify.com/admin/api/2024-10/orders/10482.json", token)
	northwind := authorizedGET(t, client, "https://northwind-market.myshopify.com/admin/api/2024-10/orders/10490.json", token)
	tiny := authorizedGET(t, client, "https://tinyshop.myshopify.com/admin/api/2024-10/orders/10491.json", token)
	assertContains(t, goods, "agency acme goods", "10482", "priya@fernworks.example")
	assertContains(t, northwind, "agency northwind", "10490", "jules@northwind.example")
	assertContains(t, tiny, "agency tinyshop", "10491", "anita.desai@consumer.example")

	action := authorizedGET(t, client, "https://api.impact.com/Advertisers/acct-acme/Actions?CampaignId=1001&Oid=10482", runtime.Services["impact"].Token)
	assertContains(t, action, "impact 10482", "act_10482", "partner_northwind")
}

func TestRuntimeServesAcmeCompanyOps(t *testing.T) {
	t.Setenv("FAB_LOG_DIR", t.TempDir())
	spec, err := Load(filepath.Join("..", "environments", "acme-company-ops.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := Start(context.Background(), spec, all.Registry(), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	client := runtimeProxyClient(t, runtime)
	token := func(name string) string { return runtime.Services[name].Token }

	mail := authorizedGET(t, client, "https://gmail.googleapis.com/gmail/v1/users/me/messages/msg-0001", token("support-mail"))
	invoice := authorizedGET(t, client, "https://www.zohoapis.com/books/v3/invoices/INV-4812?organization_id=org-acme", token("zohobooks"))
	subscription := authorizedGET(t, client, "https://acme.chargebee.com/api/v2/subscriptions/sub_northwind_checkout", token("chargebee"))
	request := authorizedGET(t, client, "https://acme.atlassian.net/rest/servicedeskapi/request/ITSM-4812", token("jsm"))
	user := authorizedGET(t, client, "https://acme.okta.com/api/v1/users/00u-val", token("okta"))
	file := authorizedGET(t, client, "https://www.googleapis.com/drive/v3/files/file-inv-4812-northwind", token("googledrive"))
	assertContains(t, mail, "company gmail", "INV-4812")
	assertContains(t, invoice, "company books", "INV-4812")
	assertContains(t, subscription, "company chargebee", "sub_northwind_checkout")
	assertContains(t, request, "company jsm", "ITSM-4812")
	assertContains(t, user, "company okta", "val@acme.example")
	assertContains(t, file, "company drive", "INV-4812-northwind.pdf")
}

func authorizedGET(t *testing.T, client *http.Client, rawURL, token string) string {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("%s = %d %s", rawURL, response.StatusCode, body)
	}
	return string(body)
}

func assertContains(t *testing.T, body, label string, needles ...string) {
	t.Helper()
	for _, needle := range needles {
		if !strings.Contains(body, needle) {
			t.Fatalf("%s missing %q: %s", label, needle, body)
		}
	}
}

func runtimeProxyClient(t *testing.T, runtime *Runtime) *http.Client {
	t.Helper()
	caPEM, err := os.ReadFile(runtime.Proxy.CAPath)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(caPEM)
	proxyURL, _ := url.Parse(runtime.Proxy.URL)
	return &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL), TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}}
}

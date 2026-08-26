package environment

import "testing"

func TestParseEnvironment(t *testing.T) {
	spec, err := Parse([]byte(`apiVersion: fabricate.dev/v1alpha1
kind: Environment
metadata:
  name: acme-gmail
services:
  support-mail:
    resource: gmail
    scenario: gmail.acme-corp.v1
proxy:
  enabled: true
`))
	if err != nil {
		t.Fatal(err)
	}
	if !spec.Proxy.Enabled || spec.Services["support-mail"].Scenario != "gmail.acme-corp.v1" {
		t.Fatalf("unexpected spec: %#v", spec)
	}
}

func TestParseEnvironmentRejectsUnknownField(t *testing.T) {
	_, err := Parse([]byte(`apiVersion: fabricate.dev/v1alpha1
kind: Environment
metadata: {name: acme-gmail}
services:
  mail: {resource: gmail, scenario: gmail.acme-corp.v1, typo: true}
`))
	if err == nil {
		t.Fatal("expected unknown field error")
	}
}

func TestValidateProxyMapping(t *testing.T) {
	spec := Spec{APIVersion: APIVersion, Kind: "Environment", Metadata: Metadata{Name: "acme"},
		Services: map[string]ServiceSpec{"mail": {Resource: "gmail", Scenario: "gmail.acme-corp.v1"}},
		Proxy:    ProxySpec{Hosts: map[string]string{"gmail.googleapis.com": "missing"}}}
	if err := spec.Validate(); err == nil {
		t.Fatal("expected unknown service error")
	}
}

func TestValidateProxyUnknownHostsPolicy(t *testing.T) {
	spec := Spec{APIVersion: APIVersion, Kind: "Environment", Metadata: Metadata{Name: "acme"},
		Services: map[string]ServiceSpec{"mail": {Resource: "gmail", Scenario: "gmail.acme-corp.v1"}},
		Proxy:    ProxySpec{}}
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}
	if spec.Proxy.RejectUnknownHosts() {
		t.Fatal("default policy unexpectedly rejected unknown hosts")
	}
	spec.Proxy.UnknownHosts = "reject"
	if !spec.Proxy.RejectUnknownHosts() {
		t.Fatal("reject policy was not enabled")
	}
	spec.Proxy.UnknownHosts = "anything"
	if err := spec.Validate(); err == nil {
		t.Fatal("expected unknown_hosts validation error")
	}
}

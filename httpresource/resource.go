// Package httpresource is the provider-independent contract implemented by
// compiled HTTP resources such as Gmail. It intentionally contains no CLI or
// supervisor concerns.
package httpresource

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/dumbmachine/fabricate/scenario"
)

type Descriptor struct {
	ID              string   `json:"id"`
	DisplayName     string   `json:"display_name"`
	Version         string   `json:"version"`
	OpenAPIVersion  string   `json:"openapi_version"`
	OpenAPIDigest   string   `json:"openapi_digest"`
	ScenarioVersion int      `json:"scenario_version"`
	ProviderHosts   []string `json:"provider_hosts"`
	// HostPrefixes narrows a shared provider host to a path prefix so two
	// resources can share a hostname. The key is the host; the value is a
	// prefix such as "/books/". Hosts absent from the map claim the whole host.
	HostPrefixes map[string]string `json:"host_prefixes,omitempty"`
	// TokenRoutes lists OAuth token endpoints the official client calls to
	// refresh an access token before using the API, such as
	// oauth2.googleapis.com /token for Google APIs. In proxy mode each is
	// answered by a local synthetic endpoint, so refresh tokens never leave the
	// machine. Resources sharing an identity provider declare the same route;
	// an environment installs it once.
	TokenRoutes []TokenRoute  `json:"token_routes,omitempty"`
	SDK         SDKDescriptor `json:"sdk"`
}

// TokenRoute is an OAuth token endpoint: an exact host and an exact path. The
// synthetic endpoint accepts only a form-encoded POST with
// grant_type=refresh_token and a non-empty refresh_token.
type TokenRoute struct {
	Host string `json:"host"`
	Path string `json:"path"`
}

// GoogleOAuthTokenRoute is the refresh endpoint used by Google's official API
// clients.
var GoogleOAuthTokenRoute = TokenRoute{Host: "oauth2.googleapis.com", Path: "/token"}

type SDKDescriptor struct {
	Package    string `json:"package,omitempty"`
	Language   string `json:"language,omitempty"`
	DirectTest bool   `json:"direct_test"`
	ProxyTest  bool   `json:"proxy_test"`
	Exception  string `json:"exception,omitempty"`
}

type Contract struct {
	OpenAPIJSON  []byte
	ScenarioJSON []byte
}

type Resource interface {
	Descriptor() Descriptor
	Contract() Contract
	Scenarios() ScenarioCodec
	ScenarioDocuments() ([]scenario.Document, error)
	Scenario(string) (scenario.Document, error)
	NewServer(context.Context, ServerDependencies) (Server, error)
}

type ScenarioCodec interface {
	Validate(context.Context, scenario.Document) error
	Initialize(context.Context, *sql.DB) error
	Load(context.Context, *sql.DB, scenario.Document) error
	Dump(context.Context, *sql.DB, scenario.Metadata) (scenario.Document, error)
}

type ServerDependencies struct {
	DB      *sql.DB
	Clock   Clock
	IDs     IDGenerator
	Secrets SecretSource
}

type Server interface {
	Handler() http.Handler
	Close(context.Context) error
}

type Clock interface {
	Now() time.Time
}

type IDGenerator interface {
	Next(context.Context, string) (string, error)
}

type SecretSource interface {
	Get(context.Context, string) (string, error)
}

type Event struct {
	Type    string
	Subject string
	Data    json.RawMessage
}

type FixedClock struct{ Time time.Time }

func (c FixedClock) Now() time.Time { return c.Time }

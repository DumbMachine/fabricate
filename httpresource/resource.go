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
	ID              string        `json:"id"`
	DisplayName     string        `json:"display_name"`
	Version         string        `json:"version"`
	OpenAPIVersion  string        `json:"openapi_version"`
	OpenAPIDigest   string        `json:"openapi_digest"`
	ScenarioVersion int           `json:"scenario_version"`
	ProviderHosts   []string      `json:"provider_hosts"`
	SDK             SDKDescriptor `json:"sdk"`
}

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

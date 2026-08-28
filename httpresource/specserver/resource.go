package specserver

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"sync"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/scenario"
)

// Bound is a complete httpresource.Resource backed by a vendor OpenAPI
// document and the generic spec server.
type Bound struct {
	id, display string
	hosts       []string
	spec        []byte
	schema      []byte
	scenarios   embed.FS
	drop        PathDrop

	once     sync.Once
	compiled *CompiledSpec
	codec    httpresource.ScenarioCodec
	initErr  error
}

func NewBound(id, display string, hosts []string, spec, scenarioSchema []byte, scenarios embed.FS, drop PathDrop) *Bound {
	return &Bound{
		id: id, display: display, hosts: hosts,
		spec: spec, schema: scenarioSchema, scenarios: scenarios, drop: drop,
	}
}

func (b *Bound) load() {
	b.once.Do(func() {
		compiled, err := CompileSpec(b.spec, b.drop)
		if err != nil {
			b.initErr = err
			return
		}
		b.compiled = compiled
		b.codec = NewCodec(b.id, b.schema)
	})
}

func (b *Bound) must() *CompiledSpec {
	b.load()
	if b.initErr != nil {
		panic(fmt.Sprintf("%s: %v", b.id, b.initErr))
	}
	return b.compiled
}

func (b *Bound) Descriptor() httpresource.Descriptor {
	compiled := b.must()
	return httpresource.Descriptor{
		ID: b.id, DisplayName: b.display, Version: "v1", OpenAPIVersion: compiled.Version,
		OpenAPIDigest: Digest(compiled.JSON), ScenarioVersion: 1,
		ProviderHosts: append([]string(nil), b.hosts...),
		SDK:           httpresource.SDKDescriptor{Package: "curl", Language: "http", DirectTest: true, ProxyTest: true},
	}
}

func (b *Bound) Contract() httpresource.Contract {
	compiled := b.must()
	return httpresource.Contract{OpenAPIJSON: append([]byte(nil), compiled.JSON...), ScenarioJSON: append([]byte(nil), b.schema...)}
}

func (b *Bound) Scenarios() httpresource.ScenarioCodec {
	b.load()
	if b.initErr != nil {
		panic(fmt.Sprintf("%s: %v", b.id, b.initErr))
	}
	return b.codec
}

func (b *Bound) ScenarioIDs() ([]string, error) {
	return scenario.EmbeddedIDs(b.scenarios)
}

func (b *Bound) Scenario(id string) (scenario.Document, error) {
	entries, err := b.scenarios.ReadDir("scenarios")
	if err != nil {
		return scenario.Document{}, fmt.Errorf("%s: list embedded scenarios: %w", b.id, err)
	}
	for _, entry := range entries {
		raw, err := b.scenarios.ReadFile("scenarios/" + entry.Name())
		if err != nil {
			return scenario.Document{}, fmt.Errorf("%s: read embedded scenario %s: %w", b.id, entry.Name(), err)
		}
		doc, err := scenario.Parse(raw)
		if err != nil {
			return scenario.Document{}, fmt.Errorf("%s: parse embedded scenario %s: %w", b.id, entry.Name(), err)
		}
		if doc.ID == id {
			return doc, nil
		}
	}
	return scenario.Document{}, fmt.Errorf("%s: unknown scenario %q", b.id, id)
}

func (b *Bound) NewServer(ctx context.Context, dependencies httpresource.ServerDependencies) (httpresource.Server, error) {
	b.load()
	if b.initErr != nil {
		return nil, b.initErr
	}
	return New(ctx, b.compiled, dependencies)
}

func (b *Bound) Compiled() *CompiledSpec { return b.must() }

func Digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

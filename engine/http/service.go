package httpengine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/requestlog"
	"github.com/dumbmachine/fabricate/scenario"
)

type Service struct {
	Name       string
	Resource   httpresource.Resource
	State      *ServiceState
	Server     httpresource.Server
	HTTPServer *http.Server
	Listener   net.Listener
	URL        string
	Token      string
	serveErr   chan error
	closeOnce  sync.Once
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

type serviceIDs struct {
	prefix string
	next   atomic.Uint64
}

func (g *serviceIDs) Next(_ context.Context, kind string) (string, error) {
	return fmt.Sprintf("fab-%s-%s-%06d", sanitizeID(kind), g.prefix, g.next.Add(1)), nil
}

type serviceSecrets map[string]string

func (s serviceSecrets) Get(_ context.Context, key string) (string, error) {
	value, ok := s[key]
	if !ok {
		return "", fmt.Errorf("http service: unknown secret %q", key)
	}
	return value, nil
}

func StartService(ctx context.Context, name, serviceDir string, resource httpresource.Resource, doc scenario.Document, requests *requestlog.Log) (*Service, error) {
	if resource == nil {
		return nil, fmt.Errorf("http service %q: resource is required", name)
	}
	state, err := (StateManager{}).Prepare(ctx, serviceDir, resource.Scenarios(), doc)
	if err != nil {
		return nil, fmt.Errorf("http service %q: prepare state: %w", name, err)
	}
	cleanupState := true
	defer func() {
		if cleanupState {
			_ = state.Close()
		}
	}()

	token, err := randomSecret(24)
	if err != nil {
		return nil, fmt.Errorf("http service %q: token: %w", name, err)
	}
	idPrefix, err := randomSecret(5)
	if err != nil {
		return nil, fmt.Errorf("http service %q: id prefix: %w", name, err)
	}
	resourceServer, err := resource.NewServer(ctx, httpresource.ServerDependencies{
		DB: state.DB(), Clock: systemClock{}, IDs: &serviceIDs{prefix: idPrefix},
		Secrets: serviceSecrets{"token": token},
	})
	if err != nil {
		return nil, fmt.Errorf("http service %q: construct server: %w", name, err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = resourceServer.Close(ctx)
		return nil, fmt.Errorf("http service %q: listen: %w", name, err)
	}
	handler := resourceServer.Handler()
	if requests != nil {
		handler = requests.Middleware(name, handler)
	}
	httpServer := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	service := &Service{
		Name: name, Resource: resource, State: state, Server: resourceServer,
		HTTPServer: httpServer, Listener: listener, URL: "http://" + listener.Addr().String(),
		Token: token, serveErr: make(chan error, 1),
	}
	go func() {
		err := httpServer.Serve(listener)
		if err == http.ErrServerClosed {
			err = nil
		}
		service.serveErr <- err
	}()
	cleanupState = false
	return service, nil
}

func (s *Service) Close(ctx context.Context) error {
	var closeErr error
	s.closeOnce.Do(func() {
		if err := s.HTTPServer.Shutdown(ctx); err != nil {
			closeErr = err
		}
		if err := s.Server.Close(ctx); closeErr == nil && err != nil {
			closeErr = err
		}
		if err := s.State.Close(); closeErr == nil && err != nil {
			closeErr = err
		}
	})
	return closeErr
}

func randomSecret(bytes int) (string, error) {
	raw := make([]byte, bytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func sanitizeID(value string) string {
	out := make([]byte, 0, len(value))
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			out = append(out, c)
		} else {
			out = append(out, '-')
		}
	}
	return string(out)
}

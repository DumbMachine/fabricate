// Package proxy provides Fabricate's process-scoped transparent HTTPS proxy.
// It intercepts explicitly declared provider hosts and tunnels every other
// destination unchanged unless the environment opts into strict rejection.
package proxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dumbmachine/fabricate/requestlog"
)

type Route struct {
	Host       string
	PathPrefix string
	Target     string
	Token      string
	OAuthToken bool
	Service    string
}

type compiledRoute struct {
	Route
	target *url.URL
}

type Proxy struct {
	listener      net.Listener
	server        *http.Server
	transport     *http.Transport
	routes        []compiledRoute
	passthrough   map[string]bool
	rejectUnknown bool
	requests      *requestlog.Log
	ca            *certificateAuthority
	CAPath        string
	URL           string
}

type Options struct {
	Passthrough   []string
	RejectUnknown bool
}

func Start(stateDir string, routes []Route, requests *requestlog.Log, options Options) (*Proxy, error) {
	if len(routes) == 0 {
		return nil, fmt.Errorf("proxy: at least one route is required")
	}
	compiled := make([]compiledRoute, 0, len(routes))
	for _, route := range routes {
		route.Host = canonicalHost(route.Host)
		if route.Host == "" {
			return nil, fmt.Errorf("proxy: route host is required")
		}
		if route.PathPrefix == "" {
			route.PathPrefix = "/"
		}
		entry := compiledRoute{Route: route}
		if !route.OAuthToken {
			target, err := url.Parse(route.Target)
			if err != nil || target.Scheme != "http" || target.Host == "" {
				return nil, fmt.Errorf("proxy: route %s target must be a local HTTP URL", route.Host)
			}
			entry.target = target
		}
		compiled = append(compiled, entry)
	}
	sort.SliceStable(compiled, func(i, j int) bool {
		return len(compiled[i].PathPrefix) > len(compiled[j].PathPrefix)
	})
	ca, err := newCertificateAuthority()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, fmt.Errorf("proxy: create state directory: %w", err)
	}
	caPath := filepath.Join(stateDir, "ca.pem")
	if err := os.WriteFile(caPath, ca.pem, 0o600); err != nil {
		return nil, fmt.Errorf("proxy: write CA: %w", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("proxy: listen: %w", err)
	}
	p := &Proxy{
		listener: listener, transport: &http.Transport{DisableCompression: true},
		routes: compiled, passthrough: make(map[string]bool, len(options.Passthrough)), rejectUnknown: options.RejectUnknown, requests: requests,
		ca: ca, CAPath: caPath, URL: "http://" + listener.Addr().String(),
	}
	for _, host := range options.Passthrough {
		host = canonicalHost(host)
		if host == "" {
			listener.Close()
			return nil, fmt.Errorf("proxy: passthrough host is required")
		}
		p.passthrough[host] = true
	}
	p.server = &http.Server{Handler: p, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = p.server.Serve(listener) }()
	return p, nil
}

func (p *Proxy) Environment() map[string]string {
	return map[string]string{
		"HTTP_PROXY": p.URL, "HTTPS_PROXY": p.URL,
		"http_proxy": p.URL, "https_proxy": p.URL,
		"NO_PROXY": "localhost,127.0.0.1", "no_proxy": "localhost,127.0.0.1",
		"SSL_CERT_FILE": p.CAPath, "NODE_EXTRA_CA_CERTS": p.CAPath,
		"REQUESTS_CA_BUNDLE": p.CAPath, "GIT_SSL_CAINFO": p.CAPath,
	}
}

func (p *Proxy) Close(ctx context.Context) error {
	p.transport.CloseIdleConnections()
	return p.server.Shutdown(ctx)
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	if request.Method == http.MethodConnect {
		p.connect(w, request)
		return
	}
	host := request.URL.Hostname()
	if host == "" {
		host = request.Host
	}
	route := p.match(host, request.URL.Path)
	if route == nil {
		if p.canPassthrough(host) {
			out := request.Clone(request.Context())
			out.RequestURI = ""
			stripForwardingHeaders(out.Header)
			response, err := p.transport.RoundTrip(out)
			if err != nil {
				writeProxyError(w, http.StatusBadGateway, err.Error())
				return
			}
			defer response.Body.Close()
			copyHeader(w.Header(), response.Header)
			w.WriteHeader(response.StatusCode)
			_, _ = io.Copy(w, response.Body)
			return
		}
		writeProxyError(w, http.StatusForbidden, "destination is not mapped by this Fabricate environment")
		return
	}
	response, err := p.roundTrip(request, route)
	if err != nil {
		writeProxyError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer response.Body.Close()
	copyHeader(w.Header(), response.Header)
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, response.Body)
}

func (p *Proxy) connect(w http.ResponseWriter, request *http.Request) {
	host := request.URL.Hostname()
	if host == "" {
		host, _, _ = net.SplitHostPort(request.Host)
	}
	if !p.hasHost(host) {
		if p.canPassthrough(host) {
			p.passthroughConnect(w, request)
			return
		}
		writeProxyError(w, http.StatusForbidden, "destination is not mapped by this Fabricate environment")
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		writeProxyError(w, http.StatusInternalServerError, "proxy connection cannot be hijacked")
		return
	}
	client, buffered, err := hijacker.Hijack()
	if err != nil {
		return
	}
	_, _ = buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
	if err := buffered.Flush(); err != nil {
		_ = client.Close()
		return
	}
	certificate, err := p.ca.certificateFor(canonicalHost(host))
	if err != nil {
		_ = client.Close()
		return
	}
	wrapped := &bufferedConn{Conn: client, reader: buffered.Reader}
	tlsClient := tls.Server(wrapped, &tls.Config{
		Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12,
		NextProtos: []string{"http/1.1"},
	})
	if err := tlsClient.HandshakeContext(request.Context()); err != nil {
		_ = tlsClient.Close()
		return
	}
	go p.serveTunnel(tlsClient, canonicalHost(host))
}

func (p *Proxy) canPassthrough(host string) bool {
	return !p.rejectUnknown || p.passthrough[canonicalHost(host)]
}

func (p *Proxy) passthroughConnect(w http.ResponseWriter, request *http.Request) {
	upstream, err := net.DialTimeout("tcp", request.Host, 10*time.Second)
	if err != nil {
		writeProxyError(w, http.StatusBadGateway, "explicit passthrough destination is unavailable")
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		upstream.Close()
		writeProxyError(w, http.StatusInternalServerError, "proxy connection cannot be hijacked")
		return
	}
	client, buffered, err := hijacker.Hijack()
	if err != nil {
		upstream.Close()
		return
	}
	_, _ = buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
	if err := buffered.Flush(); err != nil {
		client.Close()
		upstream.Close()
		return
	}
	go func() {
		defer client.Close()
		defer upstream.Close()
		done := make(chan struct{}, 1)
		go func() {
			_, _ = io.Copy(upstream, client)
			done <- struct{}{}
		}()
		go func() {
			_, _ = io.Copy(client, upstream)
			done <- struct{}{}
		}()
		<-done
	}()
}

func (p *Proxy) serveTunnel(connection *tls.Conn, host string) {
	defer connection.Close()
	reader := bufio.NewReader(connection)
	for {
		request, err := http.ReadRequest(reader)
		if err != nil {
			return
		}
		request.URL.Scheme = "https"
		request.URL.Host = host
		route := p.match(host, request.URL.Path)
		var response *http.Response
		if route == nil {
			response = proxyErrorResponse(request, http.StatusForbidden, "destination path is not mapped by this Fabricate environment")
		} else {
			response, err = p.roundTrip(request, route)
			if err != nil {
				response = proxyErrorResponse(request, http.StatusBadGateway, err.Error())
			}
		}
		if err := response.Write(connection); err != nil {
			response.Body.Close()
			return
		}
		response.Body.Close()
		if request.Close || response.Close {
			return
		}
	}
}

func (p *Proxy) roundTrip(request *http.Request, route *compiledRoute) (*http.Response, error) {
	if route.OAuthToken {
		started := time.Now()
		body, err := io.ReadAll(io.LimitReader(request.Body, bodyLimitForLog+1))
		if err != nil {
			return nil, err
		}
		request.Body = io.NopCloser(bytes.NewReader(body))
		response, err := oauthResponse(request, route.Token, body)
		if err != nil {
			return nil, err
		}
		responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, bodyLimitForLog+1))
		if readErr != nil {
			response.Body.Close()
			return nil, readErr
		}
		response.Body.Close()
		response.Body = io.NopCloser(bytes.NewReader(responseBody))
		if p.requests != nil {
			p.requests.Record(route.Service, request, requestlog.EntryInput{
				Started: started, Status: response.StatusCode,
				RequestBody: body[:min(len(body), bodyLimitForLog)], RequestBytes: len(body), RequestTruncated: len(body) > bodyLimitForLog,
				ResponseBody: responseBody[:min(len(responseBody), bodyLimitForLog)], ResponseBytes: len(responseBody), ResponseTruncated: len(responseBody) > bodyLimitForLog,
				ResponseHeaders: response.Header,
			})
		}
		return response, nil
	}
	out := request.Clone(request.Context())
	out.RequestURI = ""
	out.URL.Scheme = route.target.Scheme
	out.URL.Host = route.target.Host
	out.Host = route.Host
	out.Header = request.Header.Clone()
	stripForwardingHeaders(out.Header)
	// Never forward a production credential into even a local mock. The
	// resource only receives the per-environment synthetic token.
	out.Header.Set("Authorization", "Bearer "+route.Token)
	return p.transport.RoundTrip(out)
}

const bodyLimitForLog = 1 << 20

func oauthResponse(request *http.Request, token string, body []byte) (*http.Response, error) {
	if request.Method != http.MethodPost || request.URL.Path != "/token" {
		return proxyErrorResponse(request, http.StatusNotFound, "OAuth endpoint not found"), nil
	}
	values, err := url.ParseQuery(string(body))
	if err != nil || values.Get("grant_type") != "refresh_token" || strings.TrimSpace(values.Get("refresh_token")) == "" {
		payload := []byte(`{"error":"invalid_grant","error_description":"Fabricate requires a non-empty refresh_token grant"}`)
		return bytesResponse(request, http.StatusBadRequest, payload), nil
	}
	payload, _ := json.Marshal(map[string]any{"access_token": token, "token_type": "Bearer", "expires_in": 3600})
	return bytesResponse(request, http.StatusOK, payload), nil
}

func (p *Proxy) hasHost(host string) bool {
	host = canonicalHost(host)
	for i := range p.routes {
		if p.routes[i].Host == host {
			return true
		}
	}
	return false
}

func (p *Proxy) match(host, path string) *compiledRoute {
	host = canonicalHost(host)
	for i := range p.routes {
		if p.routes[i].Host == host && strings.HasPrefix(path, p.routes[i].PathPrefix) {
			return &p.routes[i]
		}
	}
	return nil
}

func canonicalHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	}
	return strings.TrimSuffix(host, ".")
}

func stripForwardingHeaders(header http.Header) {
	for _, name := range []string{"Proxy-Authorization", "Proxy-Connection", "Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "Via"} {
		header.Del(name)
	}
}

func copyHeader(destination, source http.Header) {
	for key, values := range source {
		for _, value := range values {
			destination.Add(key, value)
		}
	}
}

func writeProxyError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func proxyErrorResponse(request *http.Request, status int, message string) *http.Response {
	payload, _ := json.Marshal(map[string]string{"error": message})
	return bytesResponse(request, status, payload)
}

func bytesResponse(request *http.Request, status int, payload []byte) *http.Response {
	header := make(http.Header)
	header.Set("Content-Type", "application/json")
	return &http.Response{
		Status: fmt.Sprintf("%d %s", status, http.StatusText(status)), StatusCode: status,
		Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1, Header: header,
		Body: io.NopCloser(strings.NewReader(string(payload))), ContentLength: int64(len(payload)), Request: request,
	}
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

type certificateAuthority struct {
	certificate *x509.Certificate
	key         *ecdsa.PrivateKey
	pem         []byte
	mu          sync.Mutex
	leaves      map[string]tls.Certificate
}

func newCertificateAuthority() (*certificateAuthority, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("proxy: generate CA key: %w", err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: randomSerial(), Subject: pkix.Name{CommonName: "Fabricate environment CA"},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(24 * time.Hour), IsCA: true,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("proxy: create CA certificate: %w", err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("proxy: parse CA certificate: %w", err)
	}
	return &certificateAuthority{
		certificate: certificate, key: key,
		pem:    pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		leaves: make(map[string]tls.Certificate),
	}, nil
}

func (ca *certificateAuthority) certificateFor(host string) (tls.Certificate, error) {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	if certificate, ok := ca.leaves[host]; ok {
		return certificate, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: randomSerial(), Subject: pkix.Name{CommonName: host}, DNSNames: []string{host},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.certificate, &key.PublicKey, ca.key)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	certificate, err := tls.X509KeyPair(certPEM, keyPEM)
	if err == nil {
		ca.leaves[host] = certificate
	}
	return certificate, err
}

func randomSerial() *big.Int {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return big.NewInt(time.Now().UnixNano())
	}
	return serial
}

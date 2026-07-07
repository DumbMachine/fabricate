// Tiny reverse proxy baked into the fab github image. emulate hardcodes
// `http://localhost:4000` in its response bodies (id urls, repos_url,
// comments_url, …) — once the container is bound to a random host port,
// any SDK or agent that follows those links hits nothing. The proxy
// listens on the external-facing port (4000, exposed by the image) and
// forwards to emulate on 4001 (internal-only), rewriting URLs in the
// response body using the incoming Host header so the rewritten URL is
// whatever the consumer just dialled in on.
//
// Single-file, stdlib-only — keeps the docker build simple.
package main

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
)

func main() {
	listen := envOr("EMULATE_PROXY_PORT", "4000")
	backendPort := envOr("EMULATE_BACKEND_PORT", "4001")
	backendURL, err := url.Parse("http://127.0.0.1:" + backendPort)
	if err != nil {
		log.Fatalf("emulate-proxy: parse backend url: %v", err)
	}

	// The placeholder host:port emulate bakes into its responses. The
	// upstream service is fixed (vercel-labs/emulate listens on 4000 by
	// default and stamps that into URL fields), so we hard-pin the
	// search string here. Bump if you ever rebuild the image with a
	// different EMULATE_PORT.
	const sourceHost = "localhost:4000"

	rp := httputil.NewSingleHostReverseProxy(backendURL)
	rp.ModifyResponse = func(resp *http.Response) error {
		publicHost := resp.Request.Host
		if publicHost == "" || publicHost == sourceHost {
			// Nothing to rewrite to — either the consumer didn't send a
			// Host header (unlikely on HTTP/1.1) or it already happens to
			// match the source, so substitution is a no-op.
			return nil
		}
		ct := resp.Header.Get("Content-Type")
		if !isRewritableContentType(ct) {
			return nil
		}
		// Read + rewrite + reset Content-Length. Response bodies in
		// emulate are small (single JSON object, a handful of KB at
		// most for the github surface) — buffering is fine.
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		rewritten := bytes.ReplaceAll(body, []byte(sourceHost), []byte(publicHost))
		resp.Body = io.NopCloser(bytes.NewReader(rewritten))
		resp.ContentLength = int64(len(rewritten))
		resp.Header.Set("Content-Length", itoa(len(rewritten)))
		// Strip Content-Encoding only if we encountered one — we read
		// the body raw and substituted bytes in place; if it was
		// gzipped, our rewrite would have corrupted it. emulate doesn't
		// gzip by default, but be defensive.
		if resp.Header.Get("Content-Encoding") != "" {
			resp.Header.Del("Content-Encoding")
		}
		return nil
	}

	log.Printf("emulate-proxy: listening on :%s, forwarding to %s, rewriting %q → Host:", listen, backendURL, sourceHost)
	if err := http.ListenAndServe(":"+listen, rp); err != nil {
		log.Fatalf("emulate-proxy: serve: %v", err)
	}
}

// isRewritableContentType returns true for content types whose bodies
// are text-ish enough that a byte-level substitution is safe. JSON +
// text/* cover everything emulate's github service emits.
func isRewritableContentType(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(strings.SplitN(ct, ";", 2)[0]))
	switch {
	case ct == "":
		// No type set — assume octet-stream; don't rewrite.
		return false
	case strings.HasPrefix(ct, "text/"):
		return true
	case ct == "application/json",
		ct == "application/problem+json",
		ct == "application/vnd.github+json",
		strings.HasSuffix(ct, "+json"):
		return true
	}
	return false
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func itoa(n int) string {
	// Tiny stdlib-free int-to-string so we don't pull strconv just for
	// one Set call. n fits in int63 in this process.
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

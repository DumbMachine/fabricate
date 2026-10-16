package slack

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"

	"github.com/getkin/kin-openapi/openapi3"
)

const maxFormBody = 1 << 20

// postedReadMethods lets every read method accept POST with an
// application/x-www-form-urlencoded body, as slack.com does. The official
// clients (@slack/web-api, python-slack-sdk, bolt) send every Web API call as
// a form POST, while the curated contract declares read methods as GET with
// query parameters. The form fields are folded into the query string and the
// request continues as GET so the strict handler and validator see the
// contract's shape. A query parameter wins over a form field of the same name.
//
// Only paths the contract declares as GET without POST are rewritten, so
// chat.* write methods keep their JSON and form bodies untouched.
func postedReadMethods(spec *openapi3.T, next http.Handler) http.Handler {
	readOnly := map[string]bool{}
	if spec != nil && spec.Paths != nil {
		for path, item := range spec.Paths.Map() {
			if item.Get != nil && item.Post == nil {
				readOnly[path] = true
			}
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !readOnly[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		mediaType := ""
		if raw := r.Header.Get("Content-Type"); raw != "" {
			parsed, _, err := mime.ParseMediaType(raw)
			if err != nil {
				writeSlackError(w, http.StatusOK, "invalid_form_data")
				return
			}
			mediaType = parsed
		}
		if mediaType != "" && mediaType != "application/x-www-form-urlencoded" {
			writeSlackError(w, http.StatusOK, "invalid_form_data")
			return
		}
		form, err := readForm(r.Body)
		if err != nil {
			writeSlackError(w, http.StatusOK, "invalid_form_data")
			return
		}
		query := r.URL.Query()
		for key, values := range form {
			if !query.Has(key) {
				query[key] = values
			}
		}
		get := r.Clone(r.Context())
		get.Method = http.MethodGet
		get.URL.RawQuery = query.Encode()
		get.RequestURI = ""
		get.Body = http.NoBody
		get.GetBody = func() (io.ReadCloser, error) { return http.NoBody, nil }
		get.ContentLength = 0
		get.Header.Del("Content-Type")
		get.Header.Del("Content-Length")
		next.ServeHTTP(w, get)
	})
}

func readForm(body io.Reader) (url.Values, error) {
	if body == nil {
		return url.Values{}, nil
	}
	data, err := io.ReadAll(io.LimitReader(body, maxFormBody+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxFormBody {
		return nil, errors.New("form body too large")
	}
	return url.ParseQuery(string(data))
}

package metaads

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/meta-ads/generated"
	"github.com/getkin/kin-openapi/openapi3filter"
	middleware "github.com/oapi-codegen/nethttp-middleware"
)

type server struct {
	db      *sql.DB
	handler http.Handler
}

func (s *server) Handler() http.Handler       { return s.handler }
func (s *server) Close(context.Context) error { return nil }
func newServer(ctx context.Context, d httpresource.ServerDependencies) (*server, error) {
	if d.DB == nil || d.Secrets == nil {
		return nil, fmt.Errorf("database and synthetic credentials required")
	}
	token, err := d.Secrets.Get(ctx, "token")
	if err != nil || token == "" {
		return nil, fmt.Errorf("synthetic token required")
	}
	spec, err := generated.GetSpec()
	if err != nil {
		return nil, err
	}
	s := &server{db: d.DB}
	generatedHandler := generated.Handler(generated.NewStrictHandler(s, nil))
	validate := middleware.OapiRequestValidatorWithOptions(spec, &middleware.Options{DoNotValidateServers: true, ErrorHandlerWithOpts: func(_ context.Context, _ error, w http.ResponseWriter, _ *http.Request, opts middleware.ErrorHandlerOpts) {
		status := opts.StatusCode
		if status == 0 {
			status = 400
		}
		writeGraphError(w, status, "unsupported or invalid provider request")
	}, Options: openapi3filter.Options{AuthenticationFunc: func(_ context.Context, input *openapi3filter.AuthenticationInput) error {
		request := input.RequestValidationInput.Request
		if request.Header.Get("Authorization") != "Bearer "+token && request.URL.Query().Get("access_token") != token {
			return errors.New("invalid synthetic token")
		}
		return nil
	}}})
	inner := validate(generatedHandler)
	s.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for key := range r.URL.Query() {
			switch key {
			case "fields", "level", "time_range", "access_token", "appsecret_proof":
			default:
				writeGraphError(w, 400, "unsupported query parameter")
				return
			}
		}
		copy := r.Clone(r.Context())
		path := r.URL.Path
		if strings.HasPrefix(path, "/v23.0/") {
			path = strings.TrimPrefix(path, "/v23.0")
		}
		path = strings.TrimRight(path, "/")
		if path == "" {
			path = "/"
		}
		copy.URL.Path = path
		copy.URL.RawPath = ""
		inner.ServeHTTP(w, copy)
	})
	return s, nil
}

var _ generated.StrictServerInterface = (*server)(nil)

func convert[T any](value any) T {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	var out T
	if err = json.Unmarshal(raw, &out); err != nil {
		panic(err)
	}
	return out
}
func providerError(status int, message string) map[string]any {
	return map[string]any{"StatusCode": status, "Body": map[string]any{"error": map[string]any{"message": message, "code": 100}}}
}
func (s *server) get(ctx context.Context, kind, id string) (map[string]any, error) {
	var raw string
	if err := s.db.QueryRowContext(ctx, "SELECT body FROM documents WHERE collection=? AND id=?", kind, id).Scan(&raw); err != nil {
		return nil, err
	}
	var result map[string]any
	err := json.Unmarshal([]byte(raw), &result)
	return result, err
}
func (s *server) list(ctx context.Context, kind, account string) ([]map[string]any, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT body FROM documents WHERE collection=? ORDER BY id", kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []map[string]any{}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var row map[string]any
		if err = json.Unmarshal([]byte(raw), &row); err != nil {
			return nil, err
		}
		if row["account_id"] == account {
			result = append(result, row)
		}
	}
	return result, rows.Err()
}
func fieldsAllowed(fields *string, allowed string) bool {
	if fields == nil || *fields == "" {
		return true
	}
	set := "," + allowed + ","
	for _, field := range strings.Split(*fields, ",") {
		if !strings.Contains(set, ","+strings.TrimSpace(field)+",") {
			return false
		}
	}
	return true
}
func (s *server) GetNode(ctx context.Context, r generated.GetNodeRequestObject) (generated.GetNodeResponseObject, error) {
	for _, kind := range []string{"accounts", "adsets"} {
		value, err := s.get(ctx, kind, r.NodeId)
		if err == nil {
			allowed := "id,name,account_id,currency,status,campaign_id,daily_budget"
			if !fieldsAllowed(r.Params.Fields, allowed) {
				return convert[generated.GetNodedefaultJSONResponse](providerError(400, "unsupported field")), nil
			}
			return convert[generated.GetNode200JSONResponse](value), nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	return convert[generated.GetNodedefaultJSONResponse](providerError(404, "node not found")), nil
}
func (s *server) ListCampaigns(ctx context.Context, r generated.ListCampaignsRequestObject) (generated.ListCampaignsResponseObject, error) {
	if _, err := s.get(ctx, "accounts", r.AccountId); errors.Is(err, sql.ErrNoRows) {
		return convert[generated.ListCampaignsdefaultJSONResponse](providerError(404, "account not found")), nil
	} else if err != nil {
		return nil, err
	}
	if !fieldsAllowed(r.Params.Fields, "id,name,account_id,status") {
		return convert[generated.ListCampaignsdefaultJSONResponse](providerError(400, "unsupported field")), nil
	}
	rows, err := s.list(ctx, "campaigns", r.AccountId)
	return convert[generated.ListCampaigns200JSONResponse](map[string]any{"data": rows}), err
}
func (s *server) ListAdSets(ctx context.Context, r generated.ListAdSetsRequestObject) (generated.ListAdSetsResponseObject, error) {
	if _, err := s.get(ctx, "accounts", r.AccountId); errors.Is(err, sql.ErrNoRows) {
		return convert[generated.ListAdSetsdefaultJSONResponse](providerError(404, "account not found")), nil
	} else if err != nil {
		return nil, err
	}
	if !fieldsAllowed(r.Params.Fields, "id,name,account_id,status,campaign_id,daily_budget") {
		return convert[generated.ListAdSetsdefaultJSONResponse](providerError(400, "unsupported field")), nil
	}
	rows, err := s.list(ctx, "adsets", r.AccountId)
	return convert[generated.ListAdSets200JSONResponse](map[string]any{"data": rows}), err
}
func (s *server) GetInsights(ctx context.Context, r generated.GetInsightsRequestObject) (generated.GetInsightsResponseObject, error) {
	if _, err := s.get(ctx, "accounts", r.AccountId); errors.Is(err, sql.ErrNoRows) {
		return convert[generated.GetInsightsdefaultJSONResponse](providerError(404, "account not found")), nil
	} else if err != nil {
		return nil, err
	}
	if !fieldsAllowed(r.Params.Fields, "account_id,campaign_id,adset_id,date_start,date_stop,impressions,clicks,spend,frequency") {
		return convert[generated.GetInsightsdefaultJSONResponse](providerError(400, "unsupported field")), nil
	}
	var window struct {
		Since string `json:"since"`
		Until string `json:"until"`
	}
	err := json.Unmarshal([]byte(r.Params.TimeRange), &window)
	start, e1 := time.Parse("2006-01-02", window.Since)
	end, e2 := time.Parse("2006-01-02", window.Until)
	if err != nil || e1 != nil || e2 != nil || start.After(end) {
		return convert[generated.GetInsightsdefaultJSONResponse](providerError(400, "invalid time_range")), nil
	}
	rows, err := s.list(ctx, "insights", r.AccountId)
	if err != nil {
		return nil, err
	}
	selected := []map[string]any{}
	for _, row := range rows {
		if row["date_start"].(string) >= window.Since && row["date_stop"].(string) <= window.Until {
			selected = append(selected, row)
		}
	}
	return convert[generated.GetInsights200JSONResponse](map[string]any{"data": selected}), nil
}
func (s *server) UpdateAdSet(ctx context.Context, r generated.UpdateAdSetRequestObject) (generated.UpdateAdSetResponseObject, error) {
	var budget int
	if r.JSONBody != nil {
		budget = r.JSONBody.DailyBudget
	} else if r.FormdataBody != nil {
		budget = r.FormdataBody.DailyBudget
	}
	if budget < 100 || budget > 100000000 {
		return convert[generated.UpdateAdSetdefaultJSONResponse](providerError(400, "daily_budget outside supported range")), nil
	}
	result, err := s.db.ExecContext(ctx, "UPDATE documents SET body=json_set(body,'$.daily_budget',?) WHERE collection='adsets' AND id=?", strconv.Itoa(budget), r.NodeId)
	if err != nil {
		return nil, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if count != 1 {
		return convert[generated.UpdateAdSetdefaultJSONResponse](providerError(404, "ad set not found")), nil
	}
	return generated.UpdateAdSet200JSONResponse{Success: true}, nil
}

func writeGraphError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": message, "code": 100}})
}

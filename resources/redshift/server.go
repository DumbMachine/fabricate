package redshift

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/redshift/generated"
	"github.com/getkin/kin-openapi/openapi3filter"
	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"
)

const (
	statementDurationNs = 1_000_000
	resultPageSize      = 100
	defaultListLimit    = 100
)

type server struct {
	db      *sql.DB
	clock   httpresource.Clock
	ids     httpresource.IDGenerator
	token   string
	handler http.Handler
}

var _ generated.StrictServerInterface = (*server)(nil)

func newServer(ctx context.Context, dependencies httpresource.ServerDependencies) (*server, error) {
	if dependencies.DB == nil || dependencies.Clock == nil || dependencies.IDs == nil || dependencies.Secrets == nil {
		return nil, fmt.Errorf("redshift: database, clock, ID generator, and secrets are required")
	}
	token, err := dependencies.Secrets.Get(ctx, "token")
	if err != nil {
		return nil, fmt.Errorf("redshift: load synthetic token: %w", err)
	}
	if token == "" {
		return nil, fmt.Errorf("redshift: synthetic token is empty")
	}
	spec, err := generated.GetSwagger()
	if err != nil {
		return nil, fmt.Errorf("redshift: load OpenAPI: %w", err)
	}
	impl := &server{db: dependencies.DB, clock: dependencies.Clock, ids: dependencies.IDs, token: token}
	strict := generated.NewStrictHandler(impl, nil)
	generatedHandler := generated.Handler(strict)
	validator := nethttpmiddleware.OapiRequestValidatorWithOptions(spec, &nethttpmiddleware.Options{
		DoNotValidateServers: true,
		Options: openapi3filter.Options{AuthenticationFunc: func(_ context.Context, input *openapi3filter.AuthenticationInput) error {
			header := input.RequestValidationInput.Request.Header.Get("Authorization")
			if header != "Bearer "+token {
				return input.NewError(errors.New("invalid synthetic bearer token"))
			}
			return nil
		}},
		ErrorHandlerWithOpts: func(_ context.Context, err error, w http.ResponseWriter, _ *http.Request, opts nethttpmiddleware.ErrorHandlerOpts) {
			status := opts.StatusCode
			if status == 0 {
				status = http.StatusBadRequest
			}
			if status == http.StatusUnauthorized {
				writeAPIError(w, status, dataError("UnrecognizedClientException", "invalid synthetic bearer token", ""))
				return
			}
			code := "ValidationException"
			if status == http.StatusNotFound {
				code = "ResourceNotFoundException"
			}
			if status >= 500 {
				code = "InternalServerException"
			}
			writeAPIError(w, status, dataError(code, err.Error(), ""))
		},
	})
	inner := validator(generatedHandler)
	impl.handler = http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		cloned := request.Clone(request.Context())
		if err := routeRedshift(cloned); err != nil {
			if cloned.Header.Get("Authorization") != "Bearer "+token {
				writeAPIError(w, http.StatusUnauthorized, dataError("UnrecognizedClientException", "invalid synthetic bearer token", ""))
				return
			}
			writeAPIError(w, http.StatusBadRequest, dataError("ValidationException", err.Error(), ""))
			return
		}
		inner.ServeHTTP(&awsJSONWriter{ResponseWriter: w}, cloned)
	})
	return impl, nil
}

func (s *server) Handler() http.Handler       { return s.handler }
func (s *server) Close(context.Context) error { return nil }

func (s *server) ExecuteStatement(ctx context.Context, request generated.ExecuteStatementRequestObject) (generated.ExecuteStatementResponseObject, error) {
	if request.Body == nil {
		return executeError(http.StatusBadRequest, dataError("ValidationException", "request body is required", "")), nil
	}
	body := request.Body
	if body.WorkgroupName != nil {
		return executeError(http.StatusBadRequest, dataError("ValidationException", "WorkgroupName is not supported; this warehouse is a provisioned cluster", "")), nil
	}
	if body.SessionId != nil {
		return executeError(http.StatusBadRequest, dataError("ValidationException", "SessionId is not supported", "")), nil
	}
	if body.SessionKeepAliveSeconds != nil {
		return executeError(http.StatusBadRequest, dataError("ValidationException", "SessionKeepAliveSeconds is not supported", "")), nil
	}
	if body.ResultFormat != nil && *body.ResultFormat == generated.CSV {
		return executeError(http.StatusBadRequest, dataError("ValidationException", "ResultFormat CSV is not supported; GetStatementResult returns JSON", "")), nil
	}
	clientToken := ""
	if body.ClientToken != nil {
		clientToken = *body.ClientToken
		existing, ok, err := s.findByToken(ctx, clientToken)
		if err != nil {
			return nil, err
		}
		if ok {
			return generated.ExecuteStatement200JSONResponse(existing.executeOutput()), nil
		}
	}
	wh, err := s.loadWarehouse(ctx)
	if err != nil {
		return nil, err
	}
	if body.ClusterIdentifier != nil && *body.ClusterIdentifier != wh.Cluster {
		return executeError(http.StatusBadRequest, dataError("ValidationException", fmt.Sprintf("ClusterIdentifier %q was not found; this endpoint serves %s", *body.ClusterIdentifier, wh.Cluster), "")), nil
	}
	if body.Database == nil || *body.Database == "" {
		return executeError(http.StatusBadRequest, dataError("ValidationException", "Database is required", "")), nil
	}
	if *body.Database != wh.Database {
		return executeError(http.StatusBadRequest, dataError("ValidationException", fmt.Sprintf("Database %q was not found; cluster %s serves %s", *body.Database, wh.Cluster, wh.Database), "")), nil
	}
	dbUser := wh.DbUser
	if body.DbUser != nil {
		if *body.DbUser != wh.DbUser {
			return executeError(http.StatusBadRequest, dataError("ValidationException", fmt.Sprintf("DbUser %q was not found; cluster %s serves %s", *body.DbUser, wh.Cluster, wh.DbUser), "")), nil
		}
		dbUser = *body.DbUser
	}
	var params []generated.SqlParameter
	if body.Parameters != nil {
		params = *body.Parameters
	}
	result, err := executeSelect(wh.Database, wh.Schema, seededTables(), wh.Rows, body.Sql, params)
	if err != nil {
		var rejected *sqlError
		if errors.As(err, &rejected) {
			return executeError(http.StatusBadRequest, invalidSQL(wh, err)), nil
		}
		return nil, err
	}
	statementName := ""
	if body.StatementName != nil {
		statementName = *body.StatementName
	}
	secretArn := ""
	if body.SecretArn != nil {
		secretArn = *body.SecretArn
	}
	stored, err := s.insertStatement(ctx, newStatement{
		ClientToken: clientToken,
		Cluster:     wh.Cluster,
		Database:    wh.Database,
		DbUser:      dbUser,
		SecretArn:   secretArn,
		Name:        statementName,
		SQL:         body.Sql,
		Parameters:  params,
		Result:      result,
	})
	if err != nil {
		return nil, err
	}
	return generated.ExecuteStatement200JSONResponse(stored.executeOutput()), nil
}

func (s *server) DescribeStatement(ctx context.Context, request generated.DescribeStatementRequestObject) (generated.DescribeStatementResponseObject, error) {
	if request.Body == nil {
		return describeError(http.StatusBadRequest, dataError("ValidationException", "request body is required", "")), nil
	}
	stored, ok, err := s.findByID(ctx, request.Body.Id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return describeError(http.StatusBadRequest, dataError("ResourceNotFoundException", fmt.Sprintf("Statement %s was not found", request.Body.Id), request.Body.Id)), nil
	}
	return generated.DescribeStatement200JSONResponse(stored.describeOutput()), nil
}

func (s *server) GetStatementResult(ctx context.Context, request generated.GetStatementResultRequestObject) (generated.GetStatementResultResponseObject, error) {
	if request.Body == nil {
		return resultError(http.StatusBadRequest, dataError("ValidationException", "request body is required", "")), nil
	}
	stored, ok, err := s.findByID(ctx, request.Body.Id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return resultError(http.StatusBadRequest, dataError("ResourceNotFoundException", fmt.Sprintf("Statement %s was not found", request.Body.Id), request.Body.Id)), nil
	}
	if !stored.HasResultSet {
		return resultError(http.StatusBadRequest, dataError("ValidationException", "Statement has no result set", "")), nil
	}
	token := ""
	if request.Body.NextToken != nil {
		token = *request.Body.NextToken
	}
	page, next, err := pageRecords(stored.Records, token)
	if err != nil {
		return resultError(http.StatusBadRequest, dataError("ValidationException", err.Error(), "")), nil
	}
	return generated.GetStatementResult200JSONResponse{
		ColumnMetadata: stored.Columns,
		NextToken:      next,
		Records:        page,
		TotalNumRows:   stored.ResultRows,
	}, nil
}

func (s *server) ListStatements(ctx context.Context, request generated.ListStatementsRequestObject) (generated.ListStatementsResponseObject, error) {
	body := request.Body
	if body == nil {
		body = &generated.ListStatementsInput{}
	}
	if body.ClusterIdentifier != nil && body.WorkgroupName != nil {
		return listError(http.StatusBadRequest, dataError("ValidationException", "ClusterIdentifier and WorkgroupName cannot both be specified", "")), nil
	}
	rows, err := s.db.QueryContext(ctx, statementSelect+` ORDER BY seq DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	status := string(generated.FINISHED)
	if body.Status != nil {
		status = string(*body.Status)
	}
	matched := []generated.StatementData{}
	for rows.Next() {
		stored, err := scanStatement(rows)
		if err != nil {
			return nil, err
		}
		if body.WorkgroupName != nil {
			continue
		}
		if body.ClusterIdentifier != nil && *body.ClusterIdentifier != stored.Cluster {
			continue
		}
		if body.Database != nil && *body.Database != stored.Database {
			continue
		}
		if body.StatementName != nil && !strings.HasPrefix(stored.StatementName, *body.StatementName) {
			continue
		}
		if status != string(generated.ALL) && stored.Status != status {
			continue
		}
		matched = append(matched, stored.summary())
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	limit := defaultListLimit
	if body.MaxResults != nil {
		limit = *body.MaxResults
	}
	token := ""
	if body.NextToken != nil {
		token = *body.NextToken
	}
	page, next, err := pageSlice(matched, token, limit)
	if err != nil {
		return listError(http.StatusBadRequest, dataError("ValidationException", err.Error(), "")), nil
	}
	return generated.ListStatements200JSONResponse{NextToken: next, Statements: page}, nil
}

type warehouse struct {
	Cluster  string
	Database string
	Schema   string
	DbUser   string
	Rows     map[string][]map[string]any
}

func (s *server) loadWarehouse(ctx context.Context) (warehouse, error) {
	wh := warehouse{Rows: map[string][]map[string]any{}}
	meta := map[string]*string{
		"clusterIdentifier": &wh.Cluster,
		"database":          &wh.Database,
		"schema":            &wh.Schema,
		"dbUser":            &wh.DbUser,
	}
	for key, dest := range meta {
		if err := s.db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key=?", key).Scan(dest); err != nil {
			return warehouse{}, fmt.Errorf("redshift: load %s: %w", key, err)
		}
	}
	for name := range seededTables() {
		loaded, err := loadTableRows(ctx, s.db, name)
		if err != nil {
			return warehouse{}, err
		}
		wh.Rows[name] = loaded
	}
	return wh, nil
}

func loadTableRows(ctx context.Context, db *sql.DB, table string) ([]map[string]any, error) {
	rows, err := db.QueryContext(ctx, "SELECT body FROM "+table+" ORDER BY position")
	if err != nil {
		return nil, fmt.Errorf("redshift: load %s: %w", table, err)
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(body), &row); err != nil {
			return nil, fmt.Errorf("redshift: decode %s row: %w", table, err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

type newStatement struct {
	ClientToken string
	Cluster     string
	Database    string
	DbUser      string
	SecretArn   string
	Name        string
	SQL         string
	Parameters  []generated.SqlParameter
	Result      queryResult
}

func (s *server) insertStatement(ctx context.Context, input newStatement) (storedStatement, error) {
	if input.Result.records == nil {
		input.Result.records = [][]generated.Field{}
	}
	if input.Result.columns == nil {
		input.Result.columns = []generated.ColumnMetadata{}
	}
	if input.Parameters == nil {
		input.Parameters = []generated.SqlParameter{}
	}
	recordsJSON, err := json.Marshal(input.Result.records)
	if err != nil {
		return storedStatement{}, err
	}
	columnsJSON, err := json.Marshal(input.Result.columns)
	if err != nil {
		return storedStatement{}, err
	}
	paramsJSON, err := json.Marshal(input.Parameters)
	if err != nil {
		return storedStatement{}, err
	}
	id, err := s.ids.Next(ctx, "statement")
	if err != nil {
		return storedStatement{}, fmt.Errorf("redshift: allocate statement id: %w", err)
	}
	created := s.clock.Now().UTC().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return storedStatement{}, err
	}
	defer tx.Rollback()
	if input.ClientToken != "" {
		existing, err := scanStatement(tx.QueryRowContext(ctx, statementSelect+` WHERE client_token=?`, input.ClientToken))
		if err == nil {
			return existing, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return storedStatement{}, err
		}
	}
	var seq int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) + 1 FROM statements`).Scan(&seq); err != nil {
		return storedStatement{}, err
	}
	stored := storedStatement{
		Seq: seq, ID: id, ClientToken: input.ClientToken, CreatedAt: created, UpdatedAt: created,
		Cluster: input.Cluster, Database: input.Database, DbUser: input.DbUser, SecretArn: input.SecretArn,
		StatementName: input.Name, SQL: input.SQL, Parameters: input.Parameters, ResultFormat: "JSON",
		Status: "FINISHED", HasResultSet: true, ResultRows: int64(len(input.Result.records)),
		ResultSize: int64(len(recordsJSON)), Records: input.Result.records, Columns: input.Result.columns,
		Pid: 48000 + seq, QueryID: seq, Duration: statementDurationNs,
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO statements (
		seq, id, client_token, created_at, updated_at, cluster_identifier, database_name, db_user,
		secret_arn, statement_name, sql_text, parameters_json, result_format, status, has_result_set,
		result_rows, result_size, records_json, columns_json, redshift_pid, redshift_query_id, duration_ns
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		stored.Seq, stored.ID, stored.ClientToken, stored.CreatedAt, stored.UpdatedAt, stored.Cluster, stored.Database, stored.DbUser,
		stored.SecretArn, stored.StatementName, stored.SQL, string(paramsJSON), stored.ResultFormat, stored.Status, 1,
		stored.ResultRows, stored.ResultSize, string(recordsJSON), string(columnsJSON), stored.Pid, stored.QueryID, stored.Duration)
	if err != nil {
		if input.ClientToken != "" && strings.Contains(err.Error(), "UNIQUE") {
			_ = tx.Rollback()
			existing, ok, findErr := s.findByToken(ctx, input.ClientToken)
			if findErr != nil {
				return storedStatement{}, findErr
			}
			if ok {
				return existing, nil
			}
		}
		return storedStatement{}, fmt.Errorf("redshift: insert statement: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return storedStatement{}, err
	}
	return stored, nil
}

const statementSelect = `SELECT seq, id, client_token, created_at, updated_at, cluster_identifier, database_name, db_user, secret_arn, statement_name, sql_text, parameters_json, result_format, status, has_result_set, result_rows, result_size, records_json, columns_json, redshift_pid, redshift_query_id, duration_ns FROM statements`

type storedStatement struct {
	Seq           int64
	ID            string
	ClientToken   string
	CreatedAt     int64
	UpdatedAt     int64
	Cluster       string
	Database      string
	DbUser        string
	SecretArn     string
	StatementName string
	SQL           string
	Parameters    []generated.SqlParameter
	ResultFormat  string
	Status        string
	HasResultSet  bool
	ResultRows    int64
	ResultSize    int64
	Records       [][]generated.Field
	Columns       []generated.ColumnMetadata
	Pid           int64
	QueryID       int64
	Duration      int64
}

func (s *server) findByID(ctx context.Context, id string) (storedStatement, bool, error) {
	stored, err := scanStatement(s.db.QueryRowContext(ctx, statementSelect+` WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return storedStatement{}, false, nil
	}
	if err != nil {
		return storedStatement{}, false, err
	}
	return stored, true, nil
}

func (s *server) findByToken(ctx context.Context, token string) (storedStatement, bool, error) {
	stored, err := scanStatement(s.db.QueryRowContext(ctx, statementSelect+` WHERE client_token=?`, token))
	if errors.Is(err, sql.ErrNoRows) {
		return storedStatement{}, false, nil
	}
	if err != nil {
		return storedStatement{}, false, err
	}
	return stored, true, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanStatement(row rowScanner) (storedStatement, error) {
	var stored storedStatement
	var has int
	var params, records, columns string
	err := row.Scan(
		&stored.Seq, &stored.ID, &stored.ClientToken, &stored.CreatedAt, &stored.UpdatedAt,
		&stored.Cluster, &stored.Database, &stored.DbUser, &stored.SecretArn, &stored.StatementName,
		&stored.SQL, &params, &stored.ResultFormat, &stored.Status, &has, &stored.ResultRows,
		&stored.ResultSize, &records, &columns, &stored.Pid, &stored.QueryID, &stored.Duration,
	)
	if err != nil {
		return storedStatement{}, err
	}
	stored.HasResultSet = has != 0
	if err := json.Unmarshal([]byte(params), &stored.Parameters); err != nil {
		return storedStatement{}, fmt.Errorf("redshift: decode statement parameters: %w", err)
	}
	if err := json.Unmarshal([]byte(records), &stored.Records); err != nil {
		return storedStatement{}, fmt.Errorf("redshift: decode statement records: %w", err)
	}
	if err := json.Unmarshal([]byte(columns), &stored.Columns); err != nil {
		return storedStatement{}, fmt.Errorf("redshift: decode statement columns: %w", err)
	}
	if stored.Parameters == nil {
		stored.Parameters = []generated.SqlParameter{}
	}
	if stored.Records == nil {
		stored.Records = [][]generated.Field{}
	}
	if stored.Columns == nil {
		stored.Columns = []generated.ColumnMetadata{}
	}
	return stored, nil
}

func (stored storedStatement) executeOutput() generated.ExecuteStatementOutput {
	out := generated.ExecuteStatementOutput{
		ClusterIdentifier: stored.Cluster,
		CreatedAt:         stored.CreatedAt,
		Database:          stored.Database,
		DbUser:            stored.DbUser,
		HasResultSet:      stored.HasResultSet,
		Id:                stored.ID,
		RedshiftPid:       stored.Pid,
		Status:            stored.Status,
	}
	if stored.SecretArn != "" {
		secret := stored.SecretArn
		out.SecretArn = &secret
	}
	return out
}

func (stored storedStatement) describeOutput() generated.DescribeStatementOutput {
	out := generated.DescribeStatementOutput{
		ClusterIdentifier: stored.Cluster,
		CreatedAt:         stored.CreatedAt,
		Database:          stored.Database,
		DbUser:            stored.DbUser,
		Duration:          stored.Duration,
		HasResultSet:      stored.HasResultSet,
		Id:                stored.ID,
		QueryString:       stored.SQL,
		RedshiftPid:       stored.Pid,
		RedshiftQueryId:   stored.QueryID,
		ResultFormat:      stored.ResultFormat,
		ResultRows:        stored.ResultRows,
		ResultSize:        stored.ResultSize,
		Status:            stored.Status,
		UpdatedAt:         stored.UpdatedAt,
	}
	if len(stored.Parameters) > 0 {
		params := stored.Parameters
		out.QueryParameters = &params
	}
	if stored.SecretArn != "" {
		secret := stored.SecretArn
		out.SecretArn = &secret
	}
	return out
}

func (stored storedStatement) summary() generated.StatementData {
	item := generated.StatementData{
		CreatedAt:        stored.CreatedAt,
		Id:               stored.ID,
		IsBatchStatement: false,
		QueryString:      stored.SQL,
		ResultFormat:     stored.ResultFormat,
		Status:           stored.Status,
		UpdatedAt:        stored.UpdatedAt,
	}
	if stored.StatementName != "" {
		name := stored.StatementName
		item.StatementName = &name
	}
	if stored.SecretArn != "" {
		secret := stored.SecretArn
		item.SecretArn = &secret
	}
	if len(stored.Parameters) > 0 {
		params := stored.Parameters
		item.QueryParameters = &params
	}
	return item
}

func pageRecords(records [][]generated.Field, token string) ([][]generated.Field, *string, error) {
	return pageSlice(records, token, resultPageSize)
}

func pageSlice[T any](items []T, token string, limit int) ([]T, *string, error) {
	offset := 0
	if token != "" {
		n, err := strconv.Atoi(token)
		if err != nil || n < 0 || n > len(items) {
			return nil, nil, errors.New("NextToken is invalid")
		}
		offset = n
	}
	if limit < 0 {
		limit = 0
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	page := items[offset:end]
	if page == nil {
		page = []T{}
	}
	if end < len(items) {
		next := strconv.Itoa(end)
		return page, &next, nil
	}
	return page, nil, nil
}

func invalidSQL(wh warehouse, err error) generated.DataAPIError {
	message := err.Error()
	var rejected *sqlError
	if errors.As(err, &rejected) {
		message = fmt.Sprintf("%s; Fabricate Redshift only supports a single SELECT from %s.orders, %s.payments, %s.shipments, or %s.saas_invoices", rejected.msg, wh.Schema, wh.Schema, wh.Schema, wh.Schema)
	}
	return dataError("ValidationException", message, "")
}

func dataError(typ, message, resourceID string) generated.DataAPIError {
	out := generated.DataAPIError{UnderscoreUnderscoreType: typ, Message: message}
	if resourceID != "" {
		id := resourceID
		out.ResourceId = &id
	}
	return out
}

func executeError(status int, body generated.DataAPIError) generated.ExecuteStatementdefaultJSONResponse {
	return generated.ExecuteStatementdefaultJSONResponse{Body: body, StatusCode: status}
}

func describeError(status int, body generated.DataAPIError) generated.DescribeStatementdefaultJSONResponse {
	return generated.DescribeStatementdefaultJSONResponse{Body: body, StatusCode: status}
}

func resultError(status int, body generated.DataAPIError) generated.GetStatementResultdefaultJSONResponse {
	return generated.GetStatementResultdefaultJSONResponse{Body: body, StatusCode: status}
}

func listError(status int, body generated.DataAPIError) generated.ListStatementsdefaultJSONResponse {
	return generated.ListStatementsdefaultJSONResponse{Body: body, StatusCode: status}
}

func writeAPIError(w http.ResponseWriter, status int, body generated.DataAPIError) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.Header().Set("x-amzn-ErrorType", body.UnderscoreUnderscoreType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

type awsJSONWriter struct {
	http.ResponseWriter
}

func (w *awsJSONWriter) WriteHeader(status int) {
	if ct := w.Header().Get("Content-Type"); strings.HasPrefix(ct, "application/json") {
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	}
	w.ResponseWriter.WriteHeader(status)
}

func routeRedshift(r *http.Request) error {
	if r.Method != http.MethodPost {
		return nil
	}
	media := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]))
	switch media {
	case "", "application/json":
		r.Header.Set("Content-Type", "application/json")
	case "application/x-amz-json-1.1":
		r.Header.Set("Content-Type", "application/json")
	default:
		return fmt.Errorf("Content-Type %q is not supported; use application/x-amz-json-1.1", r.Header.Get("Content-Type"))
	}
	path := r.URL.Path
	if path == "" {
		path = "/"
	}
	if len(path) > 1 {
		path = strings.TrimSuffix(path, "/")
	}
	target := strings.TrimSpace(r.Header.Get("X-Amz-Target"))
	headerOp := ""
	if target != "" {
		op, ok := strings.CutPrefix(target, "RedshiftData.")
		if !ok || !knownOperation(op) {
			return errors.New("X-Amz-Target must be RedshiftData.ExecuteStatement, RedshiftData.DescribeStatement, RedshiftData.GetStatementResult, or RedshiftData.ListStatements")
		}
		headerOp = op
	}
	if path == "/" {
		if headerOp == "" {
			return errors.New("POST / requires header X-Amz-Target: RedshiftData.<Operation>")
		}
		setRequestPath(r, "/"+headerOp)
		return nil
	}
	op := strings.TrimPrefix(path, "/")
	if !knownOperation(op) {
		return nil
	}
	if headerOp != "" && headerOp != op {
		return fmt.Errorf("X-Amz-Target RedshiftData.%s does not match path /%s", headerOp, op)
	}
	setRequestPath(r, "/"+op)
	return nil
}

func knownOperation(name string) bool {
	switch name {
	case "ExecuteStatement", "DescribeStatement", "GetStatementResult", "ListStatements":
		return true
	default:
		return false
	}
}

func setRequestPath(r *http.Request, path string) {
	r.URL.Path = path
	r.URL.RawPath = ""
	if r.URL.RawQuery != "" {
		r.RequestURI = path + "?" + r.URL.RawQuery
		return
	}
	r.RequestURI = path
}

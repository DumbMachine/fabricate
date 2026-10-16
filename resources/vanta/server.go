package vanta

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/vanta/generated"
	"github.com/getkin/kin-openapi/openapi3filter"
	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"
)

type server struct {
	db      *sql.DB
	clock   httpresource.Clock
	ids     httpresource.IDGenerator
	handler http.Handler
}

var _ generated.StrictServerInterface = (*server)(nil)

func newServer(ctx context.Context, dependencies httpresource.ServerDependencies) (*server, error) {
	if dependencies.DB == nil || dependencies.Clock == nil || dependencies.IDs == nil || dependencies.Secrets == nil {
		return nil, fmt.Errorf("vanta: database, clock, ID generator, and secrets are required")
	}
	token, err := dependencies.Secrets.Get(ctx, "token")
	if err != nil {
		return nil, fmt.Errorf("vanta: load synthetic token: %w", err)
	}
	if token == "" {
		return nil, fmt.Errorf("vanta: synthetic token is empty")
	}
	spec, err := generated.GetSwagger()
	if err != nil {
		return nil, fmt.Errorf("vanta: load OpenAPI: %w", err)
	}
	impl := &server{db: dependencies.DB, clock: dependencies.Clock, ids: dependencies.IDs}
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
			writeError(w, status, err.Error())
		},
	})
	impl.handler = validator(generatedHandler)
	return impl, nil
}

func (s *server) Handler() http.Handler       { return s.handler }
func (s *server) Close(context.Context) error { return nil }

func (s *server) ListVendors(ctx context.Context, request generated.ListVendorsRequestObject) (generated.ListVendorsResponseObject, error) {
	vendors, err := loadTyped[generated.Vendor](ctx, s.db, "SELECT body FROM vendors ORDER BY id")
	if err != nil {
		return nil, err
	}
	filtered := make([]generated.Vendor, 0, len(vendors))
	for _, vendor := range vendors {
		if request.Params.Name != nil && *request.Params.Name != "" && !strings.Contains(strings.ToLower(vendor.Name), strings.ToLower(*request.Params.Name)) {
			continue
		}
		if request.Params.StatusMatchesAny != nil && len(*request.Params.StatusMatchesAny) > 0 && !statusAllowed(vendor.Status, *request.Params.StatusMatchesAny) {
			continue
		}
		filtered = append(filtered, vendor)
	}
	page, info, err := paginate(filtered, request.Params.PageSize, request.Params.PageCursor, func(vendor generated.Vendor) string { return vendor.Id })
	if err != nil {
		return generated.ListVendorsdefaultJSONResponse{Body: generated.Error{Message: err.Error()}, StatusCode: http.StatusBadRequest}, nil
	}
	return generated.ListVendors200JSONResponse{Results: generated.VendorResults{Data: page, PageInfo: info}}, nil
}

func (s *server) GetVendor(ctx context.Context, request generated.GetVendorRequestObject) (generated.GetVendorResponseObject, error) {
	vendor, err := loadOne[generated.Vendor](ctx, s.db, "SELECT body FROM vendors WHERE id=?", string(request.VendorId))
	if errors.Is(err, sql.ErrNoRows) {
		return generated.GetVendordefaultJSONResponse{Body: generated.Error{Message: "vendor not found"}, StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.GetVendor200JSONResponse(vendor), nil
}

func (s *server) GetSecurityReviewsByVendorId(ctx context.Context, request generated.GetSecurityReviewsByVendorIdRequestObject) (generated.GetSecurityReviewsByVendorIdResponseObject, error) {
	if err := s.requireID(ctx, "vendors", string(request.VendorId)); err != nil {
		if errors.Is(err, errNotFound) {
			return generated.GetSecurityReviewsByVendorIddefaultJSONResponse{Body: generated.Error{Message: "vendor not found"}, StatusCode: http.StatusNotFound}, nil
		}
		return nil, err
	}
	reviews, err := loadTyped[generated.SecurityReview](ctx, s.db, "SELECT body FROM security_reviews WHERE vendor_id=? ORDER BY id", string(request.VendorId))
	if err != nil {
		return nil, err
	}
	page, info, err := paginate(reviews, request.Params.PageSize, request.Params.PageCursor, func(review generated.SecurityReview) string { return review.Id })
	if err != nil {
		return generated.GetSecurityReviewsByVendorIddefaultJSONResponse{Body: generated.Error{Message: err.Error()}, StatusCode: http.StatusBadRequest}, nil
	}
	return generated.GetSecurityReviewsByVendorId200JSONResponse{Results: generated.SecurityReviewResults{Data: page, PageInfo: info}}, nil
}

func (s *server) ListTests(ctx context.Context, request generated.ListTestsRequestObject) (generated.ListTestsResponseObject, error) {
	tests, err := loadTyped[generated.Test](ctx, s.db, "SELECT body FROM tests ORDER BY id")
	if err != nil {
		return nil, err
	}
	filtered := make([]generated.Test, 0, len(tests))
	for _, test := range tests {
		if testVisible(test, request.Params) {
			filtered = append(filtered, test)
		}
	}
	page, info, err := paginate(filtered, request.Params.PageSize, request.Params.PageCursor, func(test generated.Test) string { return test.Id })
	if err != nil {
		return generated.ListTestsdefaultJSONResponse{Body: generated.Error{Message: err.Error()}, StatusCode: http.StatusBadRequest}, nil
	}
	return generated.ListTests200JSONResponse{Results: generated.TestResults{Data: page, PageInfo: info}}, nil
}

func (s *server) GetTest(ctx context.Context, request generated.GetTestRequestObject) (generated.GetTestResponseObject, error) {
	test, err := loadOne[generated.Test](ctx, s.db, "SELECT body FROM tests WHERE id=?", string(request.TestId))
	if errors.Is(err, sql.ErrNoRows) {
		return generated.GetTestdefaultJSONResponse{Body: generated.Error{Message: "test not found"}, StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.GetTest200JSONResponse(test), nil
}

func (s *server) GetTestEntities(ctx context.Context, request generated.GetTestEntitiesRequestObject) (generated.GetTestEntitiesResponseObject, error) {
	if err := s.requireID(ctx, "tests", string(request.TestId)); err != nil {
		if errors.Is(err, errNotFound) {
			return generated.GetTestEntitiesdefaultJSONResponse{Body: generated.Error{Message: "test not found"}, StatusCode: http.StatusNotFound}, nil
		}
		return nil, err
	}
	want := generated.EntityStatusFAILING
	if request.Params.EntityStatus != nil {
		want = *request.Params.EntityStatus
	}
	entities, err := loadTyped[generated.TestResourceEntity](ctx, s.db, "SELECT body FROM entities WHERE test_id=? ORDER BY id", string(request.TestId))
	if err != nil {
		return nil, err
	}
	filtered := make([]generated.TestResourceEntity, 0, len(entities))
	for _, entity := range entities {
		if entity.EntityStatus == want {
			filtered = append(filtered, entity)
		}
	}
	page, info, err := paginate(filtered, request.Params.PageSize, request.Params.PageCursor, func(entity generated.TestResourceEntity) string { return entity.Id })
	if err != nil {
		return generated.GetTestEntitiesdefaultJSONResponse{Body: generated.Error{Message: err.Error()}, StatusCode: http.StatusBadRequest}, nil
	}
	return generated.GetTestEntities200JSONResponse{Results: generated.TestEntityResults{Data: page, PageInfo: info}}, nil
}

func (s *server) ListDocuments(ctx context.Context, request generated.ListDocumentsRequestObject) (generated.ListDocumentsResponseObject, error) {
	documents, err := loadTyped[generated.Document](ctx, s.db, "SELECT body FROM documents ORDER BY id")
	if err != nil {
		return nil, err
	}
	filtered := make([]generated.Document, 0, len(documents))
	if request.Params.FrameworkMatchesAny == nil || len(*request.Params.FrameworkMatchesAny) == 0 {
		for _, document := range documents {
			if request.Params.StatusMatchesAny != nil && len(*request.Params.StatusMatchesAny) > 0 && !documentStatusAllowed(document.UploadStatus, *request.Params.StatusMatchesAny) {
				continue
			}
			filtered = append(filtered, document)
		}
	}
	page, info, err := paginate(filtered, request.Params.PageSize, request.Params.PageCursor, func(document generated.Document) string { return document.Id })
	if err != nil {
		return generated.ListDocumentsdefaultJSONResponse{Body: generated.Error{Message: err.Error()}, StatusCode: http.StatusBadRequest}, nil
	}
	return generated.ListDocuments200JSONResponse{Results: generated.DocumentResults{Data: page, PageInfo: info}}, nil
}

func (s *server) CreateDocument(ctx context.Context, request generated.CreateDocumentRequestObject) (generated.CreateDocumentResponseObject, error) {
	if request.Body == nil {
		return generated.CreateDocumentdefaultJSONResponse{Body: generated.Error{Message: "request body is required"}, StatusCode: http.StatusBadRequest}, nil
	}
	id, err := s.ids.Next(ctx, "vanta.document")
	if err != nil {
		return nil, fmt.Errorf("vanta: allocate document ID: %w", err)
	}
	now := s.clock.Now().UTC().Format(time.RFC3339)
	url := "https://app.vanta.com/documents/" + id
	record := map[string]any{
		"id":               id,
		"ownerId":          nil,
		"category":         "Custom",
		"description":      request.Body.Description,
		"isSensitive":      request.Body.IsSensitive,
		"title":            request.Body.Title,
		"uploadStatus":     "Needs document",
		"uploadStatusDate": now,
		"url":              url,
		"timeSensitivity":  string(request.Body.TimeSensitivity),
		"cadence":          string(request.Body.Cadence),
		"reminderWindow":   string(request.Body.ReminderWindow),
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, "INSERT INTO documents(id, body) VALUES(?, ?)", id, string(raw)); err != nil {
		return nil, err
	}
	document, err := loadOne[generated.Document](ctx, s.db, "SELECT body FROM documents WHERE id=?", id)
	if err != nil {
		return nil, err
	}
	return generated.CreateDocument201JSONResponse(document), nil
}

func (s *server) GetDocument(ctx context.Context, request generated.GetDocumentRequestObject) (generated.GetDocumentResponseObject, error) {
	document, err := loadOne[generated.Document](ctx, s.db, "SELECT body FROM documents WHERE id=?", string(request.DocumentId))
	if errors.Is(err, sql.ErrNoRows) {
		return generated.GetDocumentdefaultJSONResponse{Body: generated.Error{Message: "document not found"}, StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.GetDocument200JSONResponse(document), nil
}

func (s *server) ListFilesForDocument(ctx context.Context, request generated.ListFilesForDocumentRequestObject) (generated.ListFilesForDocumentResponseObject, error) {
	if err := s.requireID(ctx, "documents", string(request.DocumentId)); err != nil {
		if errors.Is(err, errNotFound) {
			return generated.ListFilesForDocumentdefaultJSONResponse{Body: generated.Error{Message: "document not found"}, StatusCode: http.StatusNotFound}, nil
		}
		return nil, err
	}
	uploads, err := loadTyped[generated.UploadedFile](ctx, s.db, "SELECT body FROM uploads WHERE document_id=? ORDER BY id", string(request.DocumentId))
	if err != nil {
		return nil, err
	}
	page, info, err := paginate(uploads, request.Params.PageSize, request.Params.PageCursor, func(upload generated.UploadedFile) string { return upload.Id })
	if err != nil {
		return generated.ListFilesForDocumentdefaultJSONResponse{Body: generated.Error{Message: err.Error()}, StatusCode: http.StatusBadRequest}, nil
	}
	return generated.ListFilesForDocument200JSONResponse{Results: generated.UploadedFileResults{Data: page, PageInfo: info}}, nil
}

var errNotFound = errors.New("not found")

func (s *server) requireID(ctx context.Context, table, id string) error {
	var n int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE id=?", id).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return errNotFound
	}
	return nil
}

func testVisible(test generated.Test, params generated.ListTestsParams) bool {
	if params.StatusFilter != nil && test.Status != *params.StatusFilter {
		return false
	}
	if params.FrameworkFilter != nil && *params.FrameworkFilter != "" {
		return false
	}
	if params.ControlFilter != nil && *params.ControlFilter != "" {
		return false
	}
	if params.IntegrationFilter != nil && *params.IntegrationFilter != "" && !contains(test.Integrations, *params.IntegrationFilter) {
		return false
	}
	if params.OwnerFilter != nil && *params.OwnerFilter != "" && (test.Owner == nil || test.Owner.Id != *params.OwnerFilter) {
		return false
	}
	if params.CategoryFilter != nil && testCategoryDisplay[*params.CategoryFilter] != string(test.Category) {
		return false
	}
	if params.IsInRollout != nil && *params.IsInRollout {
		return false
	}
	return true
}

var testCategoryDisplay = map[generated.TestCategory]string{
	generated.TestCategoryACCOUNTSACCESS:          "Accounts access",
	generated.TestCategoryACCOUNTSECURITY:         "Account security",
	generated.TestCategoryACCOUNTSETUP:            "Account setup",
	generated.TestCategoryCOMPUTERS:               "Computers",
	generated.TestCategoryCUSTOM:                  "Custom",
	generated.TestCategoryDATASTORAGE:             "Data storage",
	generated.TestCategoryEMPLOYEES:               "Employees",
	generated.TestCategoryINFRASTRUCTURE:          "Infrastructure",
	generated.TestCategoryIT:                      "IT",
	generated.TestCategoryLOGGING:                 "Logging",
	generated.TestCategoryMONITORINGALERTS:        "Monitoring alerts",
	generated.TestCategoryPEOPLE:                  "People",
	generated.TestCategoryPOLICIES:                "Policies",
	generated.TestCategoryRISKANALYSIS:            "Risk analysis",
	generated.TestCategorySECURITYALERTMANAGEMENT: "CSPM alert management",
	generated.TestCategorySOFTWAREDEVELOPMENT:     "Software development",
	generated.TestCategoryVENDORS:                 "Vendors",
	generated.TestCategoryVULNERABILITYMANAGEMENT: "Vulnerability management",
}

func statusAllowed(status generated.VendorStatus, allowed []generated.VendorStatus) bool {
	for _, candidate := range allowed {
		if status == candidate {
			return true
		}
	}
	return false
}

func documentStatusAllowed(status generated.DocumentStatus, allowed []generated.DocumentStatus) bool {
	for _, candidate := range allowed {
		if status == candidate {
			return true
		}
	}
	return false
}

func paginate[T any](items []T, pageSize *generated.PageSize, pageCursor *generated.PageCursor, idOf func(T) string) ([]T, generated.PageInfo, error) {
	size := 10
	if pageSize != nil {
		size = int(*pageSize)
	}
	start := 0
	if pageCursor != nil && *pageCursor != "" {
		found := -1
		for i, item := range items {
			if idOf(item) == *pageCursor {
				found = i
				break
			}
		}
		if found < 0 {
			return nil, generated.PageInfo{}, fmt.Errorf("invalid pageCursor")
		}
		start = found + 1
	}
	end := start + size
	if end > len(items) {
		end = len(items)
	}
	page := items[start:end]
	if page == nil {
		page = []T{}
	}
	info := generated.PageInfo{HasNextPage: end < len(items), HasPreviousPage: start > 0}
	if len(page) > 0 {
		startCursor := idOf(page[0])
		endCursor := idOf(page[len(page)-1])
		info.StartCursor = &startCursor
		info.EndCursor = &endCursor
	}
	return page, info, nil
}

func loadTyped[T any](ctx context.Context, db *sql.DB, query string, args ...any) ([]T, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var item T
		if err := json.Unmarshal([]byte(raw), &item); err != nil {
			return nil, fmt.Errorf("vanta: decode row: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func loadOne[T any](ctx context.Context, db *sql.DB, query string, args ...any) (T, error) {
	var zero T
	var raw string
	err := db.QueryRowContext(ctx, query, args...).Scan(&raw)
	if err != nil {
		return zero, err
	}
	var item T
	if err := json.Unmarshal([]byte(raw), &item); err != nil {
		return zero, fmt.Errorf("vanta: decode row: %w", err)
	}
	return item, nil
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(generated.Error{Message: message})
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

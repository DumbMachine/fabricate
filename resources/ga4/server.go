package ga4

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
	"github.com/dumbmachine/fabricate/resources/ga4/generated"
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
		return nil, fmt.Errorf("ga4: database, clock, ID generator, and secrets are required")
	}
	token, err := dependencies.Secrets.Get(ctx, "token")
	if err != nil {
		return nil, fmt.Errorf("ga4: load synthetic token: %w", err)
	}
	if token == "" {
		return nil, fmt.Errorf("ga4: synthetic token is empty")
	}
	spec, err := generated.GetSwagger()
	if err != nil {
		return nil, fmt.Errorf("ga4: load OpenAPI: %w", err)
	}
	impl := &server{db: dependencies.DB, clock: dependencies.Clock, ids: dependencies.IDs}
	strict := generated.NewStrictHandler(impl, nil)
	generatedHandler := generated.HandlerWithOptions(strict, generated.StdHTTPServerOptions{BaseRouter: newMethodMux()})
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
			code := "INVALID_ARGUMENT"
			if status == http.StatusUnauthorized {
				code = "UNAUTHENTICATED"
			}
			if status == 0 {
				status = http.StatusBadRequest
			}
			writeError(w, status, code, err.Error())
		},
	})
	impl.handler = validator(generatedHandler)
	return impl, nil
}

func (s *server) Handler() http.Handler       { return s.handler }
func (s *server) Close(context.Context) error { return nil }

func (s *server) PropertiesRunReport(ctx context.Context, request generated.PropertiesRunReportRequestObject) (generated.PropertiesRunReportResponseObject, error) {
	if request.Body == nil {
		return generated.PropertiesRunReportdefaultJSONResponse{Body: errorEnvelope(400, "INVALID_ARGUMENT", "Request body is required"), StatusCode: 400}, nil
	}
	report, err := s.runReport(ctx, string(request.Property), request.Body)
	if err != nil {
		if response, ok := reportError(err); ok {
			return generated.PropertiesRunReportdefaultJSONResponse{Body: response.Body, StatusCode: response.StatusCode}, nil
		}
		return nil, err
	}
	return generated.PropertiesRunReport200JSONResponse(report), nil
}

func (s *server) PropertiesBatchRunReports(ctx context.Context, request generated.PropertiesBatchRunReportsRequestObject) (generated.PropertiesBatchRunReportsResponseObject, error) {
	if request.Body == nil {
		return generated.PropertiesBatchRunReportsdefaultJSONResponse{Body: errorEnvelope(400, "INVALID_ARGUMENT", "Request body is required"), StatusCode: 400}, nil
	}
	reports := make([]generated.RunReportResponse, 0, len(request.Body.Requests))
	for _, body := range request.Body.Requests {
		report, err := s.runReport(ctx, string(request.Property), &body)
		if err != nil {
			if response, ok := reportError(err); ok {
				return generated.PropertiesBatchRunReportsdefaultJSONResponse{Body: response.Body, StatusCode: response.StatusCode}, nil
			}
			return nil, err
		}
		reports = append(reports, report)
	}
	return generated.PropertiesBatchRunReports200JSONResponse{Kind: kindBatch, Reports: reports}, nil
}

func (s *server) PropertiesRunRealtimeReport(ctx context.Context, request generated.PropertiesRunRealtimeReportRequestObject) (generated.PropertiesRunRealtimeReportResponseObject, error) {
	if request.Body == nil {
		return generated.PropertiesRunRealtimeReportdefaultJSONResponse{Body: errorEnvelope(400, "INVALID_ARGUMENT", "Request body is required"), StatusCode: 400}, nil
	}
	report, err := s.runRealtime(ctx, string(request.Property), request.Body)
	if err != nil {
		if response, ok := reportError(err); ok {
			return generated.PropertiesRunRealtimeReportdefaultJSONResponse{Body: response.Body, StatusCode: response.StatusCode}, nil
		}
		return nil, err
	}
	return generated.PropertiesRunRealtimeReport200JSONResponse(report), nil
}

func (s *server) PropertiesCheckCompatibility(ctx context.Context, request generated.PropertiesCheckCompatibilityRequestObject) (generated.PropertiesCheckCompatibilityResponseObject, error) {
	if request.Body == nil {
		return generated.PropertiesCheckCompatibilitydefaultJSONResponse{Body: errorEnvelope(400, "INVALID_ARGUMENT", "Request body is required"), StatusCode: 400}, nil
	}
	if _, err := s.loadProperty(ctx, string(request.Property)); err != nil {
		if response, ok := reportError(err); ok {
			return generated.PropertiesCheckCompatibilitydefaultJSONResponse{Body: response.Body, StatusCode: response.StatusCode}, nil
		}
		return nil, err
	}
	if err := validateFilter(request.Body.DimensionFilter, true); err != nil {
		if response, ok := reportError(err); ok {
			return generated.PropertiesCheckCompatibilitydefaultJSONResponse{Body: response.Body, StatusCode: response.StatusCode}, nil
		}
		return nil, err
	}
	if err := validateFilter(request.Body.MetricFilter, false); err != nil {
		if response, ok := reportError(err); ok {
			return generated.PropertiesCheckCompatibilitydefaultJSONResponse{Body: response.Body, StatusCode: response.StatusCode}, nil
		}
		return nil, err
	}
	filter := ""
	if request.Body.CompatibilityFilter != nil {
		filter = string(*request.Body.CompatibilityFilter)
	}
	response := generated.CheckCompatibilityResponse{
		DimensionCompatibilities: []generated.DimensionCompatibility{},
		MetricCompatibilities:    []generated.MetricCompatibility{},
	}
	if request.Body.Dimensions != nil {
		for _, dimension := range *request.Body.Dimensions {
			spec, ok := dimensionByName(dimension.Name)
			compatible := ok && dimension.Name != "minutesAgo"
			if !keepCompatibility(filter, compatible) {
				continue
			}
			metadata := generated.DimensionMetadata{
				ApiName: dimension.Name, UiName: dimension.Name,
				Description: "This dimension is not in the curated catalog.", Category: "Other",
			}
			status := generated.DimensionCompatibilityCompatibilityINCOMPATIBLE
			if compatible {
				metadata = spec.metadata()
				status = generated.DimensionCompatibilityCompatibilityCOMPATIBLE
			}
			response.DimensionCompatibilities = append(response.DimensionCompatibilities, generated.DimensionCompatibility{
				Compatibility: status, DimensionMetadata: metadata,
			})
		}
	}
	if request.Body.Metrics != nil {
		for _, metric := range *request.Body.Metrics {
			spec, compatible := metricByName(metric.Name)
			if !keepCompatibility(filter, compatible) {
				continue
			}
			metadata := generated.MetricMetadata{
				ApiName: metric.Name, UiName: metric.Name,
				Description: "This metric is not in the curated catalog.", Category: "Other",
				Type: generated.MetricMetadataTypeMETRICTYPEUNSPECIFIED,
			}
			status := generated.MetricCompatibilityCompatibilityINCOMPATIBLE
			if compatible {
				metadata = spec.metadata()
				status = generated.MetricCompatibilityCompatibilityCOMPATIBLE
			}
			response.MetricCompatibilities = append(response.MetricCompatibilities, generated.MetricCompatibility{
				Compatibility: status, MetricMetadata: metadata,
			})
		}
	}
	return generated.PropertiesCheckCompatibility200JSONResponse(response), nil
}

func (s *server) PropertiesGetMetadata(ctx context.Context, request generated.PropertiesGetMetadataRequestObject) (generated.PropertiesGetMetadataResponseObject, error) {
	propertyID := string(request.Property)
	if _, err := s.loadProperty(ctx, propertyID); err != nil {
		if response, ok := reportError(err); ok {
			return generated.PropertiesGetMetadatadefaultJSONResponse{Body: response.Body, StatusCode: response.StatusCode}, nil
		}
		return nil, err
	}
	dimensions := make([]generated.DimensionMetadata, 0, len(reportDimensions))
	for _, spec := range reportDimensions {
		if spec.apiName == "minutesAgo" {
			continue
		}
		dimensions = append(dimensions, spec.metadata())
	}
	metrics := make([]generated.MetricMetadata, 0, len(reportMetrics))
	for _, spec := range reportMetrics {
		metrics = append(metrics, spec.metadata())
	}
	return generated.PropertiesGetMetadata200JSONResponse{
		Name: "properties/" + propertyID + "/metadata", Dimensions: dimensions, Metrics: metrics,
	}, nil
}

func (s *server) PropertiesAudienceExportsList(ctx context.Context, request generated.PropertiesAudienceExportsListRequestObject) (generated.PropertiesAudienceExportsListResponseObject, error) {
	propertyID := string(request.Property)
	if _, err := s.loadProperty(ctx, propertyID); err != nil {
		if response, ok := reportError(err); ok {
			return generated.PropertiesAudienceExportsListdefaultJSONResponse{Body: response.Body, StatusCode: response.StatusCode}, nil
		}
		return nil, err
	}
	exports, err := s.listExports(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	offset := 0
	if request.Params.PageToken != nil && *request.Params.PageToken != "" {
		parsed, err := strconv.Atoi(*request.Params.PageToken)
		if err != nil || parsed < 0 {
			return generated.PropertiesAudienceExportsListdefaultJSONResponse{Body: errorEnvelope(400, "INVALID_ARGUMENT", "invalid pageToken"), StatusCode: 400}, nil
		}
		offset = parsed
	}
	limit := len(exports)
	if request.Params.PageSize != nil {
		limit = *request.Params.PageSize
	}
	if offset > len(exports) {
		offset = len(exports)
	}
	end := min(offset+limit, len(exports))
	page := make([]generated.AudienceExport, 0, end-offset)
	for _, export := range exports[offset:end] {
		page = append(page, audienceModel(export))
	}
	response := generated.ListAudienceExportsResponse{AudienceExports: page}
	if end < len(exports) {
		token := strconv.Itoa(end)
		response.NextPageToken = &token
	}
	return generated.PropertiesAudienceExportsList200JSONResponse(response), nil
}

func (s *server) PropertiesAudienceExportsCreate(ctx context.Context, request generated.PropertiesAudienceExportsCreateRequestObject) (generated.PropertiesAudienceExportsCreateResponseObject, error) {
	if request.Body == nil {
		return generated.PropertiesAudienceExportsCreatedefaultJSONResponse{Body: errorEnvelope(400, "INVALID_ARGUMENT", "Request body is required"), StatusCode: 400}, nil
	}
	propertyID := string(request.Property)
	if _, err := s.loadProperty(ctx, propertyID); err != nil {
		if response, ok := reportError(err); ok {
			return generated.PropertiesAudienceExportsCreatedefaultJSONResponse{Body: response.Body, StatusCode: response.StatusCode}, nil
		}
		return nil, err
	}
	audience := ""
	if request.Body.Audience != nil {
		audience = *request.Body.Audience
	}
	prefix := "properties/" + propertyID + "/audiences/"
	audienceID := strings.TrimPrefix(audience, prefix)
	if !strings.HasPrefix(audience, prefix) || audienceID == "" || strings.Contains(audienceID, "/") {
		return generated.PropertiesAudienceExportsCreatedefaultJSONResponse{
			Body: errorEnvelope(400, "INVALID_ARGUMENT", "audience must be properties/{property}/audiences/{audience}"), StatusCode: 400,
		}, nil
	}
	if request.Body.Dimensions == nil || len(*request.Body.Dimensions) == 0 {
		return generated.PropertiesAudienceExportsCreatedefaultJSONResponse{Body: errorEnvelope(400, "INVALID_ARGUMENT", "dimensions is required"), StatusCode: 400}, nil
	}
	dimensions := make([]string, len(*request.Body.Dimensions))
	for i, dimension := range *request.Body.Dimensions {
		if dimension.DimensionName == "" {
			return generated.PropertiesAudienceExportsCreatedefaultJSONResponse{Body: errorEnvelope(400, "INVALID_ARGUMENT", "dimensions.dimensionName is required"), StatusCode: 400}, nil
		}
		dimensions[i] = dimension.DimensionName
	}
	id, err := s.ids.Next(ctx, "ga4.audienceExport")
	if err != nil {
		return nil, fmt.Errorf("ga4: allocate audience export ID: %w", err)
	}
	export := fixtureAudienceExport{
		Name: "properties/" + propertyID + "/audienceExports/" + id, PropertyID: propertyID,
		Audience: audience, AudienceDisplayName: audienceID, Dimensions: dimensions,
		State: "ACTIVE", RowCount: 0, PercentageCompleted: 100,
		BeginCreatingTime: s.clock.Now().UTC().Format(time.RFC3339), CreationQuotaTokensCharged: 0,
	}
	if err := s.insertExport(ctx, export); err != nil {
		return nil, err
	}
	return generated.PropertiesAudienceExportsCreate200JSONResponse{
		Name: "operations/" + id, Done: true, Response: audienceModel(export),
	}, nil
}

func (s *server) PropertiesAudienceExportsGet(ctx context.Context, request generated.PropertiesAudienceExportsGetRequestObject) (generated.PropertiesAudienceExportsGetResponseObject, error) {
	export, err := s.loadExport(ctx, string(request.Property), string(request.AudienceExport))
	if err != nil {
		if response, ok := reportError(err); ok {
			return generated.PropertiesAudienceExportsGetdefaultJSONResponse{Body: response.Body, StatusCode: response.StatusCode}, nil
		}
		return nil, err
	}
	return generated.PropertiesAudienceExportsGet200JSONResponse(audienceModel(export)), nil
}

func (s *server) PropertiesAudienceExportsQuery(ctx context.Context, request generated.PropertiesAudienceExportsQueryRequestObject) (generated.PropertiesAudienceExportsQueryResponseObject, error) {
	if request.Body != nil {
		if _, err := parseLimit(request.Body.Limit, reportLimitDefault); err != nil {
			if response, ok := reportError(err); ok {
				return generated.PropertiesAudienceExportsQuerydefaultJSONResponse{Body: response.Body, StatusCode: response.StatusCode}, nil
			}
			return nil, err
		}
		if _, err := parseLimit(request.Body.Offset, 0); err != nil {
			if response, ok := reportError(err); ok {
				return generated.PropertiesAudienceExportsQuerydefaultJSONResponse{Body: response.Body, StatusCode: response.StatusCode}, nil
			}
			return nil, err
		}
	}
	export, err := s.loadExport(ctx, string(request.Property), string(request.AudienceExport))
	if err != nil {
		if response, ok := reportError(err); ok {
			return generated.PropertiesAudienceExportsQuerydefaultJSONResponse{Body: response.Body, StatusCode: response.StatusCode}, nil
		}
		return nil, err
	}
	return generated.PropertiesAudienceExportsQuery200JSONResponse{
		AudienceExport: audienceModel(export), AudienceRows: []generated.AudienceRow{}, RowCount: export.RowCount,
	}, nil
}

func (s *server) runReport(ctx context.Context, propertyID string, body *generated.RunReportRequest) (generated.RunReportResponse, error) {
	property, err := s.loadProperty(ctx, propertyID)
	if err != nil {
		return generated.RunReportResponse{}, err
	}
	if !sameProperty(propertyID, body.Property) {
		return generated.RunReportResponse{}, invalid("property must match the URL property")
	}
	if body.DateRanges == nil {
		return generated.RunReportResponse{}, invalid("dateRanges is required")
	}
	ranges, err := parseRanges(*body.DateRanges, propertyToday(property.TimeZone, s.clock.Now()))
	if err != nil {
		return generated.RunReportResponse{}, err
	}
	return s.execute(ctx, property, body.Dimensions, body.Metrics, body.DimensionFilter, body.MetricFilter, body.OrderBys, body.Limit, body.Offset, body.MetricAggregations, ranges, false, kindRunReport)
}

func (s *server) runRealtime(ctx context.Context, propertyID string, body *generated.RunRealtimeReportRequest) (generated.RunRealtimeReportResponse, error) {
	property, err := s.loadProperty(ctx, propertyID)
	if err != nil {
		return generated.RunRealtimeReportResponse{}, err
	}
	if body.MinuteRanges != nil {
		for i, minuteRange := range *body.MinuteRanges {
			if minuteRange.StartMinutesAgo == nil || minuteRange.EndMinutesAgo == nil {
				return generated.RunRealtimeReportResponse{}, invalid(fmt.Sprintf("minuteRanges[%d] requires startMinutesAgo and endMinutesAgo", i))
			}
			start, end := *minuteRange.StartMinutesAgo, *minuteRange.EndMinutesAgo
			if end < 0 || start < end || start > 60 {
				return generated.RunRealtimeReportResponse{}, invalid("minuteRanges must satisfy 0 <= endMinutesAgo <= startMinutesAgo <= 60")
			}
		}
	}
	var aggregations *[]generated.RunReportRequestMetricAggregations
	if body.MetricAggregations != nil {
		converted := make([]generated.RunReportRequestMetricAggregations, len(*body.MetricAggregations))
		for i, value := range *body.MetricAggregations {
			converted[i] = generated.RunReportRequestMetricAggregations(value)
		}
		aggregations = &converted
	}
	report, err := s.execute(ctx, property, body.Dimensions, body.Metrics, body.DimensionFilter, body.MetricFilter, body.OrderBys, body.Limit, nil, aggregations, nil, true, kindRealtime)
	if err != nil {
		return generated.RunRealtimeReportResponse{}, err
	}
	return generated.RunRealtimeReportResponse{
		Kind: report.Kind, DimensionHeaders: report.DimensionHeaders, MetricHeaders: report.MetricHeaders,
		Rows: report.Rows, RowCount: report.RowCount, Totals: report.Totals, Maximums: report.Maximums, Minimums: report.Minimums,
	}, nil
}

func (s *server) execute(ctx context.Context, property fixtureProperty, dimensions *[]generated.Dimension, metrics *[]generated.Metric, dimensionFilter, metricFilter *generated.FilterExpression, orderBys *[]generated.OrderBy, limit, offset *string, aggregations *[]generated.RunReportRequestMetricAggregations, ranges []parsedRange, realtime bool, kind string) (generated.RunReportResponse, error) {
	dims, mets := namesOf(dimensions, metrics)
	parsedLimit, err := parseLimit(limit, reportLimitDefault)
	if err != nil {
		return generated.RunReportResponse{}, err
	}
	parsedOffset, err := parseLimit(offset, 0)
	if err != nil {
		return generated.RunReportResponse{}, err
	}
	var orders []generated.OrderBy
	if orderBys != nil {
		orders = *orderBys
	}
	var aggregationNames []string
	if aggregations != nil {
		aggregationNames = make([]string, len(*aggregations))
		for i, value := range *aggregations {
			aggregationNames[i] = string(value)
		}
	}
	events := []storedEvent{}
	if !realtime {
		events, err = s.loadEvents(ctx, property.PropertyID)
		if err != nil {
			return generated.RunReportResponse{}, err
		}
	}
	return buildReport(events, reportBuild{
		kind: kind, dimensions: dims, metrics: mets, ranges: ranges,
		dimensionFilter: dimensionFilter, metricFilter: metricFilter, orderBys: orders,
		aggregations: aggregationNames, limit: parsedLimit, offset: parsedOffset,
		currency: property.CurrencyCode, timeZone: property.TimeZone, includeMeta: !realtime, realtime: realtime,
	})
}

func (s *server) loadProperty(ctx context.Context, propertyID string) (fixtureProperty, error) {
	var property fixtureProperty
	err := s.db.QueryRowContext(ctx, `SELECT property_id, display_name, currency_code, time_zone FROM properties WHERE property_id=?`, propertyID).
		Scan(&property.PropertyID, &property.DisplayName, &property.CurrencyCode, &property.TimeZone)
	if errors.Is(err, sql.ErrNoRows) {
		return fixtureProperty{}, notFound("property not found")
	}
	if err != nil {
		return fixtureProperty{}, err
	}
	return property, nil
}

func (s *server) loadEvents(ctx context.Context, propertyID string) ([]storedEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT event_date, event_name, transaction_id, session_source, session_medium,
		session_campaign_name, event_count, transactions, purchase_revenue, item_refund_amount
		FROM events WHERE property_id=? ORDER BY event_date, transaction_id, event_name`, propertyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []storedEvent{}
	for rows.Next() {
		var event storedEvent
		var date string
		if err := rows.Scan(&date, &event.eventName, &event.transactionID, &event.sessionSource, &event.sessionMedium,
			&event.sessionCampaign, &event.eventCount, &event.transactions, &event.purchaseRevenue, &event.itemRefundAmount); err != nil {
			return nil, err
		}
		parsed, err := time.Parse("2006-01-02", date)
		if err != nil {
			return nil, err
		}
		event.date = parsed
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *server) insertExport(ctx context.Context, export fixtureAudienceExport) error {
	dimensions, err := json.Marshal(export.Dimensions)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO audience_exports(
		name, property_id, audience, audience_display_name, dimensions, state, row_count,
		percentage_completed, begin_creating_time, creation_quota_tokens_charged, error_message)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		export.Name, export.PropertyID, export.Audience, export.AudienceDisplayName, string(dimensions),
		export.State, export.RowCount, export.PercentageCompleted, export.BeginCreatingTime,
		export.CreationQuotaTokensCharged, export.ErrorMessage)
	return err
}

func (s *server) listExports(ctx context.Context, propertyID string) ([]fixtureAudienceExport, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, property_id, audience, audience_display_name, dimensions, state,
		row_count, percentage_completed, begin_creating_time, creation_quota_tokens_charged, error_message
		FROM audience_exports WHERE property_id=? ORDER BY name`, propertyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	exports := []fixtureAudienceExport{}
	for rows.Next() {
		export, err := scanExport(rows)
		if err != nil {
			return nil, err
		}
		exports = append(exports, export)
	}
	return exports, rows.Err()
}

func (s *server) loadExport(ctx context.Context, propertyID, audienceExportID string) (fixtureAudienceExport, error) {
	if _, err := s.loadProperty(ctx, propertyID); err != nil {
		return fixtureAudienceExport{}, err
	}
	name := "properties/" + propertyID + "/audienceExports/" + audienceExportID
	row := s.db.QueryRowContext(ctx, `SELECT name, property_id, audience, audience_display_name, dimensions, state,
		row_count, percentage_completed, begin_creating_time, creation_quota_tokens_charged, error_message
		FROM audience_exports WHERE name=? AND property_id=?`, name, propertyID)
	export, err := scanExport(row)
	if errors.Is(err, sql.ErrNoRows) {
		return fixtureAudienceExport{}, notFound("audience export not found")
	}
	return export, err
}

func scanExport(row interface{ Scan(...any) error }) (fixtureAudienceExport, error) {
	var export fixtureAudienceExport
	var dimensions string
	err := row.Scan(&export.Name, &export.PropertyID, &export.Audience, &export.AudienceDisplayName, &dimensions,
		&export.State, &export.RowCount, &export.PercentageCompleted, &export.BeginCreatingTime,
		&export.CreationQuotaTokensCharged, &export.ErrorMessage)
	if err != nil {
		return fixtureAudienceExport{}, err
	}
	if err := json.Unmarshal([]byte(dimensions), &export.Dimensions); err != nil {
		return fixtureAudienceExport{}, err
	}
	if export.Dimensions == nil {
		export.Dimensions = []string{}
	}
	return export, nil
}

func audienceModel(export fixtureAudienceExport) generated.AudienceExport {
	name, audience, display := export.Name, export.Audience, export.AudienceDisplayName
	state := generated.AudienceExportState(export.State)
	begin, message := export.BeginCreatingTime, export.ErrorMessage
	quota, rows := export.CreationQuotaTokensCharged, export.RowCount
	completed := float32(export.PercentageCompleted)
	dimensions := make([]generated.AudienceDimension, len(export.Dimensions))
	for i, dimension := range export.Dimensions {
		dimensions[i] = generated.AudienceDimension{DimensionName: dimension}
	}
	return generated.AudienceExport{
		Name: &name, Audience: &audience, AudienceDisplayName: &display, Dimensions: &dimensions,
		State: &state, BeginCreatingTime: &begin, CreationQuotaTokensCharged: &quota, RowCount: &rows,
		PercentageCompleted: &completed, ErrorMessage: &message,
	}
}

func keepCompatibility(filter string, compatible bool) bool {
	switch filter {
	case "", "COMPATIBILITY_UNSPECIFIED":
		return true
	case "COMPATIBLE":
		return compatible
	case "INCOMPATIBLE":
		return !compatible
	default:
		return true
	}
}

type apiResponse struct {
	Body       generated.ErrorEnvelope
	StatusCode int
}

func reportError(err error) (apiResponse, bool) {
	var api *statusError
	if !errors.As(err, &api) {
		return apiResponse{}, false
	}
	return apiResponse{Body: errorEnvelope(api.status, api.code, api.message), StatusCode: api.status}, true
}

func errorEnvelope(status int, code, message string) generated.ErrorEnvelope {
	return generated.ErrorEnvelope{Error: generated.ErrorBody{
		Code: status, Message: message, Status: code,
		Errors: []generated.ErrorDetail{{Domain: "global", Reason: strings.ToLower(code), Message: message}},
	}}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorEnvelope(status, code, message))
}

package ga4

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dumbmachine/fabricate/resources/ga4/generated"
)

const (
	reportLimitDefault = 10000
	reportLimitMax     = 250000
	kindRunReport      = "analyticsData#runReport"
	kindBatch          = "analyticsData#batchRunReports"
	kindRealtime       = "analyticsData#runRealtimeReport"
)

type dimensionSpec struct {
	apiName     string
	uiName      string
	description string
	category    string
	realtime    bool
}

type metricSpec struct {
	apiName     string
	uiName      string
	description string
	category    string
	metricType  generated.MetricHeaderType
	metaType    generated.MetricMetadataType
}

var reportDimensions = []dimensionSpec{
	{"date", "Date", "The date of the event, formatted YYYYMMDD.", "Time", false},
	{"dateRange", "Date range", "The name of the date range for this row.", "Time", false},
	{"eventName", "Event name", "The name of the event.", "Event", true},
	{"minutesAgo", "Minutes ago", "Minutes before the request time. Realtime only; seeded events have a date and no minute.", "Time", true},
	{"sessionCampaignName", "Session campaign", "The campaign name of the session.", "Traffic source", true},
	{"sessionMedium", "Session medium", "The medium of the session.", "Traffic source", true},
	{"sessionSource", "Session source", "The source of the session.", "Traffic source", true},
	{"transactionId", "Transaction ID", "The transaction ID of the ecommerce event.", "Ecommerce", true},
}

var reportMetrics = []metricSpec{
	{"eventCount", "Event count", "The number of events.", "Event", generated.MetricHeaderTypeTYPEINTEGER, generated.MetricMetadataTypeTYPEINTEGER},
	{"itemRefundAmount", "Item refund amount", "Refund amount for the event, in the property currency.", "Ecommerce", generated.MetricHeaderTypeTYPECURRENCY, generated.MetricMetadataTypeTYPECURRENCY},
	{"purchaseRevenue", "Purchase revenue", "Purchase revenue for the event, in the property currency.", "Ecommerce", generated.MetricHeaderTypeTYPECURRENCY, generated.MetricMetadataTypeTYPECURRENCY},
	{"totalRevenue", "Total revenue", "Purchase revenue minus item refund amount, in the property currency.", "Ecommerce", generated.MetricHeaderTypeTYPECURRENCY, generated.MetricMetadataTypeTYPECURRENCY},
	{"transactions", "Transactions", "The number of purchase transactions.", "Ecommerce", generated.MetricHeaderTypeTYPEINTEGER, generated.MetricMetadataTypeTYPEINTEGER},
}

func dimensionByName(name string) (dimensionSpec, bool) {
	for _, spec := range reportDimensions {
		if spec.apiName == name {
			return spec, true
		}
	}
	return dimensionSpec{}, false
}

func metricByName(name string) (metricSpec, bool) {
	for _, spec := range reportMetrics {
		if spec.apiName == name {
			return spec, true
		}
	}
	return metricSpec{}, false
}

func (spec dimensionSpec) metadata() generated.DimensionMetadata {
	return generated.DimensionMetadata{
		ApiName: spec.apiName, UiName: spec.uiName, Description: spec.description,
		Category: spec.category, CustomDefinition: false,
	}
}

func (spec metricSpec) metadata() generated.MetricMetadata {
	return generated.MetricMetadata{
		ApiName: spec.apiName, UiName: spec.uiName, Description: spec.description,
		Category: spec.category, Type: spec.metaType, CustomDefinition: false,
	}
}

type parsedRange struct {
	name  string
	start time.Time
	end   time.Time
}

type statusError struct {
	status  int
	code    string
	message string
}

func (e *statusError) Error() string { return e.message }

func invalid(message string) error {
	return &statusError{status: 400, code: "INVALID_ARGUMENT", message: message}
}

func notFound(message string) error {
	return &statusError{status: 404, code: "NOT_FOUND", message: message}
}

type storedEvent struct {
	date             time.Time
	eventName        string
	transactionID    string
	sessionSource    string
	sessionMedium    string
	sessionCampaign  string
	eventCount       int64
	transactions     int64
	purchaseRevenue  int64
	itemRefundAmount int64
}

func (event storedEvent) dimension(name, dateRange string) (string, bool) {
	switch name {
	case "transactionId":
		return event.transactionID, true
	case "sessionSource":
		return notSet(event.sessionSource), true
	case "sessionMedium":
		return notSet(event.sessionMedium), true
	case "sessionCampaignName":
		return notSet(event.sessionCampaign), true
	case "eventName":
		return event.eventName, true
	case "date":
		return event.date.Format("20060102"), true
	case "dateRange":
		return dateRange, true
	default:
		return "", false
	}
}

func (event storedEvent) metric(name string) (int64, bool) {
	switch name {
	case "eventCount":
		return event.eventCount, true
	case "transactions":
		return event.transactions, true
	case "purchaseRevenue":
		return event.purchaseRevenue, true
	case "itemRefundAmount":
		return event.itemRefundAmount, true
	case "totalRevenue":
		return event.purchaseRevenue - event.itemRefundAmount, true
	default:
		return 0, false
	}
}

func notSet(value string) string {
	if value == "" {
		return "(not set)"
	}
	return value
}

type bucket struct {
	dims []string
	sums map[string]int64
}

type reportBuild struct {
	kind            string
	dimensions      []string
	metrics         []string
	ranges          []parsedRange
	dimensionFilter *generated.FilterExpression
	metricFilter    *generated.FilterExpression
	orderBys        []generated.OrderBy
	aggregations    []string
	limit           int
	offset          int
	currency        string
	timeZone        string
	includeMeta     bool
	realtime        bool
}

func buildReport(events []storedEvent, request reportBuild) (generated.RunReportResponse, error) {
	if err := validateNames(request.dimensions, request.realtime); err != nil {
		return generated.RunReportResponse{}, err
	}
	if len(request.metrics) == 0 {
		return generated.RunReportResponse{}, invalid("metrics is required")
	}
	seenMetric := map[string]struct{}{}
	for _, name := range request.metrics {
		if _, ok := metricByName(name); !ok {
			return generated.RunReportResponse{}, invalid(fmt.Sprintf("unknown metric %q", name))
		}
		if _, exists := seenMetric[name]; exists {
			return generated.RunReportResponse{}, invalid(fmt.Sprintf("duplicate metric %q", name))
		}
		seenMetric[name] = struct{}{}
	}
	if err := validateFilter(request.dimensionFilter, true); err != nil {
		return generated.RunReportResponse{}, err
	}
	if err := validateFilter(request.metricFilter, false); err != nil {
		return generated.RunReportResponse{}, err
	}
	dimensions := append([]string{}, request.dimensions...)
	if len(request.ranges) > 1 && !contains(dimensions, "dateRange") {
		dimensions = append(dimensions, "dateRange")
	}
	groups := map[string]*bucket{}
	order := []string{}
	for _, event := range events {
		ranges := request.ranges
		if len(ranges) == 0 {
			ranges = []parsedRange{{}}
		}
		for _, dateRange := range ranges {
			if len(request.ranges) > 0 && (event.date.Before(dateRange.start) || event.date.After(dateRange.end)) {
				continue
			}
			values := dimensionValues(event, dateRange.name)
			matched, err := matchFilter(request.dimensionFilter, values, nil)
			if err != nil {
				return generated.RunReportResponse{}, err
			}
			if !matched {
				continue
			}
			keyParts := make([]string, len(dimensions))
			for i, name := range dimensions {
				keyParts[i] = values[name]
			}
			key := strings.Join(keyParts, "\x00")
			row, ok := groups[key]
			if !ok {
				row = &bucket{dims: keyParts, sums: map[string]int64{}}
				groups[key] = row
				order = append(order, key)
			}
			for _, spec := range reportMetrics {
				value, _ := event.metric(spec.apiName)
				row.sums[spec.apiName] += value
			}
		}
	}
	filtered := make([]*bucket, 0, len(order))
	for _, key := range order {
		row := groups[key]
		matched, err := matchFilter(request.metricFilter, nil, row.sums)
		if err != nil {
			return generated.RunReportResponse{}, err
		}
		if matched {
			filtered = append(filtered, row)
		}
	}
	if err := sortBuckets(filtered, dimensions, request.metrics, request.orderBys); err != nil {
		return generated.RunReportResponse{}, err
	}
	response := generated.RunReportResponse{
		Kind:             request.kind,
		DimensionHeaders: dimensionHeaders(dimensions),
		MetricHeaders:    metricHeaders(request.metrics),
		Rows:             []generated.Row{},
		RowCount:         len(filtered),
	}
	if request.includeMeta {
		response.Metadata = generated.ResponseMetaData{CurrencyCode: request.currency, TimeZone: request.timeZone}
	}
	if err := applyAggregations(&response, filtered, dimensions, request.metrics, request.aggregations); err != nil {
		return generated.RunReportResponse{}, err
	}
	offset := request.offset
	if offset > len(filtered) {
		offset = len(filtered)
	}
	end := min(offset+request.limit, len(filtered))
	for _, row := range filtered[offset:end] {
		response.Rows = append(response.Rows, encodeRow(row.dims, request.metrics, row.sums))
	}
	return response, nil
}

func dimensionValues(event storedEvent, dateRange string) map[string]string {
	values := make(map[string]string, len(reportDimensions))
	for _, spec := range reportDimensions {
		if spec.apiName == "minutesAgo" {
			continue
		}
		value, ok := event.dimension(spec.apiName, dateRange)
		if ok {
			values[spec.apiName] = value
		}
	}
	return values
}

func validateNames(dimensions []string, realtime bool) error {
	seen := map[string]struct{}{}
	for _, name := range dimensions {
		spec, ok := dimensionByName(name)
		if !ok || (realtime && !spec.realtime) || (!realtime && name == "minutesAgo") {
			return invalid(fmt.Sprintf("unknown dimension %q", name))
		}
		if _, exists := seen[name]; exists {
			return invalid(fmt.Sprintf("duplicate dimension %q", name))
		}
		seen[name] = struct{}{}
	}
	return nil
}

func dimensionHeaders(names []string) []generated.DimensionHeader {
	headers := make([]generated.DimensionHeader, len(names))
	for i, name := range names {
		headers[i] = generated.DimensionHeader{Name: name}
	}
	return headers
}

func metricHeaders(names []string) []generated.MetricHeader {
	headers := make([]generated.MetricHeader, len(names))
	for i, name := range names {
		spec, _ := metricByName(name)
		headers[i] = generated.MetricHeader{Name: name, Type: spec.metricType}
	}
	return headers
}

func encodeRow(dims, metrics []string, sums map[string]int64) generated.Row {
	row := generated.Row{
		DimensionValues: make([]generated.DimensionValue, len(dims)),
		MetricValues:    make([]generated.MetricValue, len(metrics)),
	}
	for i, value := range dims {
		row.DimensionValues[i] = generated.DimensionValue{Value: value}
	}
	for i, name := range metrics {
		row.MetricValues[i] = generated.MetricValue{Value: strconv.FormatInt(sums[name], 10)}
	}
	return row
}

func applyAggregations(response *generated.RunReportResponse, rows []*bucket, dimensions, metrics, aggregations []string) error {
	if len(aggregations) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	for _, name := range aggregations {
		if _, exists := seen[name]; exists {
			return invalid(fmt.Sprintf("duplicate metric aggregation %q", name))
		}
		seen[name] = struct{}{}
		switch name {
		case "TOTAL", "MINIMUM", "MAXIMUM", "COUNT":
		default:
			return invalid(fmt.Sprintf("unknown metric aggregation %q", name))
		}
	}
	reserved := func(label string) []string {
		values := make([]string, len(dimensions))
		for i := range values {
			values[i] = label
		}
		return values
	}
	if contains(aggregations, "TOTAL") {
		sums := map[string]int64{}
		for _, row := range rows {
			for _, name := range metrics {
				sums[name] += row.sums[name]
			}
		}
		totals := []generated.Row{encodeRow(reserved("RESERVED_TOTAL"), metrics, sums)}
		response.Totals = &totals
	}
	if contains(aggregations, "MINIMUM") {
		sums := map[string]int64{}
		for _, name := range metrics {
			for j, row := range rows {
				if j == 0 || row.sums[name] < sums[name] {
					sums[name] = row.sums[name]
				}
			}
		}
		minimums := []generated.Row{encodeRow(reserved("RESERVED_MINIMUM"), metrics, sums)}
		response.Minimums = &minimums
	}
	if contains(aggregations, "MAXIMUM") {
		sums := map[string]int64{}
		for _, name := range metrics {
			for j, row := range rows {
				if j == 0 || row.sums[name] > sums[name] {
					sums[name] = row.sums[name]
				}
			}
		}
		maximums := []generated.Row{encodeRow(reserved("RESERVED_MAXIMUM"), metrics, sums)}
		response.Maximums = &maximums
	}
	if contains(aggregations, "COUNT") {
		sums := map[string]int64{}
		for _, name := range metrics {
			sums[name] = int64(len(rows))
		}
		totals := []generated.Row{encodeRow(reserved("RESERVED_COUNT"), metrics, sums)}
		if response.Totals == nil {
			response.Totals = &totals
		} else {
			combined := append(*response.Totals, totals...)
			response.Totals = &combined
		}
	}
	return nil
}

func sortBuckets(rows []*bucket, dimensions, metrics []string, orderBys []generated.OrderBy) error {
	dimIndex := map[string]int{}
	for i, name := range dimensions {
		dimIndex[name] = i
	}
	metricIndex := map[string]int{}
	for i, name := range metrics {
		metricIndex[name] = i
	}
	for _, order := range orderBys {
		switch {
		case order.Dimension != nil && order.Metric != nil:
			return invalid("orderBy accepts dimension or metric, not both")
		case order.Dimension == nil && order.Metric == nil:
			return invalid("orderBy requires dimension or metric")
		case order.Dimension != nil:
			if _, ok := dimIndex[order.Dimension.DimensionName]; !ok {
				return invalid(fmt.Sprintf("orderBy dimension %q is not in the request", order.Dimension.DimensionName))
			}
		case order.Metric != nil:
			if _, ok := metricIndex[order.Metric.MetricName]; !ok {
				return invalid(fmt.Sprintf("orderBy metric %q is not in the request", order.Metric.MetricName))
			}
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return compareBuckets(rows[i], rows[j], dimensions, orderBys, dimIndex) < 0
	})
	return nil
}

func compareBuckets(left, right *bucket, dimensions []string, orderBys []generated.OrderBy, dimIndex map[string]int) int {
	if len(orderBys) == 0 {
		for i := range dimensions {
			if cmp := strings.Compare(left.dims[i], right.dims[i]); cmp != 0 {
				return cmp
			}
		}
		return 0
	}
	for _, order := range orderBys {
		cmp := 0
		switch {
		case order.Dimension != nil:
			idx := dimIndex[order.Dimension.DimensionName]
			cmp = compareDimension(left.dims[idx], right.dims[idx], order.Dimension.OrderType)
		case order.Metric != nil:
			cmp = compareInt(left.sums[order.Metric.MetricName], right.sums[order.Metric.MetricName])
		}
		if order.Desc != nil && *order.Desc {
			cmp = -cmp
		}
		if cmp != 0 {
			return cmp
		}
	}
	return 0
}

func compareDimension(left, right string, orderType *generated.DimensionOrderByOrderType) int {
	kind := generated.ALPHANUMERIC
	if orderType != nil && *orderType != "" && *orderType != generated.ORDERTYPEUNSPECIFIED {
		kind = *orderType
	}
	switch kind {
	case generated.NUMERIC:
		leftNumber, leftErr := strconv.ParseFloat(left, 64)
		rightNumber, rightErr := strconv.ParseFloat(right, 64)
		if leftErr == nil && rightErr == nil {
			return compareFloat(leftNumber, rightNumber)
		}
		return strings.Compare(left, right)
	case generated.CASEINSENSITIVEALPHANUMERIC:
		return strings.Compare(strings.ToLower(left), strings.ToLower(right))
	case generated.ALPHANUMERIC:
		return strings.Compare(left, right)
	default:
		return strings.Compare(left, right)
	}
}

func compareInt(left, right int64) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func compareFloat(left, right float64) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func propertyToday(timeZone string, now time.Time) time.Time {
	location, err := time.LoadLocation(timeZone)
	if err != nil {
		location = time.UTC
	}
	local := now.In(location)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
}

func parseRanges(ranges []generated.DateRange, today time.Time) ([]parsedRange, error) {
	if len(ranges) == 0 {
		return nil, invalid("dateRanges is required")
	}
	if len(ranges) > 4 {
		return nil, invalid("at most 4 date ranges are allowed")
	}
	parsed := make([]parsedRange, len(ranges))
	for i, dateRange := range ranges {
		start, err := parseCivilDate(dateRange.StartDate, today)
		if err != nil {
			return nil, invalid(fmt.Sprintf("dateRanges[%d].startDate: %s", i, err.Error()))
		}
		end, err := parseCivilDate(dateRange.EndDate, today)
		if err != nil {
			return nil, invalid(fmt.Sprintf("dateRanges[%d].endDate: %s", i, err.Error()))
		}
		if end.Before(start) {
			return nil, invalid(fmt.Sprintf("dateRanges[%d] endDate is before startDate", i))
		}
		name := fmt.Sprintf("date_range_%d", i)
		if dateRange.Name != nil && *dateRange.Name != "" {
			name = *dateRange.Name
			if strings.HasPrefix(name, "date_range_") || strings.HasPrefix(name, "RESERVED_") {
				return nil, invalid(fmt.Sprintf("dateRanges[%d].name cannot begin with date_range_ or RESERVED_", i))
			}
		}
		parsed[i] = parsedRange{name: name, start: start, end: end}
	}
	return parsed, nil
}

func parseCivilDate(value string, today time.Time) (time.Time, error) {
	switch value {
	case "today":
		return today, nil
	case "yesterday":
		return today.AddDate(0, 0, -1), nil
	}
	if strings.HasSuffix(value, "daysAgo") {
		days, err := strconv.Atoi(strings.TrimSuffix(value, "daysAgo"))
		if err != nil || days < 0 || !strings.HasSuffix(value, "daysAgo") {
			return time.Time{}, fmt.Errorf("expected YYYY-MM-DD, today, yesterday, or NdaysAgo")
		}
		return today.AddDate(0, 0, -days), nil
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return time.Time{}, fmt.Errorf("expected YYYY-MM-DD, today, yesterday, or NdaysAgo")
	}
	return parsed, nil
}

func parseLimit(raw *string, fallback int) (int, error) {
	if raw == nil || *raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(*raw, 10, 64)
	if err != nil || value < 0 {
		return 0, invalid("limit and offset must be non-negative integers encoded as strings")
	}
	if value > reportLimitMax {
		value = reportLimitMax
	}
	return int(value), nil
}

func sameProperty(propertyID string, body *string) bool {
	if body == nil || *body == "" {
		return true
	}
	return *body == propertyID || *body == "properties/"+propertyID
}

func namesOf(dimensions *[]generated.Dimension, metrics *[]generated.Metric) (dims, mets []string) {
	if dimensions != nil {
		dims = make([]string, len(*dimensions))
		for i, dimension := range *dimensions {
			dims[i] = dimension.Name
		}
	}
	if metrics != nil {
		mets = make([]string, len(*metrics))
		for i, metric := range *metrics {
			mets[i] = metric.Name
		}
	}
	return dims, mets
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func validateFilter(expr *generated.FilterExpression, dimension bool) error {
	if expr == nil {
		return nil
	}
	branches := 0
	if expr.AndGroup != nil {
		branches++
	}
	if expr.OrGroup != nil {
		branches++
	}
	if expr.NotExpression != nil {
		branches++
	}
	if expr.Filter != nil {
		branches++
	}
	if branches > 1 {
		return invalid("a filter expression accepts only one of andGroup, orGroup, notExpression, or filter")
	}
	if expr.AndGroup != nil {
		for i := range expr.AndGroup.Expressions {
			if err := validateFilter(&expr.AndGroup.Expressions[i], dimension); err != nil {
				return err
			}
		}
	}
	if expr.OrGroup != nil {
		for i := range expr.OrGroup.Expressions {
			if err := validateFilter(&expr.OrGroup.Expressions[i], dimension); err != nil {
				return err
			}
		}
	}
	if expr.NotExpression != nil {
		if err := validateFilter(expr.NotExpression, dimension); err != nil {
			return err
		}
	}
	if expr.Filter != nil {
		return validateLeaf(*expr.Filter, dimension)
	}
	return nil
}

func validateLeaf(filter generated.Filter, dimension bool) error {
	modes := 0
	if filter.StringFilter != nil {
		modes++
	}
	if filter.InListFilter != nil {
		modes++
	}
	if filter.NumericFilter != nil {
		modes++
	}
	if modes != 1 {
		return invalid("filter requires one of stringFilter, inListFilter, or numericFilter")
	}
	if dimension && filter.NumericFilter != nil {
		return invalid(fmt.Sprintf("dimension filter %q does not accept numericFilter", filter.FieldName))
	}
	if !dimension && (filter.StringFilter != nil || filter.InListFilter != nil) {
		return invalid(fmt.Sprintf("metric filter %q requires numericFilter", filter.FieldName))
	}
	if dimension {
		if _, ok := dimensionByName(filter.FieldName); !ok || filter.FieldName == "minutesAgo" {
			return invalid(fmt.Sprintf("unknown dimension %q", filter.FieldName))
		}
	} else if _, ok := metricByName(filter.FieldName); !ok {
		return invalid(fmt.Sprintf("unknown metric %q", filter.FieldName))
	}
	if filter.StringFilter != nil {
		return compileMatch(*filter.StringFilter)
	}
	if filter.NumericFilter != nil && filter.NumericFilter.Value.Int64Value == nil && filter.NumericFilter.Value.DoubleValue == nil {
		return invalid("numericFilter.value requires int64Value or doubleValue")
	}
	if filter.NumericFilter != nil && filter.NumericFilter.Value.Int64Value != nil {
		if _, err := strconv.ParseInt(*filter.NumericFilter.Value.Int64Value, 10, 64); err != nil {
			return invalid("numericFilter.value.int64Value must be an integer string")
		}
	}
	return nil
}

func compileMatch(filter generated.StringFilter) error {
	matchType := generated.EXACT
	if filter.MatchType != nil && *filter.MatchType != "" && *filter.MatchType != generated.MATCHTYPEUNSPECIFIED {
		matchType = *filter.MatchType
	}
	if matchType != generated.FULLREGEXP && matchType != generated.PARTIALREGEXP {
		return nil
	}
	pattern := filter.Value
	if filter.CaseSensitive == nil || !*filter.CaseSensitive {
		pattern = "(?i)" + pattern
	}
	if matchType == generated.FULLREGEXP {
		pattern = "^(?:" + pattern + ")$"
	}
	if _, err := regexp.Compile(pattern); err != nil {
		return invalid("stringFilter value is not a valid regular expression")
	}
	return nil
}

func matchFilter(expr *generated.FilterExpression, dimensions map[string]string, metrics map[string]int64) (bool, error) {
	if expr == nil {
		return true, nil
	}
	switch {
	case expr.AndGroup != nil:
		for i := range expr.AndGroup.Expressions {
			ok, err := matchFilter(&expr.AndGroup.Expressions[i], dimensions, metrics)
			if err != nil || !ok {
				return ok, err
			}
		}
		return true, nil
	case expr.OrGroup != nil:
		if len(expr.OrGroup.Expressions) == 0 {
			return false, nil
		}
		for i := range expr.OrGroup.Expressions {
			ok, err := matchFilter(&expr.OrGroup.Expressions[i], dimensions, metrics)
			if err != nil || ok {
				return ok, err
			}
		}
		return false, nil
	case expr.NotExpression != nil:
		ok, err := matchFilter(expr.NotExpression, dimensions, metrics)
		return !ok, err
	case expr.Filter != nil:
		return matchLeaf(*expr.Filter, dimensions, metrics)
	default:
		return true, nil
	}
}

func matchLeaf(filter generated.Filter, dimensions map[string]string, metrics map[string]int64) (bool, error) {
	if filter.NumericFilter != nil {
		return compareNumeric(metrics[filter.FieldName], filter.NumericFilter), nil
	}
	value := dimensions[filter.FieldName]
	if filter.InListFilter != nil {
		for _, candidate := range filter.InListFilter.Values {
			if foldEqual(value, candidate, filter.InListFilter.CaseSensitive) {
				return true, nil
			}
		}
		return false, nil
	}
	return matchString(value, *filter.StringFilter)
}

func foldEqual(left, right string, caseSensitive *bool) bool {
	if caseSensitive != nil && *caseSensitive {
		return left == right
	}
	return strings.EqualFold(left, right)
}

func matchString(value string, filter generated.StringFilter) (bool, error) {
	matchType := generated.EXACT
	if filter.MatchType != nil && *filter.MatchType != "" && *filter.MatchType != generated.MATCHTYPEUNSPECIFIED {
		matchType = *filter.MatchType
	}
	left, right := value, filter.Value
	sensitive := filter.CaseSensitive != nil && *filter.CaseSensitive
	if !sensitive && matchType != generated.FULLREGEXP && matchType != generated.PARTIALREGEXP {
		left, right = strings.ToLower(left), strings.ToLower(right)
	}
	switch matchType {
	case generated.EXACT, generated.MATCHTYPEUNSPECIFIED:
		return left == right, nil
	case generated.BEGINSWITH:
		return strings.HasPrefix(left, right), nil
	case generated.ENDSWITH:
		return strings.HasSuffix(left, right), nil
	case generated.CONTAINS:
		return strings.Contains(left, right), nil
	case generated.FULLREGEXP, generated.PARTIALREGEXP:
		pattern := filter.Value
		if !sensitive {
			pattern = "(?i)" + pattern
		}
		if matchType == generated.FULLREGEXP {
			pattern = "^(?:" + pattern + ")$"
		}
		return regexp.MatchString(pattern, value)
	default:
		return false, invalid("unsupported stringFilter matchType")
	}
}

func compareNumeric(actual int64, filter *generated.NumericFilter) bool {
	var wanted float64
	if filter.Value.Int64Value != nil {
		parsed, _ := strconv.ParseInt(*filter.Value.Int64Value, 10, 64)
		wanted = float64(parsed)
	} else if filter.Value.DoubleValue != nil {
		wanted = float64(*filter.Value.DoubleValue)
	}
	got := float64(actual)
	switch filter.Operation {
	case generated.LESSTHAN:
		return got < wanted
	case generated.LESSTHANOREQUAL:
		return got <= wanted
	case generated.GREATERTHAN:
		return got > wanted
	case generated.GREATERTHANOREQUAL:
		return got >= wanted
	case generated.EQUAL, generated.OPERATIONUNSPECIFIED, "":
		return got == wanted
	default:
		return false
	}
}

package redshift

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/dumbmachine/fabricate/resources/redshift/generated"
)

// sqlError is a rejected statement. The server turns it into ValidationException.
// The parser accepts only a single SELECT of one seeded table.
type sqlError struct{ msg string }

func (e *sqlError) Error() string { return e.msg }

func sqlFail(format string, args ...any) error {
	return &sqlError{msg: fmt.Sprintf(format, args...)}
}

type literalKind int

const (
	litString literalKind = iota
	litInt
	litBool
)

type literal struct {
	kind literalKind
	str  string
	n    int64
	b    bool
}

type predicate struct {
	column  string
	literal literal
}

type selectQuery struct {
	star    bool
	columns []string
	parts   []string
	where   *predicate
	limit   *int
}

type queryResult struct {
	columns []generated.ColumnMetadata
	records [][]generated.Field
}

func executeSelect(database, schemaName string, tables map[string]tableDef, rows map[string][]map[string]any, sqlText string, params []generated.SqlParameter) (queryResult, error) {
	substituted, err := applyParameters(sqlText, params)
	if err != nil {
		return queryResult{}, err
	}
	query, err := parseSelect(substituted)
	if err != nil {
		return queryResult{}, err
	}
	tableName, err := resolveTable(database, schemaName, tables, query.parts)
	if err != nil {
		return queryResult{}, err
	}
	table := tables[tableName]
	selected, err := selectedColumns(table, query)
	if err != nil {
		return queryResult{}, err
	}
	filtered := make([]map[string]any, 0, len(rows[tableName]))
	for _, row := range rows[tableName] {
		ok, err := rowMatches(table, row, query.where)
		if err != nil {
			return queryResult{}, err
		}
		if ok {
			filtered = append(filtered, row)
		}
	}
	if query.limit != nil && *query.limit < len(filtered) {
		filtered = filtered[:*query.limit]
	}
	result := queryResult{
		columns: make([]generated.ColumnMetadata, 0, len(selected)),
		records: make([][]generated.Field, 0, len(filtered)),
	}
	for _, column := range selected {
		result.columns = append(result.columns, column.metadata(schemaName, tableName))
	}
	for _, row := range filtered {
		record := make([]generated.Field, 0, len(selected))
		for _, column := range selected {
			field, err := fieldFor(column, row[column.Name])
			if err != nil {
				return queryResult{}, err
			}
			record = append(record, field)
		}
		result.records = append(result.records, record)
	}
	return result, nil
}

func resolveTable(database, schemaName string, tables map[string]tableDef, parts []string) (string, error) {
	var name string
	switch len(parts) {
	case 1:
		name = parts[0]
	case 2:
		if parts[0] != schemaName {
			return "", sqlFail("unknown schema %q", parts[0])
		}
		name = parts[1]
	case 3:
		if parts[0] != database || parts[1] != schemaName {
			return "", sqlFail("unknown table %q", strings.Join(parts, "."))
		}
		name = parts[2]
	default:
		return "", sqlFail("table name %q has too many qualifiers", strings.Join(parts, "."))
	}
	if _, ok := tables[name]; !ok {
		return "", sqlFail("unknown table %q", name)
	}
	return name, nil
}

func selectedColumns(table tableDef, query selectQuery) ([]columnDef, error) {
	if query.star {
		return table.Columns, nil
	}
	byName := map[string]columnDef{}
	for _, column := range table.Columns {
		byName[column.Name] = column
	}
	out := make([]columnDef, 0, len(query.columns))
	seen := map[string]struct{}{}
	for _, name := range query.columns {
		column, ok := byName[name]
		if !ok {
			return nil, sqlFail("unknown column %q on %s", name, table.Name)
		}
		if _, dup := seen[name]; dup {
			return nil, sqlFail("column %q is repeated", name)
		}
		seen[name] = struct{}{}
		out = append(out, column)
	}
	return out, nil
}

func rowMatches(table tableDef, row map[string]any, where *predicate) (bool, error) {
	if where == nil {
		return true, nil
	}
	var column columnDef
	found := false
	for _, candidate := range table.Columns {
		if candidate.Name == where.column {
			column = candidate
			found = true
			break
		}
	}
	if !found {
		return false, sqlFail("unknown column %q on %s", where.column, table.Name)
	}
	cell, ok := row[column.Name]
	if !ok || cell == nil {
		return false, nil
	}
	switch column.TypeName {
	case typeVarchar, typeTimestamp:
		text, ok := cell.(string)
		if !ok {
			return false, fmt.Errorf("redshift: column %s is not a string", column.Name)
		}
		switch where.literal.kind {
		case litString:
			return text == where.literal.str, nil
		case litInt:
			return text == strconv.FormatInt(where.literal.n, 10), nil
		default:
			return false, sqlFail("column %s cannot be compared with a boolean", column.Name)
		}
	case typeInt8:
		number, err := asInt64(cell)
		if err != nil {
			return false, err
		}
		switch where.literal.kind {
		case litInt:
			return number == where.literal.n, nil
		case litString:
			parsed, err := strconv.ParseInt(where.literal.str, 10, 64)
			if err != nil {
				return false, sqlFail("column %s cannot be compared with %q", column.Name, where.literal.str)
			}
			return number == parsed, nil
		default:
			return false, sqlFail("column %s cannot be compared with a boolean", column.Name)
		}
	case typeBool:
		value, ok := cell.(bool)
		if !ok {
			return false, fmt.Errorf("redshift: column %s is not a boolean", column.Name)
		}
		if where.literal.kind != litBool {
			return false, sqlFail("column %s requires true or false", column.Name)
		}
		return value == where.literal.b, nil
	default:
		return false, fmt.Errorf("redshift: column %s has unknown type %s", column.Name, column.TypeName)
	}
}

func fieldFor(column columnDef, cell any) (generated.Field, error) {
	if cell == nil {
		if !column.Nullable {
			return generated.Field{}, fmt.Errorf("redshift: column %s is null", column.Name)
		}
		isNull := true
		return generated.Field{IsNull: &isNull}, nil
	}
	switch column.TypeName {
	case typeVarchar, typeTimestamp:
		text, ok := cell.(string)
		if !ok {
			return generated.Field{}, fmt.Errorf("redshift: column %s is not a string", column.Name)
		}
		return generated.Field{StringValue: &text}, nil
	case typeInt8:
		number, err := asInt64(cell)
		if err != nil {
			return generated.Field{}, err
		}
		return generated.Field{LongValue: &number}, nil
	case typeBool:
		value, ok := cell.(bool)
		if !ok {
			return generated.Field{}, fmt.Errorf("redshift: column %s is not a boolean", column.Name)
		}
		return generated.Field{BooleanValue: &value}, nil
	default:
		return generated.Field{}, fmt.Errorf("redshift: column %s has unknown type %s", column.Name, column.TypeName)
	}
}

func asInt64(value any) (int64, error) {
	switch typed := value.(type) {
	case int64:
		return typed, nil
	case float64:
		if typed != float64(int64(typed)) {
			return 0, fmt.Errorf("redshift: numeric value %v is not an integer", typed)
		}
		return int64(typed), nil
	case json.Number:
		return typed.Int64()
	default:
		return 0, fmt.Errorf("redshift: numeric value %T is not an integer", value)
	}
}

func applyParameters(sqlText string, params []generated.SqlParameter) (string, error) {
	values := map[string]string{}
	for _, param := range params {
		if !validParamName(param.Name) {
			return "", sqlFail("parameter name %q is invalid", param.Name)
		}
		if _, exists := values[param.Name]; exists {
			return "", sqlFail("parameter %q is repeated", param.Name)
		}
		values[param.Name] = param.Value
	}
	var b strings.Builder
	used := map[string]bool{}
	inString := false
	inIdent := false
	for i := 0; i < len(sqlText); i++ {
		ch := sqlText[i]
		if inString {
			b.WriteByte(ch)
			if ch == '\'' {
				if i+1 < len(sqlText) && sqlText[i+1] == '\'' {
					b.WriteByte('\'')
					i++
					continue
				}
				inString = false
			}
			continue
		}
		if inIdent {
			b.WriteByte(ch)
			if ch == '"' {
				if i+1 < len(sqlText) && sqlText[i+1] == '"' {
					b.WriteByte('"')
					i++
					continue
				}
				inIdent = false
			}
			continue
		}
		if ch == '\'' {
			inString = true
			b.WriteByte(ch)
			continue
		}
		if ch == '"' {
			inIdent = true
			b.WriteByte(ch)
			continue
		}
		if ch != ':' {
			b.WriteByte(ch)
			continue
		}
		if i+1 < len(sqlText) && sqlText[i+1] == ':' {
			return "", sqlFail("casts are not supported")
		}
		start := i + 1
		end := start
		for end < len(sqlText) && isIdentByte(sqlText[end]) {
			end++
		}
		if end == start {
			return "", sqlFail("expected a parameter name after ':'")
		}
		name := sqlText[start:end]
		value, ok := values[name]
		if !ok {
			return "", sqlFail("parameter %q is not defined", name)
		}
		used[name] = true
		b.WriteString(quoteLiteral(value))
		i = end - 1
	}
	if inString || inIdent {
		return "", sqlFail("unterminated literal")
	}
	for name := range values {
		if !used[name] {
			return "", sqlFail("parameter %q is not used in the SQL statement", name)
		}
	}
	return b.String(), nil
}

func quoteLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func validParamName(value string) bool {
	if value == "" || !isIdentStart(value[0]) {
		return false
	}
	for i := 1; i < len(value); i++ {
		if !isIdentByte(value[i]) {
			return false
		}
	}
	return true
}

func isIdentStart(ch byte) bool {
	return ch == '_' || (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z')
}

func isIdentByte(ch byte) bool {
	return isIdentStart(ch) || (ch >= '0' && ch <= '9')
}

type tokenKind int

const (
	tokEOF tokenKind = iota
	tokIdent
	tokStar
	tokComma
	tokDot
	tokEq
	tokString
	tokNumber
	tokSemi
)

type token struct {
	kind   tokenKind
	text   string
	quoted bool
}

type scanner struct {
	src string
	pos int
}

func parseSelect(sqlText string) (selectQuery, error) {
	sc := scanner{src: sqlText}
	var query selectQuery
	tok, err := sc.next()
	if err != nil {
		return selectQuery{}, err
	}
	if !tok.isKeyword("select") {
		return selectQuery{}, sqlFail("only SELECT is supported")
	}
	tok, err = sc.next()
	if err != nil {
		return selectQuery{}, err
	}
	if tok.kind == tokStar {
		query.star = true
		tok, err = sc.next()
		if err != nil {
			return selectQuery{}, err
		}
	} else {
		for {
			if tok.kind != tokIdent || tok.isKeyword("from") {
				return selectQuery{}, sqlFail("SELECT requires a column list or *")
			}
			query.columns = append(query.columns, tok.identifier())
			tok, err = sc.next()
			if err != nil {
				return selectQuery{}, err
			}
			if tok.kind != tokComma {
				break
			}
			tok, err = sc.next()
			if err != nil {
				return selectQuery{}, err
			}
		}
	}
	if !tok.isKeyword("from") {
		return selectQuery{}, sqlFail("SELECT requires FROM")
	}
	for {
		tok, err = sc.next()
		if err != nil {
			return selectQuery{}, err
		}
		if tok.kind != tokIdent {
			return selectQuery{}, sqlFail("FROM requires a table name")
		}
		query.parts = append(query.parts, tok.identifier())
		tok, err = sc.next()
		if err != nil {
			return selectQuery{}, err
		}
		if tok.kind != tokDot {
			break
		}
	}
	if tok.isKeyword("where") {
		column, err := sc.next()
		if err != nil {
			return selectQuery{}, err
		}
		if column.kind != tokIdent {
			return selectQuery{}, sqlFail("WHERE requires a column name")
		}
		eq, err := sc.next()
		if err != nil {
			return selectQuery{}, err
		}
		if eq.kind != tokEq {
			return selectQuery{}, sqlFail("WHERE only supports column = literal")
		}
		raw, err := sc.next()
		if err != nil {
			return selectQuery{}, err
		}
		lit, err := raw.asLiteral()
		if err != nil {
			return selectQuery{}, err
		}
		query.where = &predicate{column: column.identifier(), literal: lit}
		tok, err = sc.next()
		if err != nil {
			return selectQuery{}, err
		}
	}
	if tok.isKeyword("limit") {
		raw, err := sc.next()
		if err != nil {
			return selectQuery{}, err
		}
		if raw.kind != tokNumber {
			return selectQuery{}, sqlFail("LIMIT requires an integer")
		}
		n, err := strconv.Atoi(raw.text)
		if err != nil {
			return selectQuery{}, sqlFail("LIMIT value is out of range")
		}
		query.limit = &n
		tok, err = sc.next()
		if err != nil {
			return selectQuery{}, err
		}
	}
	if tok.kind == tokSemi {
		tok, err = sc.next()
		if err != nil {
			return selectQuery{}, err
		}
	}
	if tok.kind != tokEOF {
		return selectQuery{}, sqlFail("unexpected token %q", tok.text)
	}
	return query, nil
}

func (t token) isKeyword(word string) bool {
	return t.kind == tokIdent && !t.quoted && strings.EqualFold(t.text, word)
}

func (t token) identifier() string {
	if t.quoted {
		return t.text
	}
	return strings.ToLower(t.text)
}

func (t token) asLiteral() (literal, error) {
	switch t.kind {
	case tokString:
		return literal{kind: litString, str: t.text}, nil
	case tokNumber:
		n, err := strconv.ParseInt(t.text, 10, 64)
		if err != nil {
			return literal{}, sqlFail("integer %q is out of range", t.text)
		}
		return literal{kind: litInt, n: n}, nil
	case tokIdent:
		if strings.EqualFold(t.text, "true") {
			return literal{kind: litBool, b: true}, nil
		}
		if strings.EqualFold(t.text, "false") {
			return literal{kind: litBool, b: false}, nil
		}
	}
	return literal{}, sqlFail("WHERE only supports a string, integer, true, or false")
}

func (s *scanner) next() (token, error) {
	s.skipSpace()
	if s.pos >= len(s.src) {
		return token{kind: tokEOF}, nil
	}
	ch := s.src[s.pos]
	if ch == '-' && s.peek() == '-' {
		return token{}, sqlFail("comments are not supported")
	}
	if ch == '/' && s.peek() == '*' {
		return token{}, sqlFail("comments are not supported")
	}
	switch ch {
	case '*':
		s.pos++
		return token{kind: tokStar, text: "*"}, nil
	case ',':
		s.pos++
		return token{kind: tokComma, text: ","}, nil
	case '.':
		s.pos++
		return token{kind: tokDot, text: "."}, nil
	case '=':
		s.pos++
		return token{kind: tokEq, text: "="}, nil
	case ';':
		s.pos++
		return token{kind: tokSemi, text: ";"}, nil
	case '\'':
		return s.scanString()
	case '"':
		return s.scanIdent()
	}
	if ch >= '0' && ch <= '9' {
		start := s.pos
		for s.pos < len(s.src) && s.src[s.pos] >= '0' && s.src[s.pos] <= '9' {
			s.pos++
		}
		return token{kind: tokNumber, text: s.src[start:s.pos]}, nil
	}
	if isIdentStart(ch) {
		start := s.pos
		s.pos++
		for s.pos < len(s.src) && isIdentByte(s.src[s.pos]) {
			s.pos++
		}
		return token{kind: tokIdent, text: s.src[start:s.pos]}, nil
	}
	return token{}, sqlFail("unexpected character %q", string(ch))
}

func (s *scanner) scanString() (token, error) {
	s.pos++
	var b strings.Builder
	for s.pos < len(s.src) {
		ch := s.src[s.pos]
		s.pos++
		if ch == '\'' {
			if s.pos < len(s.src) && s.src[s.pos] == '\'' {
				b.WriteByte('\'')
				s.pos++
				continue
			}
			return token{kind: tokString, text: b.String()}, nil
		}
		b.WriteByte(ch)
	}
	return token{}, sqlFail("unterminated string")
}

func (s *scanner) scanIdent() (token, error) {
	s.pos++
	var b strings.Builder
	for s.pos < len(s.src) {
		ch := s.src[s.pos]
		s.pos++
		if ch == '"' {
			if s.pos < len(s.src) && s.src[s.pos] == '"' {
				b.WriteByte('"')
				s.pos++
				continue
			}
			return token{kind: tokIdent, text: b.String(), quoted: true}, nil
		}
		b.WriteByte(ch)
	}
	return token{}, sqlFail("unterminated identifier")
}

func (s *scanner) skipSpace() {
	for s.pos < len(s.src) {
		ch := rune(s.src[s.pos])
		if ch > unicode.MaxASCII || !unicode.IsSpace(ch) {
			return
		}
		s.pos++
	}
}

func (s *scanner) peek() byte {
	if s.pos+1 >= len(s.src) {
		return 0
	}
	return s.src[s.pos+1]
}

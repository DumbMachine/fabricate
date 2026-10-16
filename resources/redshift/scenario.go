package redshift

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/mail"
	"regexp"

	"github.com/dumbmachine/fabricate/scenario"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed schema.sql
var schemaSQL string

//go:embed scenario.schema.json
var scenarioSchema []byte

//go:embed scenarios/*.json
var builtInScenarios embed.FS

const resourceVersion = "2012-12-01"

var identPattern = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)
var clusterPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)
var userPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var compiledScenarioSchema = func() *jsonschema.Schema {
	parsed, err := jsonschema.UnmarshalJSON(bytes.NewReader(scenarioSchema))
	if err != nil {
		panic(fmt.Sprintf("redshift: parse embedded scenario schema: %v", err))
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("redshift-scenario.json", parsed); err != nil {
		panic(fmt.Sprintf("redshift: add embedded scenario schema: %v", err))
	}
	compiled, err := compiler.Compile("redshift-scenario.json")
	if err != nil {
		panic(fmt.Sprintf("redshift: compile embedded scenario schema: %v", err))
	}
	return compiled
}()

type scenarioCodec struct{}

type fixtureState struct {
	ClusterIdentifier string           `json:"clusterIdentifier"`
	Database          string           `json:"database"`
	Schema            string           `json:"schema"`
	DbUser            string           `json:"dbUser"`
	Orders            []orderRow       `json:"orders"`
	Payments          []paymentRow     `json:"payments"`
	Shipments         []shipmentRow    `json:"shipments"`
	SaasInvoices      []saasInvoiceRow `json:"saasInvoices"`
}

func (scenarioCodec) Validate(_ context.Context, doc scenario.Document) error {
	if err := doc.ValidateEnvelope(); err != nil {
		return err
	}
	if doc.Resource != "redshift" || doc.ResourceVersion != resourceVersion {
		return fmt.Errorf("redshift scenario: expected resource redshift %s, got %s %s", resourceVersion, doc.Resource, doc.ResourceVersion)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc.State))
	if err != nil {
		return fmt.Errorf("redshift scenario: decode state for schema validation: %w", err)
	}
	if err := compiledScenarioSchema.Validate(instance); err != nil {
		return fmt.Errorf("redshift scenario: schema validation: %w", err)
	}
	state, err := decodeState(doc.State)
	if err != nil {
		return err
	}
	if !clusterPattern.MatchString(state.ClusterIdentifier) {
		return fmt.Errorf("redshift scenario: clusterIdentifier %q is invalid", state.ClusterIdentifier)
	}
	if !identPattern.MatchString(state.Database) || !identPattern.MatchString(state.Schema) {
		return fmt.Errorf("redshift scenario: database and schema must be identifiers")
	}
	if !userPattern.MatchString(state.DbUser) {
		return fmt.Errorf("redshift scenario: dbUser %q is invalid", state.DbUser)
	}
	orders := map[string]struct{}{}
	for i, row := range state.Orders {
		if err := requireText(fmt.Sprintf("orders[%d]", i), row.OrderID, row.PlacedAt, row.CustomerName, row.CustomerEmail, row.CustomerPhone, row.ShipTo, row.Lines, row.Shop, row.Status); err != nil {
			return err
		}
		if _, err := mail.ParseAddress(row.CustomerEmail); err != nil {
			return fmt.Errorf("redshift scenario: orders[%d].customer_email: %w", i, err)
		}
		if row.Currency != "INR" {
			return fmt.Errorf("redshift scenario: orders[%d].currency must be INR", i)
		}
		if row.MerchandiseINR < 0 || row.MerchandisePaise != row.MerchandiseINR*100 {
			return fmt.Errorf("redshift scenario: orders[%d] merchandise_paise must be merchandise_inr times 100", i)
		}
		if _, exists := orders[row.OrderID]; exists {
			return fmt.Errorf("redshift scenario: duplicate order_id %q", row.OrderID)
		}
		orders[row.OrderID] = struct{}{}
	}
	payments := map[string]struct{}{}
	for i, row := range state.Payments {
		if err := requireText(fmt.Sprintf("payments[%d]", i), row.PaymentID, row.OrderID, row.Status); err != nil {
			return err
		}
		if _, exists := orders[row.OrderID]; !exists {
			return fmt.Errorf("redshift scenario: payments[%d] references unknown order_id %q", i, row.OrderID)
		}
		if err := optionalText(fmt.Sprintf("payments[%d].razorpay_order_id", i), row.RazorpayOrderID); err != nil {
			return err
		}
		if err := optionalText(fmt.Sprintf("payments[%d].method", i), row.Method); err != nil {
			return err
		}
		if err := optionalText(fmt.Sprintf("payments[%d].refund_id", i), row.RefundID); err != nil {
			return err
		}
		if row.Currency != "INR" || row.AmountPaise < 0 {
			return fmt.Errorf("redshift scenario: payments[%d] must be a non-negative INR amount in paise", i)
		}
		if _, exists := payments[row.PaymentID]; exists {
			return fmt.Errorf("redshift scenario: duplicate payment_id %q", row.PaymentID)
		}
		payments[row.PaymentID] = struct{}{}
	}
	shipments := map[string]struct{}{}
	for i, row := range state.Shipments {
		if err := requireText(fmt.Sprintf("shipments[%d]", i), row.OrderID, row.Status, row.Channel); err != nil {
			return err
		}
		if _, exists := orders[row.OrderID]; !exists {
			return fmt.Errorf("redshift scenario: shipments[%d] references unknown order_id %q", i, row.OrderID)
		}
		if err := optionalText(fmt.Sprintf("shipments[%d].awb", i), row.AWB); err != nil {
			return err
		}
		if err := optionalText(fmt.Sprintf("shipments[%d].courier", i), row.Courier); err != nil {
			return err
		}
		if err := optionalText(fmt.Sprintf("shipments[%d].ndr_reason", i), row.NDRReason); err != nil {
			return err
		}
		if err := optionalText(fmt.Sprintf("shipments[%d].return_id", i), row.ReturnID); err != nil {
			return err
		}
		if _, exists := shipments[row.OrderID]; exists {
			return fmt.Errorf("redshift scenario: duplicate shipment for order_id %q", row.OrderID)
		}
		shipments[row.OrderID] = struct{}{}
	}
	invoices := map[string]struct{}{}
	for i, row := range state.SaasInvoices {
		if err := requireText(fmt.Sprintf("saasInvoices[%d]", i), row.InvoiceID, row.CustomerName, row.ContactEmail, row.Status, row.PaymentID, row.DuplicatePaymentID, row.HubspotDealID, row.ChargebeeSubscription, row.ChargebeeCustomer); err != nil {
			return err
		}
		if _, err := mail.ParseAddress(row.ContactEmail); err != nil {
			return fmt.Errorf("redshift scenario: saasInvoices[%d].contact_email: %w", i, err)
		}
		if row.Currency != "USD" || row.AmountUSD < 0 {
			return fmt.Errorf("redshift scenario: saasInvoices[%d] must be a non-negative USD amount", i)
		}
		if row.PaymentID == row.DuplicatePaymentID {
			return fmt.Errorf("redshift scenario: saasInvoices[%d] payment ids must differ", i)
		}
		if _, exists := invoices[row.InvoiceID]; exists {
			return fmt.Errorf("redshift scenario: duplicate invoice_id %q", row.InvoiceID)
		}
		invoices[row.InvoiceID] = struct{}{}
	}
	return nil
}

func (scenarioCodec) Initialize(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("redshift scenario: initialize: %w", err)
	}
	return nil
}

func (codec scenarioCodec) Load(ctx context.Context, db *sql.DB, doc scenario.Document) error {
	if err := codec.Validate(ctx, doc); err != nil {
		return err
	}
	state, err := decodeState(doc.State)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("redshift scenario: begin load: %w", err)
	}
	defer tx.Rollback()
	for _, table := range []string{"statements", "orders", "payments", "shipments", "saas_invoices", "metadata"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("redshift scenario: clear %s: %w", table, err)
		}
	}
	meta := []struct{ key, value string }{
		{"clusterIdentifier", state.ClusterIdentifier},
		{"database", state.Database},
		{"schema", state.Schema},
		{"dbUser", state.DbUser},
	}
	for _, item := range meta {
		if _, err := tx.ExecContext(ctx, "INSERT INTO metadata(key, value) VALUES(?, ?)", item.key, item.value); err != nil {
			return fmt.Errorf("redshift scenario: insert metadata %s: %w", item.key, err)
		}
	}
	if err := insertRows(ctx, tx, "orders", state.Orders); err != nil {
		return err
	}
	if err := insertRows(ctx, tx, "payments", state.Payments); err != nil {
		return err
	}
	if err := insertRows(ctx, tx, "shipments", state.Shipments); err != nil {
		return err
	}
	if err := insertRows(ctx, tx, "saas_invoices", state.SaasInvoices); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("redshift scenario: commit load: %w", err)
	}
	return nil
}

func (scenarioCodec) Dump(ctx context.Context, db *sql.DB, metadata scenario.Metadata) (scenario.Document, error) {
	var state fixtureState
	meta := map[string]*string{
		"clusterIdentifier": &state.ClusterIdentifier,
		"database":          &state.Database,
		"schema":            &state.Schema,
		"dbUser":            &state.DbUser,
	}
	for key, dest := range meta {
		if err := db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key=?", key).Scan(dest); err != nil {
			return scenario.Document{}, fmt.Errorf("redshift scenario: dump %s: %w", key, err)
		}
	}
	if err := dumpRows(ctx, db, "orders", &state.Orders); err != nil {
		return scenario.Document{}, err
	}
	if err := dumpRows(ctx, db, "payments", &state.Payments); err != nil {
		return scenario.Document{}, err
	}
	if err := dumpRows(ctx, db, "shipments", &state.Shipments); err != nil {
		return scenario.Document{}, err
	}
	if err := dumpRows(ctx, db, "saas_invoices", &state.SaasInvoices); err != nil {
		return scenario.Document{}, err
	}
	if state.Orders == nil {
		state.Orders = []orderRow{}
	}
	if state.Payments == nil {
		state.Payments = []paymentRow{}
	}
	if state.Shipments == nil {
		state.Shipments = []shipmentRow{}
	}
	if state.SaasInvoices == nil {
		state.SaasInvoices = []saasInvoiceRow{}
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return scenario.Document{}, err
	}
	return scenario.Document{
		Contract: scenario.Contract, ContractVersion: 1, ID: metadata.ID,
		Resource: "redshift", ResourceVersion: resourceVersion, State: raw,
	}, nil
}

func insertRows(ctx context.Context, tx *sql.Tx, table string, rows any) error {
	raw, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	var encoded []json.RawMessage
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return fmt.Errorf("redshift scenario: encode %s: %w", table, err)
	}
	for i, body := range encoded {
		if _, err := tx.ExecContext(ctx, "INSERT INTO "+table+"(position, body) VALUES(?, ?)", i, string(body)); err != nil {
			return fmt.Errorf("redshift scenario: insert %s[%d]: %w", table, i, err)
		}
	}
	return nil
}

func dumpRows[T any](ctx context.Context, db *sql.DB, table string, dest *[]T) error {
	rows, err := db.QueryContext(ctx, "SELECT body FROM "+table+" ORDER BY position")
	if err != nil {
		return fmt.Errorf("redshift scenario: dump %s: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			return err
		}
		var row T
		if err := json.Unmarshal([]byte(body), &row); err != nil {
			return fmt.Errorf("redshift scenario: dump %s row: %w", table, err)
		}
		*dest = append(*dest, row)
	}
	return rows.Err()
}

func decodeState(raw []byte) (fixtureState, error) {
	var state fixtureState
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return fixtureState{}, fmt.Errorf("redshift scenario: decode state: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fixtureState{}, fmt.Errorf("redshift scenario: state has trailing data")
	}
	if state.Orders == nil || state.Payments == nil || state.Shipments == nil || state.SaasInvoices == nil {
		return fixtureState{}, fmt.Errorf("redshift scenario: orders, payments, shipments, and saasInvoices are required arrays")
	}
	return state, nil
}

func requireText(prefix string, values ...string) error {
	for _, value := range values {
		if value == "" {
			return fmt.Errorf("redshift scenario: %s has an empty required string", prefix)
		}
	}
	return nil
}

func optionalText(prefix string, value *string) error {
	if value != nil && *value == "" {
		return fmt.Errorf("redshift scenario: %s is empty; use null when the value is absent", prefix)
	}
	return nil
}

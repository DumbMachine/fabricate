package chargebee

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/mail"

	"github.com/dumbmachine/fabricate/scenario"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed schema.sql
var schemaSQL string

//go:embed scenario.schema.json
var scenarioSchema []byte

//go:embed scenarios/*.json
var builtInScenarios embed.FS

var compiledScenarioSchema = func() *jsonschema.Schema {
	parsed, err := jsonschema.UnmarshalJSON(bytes.NewReader(scenarioSchema))
	if err != nil {
		panic(fmt.Sprintf("chargebee: parse embedded scenario schema: %v", err))
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("chargebee-scenario.json", parsed); err != nil {
		panic(fmt.Sprintf("chargebee: add embedded scenario schema: %v", err))
	}
	compiled, err := compiler.Compile("chargebee-scenario.json")
	if err != nil {
		panic(fmt.Sprintf("chargebee: compile embedded scenario schema: %v", err))
	}
	return compiled
}()

type scenarioCodec struct{}

type fixtureState struct {
	Customers     []fixtureCustomer     `json:"customers"`
	Subscriptions []fixtureSubscription `json:"subscriptions"`
	Invoices      []fixtureInvoice      `json:"invoices"`
	Transactions  []fixtureTransaction  `json:"transactions"`
	Comments      []fixtureComment      `json:"comments"`
}

type fixtureCustomer struct {
	ID                    string `json:"id"`
	FirstName             string `json:"first_name"`
	LastName              string `json:"last_name"`
	Email                 string `json:"email"`
	Phone                 string `json:"phone"`
	Company               string `json:"company"`
	AutoCollection        string `json:"auto_collection"`
	NetTermDays           int64  `json:"net_term_days"`
	AllowDirectDebit      bool   `json:"allow_direct_debit"`
	CreatedAt             int64  `json:"created_at"`
	Taxability            string `json:"taxability"`
	UpdatedAt             int64  `json:"updated_at"`
	PIICleared            string `json:"pii_cleared"`
	ResourceVersion       int64  `json:"resource_version"`
	Deleted               bool   `json:"deleted"`
	PreferredCurrencyCode string `json:"preferred_currency_code"`
	PromotionalCredits    int64  `json:"promotional_credits"`
	RefundableCredits     int64  `json:"refundable_credits"`
	ExcessPayments        int64  `json:"excess_payments"`
	UnbilledCharges       int64  `json:"unbilled_charges"`
}

type fixtureItem struct {
	ItemPriceID   string `json:"item_price_id"`
	ItemType      string `json:"item_type"`
	Quantity      int64  `json:"quantity"`
	UnitPrice     int64  `json:"unit_price"`
	Amount        int64  `json:"amount"`
	FreeQuantity  int64  `json:"free_quantity"`
	BillingCycles int64  `json:"billing_cycles"`
}

type fixtureSubscription struct {
	ID                          string        `json:"id"`
	CustomerID                  string        `json:"customer_id"`
	CurrencyCode                string        `json:"currency_code"`
	Status                      string        `json:"status"`
	BillingPeriod               int64         `json:"billing_period"`
	BillingPeriodUnit           string        `json:"billing_period_unit"`
	CreatedAt                   int64         `json:"created_at"`
	StartedAt                   int64         `json:"started_at"`
	ActivatedAt                 int64         `json:"activated_at"`
	CurrentTermStart            int64         `json:"current_term_start"`
	CurrentTermEnd              int64         `json:"current_term_end"`
	NextBillingAt               *int64        `json:"next_billing_at,omitempty"`
	CancelledAt                 *int64        `json:"cancelled_at,omitempty"`
	CancelReasonCode            string        `json:"cancel_reason_code,omitempty"`
	RemainingBillingCycles      *int64        `json:"remaining_billing_cycles,omitempty"`
	DueInvoicesCount            int64         `json:"due_invoices_count"`
	DueSince                    *int64        `json:"due_since,omitempty"`
	TotalDues                   int64         `json:"total_dues"`
	Mrr                         int64         `json:"mrr"`
	HasScheduledChanges         bool          `json:"has_scheduled_changes"`
	HasScheduledAdvanceInvoices bool          `json:"has_scheduled_advance_invoices"`
	Deleted                     bool          `json:"deleted"`
	Decommissioned              bool          `json:"decommissioned"`
	ResourceVersion             int64         `json:"resource_version"`
	UpdatedAt                   int64         `json:"updated_at"`
	SubscriptionItems           []fixtureItem `json:"subscription_items"`
}

type fixtureLine struct {
	ID             string `json:"id"`
	CustomerID     string `json:"customer_id"`
	SubscriptionID string `json:"subscription_id"`
	DateFrom       int64  `json:"date_from"`
	DateTo         int64  `json:"date_to"`
	Description    string `json:"description"`
	EntityID       string `json:"entity_id"`
	EntityType     string `json:"entity_type"`
	Quantity       int64  `json:"quantity"`
	UnitAmount     int64  `json:"unit_amount"`
	Amount         int64  `json:"amount"`
	DiscountAmount int64  `json:"discount_amount"`
	TaxAmount      int64  `json:"tax_amount"`
}

type fixtureLinked struct {
	TxnID         string `json:"txn_id"`
	TxnStatus     string `json:"txn_status"`
	TxnDate       int64  `json:"txn_date"`
	TxnAmount     int64  `json:"txn_amount"`
	AppliedAmount int64  `json:"applied_amount"`
	AppliedAt     int64  `json:"applied_at"`
}

type fixtureInvoice struct {
	ID              string          `json:"id"`
	CustomerID      string          `json:"customer_id"`
	SubscriptionID  string          `json:"subscription_id"`
	Recurring       bool            `json:"recurring"`
	Status          string          `json:"status"`
	Date            int64           `json:"date"`
	DueDate         int64           `json:"due_date"`
	PaidAt          *int64          `json:"paid_at,omitempty"`
	NetTermDays     int64           `json:"net_term_days"`
	PriceType       string          `json:"price_type"`
	CurrencyCode    string          `json:"currency_code"`
	ExchangeRate    int64           `json:"exchange_rate"`
	SubTotal        int64           `json:"sub_total"`
	Tax             int64           `json:"tax"`
	Total           int64           `json:"total"`
	AmountPaid      int64           `json:"amount_paid"`
	AmountDue       int64           `json:"amount_due"`
	AmountAdjusted  int64           `json:"amount_adjusted"`
	CreditsApplied  int64           `json:"credits_applied"`
	WriteOffAmount  int64           `json:"write_off_amount"`
	AmountToCollect int64           `json:"amount_to_collect"`
	RoundOffAmount  int64           `json:"round_off_amount"`
	FirstInvoice    bool            `json:"first_invoice"`
	Deleted         bool            `json:"deleted"`
	ResourceVersion int64           `json:"resource_version"`
	UpdatedAt       int64           `json:"updated_at"`
	LineItems       []fixtureLine   `json:"line_items"`
	LinkedPayments  []fixtureLinked `json:"linked_payments"`
}

type fixtureTransaction struct {
	ID              string `json:"id"`
	CustomerID      string `json:"customer_id"`
	SubscriptionID  string `json:"subscription_id"`
	InvoiceID       string `json:"invoice_id"`
	Type            string `json:"type"`
	Status          string `json:"status"`
	Amount          int64  `json:"amount"`
	AmountUnused    int64  `json:"amount_unused"`
	CurrencyCode    string `json:"currency_code"`
	Date            int64  `json:"date"`
	PaymentMethod   string `json:"payment_method"`
	Gateway         string `json:"gateway"`
	ExchangeRate    int64  `json:"exchange_rate"`
	Deleted         bool   `json:"deleted"`
	ResourceVersion int64  `json:"resource_version"`
	UpdatedAt       int64  `json:"updated_at"`
}

type fixtureComment struct {
	ID         string `json:"id"`
	EntityType string `json:"entity_type"`
	EntityID   string `json:"entity_id"`
	Notes      string `json:"notes"`
	AddedBy    string `json:"added_by"`
	Type       string `json:"type"`
	CreatedAt  int64  `json:"created_at"`
}

func (scenarioCodec) Validate(_ context.Context, doc scenario.Document) error {
	if err := doc.ValidateEnvelope(); err != nil {
		return err
	}
	if doc.Resource != "chargebee" || doc.ResourceVersion != "v2" {
		return fmt.Errorf("chargebee scenario: expected resource chargebee v2, got %s %s", doc.Resource, doc.ResourceVersion)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc.State))
	if err != nil {
		return fmt.Errorf("chargebee scenario: decode state for schema validation: %w", err)
	}
	if err := compiledScenarioSchema.Validate(instance); err != nil {
		return fmt.Errorf("chargebee scenario: schema validation: %w", err)
	}
	state, err := decodeState(doc.State)
	if err != nil {
		return err
	}
	return validateState(state)
}

func validateState(state fixtureState) error {
	customers := map[string]fixtureCustomer{}
	for i, customer := range state.Customers {
		if _, err := mail.ParseAddress(customer.Email); err != nil {
			return fmt.Errorf("chargebee scenario: customers[%d].email: %w", i, err)
		}
		if customer.PreferredCurrencyCode != "USD" {
			return fmt.Errorf("chargebee scenario: customer %q currency must be USD", customer.ID)
		}
		if _, exists := customers[customer.ID]; exists {
			return fmt.Errorf("chargebee scenario: duplicate customer id %q", customer.ID)
		}
		customers[customer.ID] = customer
	}
	subscriptions := map[string]fixtureSubscription{}
	for i, subscription := range state.Subscriptions {
		if _, ok := customers[subscription.CustomerID]; !ok {
			return fmt.Errorf("chargebee scenario: subscription %q references unknown customer %q", subscription.ID, subscription.CustomerID)
		}
		if _, exists := subscriptions[subscription.ID]; exists {
			return fmt.Errorf("chargebee scenario: duplicate subscription id %q", subscription.ID)
		}
		if len(subscription.SubscriptionItems) == 0 {
			return fmt.Errorf("chargebee scenario: subscription %q requires subscription_items", subscription.ID)
		}
		for j, item := range subscription.SubscriptionItems {
			if item.Amount != item.UnitPrice*item.Quantity {
				return fmt.Errorf("chargebee scenario: subscriptions[%d].subscription_items[%d] amount must equal unit_price * quantity", i, j)
			}
		}
		subscriptions[subscription.ID] = subscription
	}
	invoices := map[string]fixtureInvoice{}
	for i, invoice := range state.Invoices {
		subscription, ok := subscriptions[invoice.SubscriptionID]
		if !ok {
			return fmt.Errorf("chargebee scenario: invoice %q references unknown subscription %q", invoice.ID, invoice.SubscriptionID)
		}
		if invoice.CustomerID != subscription.CustomerID {
			return fmt.Errorf("chargebee scenario: invoice %q customer does not match subscription %q", invoice.ID, invoice.SubscriptionID)
		}
		if _, exists := invoices[invoice.ID]; exists {
			return fmt.Errorf("chargebee scenario: duplicate invoice id %q", invoice.ID)
		}
		if invoice.Total != invoice.SubTotal+invoice.Tax {
			return fmt.Errorf("chargebee scenario: invoice %q total must equal sub_total + tax", invoice.ID)
		}
		if len(invoice.LineItems) == 0 {
			return fmt.Errorf("chargebee scenario: invoice %q requires line_items", invoice.ID)
		}
		seenLines := map[string]struct{}{}
		for j, line := range invoice.LineItems {
			if line.CustomerID != invoice.CustomerID || line.SubscriptionID != invoice.SubscriptionID {
				return fmt.Errorf("chargebee scenario: invoice %q line %q does not match the invoice", invoice.ID, line.ID)
			}
			if line.Amount != line.UnitAmount*line.Quantity-line.DiscountAmount {
				return fmt.Errorf("chargebee scenario: invoices[%d].line_items[%d] amount must equal unit_amount * quantity - discount_amount", i, j)
			}
			if _, exists := seenLines[line.ID]; exists {
				return fmt.Errorf("chargebee scenario: duplicate line item id %q", line.ID)
			}
			seenLines[line.ID] = struct{}{}
		}
		if invoice.LinkedPayments == nil {
			return fmt.Errorf("chargebee scenario: invoice %q linked_payments is required", invoice.ID)
		}
		invoices[invoice.ID] = invoice
	}
	transactions := map[string]fixtureTransaction{}
	for _, txn := range state.Transactions {
		invoice, ok := invoices[txn.InvoiceID]
		if !ok {
			return fmt.Errorf("chargebee scenario: transaction %q references unknown invoice %q", txn.ID, txn.InvoiceID)
		}
		if txn.CustomerID != invoice.CustomerID || txn.SubscriptionID != invoice.SubscriptionID {
			return fmt.Errorf("chargebee scenario: transaction %q does not match invoice %q", txn.ID, txn.InvoiceID)
		}
		if _, exists := transactions[txn.ID]; exists {
			return fmt.Errorf("chargebee scenario: duplicate transaction id %q", txn.ID)
		}
		transactions[txn.ID] = txn
	}
	for _, invoice := range state.Invoices {
		seen := map[string]struct{}{}
		for _, link := range invoice.LinkedPayments {
			txn, ok := transactions[link.TxnID]
			if !ok {
				return fmt.Errorf("chargebee scenario: invoice %q links unknown transaction %q", invoice.ID, link.TxnID)
			}
			if txn.InvoiceID != invoice.ID {
				return fmt.Errorf("chargebee scenario: invoice %q links transaction %q from another invoice", invoice.ID, link.TxnID)
			}
			if _, exists := seen[link.TxnID]; exists {
				return fmt.Errorf("chargebee scenario: invoice %q repeats transaction %q", invoice.ID, link.TxnID)
			}
			seen[link.TxnID] = struct{}{}
		}
	}
	comments := map[string]struct{}{}
	for _, comment := range state.Comments {
		if _, exists := comments[comment.ID]; exists {
			return fmt.Errorf("chargebee scenario: duplicate comment id %q", comment.ID)
		}
		comments[comment.ID] = struct{}{}
		if !entityExists(state, comment.EntityType, comment.EntityID) {
			return fmt.Errorf("chargebee scenario: comment %q references unknown %s %q", comment.ID, comment.EntityType, comment.EntityID)
		}
	}
	return nil
}

func entityExists(state fixtureState, entityType, entityID string) bool {
	switch entityType {
	case "customer":
		for _, customer := range state.Customers {
			if customer.ID == entityID {
				return true
			}
		}
	case "subscription":
		for _, subscription := range state.Subscriptions {
			if subscription.ID == entityID {
				return true
			}
		}
	case "invoice":
		for _, invoice := range state.Invoices {
			if invoice.ID == entityID {
				return true
			}
		}
	}
	return false
}

func (scenarioCodec) Initialize(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("chargebee scenario: initialize: %w", err)
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
		return fmt.Errorf("chargebee scenario: begin load: %w", err)
	}
	defer tx.Rollback()
	for _, table := range []string{"comments", "transactions", "invoices", "subscriptions", "customers"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("chargebee scenario: clear %s: %w", table, err)
		}
	}
	for _, customer := range state.Customers {
		if err := insertCustomer(ctx, tx, customer); err != nil {
			return err
		}
	}
	for _, subscription := range state.Subscriptions {
		if err := insertSubscription(ctx, tx, subscription); err != nil {
			return err
		}
	}
	for _, invoice := range state.Invoices {
		if err := insertInvoice(ctx, tx, invoice); err != nil {
			return err
		}
	}
	for _, txn := range state.Transactions {
		if err := insertTransaction(ctx, tx, txn); err != nil {
			return err
		}
	}
	for _, comment := range state.Comments {
		if err := insertComment(ctx, tx, comment); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("chargebee scenario: commit load: %w", err)
	}
	return nil
}

func (scenarioCodec) Dump(ctx context.Context, db *sql.DB, metadata scenario.Metadata) (scenario.Document, error) {
	state, err := readState(ctx, db)
	if err != nil {
		return scenario.Document{}, err
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return scenario.Document{}, err
	}
	return scenario.Document{
		Contract: scenario.Contract, ContractVersion: 1, ID: metadata.ID,
		Resource: "chargebee", ResourceVersion: "v2", State: raw,
	}, nil
}

func readState(ctx context.Context, db *sql.DB) (fixtureState, error) {
	var state fixtureState
	customers, err := queryCustomers(ctx, db, "SELECT "+customerColumns+" FROM customers ORDER BY id")
	if err != nil {
		return fixtureState{}, err
	}
	subscriptions, err := querySubscriptions(ctx, db, "SELECT "+subscriptionColumns+" FROM subscriptions ORDER BY id")
	if err != nil {
		return fixtureState{}, err
	}
	invoices, err := queryInvoices(ctx, db, "SELECT "+invoiceColumns+" FROM invoices ORDER BY id")
	if err != nil {
		return fixtureState{}, err
	}
	transactions, err := queryTransactions(ctx, db, "SELECT "+transactionColumns+" FROM transactions ORDER BY id")
	if err != nil {
		return fixtureState{}, err
	}
	comments, err := queryComments(ctx, db, "SELECT "+commentColumns+" FROM comments ORDER BY id")
	if err != nil {
		return fixtureState{}, err
	}
	state.Customers = customers
	state.Subscriptions = subscriptions
	state.Invoices = invoices
	state.Transactions = transactions
	state.Comments = comments
	if state.Customers == nil {
		state.Customers = []fixtureCustomer{}
	}
	if state.Subscriptions == nil {
		state.Subscriptions = []fixtureSubscription{}
	}
	if state.Invoices == nil {
		state.Invoices = []fixtureInvoice{}
	}
	if state.Transactions == nil {
		state.Transactions = []fixtureTransaction{}
	}
	if state.Comments == nil {
		state.Comments = []fixtureComment{}
	}
	return state, nil
}

func decodeState(raw []byte) (fixtureState, error) {
	var state fixtureState
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return fixtureState{}, fmt.Errorf("chargebee scenario: decode state: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fixtureState{}, fmt.Errorf("chargebee scenario: state has trailing data")
	}
	if state.Customers == nil || state.Subscriptions == nil || state.Invoices == nil || state.Transactions == nil || state.Comments == nil {
		return fixtureState{}, fmt.Errorf("chargebee scenario: customers, subscriptions, invoices, transactions, and comments are required arrays")
	}
	return state, nil
}

const customerColumns = `id, first_name, last_name, email, phone, company, auto_collection, net_term_days,
	allow_direct_debit, created_at, taxability, updated_at, pii_cleared, resource_version, deleted,
	preferred_currency_code, promotional_credits, refundable_credits, excess_payments, unbilled_charges`

const subscriptionColumns = `id, customer_id, currency_code, status, billing_period, billing_period_unit,
	created_at, started_at, activated_at, current_term_start, current_term_end, next_billing_at,
	cancelled_at, cancel_reason_code, remaining_billing_cycles, due_invoices_count, due_since, total_dues,
	mrr, has_scheduled_changes, has_scheduled_advance_invoices, deleted, decommissioned, resource_version,
	updated_at, subscription_items`

const invoiceColumns = `id, customer_id, subscription_id, recurring, status, date, due_date, paid_at,
	net_term_days, price_type, currency_code, exchange_rate, sub_total, tax, total, amount_paid, amount_due,
	amount_adjusted, credits_applied, write_off_amount, amount_to_collect, round_off_amount, first_invoice,
	deleted, resource_version, updated_at, line_items, linked_payments`

const transactionColumns = `id, customer_id, subscription_id, invoice_id, type, status, amount, amount_unused,
	currency_code, date, payment_method, gateway, exchange_rate, deleted, resource_version, updated_at`

const commentColumns = `id, entity_type, entity_id, notes, added_by, type, created_at`

type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func insertCustomer(ctx context.Context, db execer, customer fixtureCustomer) error {
	_, err := db.ExecContext(ctx, `INSERT INTO customers (`+customerColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		customer.ID, customer.FirstName, customer.LastName, customer.Email, customer.Phone, customer.Company,
		customer.AutoCollection, customer.NetTermDays, boolArg(customer.AllowDirectDebit), customer.CreatedAt,
		customer.Taxability, customer.UpdatedAt, customer.PIICleared, customer.ResourceVersion, boolArg(customer.Deleted),
		customer.PreferredCurrencyCode, customer.PromotionalCredits, customer.RefundableCredits, customer.ExcessPayments,
		customer.UnbilledCharges)
	if err != nil {
		return fmt.Errorf("chargebee scenario: insert customer %s: %w", customer.ID, err)
	}
	return nil
}

func insertSubscription(ctx context.Context, db execer, subscription fixtureSubscription) error {
	items, err := marshalJSON(subscription.SubscriptionItems)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT INTO subscriptions (`+subscriptionColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		subscription.ID, subscription.CustomerID, subscription.CurrencyCode, subscription.Status, subscription.BillingPeriod,
		subscription.BillingPeriodUnit, subscription.CreatedAt, subscription.StartedAt, subscription.ActivatedAt,
		subscription.CurrentTermStart, subscription.CurrentTermEnd, intPtrArg(subscription.NextBillingAt),
		intPtrArg(subscription.CancelledAt), strArg(subscription.CancelReasonCode), intPtrArg(subscription.RemainingBillingCycles),
		subscription.DueInvoicesCount, intPtrArg(subscription.DueSince), subscription.TotalDues, subscription.Mrr,
		boolArg(subscription.HasScheduledChanges), boolArg(subscription.HasScheduledAdvanceInvoices), boolArg(subscription.Deleted),
		boolArg(subscription.Decommissioned), subscription.ResourceVersion, subscription.UpdatedAt, items)
	if err != nil {
		return fmt.Errorf("chargebee scenario: insert subscription %s: %w", subscription.ID, err)
	}
	return nil
}

func updateSubscription(ctx context.Context, db execer, subscription fixtureSubscription) error {
	items, err := marshalJSON(subscription.SubscriptionItems)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `UPDATE subscriptions SET customer_id=?, currency_code=?, status=?, billing_period=?,
		billing_period_unit=?, created_at=?, started_at=?, activated_at=?, current_term_start=?, current_term_end=?,
		next_billing_at=?, cancelled_at=?, cancel_reason_code=?, remaining_billing_cycles=?, due_invoices_count=?,
		due_since=?, total_dues=?, mrr=?, has_scheduled_changes=?, has_scheduled_advance_invoices=?, deleted=?,
		decommissioned=?, resource_version=?, updated_at=?, subscription_items=? WHERE id=?`,
		subscription.CustomerID, subscription.CurrencyCode, subscription.Status, subscription.BillingPeriod,
		subscription.BillingPeriodUnit, subscription.CreatedAt, subscription.StartedAt, subscription.ActivatedAt,
		subscription.CurrentTermStart, subscription.CurrentTermEnd, intPtrArg(subscription.NextBillingAt),
		intPtrArg(subscription.CancelledAt), strArg(subscription.CancelReasonCode), intPtrArg(subscription.RemainingBillingCycles),
		subscription.DueInvoicesCount, intPtrArg(subscription.DueSince), subscription.TotalDues, subscription.Mrr,
		boolArg(subscription.HasScheduledChanges), boolArg(subscription.HasScheduledAdvanceInvoices), boolArg(subscription.Deleted),
		boolArg(subscription.Decommissioned), subscription.ResourceVersion, subscription.UpdatedAt, items, subscription.ID)
	if err != nil {
		return fmt.Errorf("chargebee scenario: update subscription %s: %w", subscription.ID, err)
	}
	return nil
}

func insertInvoice(ctx context.Context, db execer, invoice fixtureInvoice) error {
	lines, err := marshalJSON(invoice.LineItems)
	if err != nil {
		return err
	}
	links, err := marshalJSON(invoice.LinkedPayments)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT INTO invoices (`+invoiceColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		invoice.ID, invoice.CustomerID, invoice.SubscriptionID, boolArg(invoice.Recurring), invoice.Status, invoice.Date,
		invoice.DueDate, intPtrArg(invoice.PaidAt), invoice.NetTermDays, invoice.PriceType, invoice.CurrencyCode,
		invoice.ExchangeRate, invoice.SubTotal, invoice.Tax, invoice.Total, invoice.AmountPaid, invoice.AmountDue,
		invoice.AmountAdjusted, invoice.CreditsApplied, invoice.WriteOffAmount, invoice.AmountToCollect, invoice.RoundOffAmount,
		boolArg(invoice.FirstInvoice), boolArg(invoice.Deleted), invoice.ResourceVersion, invoice.UpdatedAt, lines, links)
	if err != nil {
		return fmt.Errorf("chargebee scenario: insert invoice %s: %w", invoice.ID, err)
	}
	return nil
}

func insertTransaction(ctx context.Context, db execer, txn fixtureTransaction) error {
	_, err := db.ExecContext(ctx, `INSERT INTO transactions (`+transactionColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		txn.ID, txn.CustomerID, txn.SubscriptionID, txn.InvoiceID, txn.Type, txn.Status, txn.Amount, txn.AmountUnused,
		txn.CurrencyCode, txn.Date, txn.PaymentMethod, txn.Gateway, txn.ExchangeRate, boolArg(txn.Deleted),
		txn.ResourceVersion, txn.UpdatedAt)
	if err != nil {
		return fmt.Errorf("chargebee scenario: insert transaction %s: %w", txn.ID, err)
	}
	return nil
}

func insertComment(ctx context.Context, db execer, comment fixtureComment) error {
	_, err := db.ExecContext(ctx, `INSERT INTO comments (`+commentColumns+`) VALUES (?,?,?,?,?,?,?)`,
		comment.ID, comment.EntityType, comment.EntityID, comment.Notes, comment.AddedBy, comment.Type, comment.CreatedAt)
	if err != nil {
		return fmt.Errorf("chargebee scenario: insert comment %s: %w", comment.ID, err)
	}
	return nil
}

func queryCustomers(ctx context.Context, db *sql.DB, query string, args ...any) ([]fixtureCustomer, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("chargebee scenario: query customers: %w", err)
	}
	defer rows.Close()
	out := []fixtureCustomer{}
	for rows.Next() {
		customer, err := scanCustomer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, customer)
	}
	return out, rows.Err()
}

func querySubscriptions(ctx context.Context, db *sql.DB, query string, args ...any) ([]fixtureSubscription, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("chargebee scenario: query subscriptions: %w", err)
	}
	defer rows.Close()
	out := []fixtureSubscription{}
	for rows.Next() {
		subscription, err := scanSubscription(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, subscription)
	}
	return out, rows.Err()
}

func queryInvoices(ctx context.Context, db *sql.DB, query string, args ...any) ([]fixtureInvoice, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("chargebee scenario: query invoices: %w", err)
	}
	defer rows.Close()
	out := []fixtureInvoice{}
	for rows.Next() {
		invoice, err := scanInvoice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, invoice)
	}
	return out, rows.Err()
}

func queryTransactions(ctx context.Context, db *sql.DB, query string, args ...any) ([]fixtureTransaction, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("chargebee scenario: query transactions: %w", err)
	}
	defer rows.Close()
	out := []fixtureTransaction{}
	for rows.Next() {
		txn, err := scanTransaction(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, txn)
	}
	return out, rows.Err()
}

func queryComments(ctx context.Context, db *sql.DB, query string, args ...any) ([]fixtureComment, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("chargebee scenario: query comments: %w", err)
	}
	defer rows.Close()
	out := []fixtureComment{}
	for rows.Next() {
		comment, err := scanComment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, comment)
	}
	return out, rows.Err()
}

type scanner interface {
	Scan(...any) error
}

func scanCustomer(row scanner) (fixtureCustomer, error) {
	var customer fixtureCustomer
	var allow, deleted int
	err := row.Scan(&customer.ID, &customer.FirstName, &customer.LastName, &customer.Email, &customer.Phone,
		&customer.Company, &customer.AutoCollection, &customer.NetTermDays, &allow, &customer.CreatedAt,
		&customer.Taxability, &customer.UpdatedAt, &customer.PIICleared, &customer.ResourceVersion, &deleted,
		&customer.PreferredCurrencyCode, &customer.PromotionalCredits, &customer.RefundableCredits,
		&customer.ExcessPayments, &customer.UnbilledCharges)
	if err != nil {
		return fixtureCustomer{}, err
	}
	customer.AllowDirectDebit = allow != 0
	customer.Deleted = deleted != 0
	return customer, nil
}

func scanSubscription(row scanner) (fixtureSubscription, error) {
	var subscription fixtureSubscription
	var next, cancelled, remaining, dueSince sql.NullInt64
	var reason sql.NullString
	var scheduled, advance, deleted, decommissioned int
	var items string
	err := row.Scan(&subscription.ID, &subscription.CustomerID, &subscription.CurrencyCode, &subscription.Status,
		&subscription.BillingPeriod, &subscription.BillingPeriodUnit, &subscription.CreatedAt, &subscription.StartedAt,
		&subscription.ActivatedAt, &subscription.CurrentTermStart, &subscription.CurrentTermEnd, &next, &cancelled,
		&reason, &remaining, &subscription.DueInvoicesCount, &dueSince, &subscription.TotalDues, &subscription.Mrr,
		&scheduled, &advance, &deleted, &decommissioned, &subscription.ResourceVersion, &subscription.UpdatedAt, &items)
	if err != nil {
		return fixtureSubscription{}, err
	}
	subscription.NextBillingAt = nullInt(next)
	subscription.CancelledAt = nullInt(cancelled)
	if reason.Valid {
		subscription.CancelReasonCode = reason.String
	}
	subscription.RemainingBillingCycles = nullInt(remaining)
	subscription.DueSince = nullInt(dueSince)
	subscription.HasScheduledChanges = scheduled != 0
	subscription.HasScheduledAdvanceInvoices = advance != 0
	subscription.Deleted = deleted != 0
	subscription.Decommissioned = decommissioned != 0
	if err := json.Unmarshal([]byte(items), &subscription.SubscriptionItems); err != nil {
		return fixtureSubscription{}, fmt.Errorf("chargebee scenario: subscription %s items: %w", subscription.ID, err)
	}
	if subscription.SubscriptionItems == nil {
		subscription.SubscriptionItems = []fixtureItem{}
	}
	return subscription, nil
}

func scanInvoice(row scanner) (fixtureInvoice, error) {
	var invoice fixtureInvoice
	var recurring, first, deleted int
	var paid sql.NullInt64
	var lines, links string
	err := row.Scan(&invoice.ID, &invoice.CustomerID, &invoice.SubscriptionID, &recurring, &invoice.Status, &invoice.Date,
		&invoice.DueDate, &paid, &invoice.NetTermDays, &invoice.PriceType, &invoice.CurrencyCode, &invoice.ExchangeRate,
		&invoice.SubTotal, &invoice.Tax, &invoice.Total, &invoice.AmountPaid, &invoice.AmountDue, &invoice.AmountAdjusted,
		&invoice.CreditsApplied, &invoice.WriteOffAmount, &invoice.AmountToCollect, &invoice.RoundOffAmount, &first,
		&deleted, &invoice.ResourceVersion, &invoice.UpdatedAt, &lines, &links)
	if err != nil {
		return fixtureInvoice{}, err
	}
	invoice.Recurring = recurring != 0
	invoice.FirstInvoice = first != 0
	invoice.Deleted = deleted != 0
	invoice.PaidAt = nullInt(paid)
	if err := json.Unmarshal([]byte(lines), &invoice.LineItems); err != nil {
		return fixtureInvoice{}, fmt.Errorf("chargebee scenario: invoice %s lines: %w", invoice.ID, err)
	}
	if err := json.Unmarshal([]byte(links), &invoice.LinkedPayments); err != nil {
		return fixtureInvoice{}, fmt.Errorf("chargebee scenario: invoice %s payments: %w", invoice.ID, err)
	}
	if invoice.LineItems == nil {
		invoice.LineItems = []fixtureLine{}
	}
	if invoice.LinkedPayments == nil {
		invoice.LinkedPayments = []fixtureLinked{}
	}
	return invoice, nil
}

func scanTransaction(row scanner) (fixtureTransaction, error) {
	var txn fixtureTransaction
	var deleted int
	err := row.Scan(&txn.ID, &txn.CustomerID, &txn.SubscriptionID, &txn.InvoiceID, &txn.Type, &txn.Status, &txn.Amount,
		&txn.AmountUnused, &txn.CurrencyCode, &txn.Date, &txn.PaymentMethod, &txn.Gateway, &txn.ExchangeRate, &deleted,
		&txn.ResourceVersion, &txn.UpdatedAt)
	if err != nil {
		return fixtureTransaction{}, err
	}
	txn.Deleted = deleted != 0
	return txn, nil
}

func scanComment(row scanner) (fixtureComment, error) {
	var comment fixtureComment
	err := row.Scan(&comment.ID, &comment.EntityType, &comment.EntityID, &comment.Notes, &comment.AddedBy, &comment.Type, &comment.CreatedAt)
	if err != nil {
		return fixtureComment{}, err
	}
	return comment, nil
}

func getCustomer(ctx context.Context, db *sql.DB, id string) (fixtureCustomer, error) {
	return scanCustomer(db.QueryRowContext(ctx, "SELECT "+customerColumns+" FROM customers WHERE id=?", id))
}

func getSubscription(ctx context.Context, db *sql.DB, id string) (fixtureSubscription, error) {
	return scanSubscription(db.QueryRowContext(ctx, "SELECT "+subscriptionColumns+" FROM subscriptions WHERE id=?", id))
}

func getInvoice(ctx context.Context, db *sql.DB, id string) (fixtureInvoice, error) {
	return scanInvoice(db.QueryRowContext(ctx, "SELECT "+invoiceColumns+" FROM invoices WHERE id=?", id))
}

func getTransaction(ctx context.Context, db *sql.DB, id string) (fixtureTransaction, error) {
	return scanTransaction(db.QueryRowContext(ctx, "SELECT "+transactionColumns+" FROM transactions WHERE id=?", id))
}

func marshalJSON(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	if string(raw) == "null" {
		return "[]", nil
	}
	return string(raw), nil
}

func boolArg(value bool) int {
	if value {
		return 1
	}
	return 0
}

func intPtrArg(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func strArg(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullInt(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	out := value.Int64
	return &out
}

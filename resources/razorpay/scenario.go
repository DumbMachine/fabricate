package razorpay

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io"

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
		panic(fmt.Sprintf("razorpay: parse embedded scenario schema: %v", err))
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("razorpay-scenario.json", parsed); err != nil {
		panic(fmt.Sprintf("razorpay: add embedded scenario schema: %v", err))
	}
	compiled, err := compiler.Compile("razorpay-scenario.json")
	if err != nil {
		panic(fmt.Sprintf("razorpay: compile embedded scenario schema: %v", err))
	}
	return compiled
}()

type scenarioCodec struct{}

type fixtureState struct {
	Orders       []fixtureOrder       `json:"orders"`
	Payments     []fixturePayment     `json:"payments"`
	Refunds      []fixtureRefund      `json:"refunds"`
	PaymentLinks []fixturePaymentLink `json:"paymentLinks"`
}

type fixtureOrder struct {
	ID         string            `json:"id"`
	Amount     int64             `json:"amount"`
	AmountPaid int64             `json:"amountPaid"`
	AmountDue  int64             `json:"amountDue"`
	Currency   string            `json:"currency"`
	Receipt    string            `json:"receipt"`
	Status     string            `json:"status"`
	Attempts   int               `json:"attempts"`
	Notes      map[string]string `json:"notes"`
	CreatedAt  int64             `json:"createdAt"`
}

type fixturePayment struct {
	ID             string            `json:"id"`
	Amount         int64             `json:"amount"`
	Currency       string            `json:"currency"`
	Status         string            `json:"status"`
	OrderID        string            `json:"orderId"`
	Method         string            `json:"method"`
	AmountRefunded int64             `json:"amountRefunded"`
	RefundStatus   string            `json:"refundStatus"`
	Captured       bool              `json:"captured"`
	Description    string            `json:"description"`
	Email          string            `json:"email"`
	Contact        string            `json:"contact"`
	VPA            string            `json:"vpa"`
	Notes          map[string]string `json:"notes"`
	Fee            int64             `json:"fee"`
	Tax            int64             `json:"tax"`
	International  bool              `json:"international"`
	CreatedAt      int64             `json:"createdAt"`
}

type fixtureRefund struct {
	ID             string            `json:"id"`
	PaymentID      string            `json:"paymentId"`
	Amount         int64             `json:"amount"`
	Currency       string            `json:"currency"`
	Status         string            `json:"status"`
	SpeedRequested string            `json:"speedRequested"`
	SpeedProcessed string            `json:"speedProcessed"`
	Receipt        string            `json:"receipt"`
	Notes          map[string]string `json:"notes"`
	CreatedAt      int64             `json:"createdAt"`
}

type fixturePaymentLink struct {
	ID              string            `json:"id"`
	Amount          int64             `json:"amount"`
	AmountPaid      int64             `json:"amountPaid"`
	Currency        string            `json:"currency"`
	AcceptPartial   bool              `json:"acceptPartial"`
	Description     string            `json:"description"`
	ReferenceID     string            `json:"referenceId"`
	CustomerName    string            `json:"customerName"`
	CustomerEmail   string            `json:"customerEmail"`
	CustomerContact string            `json:"customerContact"`
	Notes           map[string]string `json:"notes"`
	NotifyEmail     bool              `json:"notifyEmail"`
	NotifySMS       bool              `json:"notifySms"`
	ReminderEnable  bool              `json:"reminderEnable"`
	Status          string            `json:"status"`
	ShortURL        string            `json:"shortUrl"`
	ExpireBy        int64             `json:"expireBy"`
	ExpiredAt       int64             `json:"expiredAt"`
	CancelledAt     int64             `json:"cancelledAt"`
	CreatedAt       int64             `json:"createdAt"`
	UpdatedAt       int64             `json:"updatedAt"`
	UserID          string            `json:"userId"`
}

func (scenarioCodec) Validate(_ context.Context, doc scenario.Document) error {
	if err := doc.ValidateEnvelope(); err != nil {
		return err
	}
	if doc.Resource != "razorpay" || doc.ResourceVersion != "v1" {
		return fmt.Errorf("razorpay scenario: expected resource razorpay v1, got %s %s", doc.Resource, doc.ResourceVersion)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc.State))
	if err != nil {
		return fmt.Errorf("razorpay scenario: decode state for schema validation: %w", err)
	}
	if err := compiledScenarioSchema.Validate(instance); err != nil {
		return fmt.Errorf("razorpay scenario: schema validation: %w", err)
	}
	state, err := decodeState(doc.State)
	if err != nil {
		return err
	}
	return validateState(state)
}

func validateState(state fixtureState) error {
	orders := map[string]fixtureOrder{}
	receipts := map[string]struct{}{}
	for i, order := range state.Orders {
		if _, exists := orders[order.ID]; exists {
			return fmt.Errorf("razorpay scenario: duplicate order id %q", order.ID)
		}
		if order.AmountPaid+order.AmountDue != order.Amount {
			return fmt.Errorf("razorpay scenario: orders[%d] amountPaid + amountDue must equal amount", i)
		}
		if order.Status == "paid" && (order.AmountDue != 0 || order.AmountPaid != order.Amount) {
			return fmt.Errorf("razorpay scenario: paid order %q must have no amount due", order.ID)
		}
		if len(order.Notes) > 15 {
			return fmt.Errorf("razorpay scenario: order %q has more than 15 notes", order.ID)
		}
		if order.Receipt != "" {
			if _, exists := receipts[order.Receipt]; exists {
				return fmt.Errorf("razorpay scenario: duplicate order receipt %q", order.Receipt)
			}
			receipts[order.Receipt] = struct{}{}
		}
		orders[order.ID] = order
	}
	payments := map[string]fixturePayment{}
	for _, payment := range state.Payments {
		if _, exists := payments[payment.ID]; exists {
			return fmt.Errorf("razorpay scenario: duplicate payment id %q", payment.ID)
		}
		order, ok := orders[payment.OrderID]
		if !ok {
			return fmt.Errorf("razorpay scenario: payment %q references unknown order %q", payment.ID, payment.OrderID)
		}
		if payment.Currency != order.Currency {
			return fmt.Errorf("razorpay scenario: payment %q currency does not match its order", payment.ID)
		}
		if payment.AmountRefunded > payment.Amount {
			return fmt.Errorf("razorpay scenario: payment %q refunds more than its amount", payment.ID)
		}
		switch payment.RefundStatus {
		case "":
			if payment.AmountRefunded != 0 || payment.Status == "refunded" {
				return fmt.Errorf("razorpay scenario: payment %q refund state is inconsistent", payment.ID)
			}
		case "partial":
			if payment.Status != "captured" || payment.AmountRefunded <= 0 || payment.AmountRefunded >= payment.Amount {
				return fmt.Errorf("razorpay scenario: payment %q partial refund is inconsistent", payment.ID)
			}
		case "full":
			if payment.Status != "refunded" || payment.AmountRefunded != payment.Amount {
				return fmt.Errorf("razorpay scenario: payment %q full refund is inconsistent", payment.ID)
			}
		default:
			return fmt.Errorf("razorpay scenario: payment %q has refund status %q", payment.ID, payment.RefundStatus)
		}
		if (payment.Status == "captured" || payment.Status == "refunded") && !payment.Captured {
			return fmt.Errorf("razorpay scenario: payment %q must be captured", payment.ID)
		}
		if len(payment.Notes) > 15 {
			return fmt.Errorf("razorpay scenario: payment %q has more than 15 notes", payment.ID)
		}
		payments[payment.ID] = payment
	}
	refunded := map[string]int64{}
	seenRefunds := map[string]struct{}{}
	for _, refund := range state.Refunds {
		if _, exists := seenRefunds[refund.ID]; exists {
			return fmt.Errorf("razorpay scenario: duplicate refund id %q", refund.ID)
		}
		seenRefunds[refund.ID] = struct{}{}
		payment, ok := payments[refund.PaymentID]
		if !ok {
			return fmt.Errorf("razorpay scenario: refund %q references unknown payment %q", refund.ID, refund.PaymentID)
		}
		if refund.Currency != payment.Currency {
			return fmt.Errorf("razorpay scenario: refund %q currency does not match its payment", refund.ID)
		}
		if len(refund.Notes) > 15 {
			return fmt.Errorf("razorpay scenario: refund %q has more than 15 notes", refund.ID)
		}
		refunded[refund.PaymentID] += refund.Amount
	}
	for id, payment := range payments {
		if refunded[id] != payment.AmountRefunded {
			return fmt.Errorf("razorpay scenario: payment %q amountRefunded does not match its refunds", id)
		}
	}
	seenLinks := map[string]struct{}{}
	for _, link := range state.PaymentLinks {
		if _, exists := seenLinks[link.ID]; exists {
			return fmt.Errorf("razorpay scenario: duplicate payment link id %q", link.ID)
		}
		seenLinks[link.ID] = struct{}{}
		if len(link.Notes) > 15 {
			return fmt.Errorf("razorpay scenario: payment link %q has more than 15 notes", link.ID)
		}
	}
	return nil
}

func (scenarioCodec) Initialize(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("razorpay scenario: initialize: %w", err)
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
		return fmt.Errorf("razorpay scenario: begin load: %w", err)
	}
	defer tx.Rollback()
	for _, table := range []string{"refunds", "payments", "orders", "payment_links"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("razorpay scenario: clear %s: %w", table, err)
		}
	}
	for _, order := range state.Orders {
		order.Notes = notesOrEmpty(order.Notes)
		raw, err := json.Marshal(order)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO orders(id, body) VALUES(?, ?)`, order.ID, string(raw)); err != nil {
			return fmt.Errorf("razorpay scenario: insert order %s: %w", order.ID, err)
		}
	}
	for _, payment := range state.Payments {
		payment.Notes = notesOrEmpty(payment.Notes)
		raw, err := json.Marshal(payment)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO payments(id, order_id, body) VALUES(?, ?, ?)`, payment.ID, payment.OrderID, string(raw)); err != nil {
			return fmt.Errorf("razorpay scenario: insert payment %s: %w", payment.ID, err)
		}
	}
	for _, refund := range state.Refunds {
		refund.Notes = notesOrEmpty(refund.Notes)
		raw, err := json.Marshal(refund)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO refunds(id, payment_id, body) VALUES(?, ?, ?)`, refund.ID, refund.PaymentID, string(raw)); err != nil {
			return fmt.Errorf("razorpay scenario: insert refund %s: %w", refund.ID, err)
		}
	}
	for _, link := range state.PaymentLinks {
		link.Notes = notesOrEmpty(link.Notes)
		raw, err := json.Marshal(link)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO payment_links(id, body) VALUES(?, ?)`, link.ID, string(raw)); err != nil {
			return fmt.Errorf("razorpay scenario: insert payment link %s: %w", link.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("razorpay scenario: commit load: %w", err)
	}
	return nil
}

func (scenarioCodec) Dump(ctx context.Context, db *sql.DB, metadata scenario.Metadata) (scenario.Document, error) {
	orders, err := dumpRows[fixtureOrder](ctx, db, `SELECT id, body FROM orders ORDER BY id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("razorpay scenario: dump orders: %w", err)
	}
	payments, err := dumpRows[fixturePayment](ctx, db, `SELECT id, body FROM payments ORDER BY id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("razorpay scenario: dump payments: %w", err)
	}
	refunds, err := dumpRows[fixtureRefund](ctx, db, `SELECT id, body FROM refunds ORDER BY id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("razorpay scenario: dump refunds: %w", err)
	}
	links, err := dumpRows[fixturePaymentLink](ctx, db, `SELECT id, body FROM payment_links ORDER BY id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("razorpay scenario: dump payment links: %w", err)
	}
	for i := range orders {
		orders[i].Notes = notesOrEmpty(orders[i].Notes)
	}
	for i := range payments {
		payments[i].Notes = notesOrEmpty(payments[i].Notes)
	}
	for i := range refunds {
		refunds[i].Notes = notesOrEmpty(refunds[i].Notes)
	}
	for i := range links {
		links[i].Notes = notesOrEmpty(links[i].Notes)
	}
	raw, err := json.Marshal(fixtureState{Orders: orders, Payments: payments, Refunds: refunds, PaymentLinks: links})
	if err != nil {
		return scenario.Document{}, err
	}
	return scenario.Document{
		Contract: scenario.Contract, ContractVersion: 1, ID: metadata.ID,
		Resource: "razorpay", ResourceVersion: "v1", State: raw,
	}, nil
}

func dumpRows[T any](ctx context.Context, db *sql.DB, query string) ([]T, error) {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		var item T
		if err := json.Unmarshal([]byte(raw), &item); err != nil {
			return nil, fmt.Errorf("razorpay scenario: decode %s: %w", id, err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func decodeState(raw []byte) (fixtureState, error) {
	var state fixtureState
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return fixtureState{}, fmt.Errorf("razorpay scenario: decode state: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fixtureState{}, fmt.Errorf("razorpay scenario: state has trailing data")
	}
	if state.Orders == nil || state.Payments == nil || state.Refunds == nil || state.PaymentLinks == nil {
		return fixtureState{}, fmt.Errorf("razorpay scenario: orders, payments, refunds, and paymentLinks are required arrays")
	}
	return state, nil
}

func notesOrEmpty(notes map[string]string) map[string]string {
	if notes == nil {
		return map[string]string{}
	}
	return notes
}

func ContractName() string { return scenario.Contract }

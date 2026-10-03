package zohobooks

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/mail"
	"time"

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
		panic(fmt.Sprintf("zohobooks: parse embedded scenario schema: %v", err))
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("zohobooks-scenario.json", parsed); err != nil {
		panic(fmt.Sprintf("zohobooks: add embedded scenario schema: %v", err))
	}
	compiled, err := compiler.Compile("zohobooks-scenario.json")
	if err != nil {
		panic(fmt.Sprintf("zohobooks: compile embedded scenario schema: %v", err))
	}
	return compiled
}()

type scenarioCodec struct{}

type fixtureState struct {
	Organization     fixtureOrganization `json:"organization"`
	Contacts         []fixtureContact    `json:"contacts"`
	Invoices         []fixtureInvoice    `json:"invoices"`
	CreditNotes      []fixtureCreditNote `json:"creditNotes"`
	CustomerPayments []fixturePayment    `json:"customerPayments"`
}

type fixtureOrganization struct {
	OrganizationID string `json:"organizationId"`
	Name           string `json:"name"`
	ContactName    string `json:"contactName"`
	Email          string `json:"email"`
	CurrencyCode   string `json:"currencyCode"`
	CurrencySymbol string `json:"currencySymbol"`
	TimeZone       string `json:"timeZone"`
	IsOrgActive    bool   `json:"isOrgActive"`
}

type fixtureAddress struct {
	Address string `json:"address"`
	City    string `json:"city"`
	State   string `json:"state"`
	Zip     string `json:"zip"`
	Country string `json:"country"`
}

type fixtureContact struct {
	ContactID       string          `json:"contactId"`
	ContactName     string          `json:"contactName"`
	CompanyName     string          `json:"companyName"`
	ContactType     string          `json:"contactType"`
	CustomerSubType string          `json:"customerSubType"`
	Email           string          `json:"email"`
	Phone           string          `json:"phone"`
	FirstName       string          `json:"firstName,omitempty"`
	LastName        string          `json:"lastName,omitempty"`
	CurrencyCode    string          `json:"currencyCode"`
	Notes           string          `json:"notes,omitempty"`
	Status          string          `json:"status"`
	BillingAddress  *fixtureAddress `json:"billingAddress,omitempty"`
}

type fixtureLineItem struct {
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	Rate        float64 `json:"rate"`
	Quantity    float64 `json:"quantity"`
}

type fixtureInvoice struct {
	InvoiceID       string            `json:"invoiceId"`
	InvoiceNumber   string            `json:"invoiceNumber"`
	CustomerID      string            `json:"customerId"`
	Status          string            `json:"status"`
	Date            string            `json:"date"`
	DueDate         string            `json:"dueDate"`
	CurrencyCode    string            `json:"currencyCode"`
	ReferenceNumber string            `json:"referenceNumber,omitempty"`
	Notes           string            `json:"notes,omitempty"`
	LineItems       []fixtureLineItem `json:"lineItems"`
	Total           float64           `json:"total"`
	Balance         float64           `json:"balance"`
}

type fixtureCreditNote struct {
	CreditNoteID     string  `json:"creditnoteId"`
	CreditNoteNumber string  `json:"creditnoteNumber"`
	CustomerID       string  `json:"customerId"`
	InvoiceID        string  `json:"invoiceId"`
	Status           string  `json:"status"`
	Date             string  `json:"date"`
	CurrencyCode     string  `json:"currencyCode"`
	ReferenceNumber  string  `json:"referenceNumber,omitempty"`
	Notes            string  `json:"notes,omitempty"`
	Total            float64 `json:"total"`
	Balance          float64 `json:"balance"`
}

type fixturePaymentInvoice struct {
	InvoiceID     string  `json:"invoiceId"`
	AmountApplied float64 `json:"amountApplied"`
}

type fixturePayment struct {
	PaymentID       string                  `json:"paymentId"`
	CustomerID      string                  `json:"customerId"`
	PaymentMode     string                  `json:"paymentMode"`
	Amount          float64                 `json:"amount"`
	Date            string                  `json:"date"`
	CurrencyCode    string                  `json:"currencyCode"`
	ReferenceNumber string                  `json:"referenceNumber,omitempty"`
	Description     string                  `json:"description,omitempty"`
	Invoices        []fixturePaymentInvoice `json:"invoices"`
}

func (scenarioCodec) Validate(_ context.Context, doc scenario.Document) error {
	if err := doc.ValidateEnvelope(); err != nil {
		return err
	}
	if doc.Resource != "zohobooks" || doc.ResourceVersion != "v3" {
		return fmt.Errorf("zohobooks scenario: expected resource zohobooks v3, got %s %s", doc.Resource, doc.ResourceVersion)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc.State))
	if err != nil {
		return fmt.Errorf("zohobooks scenario: decode state for schema validation: %w", err)
	}
	if err := compiledScenarioSchema.Validate(instance); err != nil {
		return fmt.Errorf("zohobooks scenario: schema validation: %w", err)
	}
	state, err := decodeState(doc.State)
	if err != nil {
		return err
	}
	if _, err := mail.ParseAddress(state.Organization.Email); err != nil {
		return fmt.Errorf("zohobooks scenario: organization.email: %w", err)
	}
	contacts := map[string]fixtureContact{}
	for i, contact := range state.Contacts {
		if _, err := mail.ParseAddress(contact.Email); err != nil {
			return fmt.Errorf("zohobooks scenario: contacts[%d].email: %w", i, err)
		}
		if _, exists := contacts[contact.ContactID]; exists {
			return fmt.Errorf("zohobooks scenario: duplicate contact id %q", contact.ContactID)
		}
		contacts[contact.ContactID] = contact
	}
	invoices := map[string]fixtureInvoice{}
	numbers := map[string]struct{}{}
	for i, invoice := range state.Invoices {
		if _, err := parseDay(invoice.Date); err != nil {
			return fmt.Errorf("zohobooks scenario: invoices[%d].date: %w", i, err)
		}
		if _, err := parseDay(invoice.DueDate); err != nil {
			return fmt.Errorf("zohobooks scenario: invoices[%d].dueDate: %w", i, err)
		}
		customer, ok := contacts[invoice.CustomerID]
		if !ok {
			return fmt.Errorf("zohobooks scenario: invoices[%d] references unknown contact %q", i, invoice.CustomerID)
		}
		if customer.CurrencyCode != invoice.CurrencyCode {
			return fmt.Errorf("zohobooks scenario: invoices[%d] currency %s does not match contact %s", i, invoice.CurrencyCode, customer.CurrencyCode)
		}
		if _, exists := invoices[invoice.InvoiceID]; exists {
			return fmt.Errorf("zohobooks scenario: duplicate invoice id %q", invoice.InvoiceID)
		}
		if _, exists := numbers[invoice.InvoiceNumber]; exists {
			return fmt.Errorf("zohobooks scenario: duplicate invoice number %q", invoice.InvoiceNumber)
		}
		var sum float64
		for _, line := range invoice.LineItems {
			if line.Quantity <= 0 {
				return fmt.Errorf("zohobooks scenario: invoice %s has a non-positive quantity", invoice.InvoiceID)
			}
			sum += line.Rate * line.Quantity
		}
		if !sameMoney(sum, invoice.Total) {
			return fmt.Errorf("zohobooks scenario: invoice %s total %v does not match line items %v", invoice.InvoiceID, invoice.Total, sum)
		}
		if invoice.Balance > invoice.Total && !sameMoney(invoice.Balance, invoice.Total) {
			return fmt.Errorf("zohobooks scenario: invoice %s balance exceeds total", invoice.InvoiceID)
		}
		if (invoice.Status == "paid" || invoice.Status == "void") && !sameMoney(invoice.Balance, 0) {
			return fmt.Errorf("zohobooks scenario: invoice %s status %s requires a zero balance", invoice.InvoiceID, invoice.Status)
		}
		numbers[invoice.InvoiceNumber] = struct{}{}
		invoices[invoice.InvoiceID] = invoice
	}
	creditNotes := map[string]struct{}{}
	creditNumbers := map[string]struct{}{}
	for i, note := range state.CreditNotes {
		if _, err := parseDay(note.Date); err != nil {
			return fmt.Errorf("zohobooks scenario: creditNotes[%d].date: %w", i, err)
		}
		invoice, ok := invoices[note.InvoiceID]
		if !ok {
			return fmt.Errorf("zohobooks scenario: creditNotes[%d] references unknown invoice %q", i, note.InvoiceID)
		}
		if _, ok := contacts[note.CustomerID]; !ok {
			return fmt.Errorf("zohobooks scenario: creditNotes[%d] references unknown contact %q", i, note.CustomerID)
		}
		if note.CustomerID != invoice.CustomerID {
			return fmt.Errorf("zohobooks scenario: credit note %s customer does not match invoice %s", note.CreditNoteID, note.InvoiceID)
		}
		if note.CurrencyCode != invoice.CurrencyCode {
			return fmt.Errorf("zohobooks scenario: credit note %s currency does not match invoice %s", note.CreditNoteID, note.InvoiceID)
		}
		if (note.Status == "closed" || note.Status == "void") && !sameMoney(note.Balance, 0) {
			return fmt.Errorf("zohobooks scenario: credit note %s status %s requires a zero balance", note.CreditNoteID, note.Status)
		}
		if _, exists := creditNotes[note.CreditNoteID]; exists {
			return fmt.Errorf("zohobooks scenario: duplicate credit note id %q", note.CreditNoteID)
		}
		if _, exists := creditNumbers[note.CreditNoteNumber]; exists {
			return fmt.Errorf("zohobooks scenario: duplicate credit note number %q", note.CreditNoteNumber)
		}
		creditNotes[note.CreditNoteID] = struct{}{}
		creditNumbers[note.CreditNoteNumber] = struct{}{}
	}
	payments := map[string]struct{}{}
	for i, payment := range state.CustomerPayments {
		if _, err := parseDay(payment.Date); err != nil {
			return fmt.Errorf("zohobooks scenario: customerPayments[%d].date: %w", i, err)
		}
		customer, ok := contacts[payment.CustomerID]
		if !ok {
			return fmt.Errorf("zohobooks scenario: customerPayments[%d] references unknown contact %q", i, payment.CustomerID)
		}
		if customer.CurrencyCode != payment.CurrencyCode {
			return fmt.Errorf("zohobooks scenario: payment %s currency does not match contact %s", payment.PaymentID, customer.CurrencyCode)
		}
		if _, exists := payments[payment.PaymentID]; exists {
			return fmt.Errorf("zohobooks scenario: duplicate payment id %q", payment.PaymentID)
		}
		seen := map[string]struct{}{}
		var applied float64
		for _, link := range payment.Invoices {
			invoice, ok := invoices[link.InvoiceID]
			if !ok {
				return fmt.Errorf("zohobooks scenario: payment %s references unknown invoice %q", payment.PaymentID, link.InvoiceID)
			}
			if invoice.CustomerID != payment.CustomerID {
				return fmt.Errorf("zohobooks scenario: payment %s invoice %s belongs to another customer", payment.PaymentID, link.InvoiceID)
			}
			if invoice.CurrencyCode != payment.CurrencyCode {
				return fmt.Errorf("zohobooks scenario: payment %s currency does not match invoice %s", payment.PaymentID, link.InvoiceID)
			}
			if _, exists := seen[link.InvoiceID]; exists {
				return fmt.Errorf("zohobooks scenario: payment %s repeats invoice %s", payment.PaymentID, link.InvoiceID)
			}
			seen[link.InvoiceID] = struct{}{}
			applied += link.AmountApplied
		}
		// Payments are not folded into invoice balance. INV-4812 stays sent
		// while both duplicate charges apply the full amount.
		if !sameMoney(applied, payment.Amount) {
			return fmt.Errorf("zohobooks scenario: payment %s amount does not match applied invoices", payment.PaymentID)
		}
		payments[payment.PaymentID] = struct{}{}
	}
	return nil
}

func (scenarioCodec) Initialize(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("zohobooks scenario: initialize: %w", err)
	}
	return nil
}

func (codec scenarioCodec) Load(ctx context.Context, db *sql.DB, doc scenario.Document) error {
	if err := codec.Validate(ctx, doc); err != nil {
		return err
	}
	state, _ := decodeState(doc.State)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("zohobooks scenario: begin load: %w", err)
	}
	defer tx.Rollback()
	for _, table := range []string{"payment_invoices", "customer_payments", "credit_notes", "invoice_lines", "invoices", "contacts", "organizations"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("zohobooks scenario: clear %s: %w", table, err)
		}
	}
	org := state.Organization
	if _, err := tx.ExecContext(ctx, `INSERT INTO organizations
		(organization_id, name, contact_name, email, currency_code, currency_symbol, time_zone, is_org_active)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		org.OrganizationID, org.Name, org.ContactName, org.Email, org.CurrencyCode, org.CurrencySymbol, org.TimeZone, boolInt(org.IsOrgActive)); err != nil {
		return fmt.Errorf("zohobooks scenario: insert organization: %w", err)
	}
	for _, contact := range state.Contacts {
		address := fixtureAddress{}
		if contact.BillingAddress != nil {
			address = *contact.BillingAddress
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO contacts
			(contact_id, contact_name, company_name, contact_type, customer_sub_type, email, phone, first_name, last_name,
			 currency_code, notes, status, address, city, state, zip, country, created_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			contact.ContactID, contact.ContactName, contact.CompanyName, contact.ContactType, contact.CustomerSubType,
			contact.Email, contact.Phone, contact.FirstName, contact.LastName, contact.CurrencyCode, contact.Notes, contact.Status,
			address.Address, address.City, address.State, address.Zip, address.Country, seedCreatedAt); err != nil {
			return fmt.Errorf("zohobooks scenario: insert contact %s: %w", contact.ContactID, err)
		}
	}
	for _, invoice := range state.Invoices {
		if _, err := tx.ExecContext(ctx, `INSERT INTO invoices
			(invoice_id, invoice_number, customer_id, status, date, due_date, currency_code, reference_number, notes, total, balance, created_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			invoice.InvoiceID, invoice.InvoiceNumber, invoice.CustomerID, invoice.Status, invoice.Date, invoice.DueDate,
			invoice.CurrencyCode, invoice.ReferenceNumber, invoice.Notes, money(invoice.Total), money(invoice.Balance), createdAtFromDate(invoice.Date)); err != nil {
			return fmt.Errorf("zohobooks scenario: insert invoice %s: %w", invoice.InvoiceID, err)
		}
		for position, line := range invoice.LineItems {
			if _, err := tx.ExecContext(ctx, `INSERT INTO invoice_lines
				(invoice_id, position, name, description, rate, quantity) VALUES(?, ?, ?, ?, ?, ?)`,
				invoice.InvoiceID, position, line.Name, line.Description, money(line.Rate), money(line.Quantity)); err != nil {
				return fmt.Errorf("zohobooks scenario: insert invoice %s line %d: %w", invoice.InvoiceID, position, err)
			}
		}
	}
	for _, note := range state.CreditNotes {
		if _, err := tx.ExecContext(ctx, `INSERT INTO credit_notes
			(creditnote_id, creditnote_number, customer_id, invoice_id, status, date, currency_code, reference_number, notes, total, balance, created_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			note.CreditNoteID, note.CreditNoteNumber, note.CustomerID, note.InvoiceID, note.Status, note.Date,
			note.CurrencyCode, note.ReferenceNumber, note.Notes, money(note.Total), money(note.Balance), createdAtFromDate(note.Date)); err != nil {
			return fmt.Errorf("zohobooks scenario: insert credit note %s: %w", note.CreditNoteID, err)
		}
	}
	for _, payment := range state.CustomerPayments {
		if _, err := tx.ExecContext(ctx, `INSERT INTO customer_payments
			(payment_id, customer_id, payment_mode, amount, date, currency_code, reference_number, description, created_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			payment.PaymentID, payment.CustomerID, payment.PaymentMode, money(payment.Amount), payment.Date,
			payment.CurrencyCode, payment.ReferenceNumber, payment.Description, createdAtFromDate(payment.Date)); err != nil {
			return fmt.Errorf("zohobooks scenario: insert payment %s: %w", payment.PaymentID, err)
		}
		for _, link := range payment.Invoices {
			if _, err := tx.ExecContext(ctx, `INSERT INTO payment_invoices(payment_id, invoice_id, amount_applied) VALUES(?, ?, ?)`,
				payment.PaymentID, link.InvoiceID, money(link.AmountApplied)); err != nil {
				return fmt.Errorf("zohobooks scenario: insert payment %s invoice %s: %w", payment.PaymentID, link.InvoiceID, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("zohobooks scenario: commit load: %w", err)
	}
	return nil
}

func (scenarioCodec) Dump(ctx context.Context, db *sql.DB, metadata scenario.Metadata) (scenario.Document, error) {
	var state fixtureState
	var active int
	err := db.QueryRowContext(ctx, `SELECT organization_id, name, contact_name, email, currency_code, currency_symbol, time_zone, is_org_active
		FROM organizations`).Scan(&state.Organization.OrganizationID, &state.Organization.Name, &state.Organization.ContactName,
		&state.Organization.Email, &state.Organization.CurrencyCode, &state.Organization.CurrencySymbol, &state.Organization.TimeZone, &active)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("zohobooks scenario: dump organization: %w", err)
	}
	state.Organization.IsOrgActive = active != 0

	contactRows, err := db.QueryContext(ctx, `SELECT contact_id, contact_name, company_name, contact_type, customer_sub_type, email, phone,
		first_name, last_name, currency_code, notes, status, address, city, state, zip, country
		FROM contacts ORDER BY contact_id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("zohobooks scenario: dump contacts: %w", err)
	}
	for contactRows.Next() {
		var contact fixtureContact
		var address fixtureAddress
		if err := contactRows.Scan(&contact.ContactID, &contact.ContactName, &contact.CompanyName, &contact.ContactType, &contact.CustomerSubType,
			&contact.Email, &contact.Phone, &contact.FirstName, &contact.LastName, &contact.CurrencyCode, &contact.Notes, &contact.Status,
			&address.Address, &address.City, &address.State, &address.Zip, &address.Country); err != nil {
			contactRows.Close()
			return scenario.Document{}, err
		}
		if address != (fixtureAddress{}) {
			contact.BillingAddress = &address
		}
		state.Contacts = append(state.Contacts, contact)
	}
	if err := contactRows.Close(); err != nil {
		return scenario.Document{}, err
	}

	invoiceRows, err := db.QueryContext(ctx, `SELECT invoice_id, invoice_number, customer_id, status, date, due_date, currency_code,
		reference_number, notes, total, balance FROM invoices ORDER BY invoice_id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("zohobooks scenario: dump invoices: %w", err)
	}
	for invoiceRows.Next() {
		var invoice fixtureInvoice
		if err := invoiceRows.Scan(&invoice.InvoiceID, &invoice.InvoiceNumber, &invoice.CustomerID, &invoice.Status, &invoice.Date, &invoice.DueDate,
			&invoice.CurrencyCode, &invoice.ReferenceNumber, &invoice.Notes, &invoice.Total, &invoice.Balance); err != nil {
			invoiceRows.Close()
			return scenario.Document{}, err
		}
		invoice.Total = money(invoice.Total)
		invoice.Balance = money(invoice.Balance)
		invoice.LineItems = []fixtureLineItem{}
		state.Invoices = append(state.Invoices, invoice)
	}
	if err := invoiceRows.Close(); err != nil {
		return scenario.Document{}, err
	}
	for i := range state.Invoices {
		lineRows, err := db.QueryContext(ctx, `SELECT name, description, rate, quantity FROM invoice_lines
			WHERE invoice_id=? ORDER BY position`, state.Invoices[i].InvoiceID)
		if err != nil {
			return scenario.Document{}, fmt.Errorf("zohobooks scenario: dump invoice lines: %w", err)
		}
		for lineRows.Next() {
			var line fixtureLineItem
			if err := lineRows.Scan(&line.Name, &line.Description, &line.Rate, &line.Quantity); err != nil {
				lineRows.Close()
				return scenario.Document{}, err
			}
			line.Rate = money(line.Rate)
			line.Quantity = money(line.Quantity)
			state.Invoices[i].LineItems = append(state.Invoices[i].LineItems, line)
		}
		if err := lineRows.Close(); err != nil {
			return scenario.Document{}, err
		}
	}

	noteRows, err := db.QueryContext(ctx, `SELECT creditnote_id, creditnote_number, customer_id, invoice_id, status, date, currency_code,
		reference_number, notes, total, balance FROM credit_notes ORDER BY creditnote_id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("zohobooks scenario: dump credit notes: %w", err)
	}
	for noteRows.Next() {
		var note fixtureCreditNote
		if err := noteRows.Scan(&note.CreditNoteID, &note.CreditNoteNumber, &note.CustomerID, &note.InvoiceID, &note.Status, &note.Date,
			&note.CurrencyCode, &note.ReferenceNumber, &note.Notes, &note.Total, &note.Balance); err != nil {
			noteRows.Close()
			return scenario.Document{}, err
		}
		note.Total = money(note.Total)
		note.Balance = money(note.Balance)
		state.CreditNotes = append(state.CreditNotes, note)
	}
	if err := noteRows.Close(); err != nil {
		return scenario.Document{}, err
	}

	paymentRows, err := db.QueryContext(ctx, `SELECT payment_id, customer_id, payment_mode, amount, date, currency_code, reference_number, description
		FROM customer_payments ORDER BY payment_id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("zohobooks scenario: dump payments: %w", err)
	}
	for paymentRows.Next() {
		var payment fixturePayment
		if err := paymentRows.Scan(&payment.PaymentID, &payment.CustomerID, &payment.PaymentMode, &payment.Amount, &payment.Date,
			&payment.CurrencyCode, &payment.ReferenceNumber, &payment.Description); err != nil {
			paymentRows.Close()
			return scenario.Document{}, err
		}
		payment.Amount = money(payment.Amount)
		payment.Invoices = []fixturePaymentInvoice{}
		state.CustomerPayments = append(state.CustomerPayments, payment)
	}
	if err := paymentRows.Close(); err != nil {
		return scenario.Document{}, err
	}
	for i := range state.CustomerPayments {
		linkRows, err := db.QueryContext(ctx, `SELECT invoice_id, amount_applied FROM payment_invoices
			WHERE payment_id=? ORDER BY invoice_id`, state.CustomerPayments[i].PaymentID)
		if err != nil {
			return scenario.Document{}, fmt.Errorf("zohobooks scenario: dump payment invoices: %w", err)
		}
		for linkRows.Next() {
			var link fixturePaymentInvoice
			if err := linkRows.Scan(&link.InvoiceID, &link.AmountApplied); err != nil {
				linkRows.Close()
				return scenario.Document{}, err
			}
			link.AmountApplied = money(link.AmountApplied)
			state.CustomerPayments[i].Invoices = append(state.CustomerPayments[i].Invoices, link)
		}
		if err := linkRows.Close(); err != nil {
			return scenario.Document{}, err
		}
	}

	if state.Contacts == nil {
		state.Contacts = []fixtureContact{}
	}
	if state.Invoices == nil {
		state.Invoices = []fixtureInvoice{}
	}
	if state.CreditNotes == nil {
		state.CreditNotes = []fixtureCreditNote{}
	}
	if state.CustomerPayments == nil {
		state.CustomerPayments = []fixturePayment{}
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return scenario.Document{}, err
	}
	return scenario.Document{
		Contract: ContractName(), ContractVersion: 1, ID: metadata.ID,
		Resource: "zohobooks", ResourceVersion: "v3", State: raw,
	}, nil
}

func decodeState(raw []byte) (fixtureState, error) {
	var state fixtureState
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return fixtureState{}, fmt.Errorf("zohobooks scenario: decode state: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fixtureState{}, fmt.Errorf("zohobooks scenario: state has trailing data")
	}
	if state.Contacts == nil || state.Invoices == nil || state.CreditNotes == nil || state.CustomerPayments == nil {
		return fixtureState{}, fmt.Errorf("zohobooks scenario: contacts, invoices, creditNotes, and customerPayments are required arrays")
	}
	return state, nil
}

func ContractName() string { return scenario.Contract }

func money(value float64) float64 {
	return math.Round(value*100) / 100
}

func sameMoney(left, right float64) bool {
	return money(left) == money(right)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func parseDay(value string) (time.Time, error) {
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return time.Time{}, fmt.Errorf("must be yyyy-mm-dd")
	}
	return parsed, nil
}

const seedCreatedAt = "2026-08-01T12:00:00Z"

func createdAtFromDate(day string) string {
	return day + "T12:00:00Z"
}

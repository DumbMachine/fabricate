package zohobooks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/dumbmachine/fabricate/resources/zohobooks/generated"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func (s *server) requireOrg(ctx context.Context, id string) error {
	var got string
	err := s.db.QueryRowContext(ctx, "SELECT organization_id FROM organizations").Scan(&got)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && got != id) {
		return notFound("Organization does not exist.")
	}
	return err
}

func (s *server) loadOrganization(ctx context.Context) (generated.Organization, error) {
	var id, name, contact, email, currency, symbol, zone string
	var active int
	err := s.db.QueryRowContext(ctx, `SELECT organization_id, name, contact_name, email, currency_code, currency_symbol, time_zone, is_org_active
		FROM organizations`).Scan(&id, &name, &contact, &email, &currency, &symbol, &zone, &active)
	if err != nil {
		return generated.Organization{}, err
	}
	return generated.Organization{
		OrganizationId: id, Name: name, ContactName: contact, Email: openapi_types.Email(email),
		IsDefaultOrg: true, LanguageCode: "en", FiscalYearStartMonth: 3, AccountCreatedDate: "2024-04-01",
		TimeZone: zone, IsOrgActive: active != 0, CurrencyCode: currency, CurrencySymbol: symbol,
		CurrencyFormat: "###,##0.00", PricePrecision: 2,
	}, nil
}

type contactRow struct {
	ID, Name, Company, Type, SubType, Email, Phone, First, Last, Currency, Notes, Status string
	Address, City, Region, Zip, Country, CreatedAt                                       string
}

func (s *server) listContacts(ctx context.Context, params generated.ListContactsParams) ([]contactRow, error) {
	query := `SELECT contact_id, contact_name, company_name, contact_type, customer_sub_type, email, phone, first_name, last_name,
		currency_code, notes, status, address, city, state, zip, country, created_at FROM contacts WHERE 1=1`
	var args []any
	if email := deref(params.Email); email != "" {
		query += " AND email = ?"
		args = append(args, email)
	}
	if name := deref(params.ContactName); name != "" {
		query += ` AND contact_name LIKE ? ESCAPE '\'`
		args = append(args, likeContains(name))
	}
	query += " ORDER BY contact_name, contact_id"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []contactRow
	for rows.Next() {
		row, err := scanContact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *server) loadContact(ctx context.Context, id string) (contactRow, error) {
	row := s.db.QueryRowContext(ctx, `SELECT contact_id, contact_name, company_name, contact_type, customer_sub_type, email, phone, first_name, last_name,
		currency_code, notes, status, address, city, state, zip, country, created_at FROM contacts WHERE contact_id=?`, id)
	contact, err := scanContact(row)
	if errors.Is(err, sql.ErrNoRows) {
		return contactRow{}, notFound("Contact does not exist.")
	}
	return contact, err
}

func scanContact(row interface{ Scan(...any) error }) (contactRow, error) {
	var contact contactRow
	err := row.Scan(&contact.ID, &contact.Name, &contact.Company, &contact.Type, &contact.SubType, &contact.Email, &contact.Phone,
		&contact.First, &contact.Last, &contact.Currency, &contact.Notes, &contact.Status, &contact.Address, &contact.City,
		&contact.Region, &contact.Zip, &contact.Country, &contact.CreatedAt)
	return contact, err
}

func (s *server) receivables(ctx context.Context) (map[string]float64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT i.customer_id, COALESCE(SUM(i.balance), 0)
		FROM invoices i
		JOIN contacts c ON c.contact_id = i.customer_id AND c.currency_code = i.currency_code
		GROUP BY i.customer_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var id string
		var amount float64
		if err := rows.Scan(&id, &amount); err != nil {
			return nil, err
		}
		out[id] = money(amount)
	}
	return out, rows.Err()
}

type invoiceRow struct {
	ID, Number, CustomerID, CustomerName, Status, Date, DueDate, Currency, Reference, Notes, CreatedAt string
	Total, Balance                                                                                     float64
}

func (s *server) listInvoices(ctx context.Context, params generated.ListInvoicesParams) ([]invoiceRow, error) {
	query := `SELECT i.invoice_id, i.invoice_number, i.customer_id, c.contact_name, i.status, i.date, i.due_date, i.currency_code,
		i.reference_number, i.notes, i.total, i.balance, i.created_at
		FROM invoices i JOIN contacts c ON c.contact_id = i.customer_id WHERE 1=1`
	var args []any
	if value := deref(params.InvoiceNumber); value != "" {
		query += " AND i.invoice_number = ?"
		args = append(args, value)
	}
	if value := deref(params.Status); value != "" {
		query += " AND i.status = ?"
		args = append(args, value)
	}
	if value := deref(params.CustomerId); value != "" {
		query += " AND i.customer_id = ?"
		args = append(args, value)
	}
	if value := deref(params.ReferenceNumber); value != "" {
		query += " AND i.reference_number = ?"
		args = append(args, value)
	}
	query += " ORDER BY i.invoice_number, i.invoice_id"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []invoiceRow
	for rows.Next() {
		row, err := scanInvoice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *server) invoiceByID(ctx context.Context, id string) (generated.Invoice, error) {
	row := s.db.QueryRowContext(ctx, `SELECT i.invoice_id, i.invoice_number, i.customer_id, c.contact_name, i.status, i.date, i.due_date, i.currency_code,
		i.reference_number, i.notes, i.total, i.balance, i.created_at
		FROM invoices i JOIN contacts c ON c.contact_id = i.customer_id WHERE i.invoice_id=?`, id)
	invoice, err := scanInvoice(row)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.Invoice{}, notFound("Invoice does not exist.")
	}
	if err != nil {
		return generated.Invoice{}, err
	}
	lines, err := s.lineItems(ctx, id)
	if err != nil {
		return generated.Invoice{}, err
	}
	return invoiceAPI(invoice, lines, true), nil
}

func scanInvoice(row interface{ Scan(...any) error }) (invoiceRow, error) {
	var invoice invoiceRow
	err := row.Scan(&invoice.ID, &invoice.Number, &invoice.CustomerID, &invoice.CustomerName, &invoice.Status, &invoice.Date, &invoice.DueDate,
		&invoice.Currency, &invoice.Reference, &invoice.Notes, &invoice.Total, &invoice.Balance, &invoice.CreatedAt)
	return invoice, err
}

func (s *server) lineItems(ctx context.Context, invoiceID string) ([]generated.LineItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT position, name, description, rate, quantity FROM invoice_lines WHERE invoice_id=? ORDER BY position`, invoiceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []generated.LineItem{}
	for rows.Next() {
		var position int
		var name, description string
		var rate, quantity float64
		if err := rows.Scan(&position, &name, &description, &rate, &quantity); err != nil {
			return nil, err
		}
		rate, quantity = money(rate), money(quantity)
		out = append(out, generated.LineItem{
			LineItemId: strPtr(fmt.Sprintf("%s-%d", invoiceID, position+1)),
			Name:       name, Description: strPtrOmit(description),
			Rate: rate, Quantity: quantity, ItemTotal: money(rate * quantity),
		})
	}
	return out, rows.Err()
}

type creditNoteRow struct {
	ID, Number, CustomerID, CustomerName, InvoiceID, Status, Date, Currency, Reference, Notes, CreatedAt string
	Total, Balance                                                                                       float64
}

func (s *server) listCreditNotes(ctx context.Context, params generated.ListCreditNotesParams) ([]creditNoteRow, error) {
	query := `SELECT n.creditnote_id, n.creditnote_number, n.customer_id, c.contact_name, n.invoice_id, n.status, n.date, n.currency_code,
		n.reference_number, n.notes, n.total, n.balance, n.created_at
		FROM credit_notes n JOIN contacts c ON c.contact_id = n.customer_id WHERE 1=1`
	var args []any
	if value := deref(params.CreditnoteNumber); value != "" {
		query += " AND n.creditnote_number = ?"
		args = append(args, value)
	}
	if value := deref(params.CustomerId); value != "" {
		query += " AND n.customer_id = ?"
		args = append(args, value)
	}
	if value := deref(params.ReferenceNumber); value != "" {
		query += " AND n.reference_number = ?"
		args = append(args, value)
	}
	query += " ORDER BY n.creditnote_number, n.creditnote_id"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []creditNoteRow
	for rows.Next() {
		row, err := scanCreditNote(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *server) loadCreditNote(ctx context.Context, id string) (creditNoteRow, error) {
	row := s.db.QueryRowContext(ctx, `SELECT n.creditnote_id, n.creditnote_number, n.customer_id, c.contact_name, n.invoice_id, n.status, n.date, n.currency_code,
		n.reference_number, n.notes, n.total, n.balance, n.created_at
		FROM credit_notes n JOIN contacts c ON c.contact_id = n.customer_id WHERE n.creditnote_id=?`, id)
	note, err := scanCreditNote(row)
	if errors.Is(err, sql.ErrNoRows) {
		return creditNoteRow{}, notFound("Credit note does not exist.")
	}
	return note, err
}

func scanCreditNote(row interface{ Scan(...any) error }) (creditNoteRow, error) {
	var note creditNoteRow
	err := row.Scan(&note.ID, &note.Number, &note.CustomerID, &note.CustomerName, &note.InvoiceID, &note.Status, &note.Date,
		&note.Currency, &note.Reference, &note.Notes, &note.Total, &note.Balance, &note.CreatedAt)
	return note, err
}

type paymentRow struct {
	ID, CustomerID, CustomerName, Mode, Date, Currency, Reference, Description, CreatedAt string
	Amount                                                                                float64
}

func (s *server) listPayments(ctx context.Context, params generated.ListCustomerPaymentsParams) ([]paymentRow, error) {
	query := `SELECT p.payment_id, p.customer_id, c.contact_name, p.payment_mode, p.amount, p.date, p.currency_code,
		p.reference_number, p.description, p.created_at
		FROM customer_payments p JOIN contacts c ON c.contact_id = p.customer_id WHERE 1=1`
	var args []any
	if value := deref(params.CustomerId); value != "" {
		query += " AND p.customer_id = ?"
		args = append(args, value)
	}
	if value := deref(params.ReferenceNumber); value != "" {
		query += " AND p.reference_number = ?"
		args = append(args, value)
	}
	query += " ORDER BY p.payment_id"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []paymentRow
	for rows.Next() {
		row, err := scanPayment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *server) loadPayment(ctx context.Context, id string) (paymentRow, error) {
	row := s.db.QueryRowContext(ctx, `SELECT p.payment_id, p.customer_id, c.contact_name, p.payment_mode, p.amount, p.date, p.currency_code,
		p.reference_number, p.description, p.created_at
		FROM customer_payments p JOIN contacts c ON c.contact_id = p.customer_id WHERE p.payment_id=?`, id)
	payment, err := scanPayment(row)
	if errors.Is(err, sql.ErrNoRows) {
		return paymentRow{}, notFound("Customer payment does not exist.")
	}
	return payment, err
}

func scanPayment(row interface{ Scan(...any) error }) (paymentRow, error) {
	var payment paymentRow
	err := row.Scan(&payment.ID, &payment.CustomerID, &payment.CustomerName, &payment.Mode, &payment.Amount, &payment.Date,
		&payment.Currency, &payment.Reference, &payment.Description, &payment.CreatedAt)
	return payment, err
}

func (s *server) paymentInvoices(ctx context.Context, paymentID string) ([]generated.PaymentInvoice, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT pi.invoice_id, i.invoice_number, i.date, i.total, pi.amount_applied, i.balance
		FROM payment_invoices pi JOIN invoices i ON i.invoice_id = pi.invoice_id
		WHERE pi.payment_id=? ORDER BY pi.invoice_id`, paymentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []generated.PaymentInvoice{}
	for rows.Next() {
		var id, number, date string
		var total, applied, balance float64
		if err := rows.Scan(&id, &number, &date, &total, &applied, &balance); err != nil {
			return nil, err
		}
		total, applied, balance = money(total), money(applied), money(balance)
		out = append(out, generated.PaymentInvoice{
			InvoiceId: id, InvoiceNumber: strPtr(number), Date: strPtr(date),
			InvoiceAmount: floatPtr(total), AmountApplied: applied, BalanceAmount: floatPtr(balance),
		})
	}
	return out, rows.Err()
}

func contactAPI(row contactRow, outstanding float64) generated.Contact {
	subType := generated.ContactCustomerSubType(row.SubType)
	contact := generated.Contact{
		ContactId: row.ID, ContactName: row.Name, CompanyName: row.Company,
		ContactType: generated.ContactContactType(row.Type), CustomerSubType: &subType,
		Status: generated.ContactStatus(row.Status), CurrencyCode: row.Currency,
		CurrencySymbol: strPtr(currencySymbol(row.Currency)), Email: strPtrOmit(row.Email), Phone: strPtrOmit(row.Phone),
		FirstName: strPtrOmit(row.First), LastName: strPtrOmit(row.Last), Notes: strPtrOmit(row.Notes),
		OutstandingReceivableAmount: floatPtr(outstanding), CreatedTime: strPtr(row.CreatedAt),
	}
	if row.Address != "" || row.City != "" || row.Region != "" || row.Zip != "" || row.Country != "" {
		contact.BillingAddress = &generated.Address{
			Address: strPtr(row.Address), City: strPtr(row.City), State: strPtr(row.Region),
			Zip: strPtr(row.Zip), Country: strPtr(row.Country), Phone: strPtrOmit(row.Phone),
		}
	}
	return contact
}

func invoiceAPI(row invoiceRow, lines []generated.LineItem, detail bool) generated.Invoice {
	invoice := generated.Invoice{
		InvoiceId: row.ID, InvoiceNumber: row.Number, CustomerId: row.CustomerID, CustomerName: row.CustomerName,
		Status: generated.InvoiceStatus(row.Status), Date: row.Date, DueDate: row.DueDate, CurrencyCode: row.Currency,
		CurrencySymbol: strPtr(currencySymbol(row.Currency)), ReferenceNumber: strPtrOmit(row.Reference), Notes: strPtrOmit(row.Notes),
		Total: money(row.Total), Balance: money(row.Balance), CreatedTime: strPtr(row.CreatedAt),
	}
	if detail {
		if lines == nil {
			lines = []generated.LineItem{}
		}
		invoice.LineItems = &lines
	}
	return invoice
}

func creditNoteAPI(row creditNoteRow) generated.CreditNote {
	return generated.CreditNote{
		CreditnoteId: row.ID, CreditnoteNumber: row.Number, CustomerId: row.CustomerID, CustomerName: row.CustomerName,
		InvoiceId: strPtr(row.InvoiceID), Status: generated.CreditNoteStatus(row.Status), Date: row.Date,
		CurrencyCode: row.Currency, CurrencySymbol: strPtr(currencySymbol(row.Currency)),
		ReferenceNumber: strPtrOmit(row.Reference), Notes: strPtrOmit(row.Notes),
		Total: money(row.Total), Balance: money(row.Balance), CreatedTime: strPtr(row.CreatedAt),
	}
}

func paymentAPI(row paymentRow, invoices []generated.PaymentInvoice) generated.CustomerPayment {
	if invoices == nil {
		invoices = []generated.PaymentInvoice{}
	}
	return generated.CustomerPayment{
		PaymentId: row.ID, CustomerId: row.CustomerID, CustomerName: row.CustomerName,
		PaymentMode: generated.CustomerPaymentPaymentMode(row.Mode), Amount: money(row.Amount), Date: row.Date,
		CurrencyCode: row.Currency, CurrencySymbol: strPtr(currencySymbol(row.Currency)),
		ReferenceNumber: strPtrOmit(row.Reference), Description: strPtrOmit(row.Description),
		CreatedTime: strPtr(row.CreatedAt), Invoices: &invoices,
	}
}

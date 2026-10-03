package zohobooks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/zohobooks/generated"
	"github.com/getkin/kin-openapi/openapi3filter"
	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

type server struct {
	db      *sql.DB
	clock   httpresource.Clock
	ids     httpresource.IDGenerator
	handler http.Handler
}

var _ generated.StrictServerInterface = (*server)(nil)

type booksError struct {
	status  int
	code    int
	message string
}

func (e booksError) Error() string { return e.message }

func notFound(message string) error {
	return booksError{status: http.StatusNotFound, code: 1002, message: message}
}

func invalid(message string) error {
	return booksError{status: http.StatusBadRequest, code: 4, message: message}
}

func apiError(err error) (generated.Error, int, bool) {
	var api booksError
	if errors.As(err, &api) {
		return generated.Error{Code: api.code, Message: api.message}, api.status, true
	}
	return generated.Error{}, 0, false
}

func newServer(ctx context.Context, dependencies httpresource.ServerDependencies) (*server, error) {
	if dependencies.DB == nil || dependencies.Clock == nil || dependencies.IDs == nil || dependencies.Secrets == nil {
		return nil, fmt.Errorf("zohobooks: database, clock, ID generator, and secrets are required")
	}
	token, err := dependencies.Secrets.Get(ctx, "token")
	if err != nil {
		return nil, fmt.Errorf("zohobooks: load synthetic token: %w", err)
	}
	if token == "" {
		return nil, fmt.Errorf("zohobooks: synthetic token is empty")
	}
	spec, err := generated.GetSwagger()
	if err != nil {
		return nil, fmt.Errorf("zohobooks: load OpenAPI: %w", err)
	}
	impl := &server{db: dependencies.DB, clock: dependencies.Clock, ids: dependencies.IDs}
	strict := generated.NewStrictHandler(impl, nil)
	validator := nethttpmiddleware.OapiRequestValidatorWithOptions(spec, &nethttpmiddleware.Options{
		DoNotValidateServers: true,
		Options: openapi3filter.Options{AuthenticationFunc: func(_ context.Context, input *openapi3filter.AuthenticationInput) error {
			// Zoho Books production sends Zoho-oauthtoken. The proxy injects a bearer token.
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
			code := 4
			message := err.Error()
			if status == http.StatusUnauthorized {
				code = 57
				message = "You are not authorized to perform this operation"
			}
			writeError(w, status, code, message)
		},
	})
	impl.handler = validator(generated.Handler(strict))
	return impl, nil
}

func (s *server) Handler() http.Handler       { return s.handler }
func (s *server) Close(context.Context) error { return nil }

func (s *server) ListOrganizations(ctx context.Context, _ generated.ListOrganizationsRequestObject) (generated.ListOrganizationsResponseObject, error) {
	org, err := s.loadOrganization(ctx)
	if err != nil {
		return nil, err
	}
	return generated.ListOrganizations200JSONResponse{Code: 0, Message: "success", Organizations: []generated.Organization{org}}, nil
}

func (s *server) GetOrganization(ctx context.Context, request generated.GetOrganizationRequestObject) (generated.GetOrganizationResponseObject, error) {
	if err := s.requireOrg(ctx, request.OrganizationId); err != nil {
		if body, status, ok := apiError(err); ok {
			return generated.GetOrganizationdefaultJSONResponse{Body: body, StatusCode: status}, nil
		}
		return nil, err
	}
	org, err := s.loadOrganization(ctx)
	if err != nil {
		return nil, err
	}
	return generated.GetOrganization200JSONResponse{Code: 0, Message: "success", Organization: org}, nil
}

func (s *server) ListContacts(ctx context.Context, request generated.ListContactsRequestObject) (generated.ListContactsResponseObject, error) {
	if err := s.requireOrg(ctx, request.Params.OrganizationId); err != nil {
		if body, status, ok := apiError(err); ok {
			return generated.ListContactsdefaultJSONResponse{Body: body, StatusCode: status}, nil
		}
		return nil, err
	}
	rows, err := s.listContacts(ctx, request.Params)
	if err != nil {
		return nil, err
	}
	amounts, err := s.receivables(ctx)
	if err != nil {
		return nil, err
	}
	window, page := paginate(rows, request.Params.Page, request.Params.PerPage, "Status.All", "contact_name")
	contacts := []generated.Contact{}
	for _, row := range window {
		contacts = append(contacts, contactAPI(row, amounts[row.ID]))
	}
	return generated.ListContacts200JSONResponse{Code: 0, Message: "success", Contacts: contacts, PageContext: page}, nil
}

func (s *server) GetContact(ctx context.Context, request generated.GetContactRequestObject) (generated.GetContactResponseObject, error) {
	if err := s.requireOrg(ctx, request.Params.OrganizationId); err != nil {
		if body, status, ok := apiError(err); ok {
			return generated.GetContactdefaultJSONResponse{Body: body, StatusCode: status}, nil
		}
		return nil, err
	}
	row, err := s.loadContact(ctx, request.ContactId)
	if err != nil {
		if body, status, ok := apiError(err); ok {
			return generated.GetContactdefaultJSONResponse{Body: body, StatusCode: status}, nil
		}
		return nil, err
	}
	amounts, err := s.receivables(ctx)
	if err != nil {
		return nil, err
	}
	return generated.GetContact200JSONResponse{Code: 0, Message: "success", Contact: contactAPI(row, amounts[row.ID])}, nil
}

func (s *server) CreateContact(ctx context.Context, request generated.CreateContactRequestObject) (generated.CreateContactResponseObject, error) {
	if err := s.requireOrg(ctx, request.Params.OrganizationId); err != nil {
		if body, status, ok := apiError(err); ok {
			return generated.CreateContactdefaultJSONResponse{Body: body, StatusCode: status}, nil
		}
		return nil, err
	}
	if request.Body == nil {
		return generated.CreateContactdefaultJSONResponse{Body: generated.Error{Code: 4, Message: "Request body is required."}, StatusCode: http.StatusBadRequest}, nil
	}
	body := request.Body
	name := strings.TrimSpace(body.ContactName)
	if name == "" {
		return generated.CreateContactdefaultJSONResponse{Body: generated.Error{Code: 4, Message: "contact_name is required."}, StatusCode: http.StatusBadRequest}, nil
	}
	email := deref(body.Email)
	if email != "" {
		if _, err := mail.ParseAddress(email); err != nil {
			return generated.CreateContactdefaultJSONResponse{Body: generated.Error{Code: 4, Message: "email is invalid."}, StatusCode: http.StatusBadRequest}, nil
		}
	}
	org, err := s.loadOrganization(ctx)
	if err != nil {
		return nil, err
	}
	contactType := "customer"
	if body.ContactType != nil {
		contactType = string(*body.ContactType)
	}
	subType := "individual"
	if body.CustomerSubType != nil {
		subType = string(*body.CustomerSubType)
	}
	company := name
	if value := strings.TrimSpace(deref(body.CompanyName)); value != "" {
		company = value
	}
	currency := org.CurrencyCode
	if body.CurrencyCode != nil {
		currency = string(*body.CurrencyCode)
	}
	id, err := s.ids.Next(ctx, "zohobooks.contact")
	if err != nil {
		return nil, fmt.Errorf("zohobooks: allocate contact ID: %w", err)
	}
	address := generated.Address{}
	if body.BillingAddress != nil {
		address = *body.BillingAddress
	}
	created := s.clock.Now().UTC().Format(time.RFC3339)
	_, err = s.db.ExecContext(ctx, `INSERT INTO contacts
		(contact_id, contact_name, company_name, contact_type, customer_sub_type, email, phone, first_name, last_name,
		 currency_code, notes, status, address, city, state, zip, country, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'active', ?, ?, ?, ?, ?, ?)`,
		id, name, company, contactType, subType, email, deref(body.Phone), deref(body.FirstName), deref(body.LastName),
		currency, deref(body.Notes), deref(address.Address), deref(address.City), deref(address.State), deref(address.Zip), deref(address.Country), created)
	if err != nil {
		return nil, err
	}
	row, err := s.loadContact(ctx, id)
	if err != nil {
		return nil, err
	}
	return generated.CreateContact201JSONResponse{Code: 0, Message: "The contact has been created.", Contact: contactAPI(row, 0)}, nil
}

func (s *server) ListInvoices(ctx context.Context, request generated.ListInvoicesRequestObject) (generated.ListInvoicesResponseObject, error) {
	if err := s.requireOrg(ctx, request.Params.OrganizationId); err != nil {
		if body, status, ok := apiError(err); ok {
			return generated.ListInvoicesdefaultJSONResponse{Body: body, StatusCode: status}, nil
		}
		return nil, err
	}
	rows, err := s.listInvoices(ctx, request.Params)
	if err != nil {
		return nil, err
	}
	window, page := paginate(rows, request.Params.Page, request.Params.PerPage, filterLabel(deref(request.Params.Status)), "invoice_number")
	invoices := []generated.Invoice{}
	for _, row := range window {
		invoices = append(invoices, invoiceAPI(row, nil, false))
	}
	return generated.ListInvoices200JSONResponse{Code: 0, Message: "success", Invoices: invoices, PageContext: page}, nil
}

func (s *server) GetInvoice(ctx context.Context, request generated.GetInvoiceRequestObject) (generated.GetInvoiceResponseObject, error) {
	if err := s.requireOrg(ctx, request.Params.OrganizationId); err != nil {
		if body, status, ok := apiError(err); ok {
			return generated.GetInvoicedefaultJSONResponse{Body: body, StatusCode: status}, nil
		}
		return nil, err
	}
	invoice, err := s.invoiceByID(ctx, request.InvoiceId)
	if err != nil {
		if body, status, ok := apiError(err); ok {
			return generated.GetInvoicedefaultJSONResponse{Body: body, StatusCode: status}, nil
		}
		return nil, err
	}
	return generated.GetInvoice200JSONResponse{Code: 0, Message: "success", Invoice: invoice}, nil
}

func (s *server) CreateInvoice(ctx context.Context, request generated.CreateInvoiceRequestObject) (generated.CreateInvoiceResponseObject, error) {
	if err := s.requireOrg(ctx, request.Params.OrganizationId); err != nil {
		if body, status, ok := apiError(err); ok {
			return generated.CreateInvoicedefaultJSONResponse{Body: body, StatusCode: status}, nil
		}
		return nil, err
	}
	if request.Body == nil {
		return generated.CreateInvoicedefaultJSONResponse{Body: generated.Error{Code: 4, Message: "Request body is required."}, StatusCode: http.StatusBadRequest}, nil
	}
	body := request.Body
	if len(body.LineItems) == 0 {
		return generated.CreateInvoicedefaultJSONResponse{Body: generated.Error{Code: 4, Message: "line_items is required."}, StatusCode: http.StatusBadRequest}, nil
	}
	day := s.clock.Now().UTC().Format("2006-01-02")
	if value := deref(body.Date); value != "" {
		parsed, err := parseDay(value)
		if err != nil {
			return generated.CreateInvoicedefaultJSONResponse{Body: generated.Error{Code: 4, Message: "date must be yyyy-mm-dd."}, StatusCode: http.StatusBadRequest}, nil
		}
		day = parsed.Format("2006-01-02")
	}
	due := day
	if value := deref(body.DueDate); value != "" {
		parsed, err := parseDay(value)
		if err != nil {
			return generated.CreateInvoicedefaultJSONResponse{Body: generated.Error{Code: 4, Message: "due_date must be yyyy-mm-dd."}, StatusCode: http.StatusBadRequest}, nil
		}
		due = parsed.Format("2006-01-02")
	}
	var total float64
	for _, line := range body.LineItems {
		if strings.TrimSpace(line.Name) == "" {
			return generated.CreateInvoicedefaultJSONResponse{Body: generated.Error{Code: 4, Message: "line item name is required."}, StatusCode: http.StatusBadRequest}, nil
		}
		if line.Quantity <= 0 {
			return generated.CreateInvoicedefaultJSONResponse{Body: generated.Error{Code: 4, Message: "quantity must be greater than 0."}, StatusCode: http.StatusBadRequest}, nil
		}
		if line.Rate < 0 {
			return generated.CreateInvoicedefaultJSONResponse{Body: generated.Error{Code: 4, Message: "rate must be zero or greater."}, StatusCode: http.StatusBadRequest}, nil
		}
		total += line.Rate * line.Quantity
	}
	total = money(total)
	status := "draft"
	if body.Status != nil {
		status = string(*body.Status)
	}
	balance := total
	if status == "paid" || status == "void" {
		balance = 0
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var customerCurrency string
	err = tx.QueryRowContext(ctx, "SELECT currency_code FROM contacts WHERE contact_id=?", body.CustomerId).Scan(&customerCurrency)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.CreateInvoicedefaultJSONResponse{Body: generated.Error{Code: 1002, Message: "Contact does not exist."}, StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	currency := customerCurrency
	if body.CurrencyCode != nil && string(*body.CurrencyCode) != customerCurrency {
		return generated.CreateInvoicedefaultJSONResponse{Body: generated.Error{Code: 4, Message: "currency_code does not match the customer."}, StatusCode: http.StatusBadRequest}, nil
	}
	number := strings.TrimSpace(deref(body.InvoiceNumber))
	id := number
	if id == "" {
		id, err = s.ids.Next(ctx, "zohobooks.invoice")
		if err != nil {
			return nil, fmt.Errorf("zohobooks: allocate invoice ID: %w", err)
		}
		number = id
	}
	var existing string
	err = tx.QueryRowContext(ctx, "SELECT invoice_id FROM invoices WHERE invoice_id=? OR invoice_number=?", id, number).Scan(&existing)
	if err == nil {
		return generated.CreateInvoicedefaultJSONResponse{Body: generated.Error{Code: 4, Message: "Invoice number already exists."}, StatusCode: http.StatusBadRequest}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	created := s.clock.Now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx, `INSERT INTO invoices
		(invoice_id, invoice_number, customer_id, status, date, due_date, currency_code, reference_number, notes, total, balance, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, number, body.CustomerId, status, day, due, currency, deref(body.ReferenceNumber), deref(body.Notes), total, balance, created); err != nil {
		return nil, err
	}
	for position, line := range body.LineItems {
		if _, err := tx.ExecContext(ctx, `INSERT INTO invoice_lines(invoice_id, position, name, description, rate, quantity)
			VALUES(?, ?, ?, ?, ?, ?)`, id, position, strings.TrimSpace(line.Name), deref(line.Description), money(line.Rate), money(line.Quantity)); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	invoice, err := s.invoiceByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return generated.CreateInvoice201JSONResponse{Code: 0, Message: "The invoice has been created.", Invoice: invoice}, nil
}

func (s *server) ListCreditNotes(ctx context.Context, request generated.ListCreditNotesRequestObject) (generated.ListCreditNotesResponseObject, error) {
	if err := s.requireOrg(ctx, request.Params.OrganizationId); err != nil {
		if body, status, ok := apiError(err); ok {
			return generated.ListCreditNotesdefaultJSONResponse{Body: body, StatusCode: status}, nil
		}
		return nil, err
	}
	rows, err := s.listCreditNotes(ctx, request.Params)
	if err != nil {
		return nil, err
	}
	window, page := paginate(rows, request.Params.Page, request.Params.PerPage, "Status.All", "creditnote_number")
	notes := []generated.CreditNote{}
	for _, row := range window {
		notes = append(notes, creditNoteAPI(row))
	}
	return generated.ListCreditNotes200JSONResponse{Code: 0, Message: "success", Creditnotes: notes, PageContext: page}, nil
}

func (s *server) GetCreditNote(ctx context.Context, request generated.GetCreditNoteRequestObject) (generated.GetCreditNoteResponseObject, error) {
	if err := s.requireOrg(ctx, request.Params.OrganizationId); err != nil {
		if body, status, ok := apiError(err); ok {
			return generated.GetCreditNotedefaultJSONResponse{Body: body, StatusCode: status}, nil
		}
		return nil, err
	}
	row, err := s.loadCreditNote(ctx, request.CreditnoteId)
	if err != nil {
		if body, status, ok := apiError(err); ok {
			return generated.GetCreditNotedefaultJSONResponse{Body: body, StatusCode: status}, nil
		}
		return nil, err
	}
	return generated.GetCreditNote200JSONResponse{Code: 0, Message: "success", Creditnote: creditNoteAPI(row)}, nil
}

func (s *server) ListCustomerPayments(ctx context.Context, request generated.ListCustomerPaymentsRequestObject) (generated.ListCustomerPaymentsResponseObject, error) {
	if err := s.requireOrg(ctx, request.Params.OrganizationId); err != nil {
		if body, status, ok := apiError(err); ok {
			return generated.ListCustomerPaymentsdefaultJSONResponse{Body: body, StatusCode: status}, nil
		}
		return nil, err
	}
	rows, err := s.listPayments(ctx, request.Params)
	if err != nil {
		return nil, err
	}
	window, page := paginate(rows, request.Params.Page, request.Params.PerPage, "Status.All", "payment_id")
	payments := []generated.CustomerPayment{}
	for _, row := range window {
		links, err := s.paymentInvoices(ctx, row.ID)
		if err != nil {
			return nil, err
		}
		payments = append(payments, paymentAPI(row, links))
	}
	return generated.ListCustomerPayments200JSONResponse{Code: 0, Message: "success", CustomerPayments: payments, PageContext: page}, nil
}

func (s *server) GetCustomerPayment(ctx context.Context, request generated.GetCustomerPaymentRequestObject) (generated.GetCustomerPaymentResponseObject, error) {
	if err := s.requireOrg(ctx, request.Params.OrganizationId); err != nil {
		if body, status, ok := apiError(err); ok {
			return generated.GetCustomerPaymentdefaultJSONResponse{Body: body, StatusCode: status}, nil
		}
		return nil, err
	}
	row, err := s.loadPayment(ctx, request.PaymentId)
	if err != nil {
		if body, status, ok := apiError(err); ok {
			return generated.GetCustomerPaymentdefaultJSONResponse{Body: body, StatusCode: status}, nil
		}
		return nil, err
	}
	links, err := s.paymentInvoices(ctx, row.ID)
	if err != nil {
		return nil, err
	}
	return generated.GetCustomerPayment200JSONResponse{Code: 0, Message: "success", Payment: paymentAPI(row, links)}, nil
}

func (s *server) CreateCustomerPayment(ctx context.Context, request generated.CreateCustomerPaymentRequestObject) (generated.CreateCustomerPaymentResponseObject, error) {
	if err := s.requireOrg(ctx, request.Params.OrganizationId); err != nil {
		if body, status, ok := apiError(err); ok {
			return generated.CreateCustomerPaymentdefaultJSONResponse{Body: body, StatusCode: status}, nil
		}
		return nil, err
	}
	if request.Body == nil {
		return generated.CreateCustomerPaymentdefaultJSONResponse{Body: generated.Error{Code: 4, Message: "Request body is required."}, StatusCode: http.StatusBadRequest}, nil
	}
	fail := func(status, code int, message string) generated.CreateCustomerPaymentResponseObject {
		return generated.CreateCustomerPaymentdefaultJSONResponse{Body: generated.Error{Code: code, Message: message}, StatusCode: status}
	}
	body := request.Body
	parsedDate, err := parseDay(body.Date)
	if err != nil {
		return fail(http.StatusBadRequest, 4, "date must be yyyy-mm-dd."), nil
	}
	if len(body.Invoices) == 0 {
		return fail(http.StatusBadRequest, 4, "invoices is required."), nil
	}
	seen := map[string]struct{}{}
	var applied float64
	for _, link := range body.Invoices {
		if _, exists := seen[link.InvoiceId]; exists {
			return fail(http.StatusBadRequest, 4, "invoices repeats "+link.InvoiceId+"."), nil
		}
		seen[link.InvoiceId] = struct{}{}
		if link.AmountApplied <= 0 {
			return fail(http.StatusBadRequest, 4, "amount_applied must be greater than 0."), nil
		}
		applied += link.AmountApplied
	}
	if !sameMoney(applied, body.Amount) {
		return fail(http.StatusBadRequest, 4, "amount must equal the sum of amount_applied."), nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var customerCurrency string
	err = tx.QueryRowContext(ctx, "SELECT currency_code FROM contacts WHERE contact_id=?", body.CustomerId).Scan(&customerCurrency)
	if errors.Is(err, sql.ErrNoRows) {
		return fail(http.StatusNotFound, 1002, "Contact does not exist."), nil
	}
	if err != nil {
		return nil, err
	}
	currency := customerCurrency
	if body.CurrencyCode != nil {
		currency = string(*body.CurrencyCode)
	}
	if currency != customerCurrency {
		return fail(http.StatusBadRequest, 4, "currency_code does not match the customer."), nil
	}
	for _, link := range body.Invoices {
		var customerID, invoiceCurrency, status string
		var balance, total float64
		err := tx.QueryRowContext(ctx, "SELECT customer_id, currency_code, status, balance, total FROM invoices WHERE invoice_id=?", link.InvoiceId).
			Scan(&customerID, &invoiceCurrency, &status, &balance, &total)
		if errors.Is(err, sql.ErrNoRows) {
			return fail(http.StatusNotFound, 1002, "Invoice does not exist."), nil
		}
		if err != nil {
			return nil, err
		}
		if customerID != body.CustomerId {
			return fail(http.StatusBadRequest, 4, "Invoice does not belong to the customer."), nil
		}
		if invoiceCurrency != currency {
			return fail(http.StatusBadRequest, 4, "payment currency does not match the invoice."), nil
		}
		if status == "void" {
			return fail(http.StatusBadRequest, 4, "Invoice is void."), nil
		}
		nextBalance := money(balance - link.AmountApplied)
		if nextBalance < 0 {
			return fail(http.StatusBadRequest, 4, "amount_applied exceeds the invoice balance."), nil
		}
		nextStatus := status
		if sameMoney(nextBalance, 0) {
			nextStatus = "paid"
		} else if nextBalance < money(total) {
			nextStatus = "partially_paid"
		}
		if _, err := tx.ExecContext(ctx, "UPDATE invoices SET balance=?, status=? WHERE invoice_id=?", nextBalance, nextStatus, link.InvoiceId); err != nil {
			return nil, err
		}
	}
	id, err := s.ids.Next(ctx, "zohobooks.payment")
	if err != nil {
		return nil, fmt.Errorf("zohobooks: allocate payment ID: %w", err)
	}
	created := s.clock.Now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx, `INSERT INTO customer_payments
		(payment_id, customer_id, payment_mode, amount, date, currency_code, reference_number, description, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, body.CustomerId, string(body.PaymentMode), money(body.Amount), parsedDate.Format("2006-01-02"), currency,
		deref(body.ReferenceNumber), deref(body.Description), created); err != nil {
		return nil, err
	}
	for _, link := range body.Invoices {
		if _, err := tx.ExecContext(ctx, `INSERT INTO payment_invoices(payment_id, invoice_id, amount_applied) VALUES(?, ?, ?)`,
			id, link.InvoiceId, money(link.AmountApplied)); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	row, err := s.loadPayment(ctx, id)
	if err != nil {
		return nil, err
	}
	links, err := s.paymentInvoices(ctx, id)
	if err != nil {
		return nil, err
	}
	return generated.CreateCustomerPayment201JSONResponse{Code: 0, Message: "The payment has been created.", Payment: paymentAPI(row, links)}, nil
}

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

func paginate[T any](items []T, pagePtr, perPagePtr *int, filter, sortColumn string) ([]T, generated.PageContext) {
	page := 1
	if pagePtr != nil && *pagePtr > 0 {
		page = *pagePtr
	}
	perPage := 200
	if perPagePtr != nil && *perPagePtr > 0 {
		perPage = *perPagePtr
	}
	start := (page - 1) * perPage
	if start > len(items) {
		start = len(items)
	}
	end := start + perPage
	if end > len(items) {
		end = len(items)
	}
	return append([]T{}, items[start:end]...), generated.PageContext{
		Page: page, PerPage: perPage, HasMorePage: end < len(items),
		AppliedFilter: filter, SortColumn: sortColumn, SortOrder: "A",
	}
}

func currencySymbol(code string) string {
	switch code {
	case "INR":
		return "₹"
	case "USD":
		return "$"
	default:
		return code
	}
}

func filterLabel(status string) string {
	if status == "" {
		return "Status.All"
	}
	return "Status." + status
}

func likeContains(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + replacer.Replace(value) + "%"
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func strPtr(value string) *string { return &value }

func strPtrOmit(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func floatPtr(value float64) *float64 { return &value }

func writeError(w http.ResponseWriter, status, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(generated.Error{Code: code, Message: message})
}

package razorpay

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/razorpay/generated"
	"github.com/getkin/kin-openapi/openapi3filter"
	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"
)

const merchantUserID = "acc_acme"

type server struct {
	db      *sql.DB
	clock   httpresource.Clock
	ids     httpresource.IDGenerator
	handler http.Handler
}

type sqlDB interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

var _ generated.StrictServerInterface = (*server)(nil)

func newServer(ctx context.Context, dependencies httpresource.ServerDependencies) (*server, error) {
	if dependencies.DB == nil || dependencies.Clock == nil || dependencies.IDs == nil || dependencies.Secrets == nil {
		return nil, fmt.Errorf("razorpay: database, clock, ID generator, and secrets are required")
	}
	token, err := dependencies.Secrets.Get(ctx, "token")
	if err != nil {
		return nil, fmt.Errorf("razorpay: load synthetic token: %w", err)
	}
	if token == "" {
		return nil, fmt.Errorf("razorpay: synthetic token is empty")
	}
	spec, err := generated.GetSwagger()
	if err != nil {
		return nil, fmt.Errorf("razorpay: load OpenAPI: %w", err)
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
			description := "The request is invalid."
			if status == http.StatusUnauthorized {
				description = "Authentication failed"
			} else if err != nil {
				description = err.Error()
			}
			writeError(w, status, description)
		},
	})
	impl.handler = validator(generatedHandler)
	return impl, nil
}

func (s *server) Handler() http.Handler       { return s.handler }
func (s *server) Close(context.Context) error { return nil }

func (s *server) RazorpayOrdersList(ctx context.Context, request generated.RazorpayOrdersListRequestObject) (generated.RazorpayOrdersListResponseObject, error) {
	orders, err := loadOrders(ctx, s.db)
	if err != nil {
		return nil, err
	}
	if request.Params.Receipt != nil && *request.Params.Receipt != "" {
		filtered := make([]fixtureOrder, 0, 1)
		for _, order := range orders {
			if order.Receipt == *request.Params.Receipt {
				filtered = append(filtered, order)
			}
		}
		orders = filtered
	}
	sortOrders(orders)
	page := slicePage(orders, pageCount(request.Params.Count), pageSkip(request.Params.Skip))
	items := make([]generated.Order, 0, len(page))
	for _, order := range page {
		items = append(items, order.api())
	}
	return generated.RazorpayOrdersList200JSONResponse{
		Entity: generated.OrderCollectionEntityCollection, Count: len(items), Items: items,
	}, nil
}

func (s *server) RazorpayOrdersCreate(ctx context.Context, request generated.RazorpayOrdersCreateRequestObject) (generated.RazorpayOrdersCreateResponseObject, error) {
	if request.Body == nil {
		return ordersCreateError(http.StatusBadRequest, "The request body is required", ""), nil
	}
	body := request.Body
	if tooSmall(body.Amount, body.Currency) {
		return ordersCreateError(http.StatusBadRequest, minimumAmountMessage(body.Currency), "amount"), nil
	}
	notes := notesFrom(body.Notes)
	if message := validateNotes(notes); message != "" {
		return ordersCreateError(http.StatusBadRequest, message, "notes"), nil
	}
	receipt := stringValue(body.Receipt)
	orders, err := loadOrders(ctx, s.db)
	if err != nil {
		return nil, err
	}
	if receipt != "" {
		for _, order := range orders {
			if order.Receipt == receipt {
				return ordersCreateError(http.StatusBadRequest, "Duplicate request. This request has already been processed.", "receipt"), nil
			}
		}
	}
	id, err := s.nextID(ctx, "razorpay.order", "order_")
	if err != nil {
		return nil, err
	}
	order := fixtureOrder{
		ID: id, Amount: int64(body.Amount), AmountPaid: 0, AmountDue: int64(body.Amount),
		Currency: body.Currency, Receipt: receipt, Status: "created", Attempts: 0,
		Notes: notes, CreatedAt: s.clock.Now().Unix(),
	}
	raw, err := json.Marshal(order)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO orders(id, body) VALUES(?, ?)`, order.ID, string(raw)); err != nil {
		return nil, err
	}
	return generated.RazorpayOrdersCreate200JSONResponse(order.api()), nil
}

func (s *server) RazorpayOrdersFetch(ctx context.Context, request generated.RazorpayOrdersFetchRequestObject) (generated.RazorpayOrdersFetchResponseObject, error) {
	order, err := getOrder(ctx, s.db, request.Id)
	if errors.Is(err, sql.ErrNoRows) {
		return orderError(http.StatusBadRequest, "The id provided does not exist", "id"), nil
	}
	if err != nil {
		return nil, err
	}
	return generated.RazorpayOrdersFetch200JSONResponse(order.api()), nil
}

func (s *server) RazorpayOrdersFetchPayments(ctx context.Context, request generated.RazorpayOrdersFetchPaymentsRequestObject) (generated.RazorpayOrdersFetchPaymentsResponseObject, error) {
	if _, err := getOrder(ctx, s.db, request.Id); errors.Is(err, sql.ErrNoRows) {
		return paymentError(http.StatusBadRequest, "The id provided does not exist", "id"), nil
	} else if err != nil {
		return nil, err
	}
	payments, err := loadPayments(ctx, s.db, request.Id)
	if err != nil {
		return nil, err
	}
	sortPayments(payments)
	page := slicePage(payments, pageCount(request.Params.Count), pageSkip(request.Params.Skip))
	return generated.RazorpayOrdersFetchPayments200JSONResponse(paymentCollection(page)), nil
}

func (s *server) RazorpayPaymentsList(ctx context.Context, request generated.RazorpayPaymentsListRequestObject) (generated.RazorpayPaymentsListResponseObject, error) {
	payments, err := loadPayments(ctx, s.db, "")
	if err != nil {
		return nil, err
	}
	sortPayments(payments)
	page := slicePage(payments, pageCount(request.Params.Count), pageSkip(request.Params.Skip))
	return generated.RazorpayPaymentsList200JSONResponse(paymentCollection(page)), nil
}

func (s *server) RazorpayPaymentsFetch(ctx context.Context, request generated.RazorpayPaymentsFetchRequestObject) (generated.RazorpayPaymentsFetchResponseObject, error) {
	payment, err := getPayment(ctx, s.db, request.Id)
	if errors.Is(err, sql.ErrNoRows) {
		return paymentFetchError(http.StatusBadRequest, "The id provided does not exist", "id"), nil
	}
	if err != nil {
		return nil, err
	}
	return generated.RazorpayPaymentsFetch200JSONResponse(payment.api()), nil
}

func (s *server) RazorpayPaymentsRefund(ctx context.Context, request generated.RazorpayPaymentsRefundRequestObject) (generated.RazorpayPaymentsRefundResponseObject, error) {
	var requested *generated.CreateRefundRequest
	if request.Body != nil {
		body := generated.CreateRefundRequest(*request.Body)
		requested = &body
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	payment, err := getPayment(ctx, tx, request.Id)
	if errors.Is(err, sql.ErrNoRows) {
		return refundError(http.StatusBadRequest, "The id provided does not exist", "id"), nil
	}
	if err != nil {
		return nil, err
	}
	if payment.Status != "captured" || payment.AmountRefunded >= payment.Amount {
		if payment.Status == "refunded" || payment.AmountRefunded >= payment.Amount {
			return refundError(http.StatusBadRequest, "The payment has been fully refunded already.", "payment_id"), nil
		}
		return refundError(http.StatusBadRequest, "The payment status should be captured for action to be taken.", "payment_id"), nil
	}
	remaining := payment.Amount - payment.AmountRefunded
	amount := remaining
	if requested != nil && requested.Amount != nil {
		if *requested.Amount <= 0 {
			return refundError(http.StatusBadRequest, "Amount cannot be blank.", "amount"), nil
		}
		amount = int64(*requested.Amount)
	}
	if tooSmall(int(amount), payment.Currency) {
		return refundError(http.StatusBadRequest, minimumAmountMessage(payment.Currency), "amount"), nil
	}
	if amount > remaining {
		return refundError(http.StatusBadRequest, "The refund amount provided is greater than amount captured.", "amount"), nil
	}
	notes := map[string]string{}
	receipt := ""
	speed := "normal"
	if requested != nil {
		notes = notesFrom(requested.Notes)
		receipt = stringValue(requested.Receipt)
		if requested.Speed != nil {
			speed = string(*requested.Speed)
		}
	}
	if message := validateNotes(notes); message != "" {
		return refundError(http.StatusBadRequest, message, "notes"), nil
	}
	if receipt != "" {
		existing, err := loadRefunds(ctx, tx, payment.ID)
		if err != nil {
			return nil, err
		}
		for _, refund := range existing {
			if refund.Receipt == receipt {
				return refundError(http.StatusBadRequest, "Duplicate receipt found for this refund request.", "receipt"), nil
			}
		}
	}
	id, err := s.nextID(ctx, "razorpay.refund", "rfnd_")
	if err != nil {
		return nil, err
	}
	refund := fixtureRefund{
		ID: id, PaymentID: payment.ID, Amount: amount, Currency: payment.Currency,
		Status: "processed", SpeedRequested: speed, SpeedProcessed: "normal",
		Receipt: receipt, Notes: notes, CreatedAt: s.clock.Now().Unix(),
	}
	payment.AmountRefunded += amount
	if payment.AmountRefunded == payment.Amount {
		payment.Status = "refunded"
		payment.RefundStatus = "full"
	} else {
		payment.RefundStatus = "partial"
	}
	paymentRaw, err := json.Marshal(payment)
	if err != nil {
		return nil, err
	}
	refundRaw, err := json.Marshal(refund)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE payments SET body=? WHERE id=?`, string(paymentRaw), payment.ID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO refunds(id, payment_id, body) VALUES(?, ?, ?)`, refund.ID, refund.PaymentID, string(refundRaw)); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return generated.RazorpayPaymentsRefund200JSONResponse(refund.api()), nil
}

func (s *server) RazorpayPaymentsFetchRefunds(ctx context.Context, request generated.RazorpayPaymentsFetchRefundsRequestObject) (generated.RazorpayPaymentsFetchRefundsResponseObject, error) {
	if _, err := getPayment(ctx, s.db, request.Id); errors.Is(err, sql.ErrNoRows) {
		return refundListError(http.StatusBadRequest, "The id provided does not exist", "id"), nil
	} else if err != nil {
		return nil, err
	}
	refunds, err := loadRefunds(ctx, s.db, request.Id)
	if err != nil {
		return nil, err
	}
	sortRefunds(refunds)
	page := slicePage(refunds, pageCount(request.Params.Count), pageSkip(request.Params.Skip))
	return generated.RazorpayPaymentsFetchRefunds200JSONResponse(refundCollection(page)), nil
}

func (s *server) RazorpayRefundsList(ctx context.Context, request generated.RazorpayRefundsListRequestObject) (generated.RazorpayRefundsListResponseObject, error) {
	refunds, err := loadRefunds(ctx, s.db, "")
	if err != nil {
		return nil, err
	}
	sortRefunds(refunds)
	page := slicePage(refunds, pageCount(request.Params.Count), pageSkip(request.Params.Skip))
	return generated.RazorpayRefundsList200JSONResponse(refundCollection(page)), nil
}

func (s *server) RazorpayRefundsFetch(ctx context.Context, request generated.RazorpayRefundsFetchRequestObject) (generated.RazorpayRefundsFetchResponseObject, error) {
	refund, err := getRefund(ctx, s.db, request.Id)
	if errors.Is(err, sql.ErrNoRows) {
		return refundFetchError(http.StatusBadRequest, "The id provided does not exist", "id"), nil
	}
	if err != nil {
		return nil, err
	}
	return generated.RazorpayRefundsFetch200JSONResponse(refund.api()), nil
}

func (s *server) RazorpayPaymentLinksList(ctx context.Context, request generated.RazorpayPaymentLinksListRequestObject) (generated.RazorpayPaymentLinksListResponseObject, error) {
	links, err := loadPaymentLinks(ctx, s.db)
	if err != nil {
		return nil, err
	}
	sortPaymentLinks(links)
	page := slicePage(links, pageCount(request.Params.Count), pageSkip(request.Params.Skip))
	return generated.RazorpayPaymentLinksList200JSONResponse(paymentLinkCollection(page)), nil
}

func (s *server) RazorpayPaymentLinksCreate(ctx context.Context, request generated.RazorpayPaymentLinksCreateRequestObject) (generated.RazorpayPaymentLinksCreateResponseObject, error) {
	if request.Body == nil {
		return paymentLinkError(http.StatusBadRequest, "The request body is required", ""), nil
	}
	body := request.Body
	if tooSmall(body.Amount, body.Currency) {
		return paymentLinkError(http.StatusBadRequest, minimumAmountMessage(body.Currency), "amount"), nil
	}
	notes := notesFrom(body.Notes)
	if message := validateNotes(notes); message != "" {
		return paymentLinkError(http.StatusBadRequest, message, "notes"), nil
	}
	id, err := s.nextID(ctx, "razorpay.payment_link", "plink_")
	if err != nil {
		return nil, err
	}
	now := s.clock.Now().Unix()
	link := fixturePaymentLink{
		ID: id, Amount: int64(body.Amount), AmountPaid: 0, Currency: body.Currency,
		AcceptPartial: boolValue(body.AcceptPartial), Description: stringValue(body.Description),
		ReferenceID: stringValue(body.ReferenceId), Notes: notes,
		NotifyEmail: false, NotifySMS: false, ReminderEnable: boolValue(body.ReminderEnable),
		Status: "created", ShortURL: "https://rzp.io/i/" + id, ExpireBy: int64(intValue(body.ExpireBy)),
		CreatedAt: now, UpdatedAt: now, UserID: merchantUserID,
	}
	if body.Customer != nil {
		link.CustomerName = body.Customer.Name
		link.CustomerEmail = body.Customer.Email
		link.CustomerContact = body.Customer.Contact
	}
	if body.Notify != nil {
		link.NotifyEmail = body.Notify.Email
		link.NotifySMS = body.Notify.Sms
	}
	raw, err := json.Marshal(link)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO payment_links(id, body) VALUES(?, ?)`, link.ID, string(raw)); err != nil {
		return nil, err
	}
	return generated.RazorpayPaymentLinksCreate200JSONResponse(link.api()), nil
}

func (s *server) RazorpayPaymentLinksFetch(ctx context.Context, request generated.RazorpayPaymentLinksFetchRequestObject) (generated.RazorpayPaymentLinksFetchResponseObject, error) {
	link, err := getPaymentLink(ctx, s.db, request.Id)
	if errors.Is(err, sql.ErrNoRows) {
		return paymentLinkFetchError(http.StatusBadRequest, "The id provided does not exist", "id"), nil
	}
	if err != nil {
		return nil, err
	}
	return generated.RazorpayPaymentLinksFetch200JSONResponse(link.api()), nil
}

func (s *server) RazorpayPaymentLinksCancel(ctx context.Context, request generated.RazorpayPaymentLinksCancelRequestObject) (generated.RazorpayPaymentLinksCancelResponseObject, error) {
	link, err := getPaymentLink(ctx, s.db, request.Id)
	if errors.Is(err, sql.ErrNoRows) {
		return paymentLinkCancelError(http.StatusBadRequest, "The id provided does not exist", "id"), nil
	}
	if err != nil {
		return nil, err
	}
	if link.Status == "cancelled" {
		return paymentLinkCancelError(http.StatusBadRequest, "Payment link is already cancelled.", "id"), nil
	}
	if link.Status != "created" {
		return paymentLinkCancelError(http.StatusBadRequest, "Payment link cannot be cancelled in its current state.", "id"), nil
	}
	now := s.clock.Now().Unix()
	link.Status = "cancelled"
	link.CancelledAt = now
	link.UpdatedAt = now
	raw, err := json.Marshal(link)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE payment_links SET body=? WHERE id=?`, string(raw), link.ID); err != nil {
		return nil, err
	}
	return generated.RazorpayPaymentLinksCancel200JSONResponse(link.api()), nil
}

func (s *server) nextID(ctx context.Context, kind, prefix string) (string, error) {
	raw, err := s.ids.Next(ctx, kind)
	if err != nil {
		return "", fmt.Errorf("razorpay: allocate %s ID: %w", kind, err)
	}
	return prefix + raw, nil
}

func (order fixtureOrder) api() generated.Order {
	return generated.Order{
		Id: order.ID, Entity: generated.OrderEntityOrder, Amount: int(order.Amount),
		AmountPaid: int(order.AmountPaid), AmountDue: int(order.AmountDue), Currency: order.Currency,
		Receipt: order.Receipt, Status: order.Status, Attempts: order.Attempts,
		Notes: generated.Notes(notesOrEmpty(order.Notes)), CreatedAt: int(order.CreatedAt),
	}
}

func (payment fixturePayment) api() generated.Payment {
	out := generated.Payment{
		Id: payment.ID, Entity: generated.PaymentEntityPayment, Amount: int(payment.Amount),
		Currency: payment.Currency, Status: payment.Status, OrderId: payment.OrderID,
		International: payment.International, Method: payment.Method, AmountRefunded: int(payment.AmountRefunded),
		Captured: payment.Captured, Description: payment.Description, Email: payment.Email, Contact: payment.Contact,
		Notes: generated.Notes(notesOrEmpty(payment.Notes)), Fee: int(payment.Fee), Tax: int(payment.Tax),
		AcquirerData: map[string]string{}, CreatedAt: int(payment.CreatedAt),
	}
	if payment.RefundStatus != "" {
		status := payment.RefundStatus
		out.RefundStatus = &status
	}
	return out
}

func (refund fixtureRefund) api() generated.Refund {
	return generated.Refund{
		Id: refund.ID, Entity: generated.RefundEntityRefund, Amount: int(refund.Amount),
		Currency: refund.Currency, PaymentId: refund.PaymentID, Notes: generated.Notes(notesOrEmpty(refund.Notes)),
		Receipt: refund.Receipt, AcquirerData: map[string]string{}, CreatedAt: int(refund.CreatedAt),
		Status: refund.Status, SpeedProcessed: refund.SpeedProcessed, SpeedRequested: refund.SpeedRequested,
	}
}

func (link fixturePaymentLink) api() generated.PaymentLink {
	return generated.PaymentLink{
		Id: link.ID, Entity: generated.PaymentLinkEntityPaymentLink, Amount: int(link.Amount),
		AmountPaid: int(link.AmountPaid), Currency: link.Currency, AcceptPartial: link.AcceptPartial,
		Description: link.Description, ReferenceId: link.ReferenceID,
		Customer:       generated.Customer{Name: link.CustomerName, Email: link.CustomerEmail, Contact: link.CustomerContact},
		Notes:          generated.Notes(notesOrEmpty(link.Notes)),
		Notify:         generated.Notify{Email: link.NotifyEmail, Sms: link.NotifySMS},
		ReminderEnable: link.ReminderEnable, ShortUrl: link.ShortURL, Status: link.Status,
		ExpireBy: int(link.ExpireBy), ExpiredAt: int(link.ExpiredAt), CancelledAt: int(link.CancelledAt),
		CreatedAt: int(link.CreatedAt), UpdatedAt: int(link.UpdatedAt), UserId: link.UserID,
	}
}

func paymentCollection(payments []fixturePayment) generated.PaymentCollection {
	items := make([]generated.Payment, 0, len(payments))
	for _, payment := range payments {
		items = append(items, payment.api())
	}
	return generated.PaymentCollection{Entity: generated.PaymentCollectionEntityCollection, Count: len(items), Items: items}
}

func refundCollection(refunds []fixtureRefund) generated.RefundCollection {
	items := make([]generated.Refund, 0, len(refunds))
	for _, refund := range refunds {
		items = append(items, refund.api())
	}
	return generated.RefundCollection{Entity: generated.RefundCollectionEntityCollection, Count: len(items), Items: items}
}

func paymentLinkCollection(links []fixturePaymentLink) generated.PaymentLinkCollection {
	items := make([]generated.PaymentLink, 0, len(links))
	for _, link := range links {
		items = append(items, link.api())
	}
	return generated.PaymentLinkCollection{Entity: generated.PaymentLinkCollectionEntityCollection, Count: len(items), Items: items}
}

func loadOrders(ctx context.Context, db sqlDB) ([]fixtureOrder, error) {
	return loadBodies[fixtureOrder](ctx, db, `SELECT id, body FROM orders`)
}

func getOrder(ctx context.Context, db sqlDB, id string) (fixtureOrder, error) {
	return getBody[fixtureOrder](ctx, db, `SELECT body FROM orders WHERE id=?`, id)
}

func loadPayments(ctx context.Context, db sqlDB, orderID string) ([]fixturePayment, error) {
	if orderID == "" {
		return loadBodies[fixturePayment](ctx, db, `SELECT id, body FROM payments`)
	}
	return loadBodies[fixturePayment](ctx, db, `SELECT id, body FROM payments WHERE order_id=?`, orderID)
}

func getPayment(ctx context.Context, db sqlDB, id string) (fixturePayment, error) {
	return getBody[fixturePayment](ctx, db, `SELECT body FROM payments WHERE id=?`, id)
}

func loadRefunds(ctx context.Context, db sqlDB, paymentID string) ([]fixtureRefund, error) {
	if paymentID == "" {
		return loadBodies[fixtureRefund](ctx, db, `SELECT id, body FROM refunds`)
	}
	return loadBodies[fixtureRefund](ctx, db, `SELECT id, body FROM refunds WHERE payment_id=?`, paymentID)
}

func getRefund(ctx context.Context, db sqlDB, id string) (fixtureRefund, error) {
	return getBody[fixtureRefund](ctx, db, `SELECT body FROM refunds WHERE id=?`, id)
}

func loadPaymentLinks(ctx context.Context, db sqlDB) ([]fixturePaymentLink, error) {
	return loadBodies[fixturePaymentLink](ctx, db, `SELECT id, body FROM payment_links`)
}

func getPaymentLink(ctx context.Context, db sqlDB, id string) (fixturePaymentLink, error) {
	return getBody[fixturePaymentLink](ctx, db, `SELECT body FROM payment_links WHERE id=?`, id)
}

func loadBodies[T any](ctx context.Context, db sqlDB, query string, args ...any) ([]T, error) {
	rows, err := db.QueryContext(ctx, query, args...)
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
			return nil, fmt.Errorf("razorpay: decode %s: %w", id, err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func getBody[T any](ctx context.Context, db sqlDB, query, id string) (T, error) {
	var zero T
	var raw string
	if err := db.QueryRowContext(ctx, query, id).Scan(&raw); err != nil {
		return zero, err
	}
	var item T
	if err := json.Unmarshal([]byte(raw), &item); err != nil {
		return zero, fmt.Errorf("razorpay: decode %s: %w", id, err)
	}
	return item, nil
}

func sortOrders(orders []fixtureOrder) {
	sort.Slice(orders, func(i, j int) bool {
		if orders[i].CreatedAt != orders[j].CreatedAt {
			return orders[i].CreatedAt > orders[j].CreatedAt
		}
		return orders[i].ID > orders[j].ID
	})
}

func sortPayments(payments []fixturePayment) {
	sort.Slice(payments, func(i, j int) bool {
		if payments[i].CreatedAt != payments[j].CreatedAt {
			return payments[i].CreatedAt > payments[j].CreatedAt
		}
		return payments[i].ID > payments[j].ID
	})
}

func sortRefunds(refunds []fixtureRefund) {
	sort.Slice(refunds, func(i, j int) bool {
		if refunds[i].CreatedAt != refunds[j].CreatedAt {
			return refunds[i].CreatedAt > refunds[j].CreatedAt
		}
		return refunds[i].ID > refunds[j].ID
	})
}

func sortPaymentLinks(links []fixturePaymentLink) {
	sort.Slice(links, func(i, j int) bool {
		if links[i].CreatedAt != links[j].CreatedAt {
			return links[i].CreatedAt > links[j].CreatedAt
		}
		return links[i].ID > links[j].ID
	})
}

func ordersCreateError(status int, description, field string) generated.RazorpayOrdersCreatedefaultJSONResponse {
	return generated.RazorpayOrdersCreatedefaultJSONResponse{Body: errorEnvelope(description, field), StatusCode: status}
}

func orderError(status int, description, field string) generated.RazorpayOrdersFetchdefaultJSONResponse {
	return generated.RazorpayOrdersFetchdefaultJSONResponse{Body: errorEnvelope(description, field), StatusCode: status}
}

func paymentError(status int, description, field string) generated.RazorpayOrdersFetchPaymentsdefaultJSONResponse {
	return generated.RazorpayOrdersFetchPaymentsdefaultJSONResponse{Body: errorEnvelope(description, field), StatusCode: status}
}

func paymentFetchError(status int, description, field string) generated.RazorpayPaymentsFetchdefaultJSONResponse {
	return generated.RazorpayPaymentsFetchdefaultJSONResponse{Body: errorEnvelope(description, field), StatusCode: status}
}

func refundError(status int, description, field string) generated.RazorpayPaymentsRefunddefaultJSONResponse {
	return generated.RazorpayPaymentsRefunddefaultJSONResponse{Body: errorEnvelope(description, field), StatusCode: status}
}

func refundListError(status int, description, field string) generated.RazorpayPaymentsFetchRefundsdefaultJSONResponse {
	return generated.RazorpayPaymentsFetchRefundsdefaultJSONResponse{Body: errorEnvelope(description, field), StatusCode: status}
}

func refundFetchError(status int, description, field string) generated.RazorpayRefundsFetchdefaultJSONResponse {
	return generated.RazorpayRefundsFetchdefaultJSONResponse{Body: errorEnvelope(description, field), StatusCode: status}
}

func paymentLinkError(status int, description, field string) generated.RazorpayPaymentLinksCreatedefaultJSONResponse {
	return generated.RazorpayPaymentLinksCreatedefaultJSONResponse{Body: errorEnvelope(description, field), StatusCode: status}
}

func paymentLinkFetchError(status int, description, field string) generated.RazorpayPaymentLinksFetchdefaultJSONResponse {
	return generated.RazorpayPaymentLinksFetchdefaultJSONResponse{Body: errorEnvelope(description, field), StatusCode: status}
}

func paymentLinkCancelError(status int, description, field string) generated.RazorpayPaymentLinksCanceldefaultJSONResponse {
	return generated.RazorpayPaymentLinksCanceldefaultJSONResponse{Body: errorEnvelope(description, field), StatusCode: status}
}

func errorEnvelope(description, field string) generated.ErrorEnvelope {
	body := generated.ErrorBody{
		Code: "BAD_REQUEST_ERROR", Description: description, Source: "business",
		Step: "payment_initiation", Reason: "input_validation_failed", Metadata: map[string]string{},
	}
	if field != "" {
		body.Field = &field
	}
	return generated.ErrorEnvelope{Error: body}
}

func writeError(w http.ResponseWriter, status int, description string) {
	source, step, reason := "business", "payment_initiation", "input_validation_failed"
	if status == http.StatusUnauthorized {
		source, step, reason = "NA", "NA", "NA"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(generated.ErrorEnvelope{Error: generated.ErrorBody{
		Code: "BAD_REQUEST_ERROR", Description: description, Source: source, Step: step, Reason: reason,
		Metadata: map[string]string{},
	}})
}

func notesFrom(notes *generated.Notes) map[string]string {
	if notes == nil {
		return map[string]string{}
	}
	return notesOrEmpty(*notes)
}

func validateNotes(notes map[string]string) string {
	if len(notes) > 15 {
		return "Notes validation failed."
	}
	for key, value := range notes {
		if key == "" || len(key) > 256 || len(value) > 512 {
			return "Notes validation failed."
		}
	}
	return ""
}

func tooSmall(amount int, currency string) bool {
	if currency == "INR" {
		return amount < 100
	}
	return amount < 1
}

func minimumAmountMessage(currency string) string {
	if currency == "INR" {
		return "The amount must be at least INR 1.00"
	}
	return "The amount must be at least 1.00"
}

func pageCount(count *int) int {
	if count == nil {
		return 10
	}
	return *count
}

func pageSkip(skip *int) int {
	if skip == nil {
		return 0
	}
	return *skip
}

func slicePage[T any](items []T, count, skip int) []T {
	if skip >= len(items) || count <= 0 {
		return []T{}
	}
	end := skip + count
	if end > len(items) {
		end = len(items)
	}
	return append([]T{}, items[skip:end]...)
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func intValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func boolValue(value *bool) bool { return value != nil && *value }

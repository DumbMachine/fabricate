package chargebee

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/chargebee/generated"
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
		return nil, fmt.Errorf("chargebee: database, clock, ID generator, and secrets are required")
	}
	token, err := dependencies.Secrets.Get(ctx, "token")
	if err != nil {
		return nil, fmt.Errorf("chargebee: load synthetic token: %w", err)
	}
	if token == "" {
		return nil, fmt.Errorf("chargebee: synthetic token is empty")
	}
	spec, err := generated.GetSwagger()
	if err != nil {
		return nil, fmt.Errorf("chargebee: load OpenAPI: %w", err)
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
			writeError(w, status, err.Error())
		},
	})
	impl.handler = validator(generatedHandler)
	return impl, nil
}

func (s *server) Handler() http.Handler       { return s.handler }
func (s *server) Close(context.Context) error { return nil }

func (s *server) ListCustomers(ctx context.Context, request generated.ListCustomersRequestObject) (generated.ListCustomersResponseObject, error) {
	customers, err := queryCustomers(ctx, s.db, "SELECT "+customerColumns+" FROM customers ORDER BY created_at DESC, id")
	if err != nil {
		return nil, err
	}
	filtered := make([]fixtureCustomer, 0, len(customers))
	for _, customer := range customers {
		if !includeRecord(request.Params.IncludeDeleted, customer.Deleted) {
			continue
		}
		if request.Params.IdIs != nil && customer.ID != string(*request.Params.IdIs) {
			continue
		}
		if request.Params.EmailIs != nil && customer.Email != string(*request.Params.EmailIs) {
			continue
		}
		filtered = append(filtered, customer)
	}
	page, next, err := paginate(filtered, request.Params.Limit, request.Params.Offset)
	if err != nil {
		return generated.ListCustomersdefaultJSONResponse{Body: apiError(http.StatusBadRequest, "invalid_request", err.Error()), StatusCode: http.StatusBadRequest}, nil
	}
	list := make([]generated.CustomerListEntry, 0, len(page))
	for _, customer := range page {
		list = append(list, generated.CustomerListEntry{Customer: customerWire(customer)})
	}
	return generated.ListCustomers200JSONResponse{List: list, NextOffset: next}, nil
}

func (s *server) RetrieveCustomer(ctx context.Context, request generated.RetrieveCustomerRequestObject) (generated.RetrieveCustomerResponseObject, error) {
	customer, err := getCustomer(ctx, s.db, request.CustomerId)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.RetrieveCustomerdefaultJSONResponse{Body: notFound(), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.RetrieveCustomer200JSONResponse{Customer: customerWire(customer)}, nil
}

func (s *server) ListSubscriptions(ctx context.Context, request generated.ListSubscriptionsRequestObject) (generated.ListSubscriptionsResponseObject, error) {
	subscriptions, err := querySubscriptions(ctx, s.db, "SELECT "+subscriptionColumns+" FROM subscriptions ORDER BY created_at DESC, id")
	if err != nil {
		return nil, err
	}
	filtered := make([]fixtureSubscription, 0, len(subscriptions))
	for _, subscription := range subscriptions {
		if !includeRecord(request.Params.IncludeDeleted, subscription.Deleted) {
			continue
		}
		if request.Params.IdIs != nil && subscription.ID != string(*request.Params.IdIs) {
			continue
		}
		if request.Params.CustomerIdIs != nil && subscription.CustomerID != string(*request.Params.CustomerIdIs) {
			continue
		}
		if request.Params.StatusIs != nil && subscription.Status != string(*request.Params.StatusIs) {
			continue
		}
		filtered = append(filtered, subscription)
	}
	page, next, err := paginate(filtered, request.Params.Limit, request.Params.Offset)
	if err != nil {
		return generated.ListSubscriptionsdefaultJSONResponse{Body: apiError(http.StatusBadRequest, "invalid_request", err.Error()), StatusCode: http.StatusBadRequest}, nil
	}
	list := make([]generated.SubscriptionListEntry, 0, len(page))
	for _, subscription := range page {
		customer, err := getCustomer(ctx, s.db, subscription.CustomerID)
		if err != nil {
			return nil, err
		}
		list = append(list, generated.SubscriptionListEntry{Subscription: subscriptionWire(subscription), Customer: customerWire(customer)})
	}
	return generated.ListSubscriptions200JSONResponse{List: list, NextOffset: next}, nil
}

func (s *server) RetrieveSubscription(ctx context.Context, request generated.RetrieveSubscriptionRequestObject) (generated.RetrieveSubscriptionResponseObject, error) {
	envelope, err := s.subscriptionEnvelope(ctx, request.SubscriptionId)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.RetrieveSubscriptiondefaultJSONResponse{Body: notFound(), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.RetrieveSubscription200JSONResponse(envelope), nil
}

func (s *server) CancelSubscriptionForItems(ctx context.Context, request generated.CancelSubscriptionForItemsRequestObject) (generated.CancelSubscriptionForItemsResponseObject, error) {
	subscription, err := getSubscription(ctx, s.db, request.SubscriptionId)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.CancelSubscriptionForItemsdefaultJSONResponse{Body: notFound(), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	if subscription.Decommissioned {
		return generated.CancelSubscriptionForItemsdefaultJSONResponse{Body: apiError(http.StatusBadRequest, "invalid_request", "subscription is decommissioned"), StatusCode: http.StatusBadRequest}, nil
	}
	if subscription.Status == string(generated.Cancelled) {
		return generated.CancelSubscriptionForItemsdefaultJSONResponse{Body: apiError(http.StatusBadRequest, "invalid_request", "subscription is already cancelled"), StatusCode: http.StatusBadRequest}, nil
	}
	option := generated.Immediately
	var reason string
	var cancelAt *int64
	if request.Body != nil {
		if request.Body.CancelOption != nil {
			option = *request.Body.CancelOption
		} else if request.Body.EndOfTerm != nil && *request.Body.EndOfTerm {
			option = generated.EndOfTerm
		}
		if request.Body.CancelReasonCode != nil {
			reason = *request.Body.CancelReasonCode
		}
		cancelAt = request.Body.CancelAt
	}
	now := s.clock.Now().UTC().Unix()
	zero := int64(0)
	switch option {
	case generated.EndOfTerm:
		subscription.Status = string(generated.NonRenewing)
		termEnd := subscription.CurrentTermEnd
		subscription.CancelledAt = &termEnd
		subscription.NextBillingAt = nil
		subscription.RemainingBillingCycles = &zero
	case generated.SpecificDate:
		if cancelAt == nil {
			return generated.CancelSubscriptionForItemsdefaultJSONResponse{Body: apiError(http.StatusBadRequest, "invalid_request", "cancel_at is required when cancel_option is specific_date"), StatusCode: http.StatusBadRequest}, nil
		}
		subscription.CancelledAt = cancelAt
		subscription.NextBillingAt = nil
		subscription.RemainingBillingCycles = &zero
		if *cancelAt <= now {
			subscription.Status = string(generated.Cancelled)
			subscription.Mrr = 0
		} else {
			subscription.Status = string(generated.NonRenewing)
		}
	default:
		subscription.Status = string(generated.Cancelled)
		subscription.CancelledAt = &now
		subscription.NextBillingAt = nil
		subscription.RemainingBillingCycles = &zero
		subscription.Mrr = 0
	}
	if reason != "" {
		subscription.CancelReasonCode = reason
	}
	for i := range subscription.SubscriptionItems {
		subscription.SubscriptionItems[i].BillingCycles = 0
	}
	subscription.UpdatedAt = now
	subscription.ResourceVersion = s.clock.Now().UTC().UnixMilli()
	if err := updateSubscription(ctx, s.db, subscription); err != nil {
		return nil, err
	}
	envelope, err := s.subscriptionEnvelope(ctx, subscription.ID)
	if err != nil {
		return nil, err
	}
	return generated.CancelSubscriptionForItems200JSONResponse(envelope), nil
}

func (s *server) ListInvoices(ctx context.Context, request generated.ListInvoicesRequestObject) (generated.ListInvoicesResponseObject, error) {
	invoices, err := queryInvoices(ctx, s.db, "SELECT "+invoiceColumns+" FROM invoices ORDER BY date DESC, id")
	if err != nil {
		return nil, err
	}
	filtered := make([]fixtureInvoice, 0, len(invoices))
	for _, invoice := range invoices {
		if !includeRecord(request.Params.IncludeDeleted, invoice.Deleted) {
			continue
		}
		if request.Params.IdIs != nil && invoice.ID != string(*request.Params.IdIs) {
			continue
		}
		if request.Params.CustomerIdIs != nil && invoice.CustomerID != string(*request.Params.CustomerIdIs) {
			continue
		}
		if request.Params.SubscriptionIdIs != nil && invoice.SubscriptionID != string(*request.Params.SubscriptionIdIs) {
			continue
		}
		if request.Params.StatusIs != nil && invoice.Status != string(*request.Params.StatusIs) {
			continue
		}
		filtered = append(filtered, invoice)
	}
	page, next, err := paginate(filtered, request.Params.Limit, request.Params.Offset)
	if err != nil {
		return generated.ListInvoicesdefaultJSONResponse{Body: apiError(http.StatusBadRequest, "invalid_request", err.Error()), StatusCode: http.StatusBadRequest}, nil
	}
	list := make([]generated.InvoiceListEntry, 0, len(page))
	for _, invoice := range page {
		list = append(list, generated.InvoiceListEntry{Invoice: invoiceWire(invoice)})
	}
	return generated.ListInvoices200JSONResponse{List: list, NextOffset: next}, nil
}

func (s *server) RetrieveInvoice(ctx context.Context, request generated.RetrieveInvoiceRequestObject) (generated.RetrieveInvoiceResponseObject, error) {
	invoice, err := getInvoice(ctx, s.db, request.InvoiceId)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.RetrieveInvoicedefaultJSONResponse{Body: notFound(), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.RetrieveInvoice200JSONResponse{Invoice: invoiceWire(invoice)}, nil
}

func (s *server) ListTransactions(ctx context.Context, request generated.ListTransactionsRequestObject) (generated.ListTransactionsResponseObject, error) {
	transactions, err := queryTransactions(ctx, s.db, "SELECT "+transactionColumns+" FROM transactions ORDER BY date DESC, id")
	if err != nil {
		return nil, err
	}
	filtered := make([]fixtureTransaction, 0, len(transactions))
	for _, txn := range transactions {
		if !includeRecord(request.Params.IncludeDeleted, txn.Deleted) {
			continue
		}
		if request.Params.IdIs != nil && txn.ID != string(*request.Params.IdIs) {
			continue
		}
		if request.Params.CustomerIdIs != nil && txn.CustomerID != string(*request.Params.CustomerIdIs) {
			continue
		}
		if request.Params.SubscriptionIdIs != nil && txn.SubscriptionID != string(*request.Params.SubscriptionIdIs) {
			continue
		}
		if request.Params.StatusIs != nil && txn.Status != string(*request.Params.StatusIs) {
			continue
		}
		filtered = append(filtered, txn)
	}
	page, next, err := paginate(filtered, request.Params.Limit, request.Params.Offset)
	if err != nil {
		return generated.ListTransactionsdefaultJSONResponse{Body: apiError(http.StatusBadRequest, "invalid_request", err.Error()), StatusCode: http.StatusBadRequest}, nil
	}
	list := make([]generated.TransactionListEntry, 0, len(page))
	for _, txn := range page {
		list = append(list, generated.TransactionListEntry{Transaction: transactionWire(txn)})
	}
	return generated.ListTransactions200JSONResponse{List: list, NextOffset: next}, nil
}

func (s *server) RetrieveTransaction(ctx context.Context, request generated.RetrieveTransactionRequestObject) (generated.RetrieveTransactionResponseObject, error) {
	txn, err := getTransaction(ctx, s.db, request.TransactionId)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.RetrieveTransactiondefaultJSONResponse{Body: notFound(), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.RetrieveTransaction200JSONResponse{Transaction: transactionWire(txn)}, nil
}

func (s *server) ListComments(ctx context.Context, request generated.ListCommentsRequestObject) (generated.ListCommentsResponseObject, error) {
	comments, err := queryComments(ctx, s.db, "SELECT "+commentColumns+" FROM comments ORDER BY created_at DESC, id DESC")
	if err != nil {
		return nil, err
	}
	filtered := make([]fixtureComment, 0, len(comments))
	for _, comment := range comments {
		if request.Params.EntityType != nil && comment.EntityType != string(*request.Params.EntityType) {
			continue
		}
		if request.Params.EntityId != nil && comment.EntityID != string(*request.Params.EntityId) {
			continue
		}
		filtered = append(filtered, comment)
	}
	page, next, err := paginate(filtered, request.Params.Limit, request.Params.Offset)
	if err != nil {
		return generated.ListCommentsdefaultJSONResponse{Body: apiError(http.StatusBadRequest, "invalid_request", err.Error()), StatusCode: http.StatusBadRequest}, nil
	}
	list := make([]generated.CommentListEntry, 0, len(page))
	for _, comment := range page {
		list = append(list, generated.CommentListEntry{Comment: commentWire(comment)})
	}
	return generated.ListComments200JSONResponse{List: list, NextOffset: next}, nil
}

func (s *server) CreateComment(ctx context.Context, request generated.CreateCommentRequestObject) (generated.CreateCommentResponseObject, error) {
	if request.Body == nil || request.Body.Notes == "" || request.Body.EntityId == "" {
		return generated.CreateCommentdefaultJSONResponse{Body: apiError(http.StatusBadRequest, "invalid_request", "entity_type, entity_id, and notes are required"), StatusCode: http.StatusBadRequest}, nil
	}
	state, err := readState(ctx, s.db)
	if err != nil {
		return nil, err
	}
	entityType := string(request.Body.EntityType)
	if !entityExists(state, entityType, request.Body.EntityId) {
		return generated.CreateCommentdefaultJSONResponse{Body: notFound(), StatusCode: http.StatusNotFound}, nil
	}
	id, err := s.ids.Next(ctx, "cmt")
	if err != nil {
		return nil, fmt.Errorf("chargebee: allocate comment ID: %w", err)
	}
	addedBy := "full_access_key_v1"
	if request.Body.AddedBy != nil && *request.Body.AddedBy != "" {
		addedBy = *request.Body.AddedBy
	}
	comment := fixtureComment{
		ID: id, EntityType: entityType, EntityID: request.Body.EntityId, Notes: request.Body.Notes,
		AddedBy: addedBy, Type: string(generated.User), CreatedAt: s.clock.Now().UTC().Unix(),
	}
	if err := insertComment(ctx, s.db, comment); err != nil {
		return nil, err
	}
	return generated.CreateComment200JSONResponse{Comment: commentWire(comment)}, nil
}

func (s *server) subscriptionEnvelope(ctx context.Context, id string) (generated.SubscriptionEnvelope, error) {
	subscription, err := getSubscription(ctx, s.db, id)
	if err != nil {
		return generated.SubscriptionEnvelope{}, err
	}
	customer, err := getCustomer(ctx, s.db, subscription.CustomerID)
	if err != nil {
		return generated.SubscriptionEnvelope{}, err
	}
	return generated.SubscriptionEnvelope{Subscription: subscriptionWire(subscription), Customer: customerWire(customer)}, nil
}

func customerWire(customer fixtureCustomer) generated.Customer {
	return generated.Customer{
		Id: customer.ID, FirstName: customer.FirstName, LastName: customer.LastName, Email: customer.Email,
		Phone: customer.Phone, Company: customer.Company, AutoCollection: generated.CustomerAutoCollection(customer.AutoCollection),
		NetTermDays: customer.NetTermDays, AllowDirectDebit: customer.AllowDirectDebit, CreatedAt: customer.CreatedAt,
		Taxability: generated.CustomerTaxability(customer.Taxability), UpdatedAt: customer.UpdatedAt, PiiCleared: customer.PIICleared,
		ResourceVersion: customer.ResourceVersion, Deleted: customer.Deleted, Object: generated.CustomerObjectCustomer,
		PreferredCurrencyCode: customer.PreferredCurrencyCode, PromotionalCredits: customer.PromotionalCredits,
		RefundableCredits: customer.RefundableCredits, ExcessPayments: customer.ExcessPayments, UnbilledCharges: customer.UnbilledCharges,
	}
}

func subscriptionWire(subscription fixtureSubscription) generated.Subscription {
	items := make([]generated.SubscriptionItem, 0, len(subscription.SubscriptionItems))
	for _, item := range subscription.SubscriptionItems {
		items = append(items, generated.SubscriptionItem{
			ItemPriceId: item.ItemPriceID, ItemType: generated.SubscriptionItemItemType(item.ItemType),
			Quantity: item.Quantity, UnitPrice: item.UnitPrice, Amount: item.Amount, FreeQuantity: item.FreeQuantity,
			BillingCycles: item.BillingCycles, Object: generated.SubscriptionItemObjectSubscriptionItem,
		})
	}
	wire := generated.Subscription{
		Id: subscription.ID, CustomerId: subscription.CustomerID, CurrencyCode: subscription.CurrencyCode,
		Status: generated.SubscriptionStatus(subscription.Status), BillingPeriod: subscription.BillingPeriod,
		BillingPeriodUnit: generated.SubscriptionBillingPeriodUnit(subscription.BillingPeriodUnit),
		CreatedAt:         subscription.CreatedAt, StartedAt: subscription.StartedAt, ActivatedAt: subscription.ActivatedAt,
		CurrentTermStart: subscription.CurrentTermStart, CurrentTermEnd: subscription.CurrentTermEnd,
		NextBillingAt: subscription.NextBillingAt, CancelledAt: subscription.CancelledAt,
		RemainingBillingCycles: subscription.RemainingBillingCycles, DueInvoicesCount: subscription.DueInvoicesCount,
		DueSince: subscription.DueSince, TotalDues: subscription.TotalDues, Mrr: subscription.Mrr,
		HasScheduledChanges: subscription.HasScheduledChanges, HasScheduledAdvanceInvoices: subscription.HasScheduledAdvanceInvoices,
		Deleted: subscription.Deleted, Decommissioned: subscription.Decommissioned, ResourceVersion: subscription.ResourceVersion,
		UpdatedAt: subscription.UpdatedAt, Object: generated.SubscriptionObjectSubscription, SubscriptionItems: items,
	}
	if subscription.CancelReasonCode != "" {
		wire.CancelReasonCode = &subscription.CancelReasonCode
	}
	return wire
}

func invoiceWire(invoice fixtureInvoice) generated.Invoice {
	lines := make([]generated.LineItem, 0, len(invoice.LineItems))
	for _, line := range invoice.LineItems {
		lines = append(lines, generated.LineItem{
			Id: line.ID, CustomerId: line.CustomerID, SubscriptionId: line.SubscriptionID, DateFrom: line.DateFrom,
			DateTo: line.DateTo, Description: line.Description, EntityId: line.EntityID,
			EntityType: generated.LineItemEntityType(line.EntityType), Quantity: line.Quantity, UnitAmount: line.UnitAmount,
			Amount: line.Amount, DiscountAmount: line.DiscountAmount, TaxAmount: line.TaxAmount, Object: generated.LineItemObjectLineItem,
		})
	}
	links := make([]generated.LinkedPayment, 0, len(invoice.LinkedPayments))
	for _, link := range invoice.LinkedPayments {
		links = append(links, generated.LinkedPayment{
			TxnId: link.TxnID, TxnStatus: generated.LinkedPaymentTxnStatus(link.TxnStatus), TxnDate: link.TxnDate,
			TxnAmount: link.TxnAmount, AppliedAmount: link.AppliedAmount, AppliedAt: link.AppliedAt,
		})
	}
	return generated.Invoice{
		Id: invoice.ID, CustomerId: invoice.CustomerID, SubscriptionId: invoice.SubscriptionID, Recurring: invoice.Recurring,
		Status: generated.InvoiceStatus(invoice.Status), Date: invoice.Date, DueDate: invoice.DueDate, PaidAt: invoice.PaidAt,
		NetTermDays: invoice.NetTermDays, PriceType: generated.InvoicePriceType(invoice.PriceType), CurrencyCode: invoice.CurrencyCode,
		ExchangeRate: invoice.ExchangeRate, SubTotal: invoice.SubTotal, Tax: invoice.Tax, Total: invoice.Total,
		AmountPaid: invoice.AmountPaid, AmountDue: invoice.AmountDue, AmountAdjusted: invoice.AmountAdjusted,
		CreditsApplied: invoice.CreditsApplied, WriteOffAmount: invoice.WriteOffAmount, AmountToCollect: invoice.AmountToCollect,
		RoundOffAmount: invoice.RoundOffAmount, FirstInvoice: invoice.FirstInvoice, Deleted: invoice.Deleted,
		ResourceVersion: invoice.ResourceVersion, UpdatedAt: invoice.UpdatedAt, Object: generated.InvoiceObjectInvoice,
		LineItems: lines, LinkedPayments: links,
	}
}

func transactionWire(txn fixtureTransaction) generated.Transaction {
	return generated.Transaction{
		Id: txn.ID, CustomerId: txn.CustomerID, SubscriptionId: txn.SubscriptionID, InvoiceId: txn.InvoiceID,
		Type: generated.TransactionType(txn.Type), Status: generated.TransactionStatus(txn.Status), Amount: txn.Amount,
		AmountUnused: txn.AmountUnused, CurrencyCode: txn.CurrencyCode, Date: txn.Date,
		PaymentMethod: generated.TransactionPaymentMethod(txn.PaymentMethod), Gateway: txn.Gateway,
		ExchangeRate: txn.ExchangeRate, Deleted: txn.Deleted, ResourceVersion: txn.ResourceVersion,
		UpdatedAt: txn.UpdatedAt, Object: generated.TransactionObjectTransaction,
	}
}

func commentWire(comment fixtureComment) generated.Comment {
	return generated.Comment{
		Id: comment.ID, EntityType: generated.CommentEntityType(comment.EntityType), EntityId: comment.EntityID,
		Notes: comment.Notes, AddedBy: comment.AddedBy, Type: generated.CommentType(comment.Type),
		CreatedAt: comment.CreatedAt, Object: generated.CommentObjectComment,
	}
}

func includeRecord(includeDeleted *bool, deleted bool) bool {
	if deleted && (includeDeleted == nil || !*includeDeleted) {
		return false
	}
	return true
}

func paginate[T any](items []T, limit *int, offset *string) ([]T, *string, error) {
	size := 10
	if limit != nil {
		size = *limit
	}
	start := 0
	if offset != nil && *offset != "" {
		parsed, err := strconv.Atoi(*offset)
		if err != nil || parsed < 0 || parsed > len(items) {
			return nil, nil, fmt.Errorf("offset is invalid")
		}
		start = parsed
	}
	end := start + size
	if end > len(items) {
		end = len(items)
	}
	var next *string
	if end < len(items) {
		value := strconv.Itoa(end)
		next = &value
	}
	return items[start:end], next, nil
}

func apiError(status int, code, message string) generated.Error {
	return generated.Error{
		Message: message, Type: generated.InvalidRequest, ApiErrorCode: code, ErrorCode: code,
		ErrorMsg: message, HttpStatusCode: status,
	}
}

func notFound() generated.Error {
	return apiError(http.StatusNotFound, "resource_not_found", "Sorry, we couldn't find that resource")
}

func writeError(w http.ResponseWriter, status int, message string) {
	code := "invalid_request"
	switch status {
	case http.StatusUnauthorized:
		code = "api_authentication_failed"
		message = "Sorry, authentication failed. Invalid api key"
	case http.StatusNotFound:
		code = "resource_not_found"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(apiError(status, code, message))
}

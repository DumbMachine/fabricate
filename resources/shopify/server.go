package shopify

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/shopify/generated"
	"github.com/getkin/kin-openapi/openapi3filter"
	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"
)

type server struct {
	db      *sql.DB
	clock   httpresource.Clock
	ids     httpresource.IDGenerator
	handler http.Handler
}

type shopHostKey struct{}

var errNotFound = errors.New("not found")

var _ generated.StrictServerInterface = (*server)(nil)

func newServer(ctx context.Context, dependencies httpresource.ServerDependencies) (*server, error) {
	if dependencies.DB == nil || dependencies.Clock == nil || dependencies.IDs == nil || dependencies.Secrets == nil {
		return nil, fmt.Errorf("shopify: database, clock, ID generator, and secrets are required")
	}
	token, err := dependencies.Secrets.Get(ctx, "token")
	if err != nil {
		return nil, fmt.Errorf("shopify: load synthetic token: %w", err)
	}
	if token == "" {
		return nil, fmt.Errorf("shopify: synthetic token is empty")
	}
	spec, err := generated.GetSwagger()
	if err != nil {
		return nil, fmt.Errorf("shopify: load OpenAPI: %w", err)
	}
	impl := &server{db: dependencies.DB, clock: dependencies.Clock, ids: dependencies.IDs}
	strict := generated.NewStrictHandlerWithOptions(impl, nil, generated.StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeError(w, http.StatusBadRequest, err.Error())
		},
		ResponseErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeError(w, http.StatusInternalServerError, err.Error())
		},
	})
	generatedHandler := generated.HandlerWithOptions(strict, generated.StdHTTPServerOptions{
		BaseRouter: &patternMux{},
		ErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeError(w, http.StatusBadRequest, err.Error())
		},
	})
	validator := nethttpmiddleware.OapiRequestValidatorWithOptions(spec, &nethttpmiddleware.Options{
		DoNotValidateServers: true,
		Options: openapi3filter.Options{AuthenticationFunc: func(_ context.Context, input *openapi3filter.AuthenticationInput) error {
			request := input.RequestValidationInput.Request
			switch input.SecuritySchemeName {
			case "bearerAuth":
				if request.Header.Get("Authorization") != "Bearer "+token {
					return input.NewError(errors.New("invalid bearer token"))
				}
			case "shopifyAccessToken":
				if request.Header.Get("X-Shopify-Access-Token") != token {
					return input.NewError(errors.New("invalid access token"))
				}
			default:
				return input.NewError(fmt.Errorf("unsupported security scheme %s", input.SecuritySchemeName))
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
	impl.handler = withShopHost(validator(generatedHandler))
	return impl, nil
}

func (s *server) Handler() http.Handler       { return s.handler }
func (s *server) Close(context.Context) error { return nil }

func withShopHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if split, _, err := net.SplitHostPort(host); err == nil {
			host = split
		}
		ctx := context.WithValue(r.Context(), shopHostKey{}, strings.ToLower(host))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(generated.Error{Errors: message})
}

func (s *server) now() string {
	return s.clock.Now().UTC().Format(time.RFC3339)
}

func (s *server) shop(ctx context.Context) (fixtureShop, error) {
	host, _ := ctx.Value(shopHostKey{}).(string)
	if host != "" {
		shop, ok, err := loadBody[fixtureShop](ctx, s.db, `SELECT body FROM shops WHERE domain = ?`, host)
		if err != nil {
			return fixtureShop{}, err
		}
		if ok {
			return shop, nil
		}
		if _, known := shopNames[host]; known {
			return fixtureShop{}, errNotFound
		}
	}
	var domain string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM metadata WHERE key = 'currentShop'`).Scan(&domain); err != nil {
		return fixtureShop{}, err
	}
	shop, ok, err := loadBody[fixtureShop](ctx, s.db, `SELECT body FROM shops WHERE domain = ?`, domain)
	if err != nil {
		return fixtureShop{}, err
	}
	if !ok {
		return fixtureShop{}, errNotFound
	}
	return shop, nil
}

func (s *server) ShopifyShopGet(ctx context.Context, _ generated.ShopifyShopGetRequestObject) (generated.ShopifyShopGetResponseObject, error) {
	shop, err := s.shop(ctx)
	if errors.Is(err, errNotFound) {
		return generated.ShopifyShopGetdefaultJSONResponse{Body: notFoundError(), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.ShopifyShopGet200JSONResponse{Shop: shopWire(shop)}, nil
}

func (s *server) ShopifyProductsList(ctx context.Context, request generated.ShopifyProductsListRequestObject) (generated.ShopifyProductsListResponseObject, error) {
	shop, err := s.shop(ctx)
	if errors.Is(err, errNotFound) {
		return generated.ShopifyProductsListdefaultJSONResponse{Body: notFoundError(), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	products, err := loadAll[fixtureProduct](ctx, s.db, `SELECT body FROM products WHERE shop = ? ORDER BY id`, shop.Domain)
	if err != nil {
		return nil, err
	}
	products = take(products, limitOf(request.Params.Limit))
	out := make([]generated.Product, 0, len(products))
	for _, product := range products {
		out = append(out, productWire(product))
	}
	return generated.ShopifyProductsList200JSONResponse{Products: out}, nil
}

func (s *server) ShopifyProductsGet(ctx context.Context, request generated.ShopifyProductsGetRequestObject) (generated.ShopifyProductsGetResponseObject, error) {
	shop, err := s.shop(ctx)
	if errors.Is(err, errNotFound) {
		return generated.ShopifyProductsGetdefaultJSONResponse{Body: notFoundError(), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	product, ok, err := loadBody[fixtureProduct](ctx, s.db, `SELECT body FROM products WHERE id = ? AND shop = ?`, request.ProductId, shop.Domain)
	if err != nil {
		return nil, err
	}
	if !ok {
		return generated.ShopifyProductsGetdefaultJSONResponse{Body: notFoundError(), StatusCode: http.StatusNotFound}, nil
	}
	return generated.ShopifyProductsGet200JSONResponse{Product: productWire(product)}, nil
}

func (s *server) ShopifyProductsCreate(ctx context.Context, request generated.ShopifyProductsCreateRequestObject) (generated.ShopifyProductsCreateResponseObject, error) {
	shop, err := s.shop(ctx)
	if errors.Is(err, errNotFound) {
		return generated.ShopifyProductsCreatedefaultJSONResponse{Body: notFoundError(), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return generated.ShopifyProductsCreatedefaultJSONResponse{Body: generated.Error{Errors: "product is required"}, StatusCode: http.StatusBadRequest}, nil
	}
	body := request.Body.Product
	title := strings.TrimSpace(body.Title)
	if title == "" {
		return generated.ShopifyProductsCreatedefaultJSONResponse{Body: generated.Error{Errors: "product title is required"}, StatusCode: http.StatusUnprocessableEntity}, nil
	}
	status := "active"
	if body.Status != nil && *body.Status != "" {
		status = *body.Status
	}
	switch status {
	case "active", "draft", "archived":
	default:
		return generated.ShopifyProductsCreatedefaultJSONResponse{Body: generated.Error{Errors: "product status is invalid"}, StatusCode: http.StatusUnprocessableEntity}, nil
	}
	vendor := shop.Name
	if body.Vendor != nil && strings.TrimSpace(*body.Vendor) != "" {
		vendor = strings.TrimSpace(*body.Vendor)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	existing, err := loadAll[fixtureProduct](ctx, tx, `SELECT body FROM products WHERE shop = ? ORDER BY id`, shop.Domain)
	if err != nil {
		return nil, err
	}
	skus := map[string]struct{}{}
	for _, product := range existing {
		for _, variant := range product.Variants {
			skus[variant.SKU] = struct{}{}
		}
	}
	productID, err := s.nextID(ctx, tx, "shopify.product")
	if err != nil {
		return nil, err
	}
	stamp := s.now()
	product := fixtureProduct{
		ID: productID, Shop: shop.Domain, Title: title, Vendor: vendor, Status: status, CreatedAt: stamp,
		Variants: []fixtureVariant{},
	}
	if body.ProductType != nil {
		product.ProductType = *body.ProductType
	}
	type variantInput struct {
		InventoryQuantity *int
		Price             *string
		Sku               *string
		Title             *string
	}
	inputs := []variantInput{{}}
	if body.Variants != nil && len(*body.Variants) > 0 {
		inputs = make([]variantInput, len(*body.Variants))
		for i, variant := range *body.Variants {
			inputs[i] = variantInput{
				InventoryQuantity: variant.InventoryQuantity,
				Price:             variant.Price,
				Sku:               variant.Sku,
				Title:             variant.Title,
			}
		}
	}
	for _, input := range inputs {
		variantID, err := s.nextID(ctx, tx, "shopify.variant")
		if err != nil {
			return nil, err
		}
		variant := fixtureVariant{ID: variantID, Title: "Default Title", Price: "0.00", SKU: fmt.Sprintf("SKU-%d", variantID)}
		if input.Title != nil && strings.TrimSpace(*input.Title) != "" {
			variant.Title = strings.TrimSpace(*input.Title)
		}
		if input.Price != nil && *input.Price != "" {
			if _, err := parseRupees(*input.Price); err != nil {
				return generated.ShopifyProductsCreatedefaultJSONResponse{Body: generated.Error{Errors: "variant price is invalid"}, StatusCode: http.StatusUnprocessableEntity}, nil
			}
			variant.Price = *input.Price
		}
		if input.Sku != nil && strings.TrimSpace(*input.Sku) != "" {
			variant.SKU = strings.TrimSpace(*input.Sku)
		}
		if _, exists := skus[variant.SKU]; exists {
			return generated.ShopifyProductsCreatedefaultJSONResponse{Body: generated.Error{Errors: "sku already exists"}, StatusCode: http.StatusUnprocessableEntity}, nil
		}
		skus[variant.SKU] = struct{}{}
		if input.InventoryQuantity != nil {
			qty := *input.InventoryQuantity
			if qty < 0 {
				return generated.ShopifyProductsCreatedefaultJSONResponse{Body: generated.Error{Errors: "inventory quantity is invalid"}, StatusCode: http.StatusUnprocessableEntity}, nil
			}
			variant.InventoryQuantity = &qty
		}
		product.Variants = append(product.Variants, variant)
	}
	raw, err := json.Marshal(product)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO products(id, shop, body) VALUES(?, ?, ?)`, product.ID, product.Shop, string(raw)); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return generated.ShopifyProductsCreate201JSONResponse{Product: productWire(product)}, nil
}

func (s *server) ShopifyVariantsGet(ctx context.Context, request generated.ShopifyVariantsGetRequestObject) (generated.ShopifyVariantsGetResponseObject, error) {
	shop, err := s.shop(ctx)
	if errors.Is(err, errNotFound) {
		return generated.ShopifyVariantsGetdefaultJSONResponse{Body: notFoundError(), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	products, err := loadAll[fixtureProduct](ctx, s.db, `SELECT body FROM products WHERE shop = ? ORDER BY id`, shop.Domain)
	if err != nil {
		return nil, err
	}
	for _, product := range products {
		for i, variant := range product.Variants {
			if variant.ID == request.VariantId {
				return generated.ShopifyVariantsGet200JSONResponse{Variant: variantWire(product, variant, i+1)}, nil
			}
		}
	}
	return generated.ShopifyVariantsGetdefaultJSONResponse{Body: notFoundError(), StatusCode: http.StatusNotFound}, nil
}

func (s *server) ShopifyCustomersList(ctx context.Context, request generated.ShopifyCustomersListRequestObject) (generated.ShopifyCustomersListResponseObject, error) {
	shop, err := s.shop(ctx)
	if errors.Is(err, errNotFound) {
		return generated.ShopifyCustomersListdefaultJSONResponse{Body: notFoundError(), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	customers, err := loadAll[fixtureCustomer](ctx, s.db, `SELECT body FROM customers WHERE shop = ? ORDER BY id`, shop.Domain)
	if err != nil {
		return nil, err
	}
	orders, err := loadAll[fixtureOrder](ctx, s.db, `SELECT body FROM orders WHERE shop = ? ORDER BY id`, shop.Domain)
	if err != nil {
		return nil, err
	}
	customers = take(customers, limitOf(request.Params.Limit))
	out := make([]generated.Customer, 0, len(customers))
	for _, customer := range customers {
		out = append(out, customerWire(shop, customer, orders))
	}
	return generated.ShopifyCustomersList200JSONResponse{Customers: out}, nil
}

func (s *server) ShopifyCustomersGet(ctx context.Context, request generated.ShopifyCustomersGetRequestObject) (generated.ShopifyCustomersGetResponseObject, error) {
	shop, err := s.shop(ctx)
	if errors.Is(err, errNotFound) {
		return generated.ShopifyCustomersGetdefaultJSONResponse{Body: notFoundError(), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	customer, ok, err := loadBody[fixtureCustomer](ctx, s.db, `SELECT body FROM customers WHERE id = ? AND shop = ?`, request.CustomerId, shop.Domain)
	if err != nil {
		return nil, err
	}
	if !ok {
		return generated.ShopifyCustomersGetdefaultJSONResponse{Body: notFoundError(), StatusCode: http.StatusNotFound}, nil
	}
	orders, err := loadAll[fixtureOrder](ctx, s.db, `SELECT body FROM orders WHERE shop = ? ORDER BY id`, shop.Domain)
	if err != nil {
		return nil, err
	}
	return generated.ShopifyCustomersGet200JSONResponse{Customer: customerWire(shop, customer, orders)}, nil
}

func (s *server) ShopifyOrdersList(ctx context.Context, request generated.ShopifyOrdersListRequestObject) (generated.ShopifyOrdersListResponseObject, error) {
	shop, err := s.shop(ctx)
	if errors.Is(err, errNotFound) {
		return generated.ShopifyOrdersListdefaultJSONResponse{Body: notFoundError(), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	status := "open"
	if request.Params.Status != nil && *request.Params.Status != "" {
		status = string(*request.Params.Status)
	}
	orders, err := s.orderWires(ctx, shop, status, limitOf(request.Params.Limit))
	if err != nil {
		return nil, err
	}
	return generated.ShopifyOrdersList200JSONResponse{Orders: orders}, nil
}

func (s *server) ShopifyOrdersGet(ctx context.Context, request generated.ShopifyOrdersGetRequestObject) (generated.ShopifyOrdersGetResponseObject, error) {
	shop, err := s.shop(ctx)
	if errors.Is(err, errNotFound) {
		return generated.ShopifyOrdersGetdefaultJSONResponse{Body: notFoundError(), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	order, ok, err := s.orderWire(ctx, shop, request.OrderId)
	if err != nil {
		return nil, err
	}
	if !ok {
		return generated.ShopifyOrdersGetdefaultJSONResponse{Body: notFoundError(), StatusCode: http.StatusNotFound}, nil
	}
	return generated.ShopifyOrdersGet200JSONResponse{Order: order}, nil
}

func (s *server) ShopifyOrdersFulfillmentsList(ctx context.Context, request generated.ShopifyOrdersFulfillmentsListRequestObject) (generated.ShopifyOrdersFulfillmentsListResponseObject, error) {
	shop, err := s.shop(ctx)
	if errors.Is(err, errNotFound) {
		return generated.ShopifyOrdersFulfillmentsListdefaultJSONResponse{Body: notFoundError(), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	order, ok, err := loadBody[fixtureOrder](ctx, s.db, `SELECT body FROM orders WHERE id = ? AND shop = ?`, request.OrderId, shop.Domain)
	if err != nil {
		return nil, err
	}
	if !ok {
		return generated.ShopifyOrdersFulfillmentsListdefaultJSONResponse{Body: notFoundError(), StatusCode: http.StatusNotFound}, nil
	}
	fulfillments, err := loadAll[fixtureFulfillment](ctx, s.db, `SELECT body FROM fulfillments WHERE shop = ? AND order_id = ? ORDER BY id`, shop.Domain, order.ID)
	if err != nil {
		return nil, err
	}
	fulfillments = take(fulfillments, limitOf(request.Params.Limit))
	out := make([]generated.Fulfillment, 0, len(fulfillments))
	for i, fulfillment := range fulfillments {
		out = append(out, fulfillmentWire(shop, order, fulfillment, i+1))
	}
	return generated.ShopifyOrdersFulfillmentsList200JSONResponse{Fulfillments: out}, nil
}

func (s *server) ShopifyFulfillmentsCreate(ctx context.Context, request generated.ShopifyFulfillmentsCreateRequestObject) (generated.ShopifyFulfillmentsCreateResponseObject, error) {
	shop, err := s.shop(ctx)
	if errors.Is(err, errNotFound) {
		return generated.ShopifyFulfillmentsCreatedefaultJSONResponse{Body: notFoundError(), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	if request.Body == nil || len(request.Body.Fulfillment.LineItemsByFulfillmentOrder) == 0 {
		return generated.ShopifyFulfillmentsCreatedefaultJSONResponse{Body: generated.Error{Errors: "fulfillment order is required"}, StatusCode: http.StatusUnprocessableEntity}, nil
	}
	orderID := request.Body.Fulfillment.LineItemsByFulfillmentOrder[0].FulfillmentOrderId
	for _, entry := range request.Body.Fulfillment.LineItemsByFulfillmentOrder[1:] {
		if entry.FulfillmentOrderId != orderID {
			return generated.ShopifyFulfillmentsCreatedefaultJSONResponse{Body: generated.Error{Errors: "a fulfillment covers one order"}, StatusCode: http.StatusUnprocessableEntity}, nil
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	order, ok, err := loadBody[fixtureOrder](ctx, tx, `SELECT body FROM orders WHERE id = ? AND shop = ?`, orderID, shop.Domain)
	if err != nil {
		return nil, err
	}
	if !ok {
		return generated.ShopifyFulfillmentsCreatedefaultJSONResponse{Body: generated.Error{Errors: "order not found"}, StatusCode: http.StatusUnprocessableEntity}, nil
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM fulfillments WHERE shop = ? AND order_id = ?`, shop.Domain, order.ID).Scan(&count); err != nil {
		return nil, err
	}
	fulfillmentID, err := s.nextID(ctx, tx, "shopify.fulfillment")
	if err != nil {
		return nil, err
	}
	stamp := s.now()
	fulfillment := fixtureFulfillment{
		ID: fulfillmentID, Shop: shop.Domain, OrderID: order.ID, Status: "success", CreatedAt: stamp,
	}
	if info := request.Body.Fulfillment.TrackingInfo; info != nil {
		fulfillment.TrackingCompany = deref(info.Company)
		fulfillment.TrackingNumber = deref(info.Number)
		fulfillment.TrackingURL = deref(info.Url)
	}
	raw, err := json.Marshal(fulfillment)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO fulfillments(id, shop, order_id, body) VALUES(?, ?, ?, ?)`, fulfillment.ID, fulfillment.Shop, fulfillment.OrderID, string(raw)); err != nil {
		return nil, err
	}
	order.FulfillmentStatus = "fulfilled"
	order.UpdatedAt = stamp
	orderRaw, err := json.Marshal(order)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE orders SET body = ? WHERE id = ?`, string(orderRaw), order.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return generated.ShopifyFulfillmentsCreate201JSONResponse{Fulfillment: fulfillmentWire(shop, order, fulfillment, count+1)}, nil
}

func (s *server) ShopifyOrdersRefundsList(ctx context.Context, request generated.ShopifyOrdersRefundsListRequestObject) (generated.ShopifyOrdersRefundsListResponseObject, error) {
	shop, err := s.shop(ctx)
	if errors.Is(err, errNotFound) {
		return generated.ShopifyOrdersRefundsListdefaultJSONResponse{Body: notFoundError(), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	order, ok, err := loadBody[fixtureOrder](ctx, s.db, `SELECT body FROM orders WHERE id = ? AND shop = ?`, request.OrderId, shop.Domain)
	if err != nil {
		return nil, err
	}
	if !ok {
		return generated.ShopifyOrdersRefundsListdefaultJSONResponse{Body: notFoundError(), StatusCode: http.StatusNotFound}, nil
	}
	refunds, err := loadAll[fixtureRefund](ctx, s.db, `SELECT body FROM refunds WHERE shop = ? AND order_id = ? ORDER BY id`, shop.Domain, order.ID)
	if err != nil {
		return nil, err
	}
	refunds = take(refunds, limitOf(request.Params.Limit))
	out := make([]generated.Refund, 0, len(refunds))
	for _, refund := range refunds {
		out = append(out, refundWire(order, refund))
	}
	return generated.ShopifyOrdersRefundsList200JSONResponse{Refunds: out}, nil
}

func (s *server) nextID(ctx context.Context, tx *sql.Tx, kind string) (int64, error) {
	if _, err := s.ids.Next(ctx, kind); err != nil {
		return 0, err
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `UPDATE sequences SET value = value + 1 WHERE name = 'id' RETURNING value`).Scan(&id); err != nil {
		return 0, fmt.Errorf("shopify: allocate id: %w", err)
	}
	return id, nil
}

func (s *server) orderWires(ctx context.Context, shop fixtureShop, status string, limit int) ([]generated.Order, error) {
	orders, err := loadAll[fixtureOrder](ctx, s.db, `SELECT body FROM orders WHERE shop = ?`, shop.Domain)
	if err != nil {
		return nil, err
	}
	filtered := make([]fixtureOrder, 0, len(orders))
	for _, order := range orders {
		if orderVisible(order, status) {
			filtered = append(filtered, order)
		}
	}
	sortOrders(filtered)
	filtered = take(filtered, limit)
	out := make([]generated.Order, 0, len(filtered))
	for _, order := range filtered {
		wire, err := s.projectOrder(ctx, shop, order)
		if err != nil {
			return nil, err
		}
		out = append(out, wire)
	}
	return out, nil
}

func (s *server) projectOrder(ctx context.Context, shop fixtureShop, order fixtureOrder) (generated.Order, error) {
	customer, ok, err := loadBody[fixtureCustomer](ctx, s.db, `SELECT body FROM customers WHERE id = ? AND shop = ?`, order.CustomerID, shop.Domain)
	if err != nil {
		return generated.Order{}, err
	}
	orders, err := loadAll[fixtureOrder](ctx, s.db, `SELECT body FROM orders WHERE shop = ? ORDER BY id`, shop.Domain)
	if err != nil {
		return generated.Order{}, err
	}
	refunds, err := loadAll[fixtureRefund](ctx, s.db, `SELECT body FROM refunds WHERE shop = ? AND order_id = ? ORDER BY id`, shop.Domain, order.ID)
	if err != nil {
		return generated.Order{}, err
	}
	var refunded int64
	for _, refund := range refunds {
		amount, err := parseRupees(refund.Amount)
		if err != nil {
			return generated.Order{}, err
		}
		refunded += amount
	}
	total, err := parseRupees(order.TotalPrice)
	if err != nil {
		return generated.Order{}, err
	}
	current := total - refunded
	if current < 0 {
		current = 0
	}
	wire := orderWire(order, formatRupees(current))
	if ok {
		projected := customerWire(shop, customer, orders)
		wire.Customer = &projected
	}
	return wire, nil
}

type rowQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func loadBody[T any](ctx context.Context, db rowQuery, query string, args ...any) (T, bool, error) {
	var zero T
	var raw string
	err := db.QueryRowContext(ctx, query, args...).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return zero, false, nil
	}
	if err != nil {
		return zero, false, err
	}
	if err := json.Unmarshal([]byte(raw), &zero); err != nil {
		return zero, false, err
	}
	return zero, true, nil
}

func orderVisible(order fixtureOrder, status string) bool {
	switch status {
	case "", "open":
		return order.CancelledAt == "" && order.ClosedAt == ""
	case "closed":
		return order.ClosedAt != ""
	case "cancelled":
		return order.CancelledAt != ""
	case "any":
		return true
	default:
		return false
	}
}

func sortOrders(orders []fixtureOrder) {
	for i := 1; i < len(orders); i++ {
		item := orders[i]
		j := i
		for j > 0 && (orders[j-1].CreatedAt < item.CreatedAt || (orders[j-1].CreatedAt == item.CreatedAt && orders[j-1].ID < item.ID)) {
			orders[j] = orders[j-1]
			j--
		}
		orders[j] = item
	}
}

func limitOf(limit *int) int {
	if limit == nil || *limit <= 0 {
		return 50
	}
	return *limit
}

func take[T any](items []T, limit int) []T {
	if limit > 0 && len(items) > limit {
		return items[:limit]
	}
	return items
}

func notFoundError() generated.Error {
	return generated.Error{Errors: "Not Found"}
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func strPtr(value string) *string {
	if value == "" {
		return nil
	}
	copy := value
	return &copy
}

func gid(kind string, id int64) string {
	return fmt.Sprintf("gid://shopify/%s/%d", kind, id)
}

func addressFromInput(input generated.AddressInput) (fixtureAddress, error) {
	address := fixtureAddress{
		FirstName: deref(input.FirstName), LastName: deref(input.LastName),
		Address1: deref(input.Address1), City: deref(input.City), Province: deref(input.Province),
		ProvinceCode: deref(input.ProvinceCode), Country: deref(input.Country),
		CountryCode: deref(input.CountryCode), Zip: deref(input.Zip), Phone: deref(input.Phone),
	}
	if address.Address1 == "" || address.City == "" || address.Country == "" || address.CountryCode == "" || address.Zip == "" || address.ProvinceCode == "" {
		return fixtureAddress{}, errors.New("shipping address requires address1, city, province_code, country, country_code, and zip")
	}
	return address, nil
}

func orderWire(order fixtureOrder, current string) generated.Order {
	lines := make([]generated.LineItem, 0, len(order.LineItems))
	for _, line := range order.LineItems {
		lines = append(lines, lineItemWire(line, order.FulfillmentStatus))
	}
	updated := order.UpdatedAt
	if updated == "" {
		updated = order.CreatedAt
	}
	wire := generated.Order{
		AdminGraphqlApiId: gid("Order", order.ID), CreatedAt: order.CreatedAt, Currency: order.Currency,
		CurrentTotalPrice: current, Email: strPtr(order.Email), FinancialStatus: order.FinancialStatus,
		Id: order.ID, LineItems: lines, Name: order.Name, OrderNumber: order.ID, Phone: strPtr(order.Phone),
		SubtotalPrice: order.SubtotalPrice, TotalDiscounts: "0.00", TotalPrice: order.TotalPrice, TotalTax: "0.00",
		UpdatedAt: updated,
	}
	if order.FulfillmentStatus != "" {
		wire.FulfillmentStatus = &order.FulfillmentStatus
	}
	if order.Note != "" {
		wire.Note = &order.Note
	}
	if len(order.NoteAttributes) > 0 {
		notes := make([]generated.NoteAttribute, 0, len(order.NoteAttributes))
		for _, note := range order.NoteAttributes {
			notes = append(notes, generated.NoteAttribute{Name: note.Name, Value: note.Value})
		}
		wire.NoteAttributes = &notes
	}
	if order.Gateway != "" {
		gateways := []string{order.Gateway}
		wire.PaymentGatewayNames = &gateways
	}
	if order.ShippingAddress != nil {
		address := addressWire(*order.ShippingAddress)
		wire.ShippingAddress = &address
		billing := address
		wire.BillingAddress = &billing
	}
	return wire
}

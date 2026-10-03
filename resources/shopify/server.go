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

func (s *server) ShopifyOrdersCreate(ctx context.Context, request generated.ShopifyOrdersCreateRequestObject) (generated.ShopifyOrdersCreateResponseObject, error) {
	shop, err := s.shop(ctx)
	if errors.Is(err, errNotFound) {
		return generated.ShopifyOrdersCreatedefaultJSONResponse{Body: notFoundError(), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return generated.ShopifyOrdersCreatedefaultJSONResponse{Body: generated.Error{Errors: "order is required"}, StatusCode: http.StatusBadRequest}, nil
	}
	body := request.Body.Order
	email := deref(body.Email)
	var customerInputFirst, customerInputLast, customerInputPhone string
	if body.Customer != nil {
		if email == "" {
			email = deref(body.Customer.Email)
		} else if other := deref(body.Customer.Email); other != "" && other != email {
			return generated.ShopifyOrdersCreatedefaultJSONResponse{Body: generated.Error{Errors: "order email does not match customer email"}, StatusCode: http.StatusUnprocessableEntity}, nil
		}
		customerInputFirst = deref(body.Customer.FirstName)
		customerInputLast = deref(body.Customer.LastName)
		customerInputPhone = deref(body.Customer.Phone)
	}
	if email == "" {
		return generated.ShopifyOrdersCreatedefaultJSONResponse{Body: generated.Error{Errors: "email is required"}, StatusCode: http.StatusUnprocessableEntity}, nil
	}
	financial := "paid"
	if body.FinancialStatus != nil && *body.FinancialStatus != "" {
		financial = *body.FinancialStatus
	}
	switch financial {
	case "pending", "authorized", "partially_paid", "paid", "partially_refunded", "voided":
	default:
		return generated.ShopifyOrdersCreatedefaultJSONResponse{Body: generated.Error{Errors: "financial status is invalid"}, StatusCode: http.StatusUnprocessableEntity}, nil
	}
	currency := shop.Currency
	if body.Currency != nil && *body.Currency != "" {
		currency = *body.Currency
	}
	if len(currency) != 3 {
		return generated.ShopifyOrdersCreatedefaultJSONResponse{Body: generated.Error{Errors: "currency is invalid"}, StatusCode: http.StatusUnprocessableEntity}, nil
	}
	if len(body.LineItems) == 0 {
		return generated.ShopifyOrdersCreatedefaultJSONResponse{Body: generated.Error{Errors: "line items are required"}, StatusCode: http.StatusUnprocessableEntity}, nil
	}
	var shipping *fixtureAddress
	if body.ShippingAddress != nil {
		address, err := addressFromInput(*body.ShippingAddress)
		if err != nil {
			return generated.ShopifyOrdersCreatedefaultJSONResponse{Body: generated.Error{Errors: err.Error()}, StatusCode: http.StatusUnprocessableEntity}, nil
		}
		shipping = &address
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	products, err := loadAll[fixtureProduct](ctx, tx, `SELECT body FROM products WHERE shop = ? ORDER BY id`, shop.Domain)
	if err != nil {
		return nil, err
	}
	variants := map[int64]struct {
		product fixtureProduct
		variant fixtureVariant
	}{}
	for _, product := range products {
		for _, variant := range product.Variants {
			variants[variant.ID] = struct {
				product fixtureProduct
				variant fixtureVariant
			}{product, variant}
		}
	}
	customers, err := loadAll[fixtureCustomer](ctx, tx, `SELECT body FROM customers WHERE shop = ? ORDER BY id`, shop.Domain)
	if err != nil {
		return nil, err
	}
	var customer fixtureCustomer
	found := false
	for _, existing := range customers {
		if existing.Email == email {
			customer = existing
			found = true
			break
		}
	}
	stamp := s.now()
	if !found {
		customerID, err := s.nextID(ctx, tx, "shopify.customer")
		if err != nil {
			return nil, err
		}
		customer = fixtureCustomer{
			ID: customerID, Shop: shop.Domain, Email: email,
			FirstName: customerInputFirst, LastName: customerInputLast, Phone: customerInputPhone, CreatedAt: stamp,
		}
		if customer.Phone == "" {
			customer.Phone = deref(body.Phone)
		}
		raw, err := json.Marshal(customer)
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO customers(id, shop, body) VALUES(?, ?, ?)`, customer.ID, customer.Shop, string(raw)); err != nil {
			return nil, err
		}
	}
	orderID, err := s.nextID(ctx, tx, "shopify.order")
	if err != nil {
		return nil, err
	}
	order := fixtureOrder{
		ID: orderID, Shop: shop.Domain, Name: fmt.Sprintf("#%d", orderID), Email: email,
		Phone: deref(body.Phone), CreatedAt: stamp, Currency: currency, FinancialStatus: financial,
		CustomerID: customer.ID, LineItems: []fixtureLineItem{}, ShippingAddress: shipping,
		Note: deref(body.Note),
	}
	if order.Phone == "" {
		order.Phone = customer.Phone
	}
	var sum int64
	for _, line := range body.LineItems {
		if line.Quantity < 1 {
			return generated.ShopifyOrdersCreatedefaultJSONResponse{Body: generated.Error{Errors: "line item quantity is invalid"}, StatusCode: http.StatusUnprocessableEntity}, nil
		}
		item := fixtureLineItem{Quantity: line.Quantity, Title: deref(line.Title), SKU: deref(line.Sku)}
		if line.VariantId != nil {
			foundVariant, ok := variants[*line.VariantId]
			if !ok {
				return generated.ShopifyOrdersCreatedefaultJSONResponse{Body: generated.Error{Errors: "variant not found"}, StatusCode: http.StatusUnprocessableEntity}, nil
			}
			item.VariantID = foundVariant.variant.ID
			item.ProductID = foundVariant.product.ID
			item.Price = foundVariant.variant.Price
			item.SKU = foundVariant.variant.SKU
			item.Title = foundVariant.product.Title
			if line.Title != nil && strings.TrimSpace(*line.Title) != "" {
				item.Title = strings.TrimSpace(*line.Title)
			}
			if line.Price != nil && *line.Price != "" && *line.Price != item.Price {
				return generated.ShopifyOrdersCreatedefaultJSONResponse{Body: generated.Error{Errors: "line item price does not match variant"}, StatusCode: http.StatusUnprocessableEntity}, nil
			}
		} else {
			if item.Title == "" || line.Price == nil || *line.Price == "" {
				return generated.ShopifyOrdersCreatedefaultJSONResponse{Body: generated.Error{Errors: "line item title and price are required"}, StatusCode: http.StatusUnprocessableEntity}, nil
			}
			if _, err := parseRupees(*line.Price); err != nil {
				return generated.ShopifyOrdersCreatedefaultJSONResponse{Body: generated.Error{Errors: "line item price is invalid"}, StatusCode: http.StatusUnprocessableEntity}, nil
			}
			item.Price = *line.Price
		}
		lineID, err := s.nextID(ctx, tx, "shopify.line_item")
		if err != nil {
			return nil, err
		}
		item.ID = lineID
		total, err := lineTotal(item.Price, item.Quantity)
		if err != nil {
			return generated.ShopifyOrdersCreatedefaultJSONResponse{Body: generated.Error{Errors: "line item price is invalid"}, StatusCode: http.StatusUnprocessableEntity}, nil
		}
		sum += total
		order.LineItems = append(order.LineItems, item)
	}
	order.TotalPrice = formatRupees(sum)
	order.SubtotalPrice = order.TotalPrice
	raw, err := json.Marshal(order)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO orders(id, shop, body) VALUES(?, ?, ?)`, order.ID, order.Shop, string(raw)); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	wire, err := s.projectOrder(ctx, shop, order)
	if err != nil {
		return nil, err
	}
	return generated.ShopifyOrdersCreate201JSONResponse{Order: wire}, nil
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

func (s *server) ShopifyOrdersRefundsCreate(ctx context.Context, request generated.ShopifyOrdersRefundsCreateRequestObject) (generated.ShopifyOrdersRefundsCreateResponseObject, error) {
	shop, err := s.shop(ctx)
	if errors.Is(err, errNotFound) {
		return generated.ShopifyOrdersRefundsCreatedefaultJSONResponse{Body: notFoundError(), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return generated.ShopifyOrdersRefundsCreatedefaultJSONResponse{Body: generated.Error{Errors: "refund is required"}, StatusCode: http.StatusBadRequest}, nil
	}
	body := request.Body.Refund
	var lineInputs []struct {
		LineItemId  int64   `json:"line_item_id"`
		Quantity    int     `json:"quantity"`
		RestockType *string `json:"restock_type,omitempty"`
	}
	if body.RefundLineItems != nil {
		lineInputs = *body.RefundLineItems
	}
	var txInputs []struct {
		Amount   string  `json:"amount"`
		Gateway  *string `json:"gateway,omitempty"`
		Kind     string  `json:"kind"`
		ParentId *int64  `json:"parent_id,omitempty"`
	}
	if body.Transactions != nil {
		txInputs = *body.Transactions
	}
	if len(lineInputs) == 0 && len(txInputs) == 0 {
		return generated.ShopifyOrdersRefundsCreatedefaultJSONResponse{Body: generated.Error{Errors: "refund line items or transactions are required"}, StatusCode: http.StatusUnprocessableEntity}, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	order, ok, err := loadBody[fixtureOrder](ctx, tx, `SELECT body FROM orders WHERE id = ? AND shop = ?`, request.OrderId, shop.Domain)
	if err != nil {
		return nil, err
	}
	if !ok {
		return generated.ShopifyOrdersRefundsCreatedefaultJSONResponse{Body: notFoundError(), StatusCode: http.StatusNotFound}, nil
	}
	if body.Currency != nil && *body.Currency != "" && *body.Currency != order.Currency {
		return generated.ShopifyOrdersRefundsCreatedefaultJSONResponse{Body: generated.Error{Errors: "refund currency does not match the order"}, StatusCode: http.StatusUnprocessableEntity}, nil
	}
	lines := map[int64]fixtureLineItem{}
	for _, line := range order.LineItems {
		lines[line.ID] = line
	}
	existing, err := loadAll[fixtureRefund](ctx, tx, `SELECT body FROM refunds WHERE shop = ? AND order_id = ? ORDER BY id`, shop.Domain, order.ID)
	if err != nil {
		return nil, err
	}
	refundedQty := map[int64]int{}
	var already int64
	for _, refund := range existing {
		amount, err := parseRupees(refund.Amount)
		if err != nil {
			return nil, err
		}
		already += amount
		for _, line := range refund.LineItems {
			refundedQty[line.LineItemID] += line.Quantity
		}
	}
	refundID, err := s.nextID(ctx, tx, "shopify.refund")
	if err != nil {
		return nil, err
	}
	stamp := s.now()
	refund := fixtureRefund{
		ID: refundID, Shop: shop.Domain, OrderID: order.ID, CreatedAt: stamp,
		Note: deref(body.Note), Currency: order.Currency, Gateway: order.Gateway, LineItems: []fixtureRefundLine{},
	}
	if refund.Gateway == "" {
		refund.Gateway = "manual"
	}
	var lineSum int64
	for _, input := range lineInputs {
		item, ok := lines[input.LineItemId]
		if !ok {
			return generated.ShopifyOrdersRefundsCreatedefaultJSONResponse{Body: generated.Error{Errors: "line item is not on the order"}, StatusCode: http.StatusUnprocessableEntity}, nil
		}
		if input.Quantity < 1 || input.Quantity > item.Quantity-refundedQty[item.ID] {
			return generated.ShopifyOrdersRefundsCreatedefaultJSONResponse{Body: generated.Error{Errors: "refund quantity exceeds the line"}, StatusCode: http.StatusUnprocessableEntity}, nil
		}
		restock := "no_restock"
		if input.RestockType != nil && *input.RestockType != "" {
			restock = *input.RestockType
		}
		switch restock {
		case "no_restock", "cancel", "return":
		default:
			return generated.ShopifyOrdersRefundsCreatedefaultJSONResponse{Body: generated.Error{Errors: "restock type is invalid"}, StatusCode: http.StatusUnprocessableEntity}, nil
		}
		subtotal, err := lineTotal(item.Price, input.Quantity)
		if err != nil {
			return nil, err
		}
		lineID, err := s.nextID(ctx, tx, "shopify.refund_line")
		if err != nil {
			return nil, err
		}
		refund.LineItems = append(refund.LineItems, fixtureRefundLine{
			ID: lineID, LineItemID: item.ID, Quantity: input.Quantity, RestockType: restock, Subtotal: formatRupees(subtotal),
		})
		refundedQty[item.ID] += input.Quantity
		lineSum += subtotal
		if restock == "return" || restock == "cancel" {
			if err := s.restock(ctx, tx, shop.Domain, item.VariantID, input.Quantity, stamp); err != nil {
				return nil, err
			}
		}
	}
	var txSum int64
	explicitGateway := ""
	for _, input := range txInputs {
		if input.Kind != "refund" {
			return generated.ShopifyOrdersRefundsCreatedefaultJSONResponse{Body: generated.Error{Errors: "transaction kind must be refund"}, StatusCode: http.StatusUnprocessableEntity}, nil
		}
		amount, err := parseRupees(input.Amount)
		if err != nil {
			return generated.ShopifyOrdersRefundsCreatedefaultJSONResponse{Body: generated.Error{Errors: "transaction amount is invalid"}, StatusCode: http.StatusUnprocessableEntity}, nil
		}
		txSum += amount
		next := deref(input.Gateway)
		if next == "" {
			continue
		}
		if explicitGateway == "" {
			explicitGateway = next
			continue
		}
		if explicitGateway != next {
			return generated.ShopifyOrdersRefundsCreatedefaultJSONResponse{Body: generated.Error{Errors: "refund gateways do not match"}, StatusCode: http.StatusUnprocessableEntity}, nil
		}
	}
	if explicitGateway != "" {
		refund.Gateway = explicitGateway
	}
	if len(lineInputs) > 0 && len(txInputs) > 0 && lineSum != txSum {
		return generated.ShopifyOrdersRefundsCreatedefaultJSONResponse{Body: generated.Error{Errors: "transaction amount does not match refund line items"}, StatusCode: http.StatusUnprocessableEntity}, nil
	}
	amount := lineSum
	if len(lineInputs) == 0 {
		amount = txSum
	}
	if amount <= 0 {
		return generated.ShopifyOrdersRefundsCreatedefaultJSONResponse{Body: generated.Error{Errors: "refund amount is required"}, StatusCode: http.StatusUnprocessableEntity}, nil
	}
	orderTotal, err := parseRupees(order.TotalPrice)
	if err != nil {
		return nil, err
	}
	if already+amount > orderTotal {
		return generated.ShopifyOrdersRefundsCreatedefaultJSONResponse{Body: generated.Error{Errors: "refund amount exceeds the order"}, StatusCode: http.StatusUnprocessableEntity}, nil
	}
	refund.Amount = formatRupees(amount)
	raw, err := json.Marshal(refund)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO refunds(id, shop, order_id, body) VALUES(?, ?, ?, ?)`, refund.ID, refund.Shop, refund.OrderID, string(raw)); err != nil {
		return nil, err
	}
	if already+amount >= orderTotal {
		order.FinancialStatus = "refunded"
	} else {
		order.FinancialStatus = "partially_refunded"
	}
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
	return generated.ShopifyOrdersRefundsCreate201JSONResponse{Refund: refundWire(order, refund)}, nil
}

func (s *server) restock(ctx context.Context, tx *sql.Tx, shop string, variantID int64, quantity int, stamp string) error {
	if variantID == 0 || quantity == 0 {
		return nil
	}
	products, err := loadAll[fixtureProduct](ctx, tx, `SELECT body FROM products WHERE shop = ? ORDER BY id`, shop)
	if err != nil {
		return err
	}
	for _, product := range products {
		for i := range product.Variants {
			if product.Variants[i].ID != variantID || product.Variants[i].InventoryQuantity == nil {
				continue
			}
			next := *product.Variants[i].InventoryQuantity + quantity
			product.Variants[i].InventoryQuantity = &next
			product.UpdatedAt = stamp
			raw, err := json.Marshal(product)
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `UPDATE products SET body = ? WHERE id = ?`, string(raw), product.ID)
			return err
		}
	}
	return nil
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

func (s *server) orderWire(ctx context.Context, shop fixtureShop, id int64) (generated.Order, bool, error) {
	order, ok, err := loadBody[fixtureOrder](ctx, s.db, `SELECT body FROM orders WHERE id = ? AND shop = ?`, id, shop.Domain)
	if err != nil || !ok {
		return generated.Order{}, ok, err
	}
	wire, err := s.projectOrder(ctx, shop, order)
	if err != nil {
		return generated.Order{}, false, err
	}
	return wire, true, nil
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

func shopWire(shop fixtureShop) generated.Shop {
	return generated.Shop{
		Address1: strPtr(shop.Address1), City: strPtr(shop.City), Country: shop.Country, CountryCode: shop.CountryCode,
		CountryName: shop.CountryName, CreatedAt: shop.CreatedAt, Currency: shop.Currency, Domain: shop.Domain,
		Email: shop.Email, IanaTimezone: shop.IanaTimezone, Id: shop.ID, MoneyFormat: shop.MoneyFormat,
		MyshopifyDomain: shop.MyshopifyDomain, Name: shop.Name, Phone: strPtr(shop.Phone), PrimaryLocale: shop.PrimaryLocale,
		Province: strPtr(shop.Province), ProvinceCode: strPtr(shop.ProvinceCode), ShopOwner: shop.ShopOwner,
		Timezone: shop.Timezone, UpdatedAt: shop.CreatedAt, WeightUnit: shop.WeightUnit, Zip: strPtr(shop.Zip),
	}
}

func productWire(product fixtureProduct) generated.Product {
	variants := make([]generated.Variant, 0, len(product.Variants))
	for i, variant := range product.Variants {
		variants = append(variants, variantWire(product, variant, i+1))
	}
	updated := product.UpdatedAt
	if updated == "" {
		updated = product.CreatedAt
	}
	return generated.Product{
		AdminGraphqlApiId: gid("Product", product.ID), CreatedAt: product.CreatedAt, Handle: slug(product.Title),
		Id: product.ID, ProductType: strPtr(product.ProductType), Status: product.Status, Title: product.Title,
		UpdatedAt: updated, Variants: variants, Vendor: product.Vendor,
	}
}

func variantWire(product fixtureProduct, variant fixtureVariant, position int) generated.Variant {
	unit := "kg"
	wire := generated.Variant{
		AdminGraphqlApiId: gid("ProductVariant", variant.ID), Id: variant.ID, Position: position, Price: variant.Price,
		ProductId: product.ID, RequiresShipping: true, Sku: variant.SKU, Taxable: true, Title: variant.Title, WeightUnit: &unit,
	}
	if variant.InventoryQuantity != nil {
		qty := *variant.InventoryQuantity
		wire.InventoryQuantity = &qty
		management := "shopify"
		wire.InventoryManagement = &management
	}
	return wire
}

func customerWire(shop fixtureShop, customer fixtureCustomer, orders []fixtureOrder) generated.Customer {
	count := 0
	var spent int64
	for _, order := range orders {
		if order.CustomerID != customer.ID {
			continue
		}
		count++
		amount, err := parseRupees(order.TotalPrice)
		if err == nil {
			spent += amount
		}
	}
	return generated.Customer{
		AdminGraphqlApiId: gid("Customer", customer.ID), CreatedAt: customer.CreatedAt, Currency: shop.Currency,
		Email: customer.Email, FirstName: customer.FirstName, Id: customer.ID, LastName: customer.LastName,
		OrdersCount: count, Phone: strPtr(customer.Phone), State: "enabled", TotalSpent: formatRupees(spent),
		UpdatedAt: customer.CreatedAt, VerifiedEmail: true,
	}
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

func lineItemWire(line fixtureLineItem, fulfillmentStatus string) generated.LineItem {
	wire := generated.LineItem{
		Id: line.ID, Name: line.Title, Price: line.Price, Quantity: line.Quantity,
		RequiresShipping: true, Taxable: true, Title: line.Title, Sku: strPtr(line.SKU),
	}
	if line.ProductID != 0 {
		id := line.ProductID
		wire.ProductId = &id
	}
	if line.VariantID != 0 {
		id := line.VariantID
		wire.VariantId = &id
	}
	if fulfillmentStatus == "fulfilled" {
		status := "fulfilled"
		wire.FulfillmentStatus = &status
	}
	return wire
}

func addressWire(address fixtureAddress) generated.Address {
	return generated.Address{
		Address1: address.Address1, City: address.City, Country: address.Country, CountryCode: address.CountryCode,
		FirstName: strPtr(address.FirstName), LastName: strPtr(address.LastName), Phone: strPtr(address.Phone),
		Province: strPtr(address.Province), ProvinceCode: strPtr(address.ProvinceCode), Zip: address.Zip,
	}
}

func fulfillmentWire(shop fixtureShop, order fixtureOrder, fulfillment fixtureFulfillment, index int) generated.Fulfillment {
	lines := make([]generated.LineItem, 0, len(order.LineItems))
	lineStatus := ""
	if fulfillment.Status == "success" {
		lineStatus = "fulfilled"
	}
	for _, line := range order.LineItems {
		lines = append(lines, lineItemWire(line, lineStatus))
	}
	updated := fulfillment.UpdatedAt
	if updated == "" {
		updated = fulfillment.CreatedAt
	}
	wire := generated.Fulfillment{
		AdminGraphqlApiId: gid("Fulfillment", fulfillment.ID), CreatedAt: fulfillment.CreatedAt, Id: fulfillment.ID,
		LineItems: lines, Name: fmt.Sprintf("%s.%d", order.Name, index), OrderId: order.ID, Service: "manual",
		Status: fulfillment.Status, UpdatedAt: updated, OriginAddress: originWire(shop),
	}
	if fulfillment.ShipmentStatus != "" {
		wire.ShipmentStatus = &fulfillment.ShipmentStatus
	}
	if fulfillment.TrackingCompany != "" {
		wire.TrackingCompany = &fulfillment.TrackingCompany
	}
	if fulfillment.TrackingNumber != "" {
		wire.TrackingNumber = &fulfillment.TrackingNumber
		numbers := []string{fulfillment.TrackingNumber}
		wire.TrackingNumbers = &numbers
	}
	if fulfillment.TrackingURL != "" {
		wire.TrackingUrl = &fulfillment.TrackingURL
		urls := []string{fulfillment.TrackingURL}
		wire.TrackingUrls = &urls
	}
	return wire
}

func originWire(shop fixtureShop) *generated.OriginAddress {
	if shop.Address1 == "" {
		return nil
	}
	return &generated.OriginAddress{
		Address1: strPtr(shop.Address1), City: strPtr(shop.City), CountryCode: shop.CountryCode,
		ProvinceCode: strPtr(shop.ProvinceCode), Zip: strPtr(shop.Zip),
	}
}

func refundWire(order fixtureOrder, refund fixtureRefund) generated.Refund {
	lines := map[int64]fixtureLineItem{}
	for _, line := range order.LineItems {
		lines[line.ID] = line
	}
	items := make([]generated.RefundLineItem, 0, len(refund.LineItems))
	for _, line := range refund.LineItems {
		items = append(items, generated.RefundLineItem{
			Id: line.ID, LineItem: lineItemWire(lines[line.LineItemID], ""), LineItemId: line.LineItemID,
			Quantity: line.Quantity, RestockType: line.RestockType, Subtotal: line.Subtotal,
		})
	}
	gateway := refund.Gateway
	if gateway == "" {
		gateway = "manual"
	}
	transaction := generated.Transaction{
		Amount: refund.Amount, CreatedAt: refund.CreatedAt, Currency: refund.Currency, Gateway: gateway,
		Id: 8000000 + refund.ID, Kind: "refund", OrderId: refund.OrderID, Status: "success",
	}
	if refund.Authorization != "" {
		transaction.Authorization = &refund.Authorization
	}
	wire := generated.Refund{
		AdminGraphqlApiId: gid("Refund", refund.ID), CreatedAt: refund.CreatedAt, Id: refund.ID,
		OrderId: refund.OrderID, ProcessedAt: refund.CreatedAt, RefundLineItems: items,
		Transactions: []generated.Transaction{transaction},
	}
	if refund.Note != "" {
		wire.Note = &refund.Note
	}
	return wire
}

func slug(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

package shopify

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
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

const resourceVersion = "2024-10"

var shopNames = map[string]string{
	"acme-goods.myshopify.com":       "Acme Goods",
	"northwind-market.myshopify.com": "Northwind Market",
	"tinyshop.myshopify.com":         "TinyShop",
	"fernworks-studio.myshopify.com": "Fernworks Studio",
}

var compiledScenarioSchema = func() *jsonschema.Schema {
	parsed, err := jsonschema.UnmarshalJSON(bytes.NewReader(scenarioSchema))
	if err != nil {
		panic(fmt.Sprintf("shopify: parse embedded scenario schema: %v", err))
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("shopify-scenario.json", parsed); err != nil {
		panic(fmt.Sprintf("shopify: add embedded scenario schema: %v", err))
	}
	compiled, err := compiler.Compile("shopify-scenario.json")
	if err != nil {
		panic(fmt.Sprintf("shopify: compile embedded scenario schema: %v", err))
	}
	return compiled
}()

type scenarioCodec struct{}

type fixtureState struct {
	CurrentShop  string               `json:"currentShop"`
	Shops        []fixtureShop        `json:"shops"`
	Products     []fixtureProduct     `json:"products"`
	Customers    []fixtureCustomer    `json:"customers"`
	Orders       []fixtureOrder       `json:"orders"`
	Fulfillments []fixtureFulfillment `json:"fulfillments"`
	Refunds      []fixtureRefund      `json:"refunds"`
}

type fixtureShop struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	Email           string `json:"email"`
	Domain          string `json:"domain"`
	MyshopifyDomain string `json:"myshopifyDomain"`
	Currency        string `json:"currency"`
	Country         string `json:"country"`
	CountryCode     string `json:"countryCode"`
	CountryName     string `json:"countryName"`
	Province        string `json:"province,omitempty"`
	ProvinceCode    string `json:"provinceCode,omitempty"`
	City            string `json:"city,omitempty"`
	Address1        string `json:"address1,omitempty"`
	Zip             string `json:"zip,omitempty"`
	Phone           string `json:"phone"`
	ShopOwner       string `json:"shopOwner"`
	IanaTimezone    string `json:"ianaTimezone"`
	Timezone        string `json:"timezone"`
	MoneyFormat     string `json:"moneyFormat"`
	PrimaryLocale   string `json:"primaryLocale"`
	WeightUnit      string `json:"weightUnit"`
	CreatedAt       string `json:"createdAt"`
}

type fixtureProduct struct {
	ID          int64            `json:"id"`
	Shop        string           `json:"shop"`
	Title       string           `json:"title"`
	Vendor      string           `json:"vendor"`
	ProductType string           `json:"productType,omitempty"`
	Status      string           `json:"status"`
	CreatedAt   string           `json:"createdAt"`
	UpdatedAt   string           `json:"updatedAt,omitempty"`
	Variants    []fixtureVariant `json:"variants"`
}

type fixtureVariant struct {
	ID                int64  `json:"id"`
	Title             string `json:"title"`
	SKU               string `json:"sku"`
	Price             string `json:"price"`
	InventoryQuantity *int   `json:"inventoryQuantity,omitempty"`
}

type fixtureCustomer struct {
	ID        int64  `json:"id"`
	Shop      string `json:"shop"`
	Email     string `json:"email"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	Phone     string `json:"phone,omitempty"`
	CreatedAt string `json:"createdAt"`
}

type fixtureOrder struct {
	ID                int64                  `json:"id"`
	Shop              string                 `json:"shop"`
	Name              string                 `json:"name"`
	Email             string                 `json:"email,omitempty"`
	Phone             string                 `json:"phone,omitempty"`
	CreatedAt         string                 `json:"createdAt"`
	UpdatedAt         string                 `json:"updatedAt,omitempty"`
	Currency          string                 `json:"currency"`
	FinancialStatus   string                 `json:"financialStatus"`
	FulfillmentStatus string                 `json:"fulfillmentStatus,omitempty"`
	TotalPrice        string                 `json:"totalPrice"`
	SubtotalPrice     string                 `json:"subtotalPrice"`
	CustomerID        int64                  `json:"customerId"`
	LineItems         []fixtureLineItem      `json:"lineItems"`
	ShippingAddress   *fixtureAddress        `json:"shippingAddress,omitempty"`
	Note              string                 `json:"note,omitempty"`
	Gateway           string                 `json:"gateway,omitempty"`
	NoteAttributes    []fixtureNoteAttribute `json:"noteAttributes,omitempty"`
	CancelledAt       string                 `json:"cancelledAt,omitempty"`
	ClosedAt          string                 `json:"closedAt,omitempty"`
}

type fixtureLineItem struct {
	ID        int64  `json:"id"`
	VariantID int64  `json:"variantId,omitempty"`
	ProductID int64  `json:"productId,omitempty"`
	Title     string `json:"title"`
	SKU       string `json:"sku,omitempty"`
	Quantity  int    `json:"quantity"`
	Price     string `json:"price"`
}

type fixtureAddress struct {
	FirstName    string `json:"firstName,omitempty"`
	LastName     string `json:"lastName,omitempty"`
	Address1     string `json:"address1"`
	City         string `json:"city"`
	Province     string `json:"province,omitempty"`
	ProvinceCode string `json:"provinceCode"`
	Country      string `json:"country"`
	CountryCode  string `json:"countryCode"`
	Zip          string `json:"zip"`
	Phone        string `json:"phone,omitempty"`
}

type fixtureNoteAttribute struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type fixtureFulfillment struct {
	ID              int64  `json:"id"`
	Shop            string `json:"shop"`
	OrderID         int64  `json:"orderId"`
	Status          string `json:"status"`
	ShipmentStatus  string `json:"shipmentStatus,omitempty"`
	TrackingCompany string `json:"trackingCompany,omitempty"`
	TrackingNumber  string `json:"trackingNumber,omitempty"`
	TrackingURL     string `json:"trackingUrl,omitempty"`
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt,omitempty"`
}

type fixtureRefund struct {
	ID            int64               `json:"id"`
	Shop          string              `json:"shop"`
	OrderID       int64               `json:"orderId"`
	CreatedAt     string              `json:"createdAt"`
	Note          string              `json:"note,omitempty"`
	Currency      string              `json:"currency"`
	Gateway       string              `json:"gateway,omitempty"`
	Authorization string              `json:"authorization,omitempty"`
	Amount        string              `json:"amount"`
	LineItems     []fixtureRefundLine `json:"lineItems"`
}

type fixtureRefundLine struct {
	ID          int64  `json:"id"`
	LineItemID  int64  `json:"lineItemId"`
	Quantity    int    `json:"quantity"`
	RestockType string `json:"restockType"`
	Subtotal    string `json:"subtotal"`
}

func (scenarioCodec) Validate(_ context.Context, doc scenario.Document) error {
	if err := doc.ValidateEnvelope(); err != nil {
		return err
	}
	if doc.Resource != "shopify" || doc.ResourceVersion != resourceVersion {
		return fmt.Errorf("shopify scenario: expected resource shopify %s, got %s %s", resourceVersion, doc.Resource, doc.ResourceVersion)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc.State))
	if err != nil {
		return fmt.Errorf("shopify scenario: decode state for schema validation: %w", err)
	}
	if err := compiledScenarioSchema.Validate(instance); err != nil {
		return fmt.Errorf("shopify scenario: schema validation: %w", err)
	}
	state, err := decodeState(doc.State)
	if err != nil {
		return err
	}
	return validateState(state)
}

func (scenarioCodec) Initialize(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("shopify scenario: initialize: %w", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO sequences(name, value) VALUES('id', 100000) ON CONFLICT(name) DO NOTHING`); err != nil {
		return fmt.Errorf("shopify scenario: initialize sequence: %w", err)
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
		return fmt.Errorf("shopify scenario: begin load: %w", err)
	}
	defer tx.Rollback()
	for _, table := range []string{"refunds", "fulfillments", "orders", "customers", "products", "shops", "metadata"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("shopify scenario: clear %s: %w", table, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sequences(name, value) VALUES('id', 100000)
		ON CONFLICT(name) DO UPDATE SET value = 100000`); err != nil {
		return fmt.Errorf("shopify scenario: reset sequence: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO metadata(key, value) VALUES('currentShop', ?)", state.CurrentShop); err != nil {
		return fmt.Errorf("shopify scenario: insert current shop: %w", err)
	}
	for _, shop := range state.Shops {
		if err := insertBody(ctx, tx, "INSERT INTO shops(id, domain, body) VALUES(?, ?, ?)", shop.ID, shop.Domain, shop); err != nil {
			return fmt.Errorf("shopify scenario: insert shop %d: %w", shop.ID, err)
		}
	}
	for _, product := range state.Products {
		if err := insertBody(ctx, tx, "INSERT INTO products(id, shop, body) VALUES(?, ?, ?)", product.ID, product.Shop, product); err != nil {
			return fmt.Errorf("shopify scenario: insert product %d: %w", product.ID, err)
		}
	}
	for _, customer := range state.Customers {
		if err := insertBody(ctx, tx, "INSERT INTO customers(id, shop, body) VALUES(?, ?, ?)", customer.ID, customer.Shop, customer); err != nil {
			return fmt.Errorf("shopify scenario: insert customer %d: %w", customer.ID, err)
		}
	}
	for _, order := range state.Orders {
		if err := insertBody(ctx, tx, "INSERT INTO orders(id, shop, body) VALUES(?, ?, ?)", order.ID, order.Shop, order); err != nil {
			return fmt.Errorf("shopify scenario: insert order %d: %w", order.ID, err)
		}
	}
	for _, fulfillment := range state.Fulfillments {
		if err := insertBody(ctx, tx, "INSERT INTO fulfillments(id, shop, order_id, body) VALUES(?, ?, ?, ?)", fulfillment.ID, fulfillment.Shop, fulfillment.OrderID, fulfillment); err != nil {
			return fmt.Errorf("shopify scenario: insert fulfillment %d: %w", fulfillment.ID, err)
		}
	}
	for _, refund := range state.Refunds {
		if err := insertBody(ctx, tx, "INSERT INTO refunds(id, shop, order_id, body) VALUES(?, ?, ?, ?)", refund.ID, refund.Shop, refund.OrderID, refund); err != nil {
			return fmt.Errorf("shopify scenario: insert refund %d: %w", refund.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("shopify scenario: commit load: %w", err)
	}
	return nil
}

func (scenarioCodec) Dump(ctx context.Context, db *sql.DB, metadata scenario.Metadata) (scenario.Document, error) {
	var state fixtureState
	if err := db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='currentShop'").Scan(&state.CurrentShop); err != nil {
		return scenario.Document{}, fmt.Errorf("shopify scenario: dump current shop: %w", err)
	}
	var err error
	if state.Shops, err = loadAll[fixtureShop](ctx, db, "SELECT body FROM shops ORDER BY id"); err != nil {
		return scenario.Document{}, fmt.Errorf("shopify scenario: dump shops: %w", err)
	}
	if state.Products, err = loadAll[fixtureProduct](ctx, db, "SELECT body FROM products ORDER BY id"); err != nil {
		return scenario.Document{}, fmt.Errorf("shopify scenario: dump products: %w", err)
	}
	if state.Customers, err = loadAll[fixtureCustomer](ctx, db, "SELECT body FROM customers ORDER BY id"); err != nil {
		return scenario.Document{}, fmt.Errorf("shopify scenario: dump customers: %w", err)
	}
	if state.Orders, err = loadAll[fixtureOrder](ctx, db, "SELECT body FROM orders ORDER BY id"); err != nil {
		return scenario.Document{}, fmt.Errorf("shopify scenario: dump orders: %w", err)
	}
	if state.Fulfillments, err = loadAll[fixtureFulfillment](ctx, db, "SELECT body FROM fulfillments ORDER BY id"); err != nil {
		return scenario.Document{}, fmt.Errorf("shopify scenario: dump fulfillments: %w", err)
	}
	if state.Refunds, err = loadAll[fixtureRefund](ctx, db, "SELECT body FROM refunds ORDER BY id"); err != nil {
		return scenario.Document{}, fmt.Errorf("shopify scenario: dump refunds: %w", err)
	}
	state.normalize()
	raw, err := json.Marshal(state)
	if err != nil {
		return scenario.Document{}, err
	}
	return scenario.Document{
		Contract: scenario.Contract, ContractVersion: 1, ID: metadata.ID,
		Resource: "shopify", ResourceVersion: resourceVersion, State: raw,
	}, nil
}

func (state *fixtureState) normalize() {
	if state.Shops == nil {
		state.Shops = []fixtureShop{}
	}
	if state.Products == nil {
		state.Products = []fixtureProduct{}
	}
	if state.Customers == nil {
		state.Customers = []fixtureCustomer{}
	}
	if state.Orders == nil {
		state.Orders = []fixtureOrder{}
	}
	if state.Fulfillments == nil {
		state.Fulfillments = []fixtureFulfillment{}
	}
	if state.Refunds == nil {
		state.Refunds = []fixtureRefund{}
	}
	for i := range state.Products {
		if state.Products[i].Variants == nil {
			state.Products[i].Variants = []fixtureVariant{}
		}
	}
	for i := range state.Orders {
		if state.Orders[i].LineItems == nil {
			state.Orders[i].LineItems = []fixtureLineItem{}
		}
		if state.Orders[i].NoteAttributes == nil {
			state.Orders[i].NoteAttributes = []fixtureNoteAttribute{}
		}
	}
	for i := range state.Refunds {
		if state.Refunds[i].LineItems == nil {
			state.Refunds[i].LineItems = []fixtureRefundLine{}
		}
	}
}

func decodeState(raw []byte) (fixtureState, error) {
	var state fixtureState
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return fixtureState{}, fmt.Errorf("shopify scenario: decode state: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fixtureState{}, fmt.Errorf("shopify scenario: state has trailing data")
	}
	if state.Shops == nil || state.Products == nil || state.Customers == nil || state.Orders == nil || state.Fulfillments == nil || state.Refunds == nil {
		return fixtureState{}, fmt.Errorf("shopify scenario: shops, products, customers, orders, fulfillments, and refunds are required arrays")
	}
	for i := range state.Products {
		if state.Products[i].Variants == nil {
			return fixtureState{}, fmt.Errorf("shopify scenario: products[%d].variants is required", i)
		}
	}
	for i := range state.Orders {
		if state.Orders[i].LineItems == nil {
			return fixtureState{}, fmt.Errorf("shopify scenario: orders[%d].lineItems is required", i)
		}
	}
	for i := range state.Refunds {
		if state.Refunds[i].LineItems == nil {
			return fixtureState{}, fmt.Errorf("shopify scenario: refunds[%d].lineItems is required", i)
		}
	}
	return state, nil
}

func validateState(state fixtureState) error {
	shops := map[string]fixtureShop{}
	shopIDs := map[int64]struct{}{}
	for i, shop := range state.Shops {
		name, known := shopNames[shop.Domain]
		if !known {
			return fmt.Errorf("shopify scenario: shops[%d] domain %q is not a known shop", i, shop.Domain)
		}
		if shop.Name != name {
			return fmt.Errorf("shopify scenario: shops[%d] name %q, want %q", i, shop.Name, name)
		}
		if shop.MyshopifyDomain != shop.Domain {
			return fmt.Errorf("shopify scenario: shops[%d] myshopifyDomain must equal domain", i)
		}
		if shop.Currency != "INR" || shop.CountryCode != "IN" {
			return fmt.Errorf("shopify scenario: shops[%d] must use INR and country code IN", i)
		}
		if _, err := time.Parse(time.RFC3339, shop.CreatedAt); err != nil {
			return fmt.Errorf("shopify scenario: shops[%d].createdAt: %w", i, err)
		}
		if _, exists := shops[shop.Domain]; exists {
			return fmt.Errorf("shopify scenario: duplicate shop domain %q", shop.Domain)
		}
		if _, exists := shopIDs[shop.ID]; exists {
			return fmt.Errorf("shopify scenario: duplicate shop id %d", shop.ID)
		}
		shops[shop.Domain] = shop
		shopIDs[shop.ID] = struct{}{}
	}
	if _, ok := shops[state.CurrentShop]; !ok {
		return fmt.Errorf("shopify scenario: currentShop %q is not in shops", state.CurrentShop)
	}

	variants := map[int64]fixtureVariant{}
	variantShop := map[int64]string{}
	variantProduct := map[int64]int64{}
	skus := map[string]struct{}{}
	productIDs := map[int64]struct{}{}
	for i, product := range state.Products {
		if _, ok := shops[product.Shop]; !ok {
			return fmt.Errorf("shopify scenario: products[%d] shop %q is unknown", i, product.Shop)
		}
		if _, exists := productIDs[product.ID]; exists {
			return fmt.Errorf("shopify scenario: duplicate product id %d", product.ID)
		}
		productIDs[product.ID] = struct{}{}
		if _, err := time.Parse(time.RFC3339, product.CreatedAt); err != nil {
			return fmt.Errorf("shopify scenario: products[%d].createdAt: %w", i, err)
		}
		seenVariant := map[int64]struct{}{}
		for j, variant := range product.Variants {
			if _, exists := variants[variant.ID]; exists {
				return fmt.Errorf("shopify scenario: duplicate variant id %d", variant.ID)
			}
			if _, err := parseRupees(variant.Price); err != nil {
				return fmt.Errorf("shopify scenario: products[%d].variants[%d].price: %w", i, j, err)
			}
			key := product.Shop + "\x00" + variant.SKU
			if _, exists := skus[key]; exists {
				return fmt.Errorf("shopify scenario: duplicate sku %q in %s", variant.SKU, product.Shop)
			}
			skus[key] = struct{}{}
			seenVariant[variant.ID] = struct{}{}
			variants[variant.ID] = variant
			variantShop[variant.ID] = product.Shop
			variantProduct[variant.ID] = product.ID
		}
	}

	customers := map[int64]fixtureCustomer{}
	for i, customer := range state.Customers {
		if _, ok := shops[customer.Shop]; !ok {
			return fmt.Errorf("shopify scenario: customers[%d] shop %q is unknown", i, customer.Shop)
		}
		if _, exists := customers[customer.ID]; exists {
			return fmt.Errorf("shopify scenario: duplicate customer id %d", customer.ID)
		}
		if _, err := time.Parse(time.RFC3339, customer.CreatedAt); err != nil {
			return fmt.Errorf("shopify scenario: customers[%d].createdAt: %w", i, err)
		}
		customers[customer.ID] = customer
	}

	orders := map[int64]fixtureOrder{}
	lineItems := map[int64]fixtureLineItem{}
	lineOrder := map[int64]int64{}
	for i, order := range state.Orders {
		if _, ok := shops[order.Shop]; !ok {
			return fmt.Errorf("shopify scenario: orders[%d] shop %q is unknown", i, order.Shop)
		}
		if order.Name != "#"+strconv.FormatInt(order.ID, 10) {
			return fmt.Errorf("shopify scenario: orders[%d] name %q, want #%d", i, order.Name, order.ID)
		}
		if _, exists := orders[order.ID]; exists {
			return fmt.Errorf("shopify scenario: duplicate order id %d", order.ID)
		}
		customer, ok := customers[order.CustomerID]
		if !ok || customer.Shop != order.Shop {
			return fmt.Errorf("shopify scenario: order %d customer %d is not in the shop", order.ID, order.CustomerID)
		}
		if order.Email != "" && order.Email != customer.Email {
			return fmt.Errorf("shopify scenario: order %d email does not match customer %d", order.ID, order.CustomerID)
		}
		if _, err := time.Parse(time.RFC3339, order.CreatedAt); err != nil {
			return fmt.Errorf("shopify scenario: orders[%d].createdAt: %w", i, err)
		}
		var sum int64
		for j, line := range order.LineItems {
			if _, exists := lineItems[line.ID]; exists {
				return fmt.Errorf("shopify scenario: duplicate line item id %d", line.ID)
			}
			unit, err := lineTotal(line.Price, line.Quantity)
			if err != nil {
				return fmt.Errorf("shopify scenario: order %d lineItems[%d]: %w", order.ID, j, err)
			}
			sum += unit
			if line.VariantID != 0 {
				if variantShop[line.VariantID] != order.Shop {
					return fmt.Errorf("shopify scenario: order %d line %d variant %d is not in the shop", order.ID, line.ID, line.VariantID)
				}
				if line.SKU != "" && line.SKU != variants[line.VariantID].SKU {
					return fmt.Errorf("shopify scenario: order %d line %d sku %q does not match variant", order.ID, line.ID, line.SKU)
				}
				if line.ProductID != 0 && line.ProductID != variantProduct[line.VariantID] {
					return fmt.Errorf("shopify scenario: order %d line %d product does not match variant", order.ID, line.ID)
				}
			}
			lineItems[line.ID] = line
			lineOrder[line.ID] = order.ID
		}
		total, err := parseRupees(order.TotalPrice)
		if err != nil {
			return fmt.Errorf("shopify scenario: order %d totalPrice: %w", order.ID, err)
		}
		subtotal, err := parseRupees(order.SubtotalPrice)
		if err != nil {
			return fmt.Errorf("shopify scenario: order %d subtotalPrice: %w", order.ID, err)
		}
		if total != sum || subtotal != sum {
			return fmt.Errorf("shopify scenario: order %d total %d does not match line items %d", order.ID, total, sum)
		}
		orders[order.ID] = order
	}

	for i, fulfillment := range state.Fulfillments {
		order, ok := orders[fulfillment.OrderID]
		if !ok || order.Shop != fulfillment.Shop {
			return fmt.Errorf("shopify scenario: fulfillments[%d] order %d is not in the shop", i, fulfillment.OrderID)
		}
		if _, err := time.Parse(time.RFC3339, fulfillment.CreatedAt); err != nil {
			return fmt.Errorf("shopify scenario: fulfillments[%d].createdAt: %w", i, err)
		}
	}

	refundIDs := map[int64]struct{}{}
	refundLineIDs := map[int64]struct{}{}
	for i, refund := range state.Refunds {
		order, ok := orders[refund.OrderID]
		if !ok || order.Shop != refund.Shop {
			return fmt.Errorf("shopify scenario: refunds[%d] order %d is not in the shop", i, refund.OrderID)
		}
		if _, exists := refundIDs[refund.ID]; exists {
			return fmt.Errorf("shopify scenario: duplicate refund id %d", refund.ID)
		}
		refundIDs[refund.ID] = struct{}{}
		if _, err := time.Parse(time.RFC3339, refund.CreatedAt); err != nil {
			return fmt.Errorf("shopify scenario: refunds[%d].createdAt: %w", i, err)
		}
		amount, err := parseRupees(refund.Amount)
		if err != nil {
			return fmt.Errorf("shopify scenario: refund %d amount: %w", refund.ID, err)
		}
		var sum int64
		for _, line := range refund.LineItems {
			if _, exists := refundLineIDs[line.ID]; exists {
				return fmt.Errorf("shopify scenario: duplicate refund line id %d", line.ID)
			}
			refundLineIDs[line.ID] = struct{}{}
			item, ok := lineItems[line.LineItemID]
			if !ok || lineOrder[line.LineItemID] != refund.OrderID {
				return fmt.Errorf("shopify scenario: refund %d line item %d is not on the order", refund.ID, line.LineItemID)
			}
			if line.Quantity > item.Quantity {
				return fmt.Errorf("shopify scenario: refund %d quantity exceeds line %d", refund.ID, line.LineItemID)
			}
			subtotal, err := parseRupees(line.Subtotal)
			if err != nil {
				return fmt.Errorf("shopify scenario: refund %d subtotal: %w", refund.ID, err)
			}
			want, err := lineTotal(item.Price, line.Quantity)
			if err != nil {
				return err
			}
			if subtotal != want {
				return fmt.Errorf("shopify scenario: refund %d line %d subtotal does not match price", refund.ID, line.LineItemID)
			}
			sum += subtotal
		}
		if len(refund.LineItems) > 0 && amount != sum {
			return fmt.Errorf("shopify scenario: refund %d amount does not match line items", refund.ID)
		}
	}
	return nil
}

func insertBody(ctx context.Context, tx *sql.Tx, query string, args ...any) error {
	bodyIndex := len(args) - 1
	raw, err := json.Marshal(args[bodyIndex])
	if err != nil {
		return err
	}
	args[bodyIndex] = string(raw)
	_, err = tx.ExecContext(ctx, query, args...)
	return err
}

type sqlQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func loadAll[T any](ctx context.Context, db sqlQuery, query string, args ...any) ([]T, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var item T
		if err := json.Unmarshal([]byte(raw), &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func parseRupees(amount string) (int64, error) {
	whole, frac, ok := bytes.Cut([]byte(amount), []byte("."))
	if !ok || len(frac) != 2 || len(whole) == 0 {
		return 0, fmt.Errorf("invalid amount %q", amount)
	}
	for _, c := range append(append([]byte{}, whole...), frac...) {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("invalid amount %q", amount)
		}
	}
	w, err := strconv.ParseInt(string(whole), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid amount %q", amount)
	}
	f, err := strconv.ParseInt(string(frac), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid amount %q", amount)
	}
	return w*100 + f, nil
}

func lineTotal(price string, quantity int) (int64, error) {
	unit, err := parseRupees(price)
	if err != nil {
		return 0, err
	}
	return unit * int64(quantity), nil
}

func formatRupees(paise int64) string {
	sign := ""
	if paise < 0 {
		sign = "-"
		paise = -paise
	}
	return fmt.Sprintf("%s%d.%02d", sign, paise/100, paise%100)
}

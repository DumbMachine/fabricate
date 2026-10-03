package zohoinventory

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io"
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
		panic(fmt.Sprintf("zohoinventory: parse embedded scenario schema: %v", err))
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("zohoinventory-scenario.json", parsed); err != nil {
		panic(fmt.Sprintf("zohoinventory: add embedded scenario schema: %v", err))
	}
	compiled, err := compiler.Compile("zohoinventory-scenario.json")
	if err != nil {
		panic(fmt.Sprintf("zohoinventory: compile embedded scenario schema: %v", err))
	}
	return compiled
}()

type scenarioCodec struct{}

type fixtureState struct {
	Organization   fixtureOrganization    `json:"organization"`
	Warehouses     []fixtureWarehouse     `json:"warehouses"`
	Items          []fixtureItem          `json:"items"`
	SalesOrders    []fixtureSalesOrder    `json:"salesOrders"`
	PurchaseOrders []fixturePurchaseOrder `json:"purchaseOrders"`
}

type fixtureOrganization struct {
	OrganizationID       string `json:"organizationId"`
	Name                 string `json:"name"`
	ContactName          string `json:"contactName"`
	Email                string `json:"email"`
	IsDefaultOrg         bool   `json:"isDefaultOrg"`
	LanguageCode         string `json:"languageCode"`
	FiscalYearStartMonth int    `json:"fiscalYearStartMonth"`
	AccountCreatedDate   string `json:"accountCreatedDate"`
	TimeZone             string `json:"timeZone"`
	IsOrgActive          bool   `json:"isOrgActive"`
	CurrencyCode         string `json:"currencyCode"`
	CurrencySymbol       string `json:"currencySymbol"`
	CurrencyFormat       string `json:"currencyFormat"`
	PricePrecision       int    `json:"pricePrecision"`
}

type fixtureWarehouse struct {
	WarehouseID   string `json:"warehouseId"`
	WarehouseName string `json:"warehouseName"`
	Address       string `json:"address"`
	City          string `json:"city"`
	State         string `json:"state"`
	Zip           string `json:"zip"`
	Country       string `json:"country"`
	Phone         string `json:"phone"`
	Email         string `json:"email"`
	IsPrimary     bool   `json:"isPrimary"`
	Status        string `json:"status"`
}

type fixtureItem struct {
	ItemID       string `json:"itemId"`
	Name         string `json:"name"`
	SKU          string `json:"sku"`
	Description  string `json:"description"`
	Status       string `json:"status"`
	Unit         string `json:"unit"`
	Rate         int    `json:"rate"`
	PurchaseRate int    `json:"purchaseRate"`
	StockOnHand  int    `json:"stockOnHand"`
	ProductType  string `json:"productType"`
	ItemType     string `json:"itemType"`
	WarehouseID  string `json:"warehouseId"`
}

type fixtureAddress struct {
	Address string `json:"address"`
	City    string `json:"city"`
	State   string `json:"state"`
	Zip     string `json:"zip"`
	Country string `json:"country"`
}

type fixtureCustomer struct {
	CustomerID string `json:"customerId"`
	Name       string `json:"name"`
	Email      string `json:"email"`
	Phone      string `json:"phone"`
}

type fixtureSalesLine struct {
	LineItemID string `json:"lineItemId"`
	ItemID     string `json:"itemId"`
	Name       string `json:"name"`
	SKU        string `json:"sku"`
	Rate       int    `json:"rate"`
	Quantity   int    `json:"quantity"`
	ItemTotal  int    `json:"itemTotal"`
	ItemOrder  int    `json:"itemOrder"`
}

type fixtureSalesOrder struct {
	SalesorderID     string             `json:"salesorderId"`
	SalesorderNumber string             `json:"salesorderNumber"`
	ReferenceNumber  string             `json:"referenceNumber"`
	Date             string             `json:"date"`
	Status           string             `json:"status"`
	CurrencyCode     string             `json:"currencyCode"`
	Total            int                `json:"total"`
	CreatedTime      string             `json:"createdTime"`
	WarehouseID      string             `json:"warehouseId"`
	Customer         fixtureCustomer    `json:"customer"`
	ShippingAddress  fixtureAddress     `json:"shippingAddress"`
	LineItems        []fixtureSalesLine `json:"lineItems"`
}

type fixtureVendor struct {
	VendorID string `json:"vendorId"`
	Name     string `json:"name"`
}

type fixturePurchaseLine struct {
	LineItemID   string `json:"lineItemId"`
	ItemID       string `json:"itemId"`
	Name         string `json:"name"`
	SKU          string `json:"sku"`
	PurchaseRate int    `json:"purchaseRate"`
	Quantity     int    `json:"quantity"`
	ItemTotal    int    `json:"itemTotal"`
	ItemOrder    int    `json:"itemOrder"`
}

type fixtureComment struct {
	CommentID       string `json:"commentId"`
	CommentedBy     string `json:"commentedBy"`
	CommentType     string `json:"commentType"`
	OperationType   string `json:"operationType"`
	Time            string `json:"time"`
	DateDescription string `json:"dateDescription"`
}

type fixturePurchaseOrder struct {
	PurchaseorderID     string                `json:"purchaseorderId"`
	PurchaseorderNumber string                `json:"purchaseorderNumber"`
	Date                string                `json:"date"`
	Status              string                `json:"status"`
	CurrencyCode        string                `json:"currencyCode"`
	Total               int                   `json:"total"`
	CreatedTime         string                `json:"createdTime"`
	LastModifiedTime    string                `json:"lastModifiedTime"`
	WarehouseID         string                `json:"warehouseId"`
	Vendor              fixtureVendor         `json:"vendor"`
	ApproverName        string                `json:"approverName"`
	ApproverEmail       string                `json:"approverEmail"`
	LineItems           []fixturePurchaseLine `json:"lineItems"`
	Comments            []fixtureComment      `json:"comments"`
}

func (scenarioCodec) Validate(_ context.Context, doc scenario.Document) error {
	if err := doc.ValidateEnvelope(); err != nil {
		return err
	}
	if doc.Resource != "zohoinventory" || doc.ResourceVersion != "v1" {
		return fmt.Errorf("zohoinventory scenario: expected resource zohoinventory v1, got %s %s", doc.Resource, doc.ResourceVersion)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc.State))
	if err != nil {
		return fmt.Errorf("zohoinventory scenario: decode state for schema validation: %w", err)
	}
	if err := compiledScenarioSchema.Validate(instance); err != nil {
		return fmt.Errorf("zohoinventory scenario: schema validation: %w", err)
	}
	state, err := decodeState(doc.State)
	if err != nil {
		return err
	}
	if _, err := mail.ParseAddress(state.Organization.Email); err != nil {
		return fmt.Errorf("zohoinventory scenario: organization.email: %w", err)
	}
	if _, err := time.Parse("2006-01-02", state.Organization.AccountCreatedDate); err != nil {
		return fmt.Errorf("zohoinventory scenario: organization.accountCreatedDate: %w", err)
	}
	warehouses := map[string]fixtureWarehouse{}
	primary := 0
	for i, warehouse := range state.Warehouses {
		if _, err := mail.ParseAddress(warehouse.Email); err != nil {
			return fmt.Errorf("zohoinventory scenario: warehouses[%d].email: %w", i, err)
		}
		if _, exists := warehouses[warehouse.WarehouseID]; exists {
			return fmt.Errorf("zohoinventory scenario: duplicate warehouse id %q", warehouse.WarehouseID)
		}
		if warehouse.IsPrimary {
			primary++
		}
		warehouses[warehouse.WarehouseID] = warehouse
	}
	if len(state.Warehouses) > 0 && primary != 1 {
		return fmt.Errorf("zohoinventory scenario: expected one primary warehouse, got %d", primary)
	}
	items := map[string]fixtureItem{}
	skus := map[string]struct{}{}
	for i, item := range state.Items {
		if _, ok := warehouses[item.WarehouseID]; !ok {
			return fmt.Errorf("zohoinventory scenario: items[%d] references unknown warehouse %q", i, item.WarehouseID)
		}
		if _, exists := items[item.ItemID]; exists {
			return fmt.Errorf("zohoinventory scenario: duplicate item id %q", item.ItemID)
		}
		if _, exists := skus[item.SKU]; exists {
			return fmt.Errorf("zohoinventory scenario: duplicate sku %q", item.SKU)
		}
		items[item.ItemID] = item
		skus[item.SKU] = struct{}{}
	}
	salesIDs := map[string]struct{}{}
	salesNumbers := map[string]struct{}{}
	references := map[string]struct{}{}
	lineIDs := map[string]struct{}{}
	for i, order := range state.SalesOrders {
		if _, ok := warehouses[order.WarehouseID]; !ok {
			return fmt.Errorf("zohoinventory scenario: salesOrders[%d] references unknown warehouse %q", i, order.WarehouseID)
		}
		if order.CurrencyCode != state.Organization.CurrencyCode {
			return fmt.Errorf("zohoinventory scenario: salesOrders[%d] currency %s does not match the organization", i, order.CurrencyCode)
		}
		if _, err := mail.ParseAddress(order.Customer.Email); err != nil {
			return fmt.Errorf("zohoinventory scenario: salesOrders[%d].customer.email: %w", i, err)
		}
		if _, err := time.Parse("2006-01-02", order.Date); err != nil {
			return fmt.Errorf("zohoinventory scenario: salesOrders[%d].date: %w", i, err)
		}
		if _, err := time.Parse(time.RFC3339, order.CreatedTime); err != nil {
			return fmt.Errorf("zohoinventory scenario: salesOrders[%d].createdTime: %w", i, err)
		}
		if _, exists := salesIDs[order.SalesorderID]; exists {
			return fmt.Errorf("zohoinventory scenario: duplicate sales order id %q", order.SalesorderID)
		}
		if _, exists := salesNumbers[order.SalesorderNumber]; exists {
			return fmt.Errorf("zohoinventory scenario: duplicate sales order number %q", order.SalesorderNumber)
		}
		if _, exists := references[order.ReferenceNumber]; exists {
			return fmt.Errorf("zohoinventory scenario: duplicate sales order reference %q", order.ReferenceNumber)
		}
		salesIDs[order.SalesorderID] = struct{}{}
		salesNumbers[order.SalesorderNumber] = struct{}{}
		references[order.ReferenceNumber] = struct{}{}
		sum := 0
		seenOrder := map[int]struct{}{}
		for j, line := range order.LineItems {
			item, ok := items[line.ItemID]
			if !ok {
				return fmt.Errorf("zohoinventory scenario: salesOrders[%d].lineItems[%d] references unknown item %q", i, j, line.ItemID)
			}
			if line.SKU != item.SKU || line.Name != item.Name {
				return fmt.Errorf("zohoinventory scenario: salesOrders[%d].lineItems[%d] does not match item %s", i, j, item.SKU)
			}
			if line.ItemTotal != line.Rate*line.Quantity {
				return fmt.Errorf("zohoinventory scenario: salesOrders[%d].lineItems[%d] itemTotal is not rate times quantity", i, j)
			}
			if _, exists := lineIDs[line.LineItemID]; exists {
				return fmt.Errorf("zohoinventory scenario: duplicate line item id %q", line.LineItemID)
			}
			if _, exists := seenOrder[line.ItemOrder]; exists {
				return fmt.Errorf("zohoinventory scenario: salesOrders[%d] repeats itemOrder %d", i, line.ItemOrder)
			}
			lineIDs[line.LineItemID] = struct{}{}
			seenOrder[line.ItemOrder] = struct{}{}
			sum += line.ItemTotal
		}
		if order.Total != sum {
			return fmt.Errorf("zohoinventory scenario: salesOrders[%d] total %d does not match lines %d", i, order.Total, sum)
		}
	}
	poIDs := map[string]struct{}{}
	poNumbers := map[string]struct{}{}
	commentIDs := map[string]struct{}{}
	for i, order := range state.PurchaseOrders {
		if _, ok := warehouses[order.WarehouseID]; !ok {
			return fmt.Errorf("zohoinventory scenario: purchaseOrders[%d] references unknown warehouse %q", i, order.WarehouseID)
		}
		if order.CurrencyCode != state.Organization.CurrencyCode {
			return fmt.Errorf("zohoinventory scenario: purchaseOrders[%d] currency %s does not match the organization", i, order.CurrencyCode)
		}
		if _, err := mail.ParseAddress(order.ApproverEmail); err != nil {
			return fmt.Errorf("zohoinventory scenario: purchaseOrders[%d].approverEmail: %w", i, err)
		}
		if _, err := time.Parse("2006-01-02", order.Date); err != nil {
			return fmt.Errorf("zohoinventory scenario: purchaseOrders[%d].date: %w", i, err)
		}
		if _, err := time.Parse(time.RFC3339, order.CreatedTime); err != nil {
			return fmt.Errorf("zohoinventory scenario: purchaseOrders[%d].createdTime: %w", i, err)
		}
		if _, err := time.Parse(time.RFC3339, order.LastModifiedTime); err != nil {
			return fmt.Errorf("zohoinventory scenario: purchaseOrders[%d].lastModifiedTime: %w", i, err)
		}
		if _, exists := poIDs[order.PurchaseorderID]; exists {
			return fmt.Errorf("zohoinventory scenario: duplicate purchase order id %q", order.PurchaseorderID)
		}
		if _, exists := poNumbers[order.PurchaseorderNumber]; exists {
			return fmt.Errorf("zohoinventory scenario: duplicate purchase order number %q", order.PurchaseorderNumber)
		}
		poIDs[order.PurchaseorderID] = struct{}{}
		poNumbers[order.PurchaseorderNumber] = struct{}{}
		sum := 0
		seenOrder := map[int]struct{}{}
		for j, line := range order.LineItems {
			item, ok := items[line.ItemID]
			if !ok {
				return fmt.Errorf("zohoinventory scenario: purchaseOrders[%d].lineItems[%d] references unknown item %q", i, j, line.ItemID)
			}
			if line.SKU != item.SKU || line.Name != item.Name {
				return fmt.Errorf("zohoinventory scenario: purchaseOrders[%d].lineItems[%d] does not match item %s", i, j, item.SKU)
			}
			if line.ItemTotal != line.PurchaseRate*line.Quantity {
				return fmt.Errorf("zohoinventory scenario: purchaseOrders[%d].lineItems[%d] itemTotal is not purchaseRate times quantity", i, j)
			}
			if _, exists := lineIDs[line.LineItemID]; exists {
				return fmt.Errorf("zohoinventory scenario: duplicate line item id %q", line.LineItemID)
			}
			if _, exists := seenOrder[line.ItemOrder]; exists {
				return fmt.Errorf("zohoinventory scenario: purchaseOrders[%d] repeats itemOrder %d", i, line.ItemOrder)
			}
			lineIDs[line.LineItemID] = struct{}{}
			seenOrder[line.ItemOrder] = struct{}{}
			sum += line.ItemTotal
		}
		if order.Total != sum {
			return fmt.Errorf("zohoinventory scenario: purchaseOrders[%d] total %d does not match lines %d", i, order.Total, sum)
		}
		for j, comment := range order.Comments {
			if _, err := time.Parse(time.RFC3339, comment.Time); err != nil {
				return fmt.Errorf("zohoinventory scenario: purchaseOrders[%d].comments[%d].time: %w", i, j, err)
			}
			if _, exists := commentIDs[comment.CommentID]; exists {
				return fmt.Errorf("zohoinventory scenario: duplicate comment id %q", comment.CommentID)
			}
			commentIDs[comment.CommentID] = struct{}{}
		}
	}
	return nil
}

func (scenarioCodec) Initialize(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("zohoinventory scenario: initialize: %w", err)
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
		return fmt.Errorf("zohoinventory scenario: begin load: %w", err)
	}
	defer tx.Rollback()
	for _, table := range []string{
		"comments", "purchase_order_lines", "purchase_orders",
		"sales_order_lines", "sales_orders", "items", "warehouses", "organization",
	} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("zohoinventory scenario: clear %s: %w", table, err)
		}
	}
	org := state.Organization
	if _, err := tx.ExecContext(ctx, `INSERT INTO organization
		(organization_id, name, contact_name, email, is_default_org, language_code, fiscal_year_start_month,
		 account_created_date, time_zone, is_org_active, currency_code, currency_symbol, currency_format, price_precision)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		org.OrganizationID, org.Name, org.ContactName, org.Email, boolInt(org.IsDefaultOrg), org.LanguageCode,
		org.FiscalYearStartMonth, org.AccountCreatedDate, org.TimeZone, boolInt(org.IsOrgActive),
		org.CurrencyCode, org.CurrencySymbol, org.CurrencyFormat, org.PricePrecision); err != nil {
		return fmt.Errorf("zohoinventory scenario: insert organization: %w", err)
	}
	for _, warehouse := range state.Warehouses {
		if _, err := tx.ExecContext(ctx, `INSERT INTO warehouses
			(warehouse_id, warehouse_name, address, city, state, zip, country, phone, email, is_primary, status)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			warehouse.WarehouseID, warehouse.WarehouseName, warehouse.Address, warehouse.City, warehouse.State,
			warehouse.Zip, warehouse.Country, warehouse.Phone, warehouse.Email, boolInt(warehouse.IsPrimary), warehouse.Status); err != nil {
			return fmt.Errorf("zohoinventory scenario: insert warehouse %s: %w", warehouse.WarehouseID, err)
		}
	}
	for _, item := range state.Items {
		if _, err := tx.ExecContext(ctx, `INSERT INTO items
			(item_id, name, sku, description, status, unit, rate, purchase_rate, stock_on_hand, product_type, item_type, warehouse_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			item.ItemID, item.Name, item.SKU, item.Description, item.Status, item.Unit, item.Rate,
			item.PurchaseRate, item.StockOnHand, item.ProductType, item.ItemType, item.WarehouseID); err != nil {
			return fmt.Errorf("zohoinventory scenario: insert item %s: %w", item.ItemID, err)
		}
	}
	for _, order := range state.SalesOrders {
		if _, err := tx.ExecContext(ctx, `INSERT INTO sales_orders
			(salesorder_id, salesorder_number, reference_number, date, status, currency_code, total, created_time,
			 warehouse_id, customer_id, customer_name, customer_email, customer_phone,
			 ship_address, ship_city, ship_state, ship_zip, ship_country)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			order.SalesorderID, order.SalesorderNumber, order.ReferenceNumber, order.Date, order.Status,
			order.CurrencyCode, order.Total, order.CreatedTime, order.WarehouseID,
			order.Customer.CustomerID, order.Customer.Name, order.Customer.Email, order.Customer.Phone,
			order.ShippingAddress.Address, order.ShippingAddress.City, order.ShippingAddress.State,
			order.ShippingAddress.Zip, order.ShippingAddress.Country); err != nil {
			return fmt.Errorf("zohoinventory scenario: insert sales order %s: %w", order.SalesorderID, err)
		}
		for _, line := range order.LineItems {
			if _, err := tx.ExecContext(ctx, `INSERT INTO sales_order_lines
				(line_item_id, salesorder_id, item_id, name, sku, rate, quantity, item_total, item_order)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				line.LineItemID, order.SalesorderID, line.ItemID, line.Name, line.SKU, line.Rate,
				line.Quantity, line.ItemTotal, line.ItemOrder); err != nil {
				return fmt.Errorf("zohoinventory scenario: insert sales line %s: %w", line.LineItemID, err)
			}
		}
	}
	for _, order := range state.PurchaseOrders {
		if _, err := tx.ExecContext(ctx, `INSERT INTO purchase_orders
			(purchaseorder_id, purchaseorder_number, date, status, currency_code, total, created_time, last_modified_time,
			 warehouse_id, vendor_id, vendor_name, approver_name, approver_email)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			order.PurchaseorderID, order.PurchaseorderNumber, order.Date, order.Status, order.CurrencyCode,
			order.Total, order.CreatedTime, order.LastModifiedTime, order.WarehouseID,
			order.Vendor.VendorID, order.Vendor.Name, order.ApproverName, order.ApproverEmail); err != nil {
			return fmt.Errorf("zohoinventory scenario: insert purchase order %s: %w", order.PurchaseorderID, err)
		}
		for _, line := range order.LineItems {
			if _, err := tx.ExecContext(ctx, `INSERT INTO purchase_order_lines
				(line_item_id, purchaseorder_id, item_id, name, sku, purchase_rate, quantity, item_total, item_order)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				line.LineItemID, order.PurchaseorderID, line.ItemID, line.Name, line.SKU, line.PurchaseRate,
				line.Quantity, line.ItemTotal, line.ItemOrder); err != nil {
				return fmt.Errorf("zohoinventory scenario: insert purchase line %s: %w", line.LineItemID, err)
			}
		}
		for _, comment := range order.Comments {
			if _, err := tx.ExecContext(ctx, `INSERT INTO comments
				(comment_id, purchaseorder_id, commented_by, comment_type, operation_type, time, date_description)
				VALUES (?, ?, ?, ?, ?, ?, ?)`,
				comment.CommentID, order.PurchaseorderID, comment.CommentedBy, comment.CommentType,
				comment.OperationType, comment.Time, comment.DateDescription); err != nil {
				return fmt.Errorf("zohoinventory scenario: insert comment %s: %w", comment.CommentID, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("zohoinventory scenario: commit load: %w", err)
	}
	return nil
}

func (scenarioCodec) Dump(ctx context.Context, db *sql.DB, metadata scenario.Metadata) (scenario.Document, error) {
	var state fixtureState
	var defaultOrg, active int
	err := db.QueryRowContext(ctx, `SELECT organization_id, name, contact_name, email, is_default_org, language_code,
		fiscal_year_start_month, account_created_date, time_zone, is_org_active, currency_code, currency_symbol,
		currency_format, price_precision FROM organization`).Scan(
		&state.Organization.OrganizationID, &state.Organization.Name, &state.Organization.ContactName,
		&state.Organization.Email, &defaultOrg, &state.Organization.LanguageCode, &state.Organization.FiscalYearStartMonth,
		&state.Organization.AccountCreatedDate, &state.Organization.TimeZone, &active, &state.Organization.CurrencyCode,
		&state.Organization.CurrencySymbol, &state.Organization.CurrencyFormat, &state.Organization.PricePrecision)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("zohoinventory scenario: dump organization: %w", err)
	}
	state.Organization.IsDefaultOrg = defaultOrg != 0
	state.Organization.IsOrgActive = active != 0

	state.Warehouses = []fixtureWarehouse{}
	warehouseRows, err := db.QueryContext(ctx, `SELECT warehouse_id, warehouse_name, address, city, state, zip, country,
		phone, email, is_primary, status FROM warehouses ORDER BY warehouse_id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("zohoinventory scenario: dump warehouses: %w", err)
	}
	for warehouseRows.Next() {
		var warehouse fixtureWarehouse
		var primary int
		if err := warehouseRows.Scan(&warehouse.WarehouseID, &warehouse.WarehouseName, &warehouse.Address, &warehouse.City,
			&warehouse.State, &warehouse.Zip, &warehouse.Country, &warehouse.Phone, &warehouse.Email, &primary, &warehouse.Status); err != nil {
			warehouseRows.Close()
			return scenario.Document{}, err
		}
		warehouse.IsPrimary = primary != 0
		state.Warehouses = append(state.Warehouses, warehouse)
	}
	if err := warehouseRows.Close(); err != nil {
		return scenario.Document{}, err
	}

	state.Items = []fixtureItem{}
	itemRows, err := db.QueryContext(ctx, `SELECT item_id, name, sku, description, status, unit, rate, purchase_rate,
		stock_on_hand, product_type, item_type, warehouse_id FROM items ORDER BY item_id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("zohoinventory scenario: dump items: %w", err)
	}
	for itemRows.Next() {
		var item fixtureItem
		if err := itemRows.Scan(&item.ItemID, &item.Name, &item.SKU, &item.Description, &item.Status, &item.Unit,
			&item.Rate, &item.PurchaseRate, &item.StockOnHand, &item.ProductType, &item.ItemType, &item.WarehouseID); err != nil {
			itemRows.Close()
			return scenario.Document{}, err
		}
		state.Items = append(state.Items, item)
	}
	if err := itemRows.Close(); err != nil {
		return scenario.Document{}, err
	}

	state.SalesOrders = []fixtureSalesOrder{}
	salesRows, err := db.QueryContext(ctx, `SELECT salesorder_id, salesorder_number, reference_number, date, status,
		currency_code, total, created_time, warehouse_id, customer_id, customer_name, customer_email, customer_phone,
		ship_address, ship_city, ship_state, ship_zip, ship_country FROM sales_orders ORDER BY salesorder_id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("zohoinventory scenario: dump sales orders: %w", err)
	}
	for salesRows.Next() {
		var order fixtureSalesOrder
		if err := salesRows.Scan(&order.SalesorderID, &order.SalesorderNumber, &order.ReferenceNumber, &order.Date,
			&order.Status, &order.CurrencyCode, &order.Total, &order.CreatedTime, &order.WarehouseID,
			&order.Customer.CustomerID, &order.Customer.Name, &order.Customer.Email, &order.Customer.Phone,
			&order.ShippingAddress.Address, &order.ShippingAddress.City, &order.ShippingAddress.State,
			&order.ShippingAddress.Zip, &order.ShippingAddress.Country); err != nil {
			salesRows.Close()
			return scenario.Document{}, err
		}
		order.LineItems = []fixtureSalesLine{}
		state.SalesOrders = append(state.SalesOrders, order)
	}
	if err := salesRows.Close(); err != nil {
		return scenario.Document{}, err
	}
	for i := range state.SalesOrders {
		lines, err := db.QueryContext(ctx, `SELECT line_item_id, item_id, name, sku, rate, quantity, item_total, item_order
			FROM sales_order_lines WHERE salesorder_id = ? ORDER BY item_order, line_item_id`, state.SalesOrders[i].SalesorderID)
		if err != nil {
			return scenario.Document{}, err
		}
		for lines.Next() {
			var line fixtureSalesLine
			if err := lines.Scan(&line.LineItemID, &line.ItemID, &line.Name, &line.SKU, &line.Rate, &line.Quantity, &line.ItemTotal, &line.ItemOrder); err != nil {
				lines.Close()
				return scenario.Document{}, err
			}
			state.SalesOrders[i].LineItems = append(state.SalesOrders[i].LineItems, line)
		}
		if err := lines.Close(); err != nil {
			return scenario.Document{}, err
		}
	}

	state.PurchaseOrders = []fixturePurchaseOrder{}
	poRows, err := db.QueryContext(ctx, `SELECT purchaseorder_id, purchaseorder_number, date, status, currency_code, total,
		created_time, last_modified_time, warehouse_id, vendor_id, vendor_name, approver_name, approver_email
		FROM purchase_orders ORDER BY purchaseorder_id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("zohoinventory scenario: dump purchase orders: %w", err)
	}
	for poRows.Next() {
		var order fixturePurchaseOrder
		if err := poRows.Scan(&order.PurchaseorderID, &order.PurchaseorderNumber, &order.Date, &order.Status,
			&order.CurrencyCode, &order.Total, &order.CreatedTime, &order.LastModifiedTime, &order.WarehouseID,
			&order.Vendor.VendorID, &order.Vendor.Name, &order.ApproverName, &order.ApproverEmail); err != nil {
			poRows.Close()
			return scenario.Document{}, err
		}
		order.LineItems = []fixturePurchaseLine{}
		order.Comments = []fixtureComment{}
		state.PurchaseOrders = append(state.PurchaseOrders, order)
	}
	if err := poRows.Close(); err != nil {
		return scenario.Document{}, err
	}
	for i := range state.PurchaseOrders {
		lines, err := db.QueryContext(ctx, `SELECT line_item_id, item_id, name, sku, purchase_rate, quantity, item_total, item_order
			FROM purchase_order_lines WHERE purchaseorder_id = ? ORDER BY item_order, line_item_id`, state.PurchaseOrders[i].PurchaseorderID)
		if err != nil {
			return scenario.Document{}, err
		}
		for lines.Next() {
			var line fixturePurchaseLine
			if err := lines.Scan(&line.LineItemID, &line.ItemID, &line.Name, &line.SKU, &line.PurchaseRate, &line.Quantity, &line.ItemTotal, &line.ItemOrder); err != nil {
				lines.Close()
				return scenario.Document{}, err
			}
			state.PurchaseOrders[i].LineItems = append(state.PurchaseOrders[i].LineItems, line)
		}
		if err := lines.Close(); err != nil {
			return scenario.Document{}, err
		}
		comments, err := db.QueryContext(ctx, `SELECT comment_id, commented_by, comment_type, operation_type, time, date_description
			FROM comments WHERE purchaseorder_id = ? ORDER BY comment_id`, state.PurchaseOrders[i].PurchaseorderID)
		if err != nil {
			return scenario.Document{}, err
		}
		for comments.Next() {
			var comment fixtureComment
			if err := comments.Scan(&comment.CommentID, &comment.CommentedBy, &comment.CommentType, &comment.OperationType, &comment.Time, &comment.DateDescription); err != nil {
				comments.Close()
				return scenario.Document{}, err
			}
			state.PurchaseOrders[i].Comments = append(state.PurchaseOrders[i].Comments, comment)
		}
		if err := comments.Close(); err != nil {
			return scenario.Document{}, err
		}
	}

	raw, err := json.Marshal(state)
	if err != nil {
		return scenario.Document{}, err
	}
	return scenario.Document{
		Contract: ContractName(), ContractVersion: 1, ID: metadata.ID,
		Resource: "zohoinventory", ResourceVersion: "v1", State: raw,
	}, nil
}

func decodeState(raw []byte) (fixtureState, error) {
	var state fixtureState
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return fixtureState{}, fmt.Errorf("zohoinventory scenario: decode state: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fixtureState{}, fmt.Errorf("zohoinventory scenario: state has trailing data")
	}
	if state.Warehouses == nil || state.Items == nil || state.SalesOrders == nil || state.PurchaseOrders == nil {
		return fixtureState{}, fmt.Errorf("zohoinventory scenario: warehouses, items, salesOrders, and purchaseOrders are required arrays")
	}
	for i, order := range state.SalesOrders {
		if order.LineItems == nil {
			return fixtureState{}, fmt.Errorf("zohoinventory scenario: salesOrders[%d].lineItems is required", i)
		}
	}
	for i, order := range state.PurchaseOrders {
		if order.LineItems == nil || order.Comments == nil {
			return fixtureState{}, fmt.Errorf("zohoinventory scenario: purchaseOrders[%d] lineItems and comments are required arrays", i)
		}
	}
	return state, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func ContractName() string { return scenario.Contract }

package zohoinventory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/zohoinventory/generated"
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

func newServer(ctx context.Context, dependencies httpresource.ServerDependencies) (*server, error) {
	if dependencies.DB == nil || dependencies.Clock == nil || dependencies.IDs == nil || dependencies.Secrets == nil {
		return nil, fmt.Errorf("zohoinventory: database, clock, ID generator, and secrets are required")
	}
	token, err := dependencies.Secrets.Get(ctx, "token")
	if err != nil {
		return nil, fmt.Errorf("zohoinventory: load synthetic token: %w", err)
	}
	if token == "" {
		return nil, fmt.Errorf("zohoinventory: synthetic token is empty")
	}
	spec, err := generated.GetSwagger()
	if err != nil {
		return nil, fmt.Errorf("zohoinventory: load OpenAPI: %w", err)
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
			code := 5
			message := err.Error()
			if status == http.StatusUnauthorized {
				code = 57
				message = "You are not authorized to perform this operation"
			}
			writeError(w, status, code, message)
		},
	})
	impl.handler = validator(generatedHandler)
	return impl, nil
}

func (s *server) Handler() http.Handler       { return s.handler }
func (s *server) Close(context.Context) error { return nil }

func (s *server) ListOrganizations(ctx context.Context, _ generated.ListOrganizationsRequestObject) (generated.ListOrganizationsResponseObject, error) {
	org, err := s.loadOrganization(ctx)
	if err != nil {
		return nil, err
	}
	return generated.ListOrganizations200JSONResponse{
		Code: 0, Message: "success", Organizations: []generated.Organization{org.api()},
	}, nil
}

func (s *server) GetOrganization(ctx context.Context, request generated.GetOrganizationRequestObject) (generated.GetOrganizationResponseObject, error) {
	org, err := s.loadOrganization(ctx)
	if err != nil {
		return nil, err
	}
	if string(request.OrganizationId) != org.OrganizationID {
		return generated.GetOrganizationdefaultJSONResponse{Body: notFound("Organization does not exist."), StatusCode: http.StatusNotFound}, nil
	}
	return generated.GetOrganization200JSONResponse{Code: 0, Message: "success", Organization: org.api()}, nil
}

func (s *server) ListWarehouses(ctx context.Context, request generated.ListWarehousesRequestObject) (generated.ListWarehousesResponseObject, error) {
	if status, body, ok, err := s.orgFailure(ctx, string(request.Params.OrganizationId)); ok || err != nil {
		if err != nil {
			return nil, err
		}
		return generated.ListWarehousesdefaultJSONResponse{Body: body, StatusCode: status}, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+warehouseColumns+` FROM warehouses ORDER BY warehouse_name, warehouse_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	warehouses := []generated.Warehouse{}
	for rows.Next() {
		warehouse, err := scanWarehouse(rows)
		if err != nil {
			return nil, err
		}
		warehouses = append(warehouses, warehouse.api())
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	page, contextPage := paginate(warehouses, request.Params.Page, request.Params.PerPage, "Status.All", "warehouse_name")
	return generated.ListWarehouses200JSONResponse{Code: 0, Message: "success", Warehouses: page, PageContext: contextPage}, nil
}

func (s *server) GetWarehouse(ctx context.Context, request generated.GetWarehouseRequestObject) (generated.GetWarehouseResponseObject, error) {
	if status, body, ok, err := s.orgFailure(ctx, string(request.Params.OrganizationId)); ok || err != nil {
		if err != nil {
			return nil, err
		}
		return generated.GetWarehousedefaultJSONResponse{Body: body, StatusCode: status}, nil
	}
	warehouse, err := scanWarehouse(s.db.QueryRowContext(ctx, `SELECT `+warehouseColumns+` FROM warehouses WHERE warehouse_id = ?`, request.WarehouseId))
	if errors.Is(err, sql.ErrNoRows) {
		return generated.GetWarehousedefaultJSONResponse{Body: notFound("Warehouse does not exist."), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.GetWarehouse200JSONResponse{Code: 0, Message: "success", Warehouse: warehouse.api()}, nil
}

func (s *server) ListItems(ctx context.Context, request generated.ListItemsRequestObject) (generated.ListItemsResponseObject, error) {
	if status, body, ok, err := s.orgFailure(ctx, string(request.Params.OrganizationId)); ok || err != nil {
		if err != nil {
			return nil, err
		}
		return generated.ListItemsdefaultJSONResponse{Body: body, StatusCode: status}, nil
	}
	query := `SELECT ` + itemColumns + ` FROM items`
	args := []any{}
	if request.Params.Sku != nil && *request.Params.Sku != "" {
		query += ` WHERE sku = ?`
		args = append(args, *request.Params.Sku)
	}
	query += ` ORDER BY sku, item_id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []generated.Item{}
	for rows.Next() {
		item, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item.api(false, warehouseRow{}))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	page, contextPage := paginate(items, request.Params.Page, request.Params.PerPage, "Status.All", "sku")
	return generated.ListItems200JSONResponse{Code: 0, Message: "success", Items: page, PageContext: contextPage}, nil
}

func (s *server) GetItem(ctx context.Context, request generated.GetItemRequestObject) (generated.GetItemResponseObject, error) {
	if status, body, ok, err := s.orgFailure(ctx, string(request.Params.OrganizationId)); ok || err != nil {
		if err != nil {
			return nil, err
		}
		return generated.GetItemdefaultJSONResponse{Body: body, StatusCode: status}, nil
	}
	item, err := scanItem(s.db.QueryRowContext(ctx, `SELECT `+itemColumns+` FROM items WHERE item_id = ?`, request.ItemId))
	if errors.Is(err, sql.ErrNoRows) {
		return generated.GetItemdefaultJSONResponse{Body: notFound("Item does not exist."), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	warehouse, err := scanWarehouse(s.db.QueryRowContext(ctx, `SELECT `+warehouseColumns+` FROM warehouses WHERE warehouse_id = ?`, item.WarehouseID))
	if err != nil {
		return nil, err
	}
	return generated.GetItem200JSONResponse{Code: 0, Message: "success", Item: item.api(true, warehouse)}, nil
}

func (s *server) ListSalesOrders(ctx context.Context, request generated.ListSalesOrdersRequestObject) (generated.ListSalesOrdersResponseObject, error) {
	if status, body, ok, err := s.orgFailure(ctx, string(request.Params.OrganizationId)); ok || err != nil {
		if err != nil {
			return nil, err
		}
		return generated.ListSalesOrdersdefaultJSONResponse{Body: body, StatusCode: status}, nil
	}
	org, err := s.loadOrganization(ctx)
	if err != nil {
		return nil, err
	}
	orders, err := s.querySalesOrders(ctx, "", request.Params.SalesorderNumber, request.Params.ReferenceNumber)
	if err != nil {
		return nil, err
	}
	out := []generated.SalesOrder{}
	for _, order := range orders {
		out = append(out, order.api(org, false))
	}
	page, contextPage := paginate(out, request.Params.Page, request.Params.PerPage, "Status.All", "salesorder_number")
	return generated.ListSalesOrders200JSONResponse{Code: 0, Message: "success", Salesorders: page, PageContext: contextPage}, nil
}

func (s *server) GetSalesOrder(ctx context.Context, request generated.GetSalesOrderRequestObject) (generated.GetSalesOrderResponseObject, error) {
	if status, body, ok, err := s.orgFailure(ctx, string(request.Params.OrganizationId)); ok || err != nil {
		if err != nil {
			return nil, err
		}
		return generated.GetSalesOrderdefaultJSONResponse{Body: body, StatusCode: status}, nil
	}
	org, err := s.loadOrganization(ctx)
	if err != nil {
		return nil, err
	}
	orders, err := s.querySalesOrders(ctx, string(request.SalesorderId), nil, nil)
	if err != nil {
		return nil, err
	}
	if len(orders) == 0 {
		return generated.GetSalesOrderdefaultJSONResponse{Body: notFound("Sales order does not exist."), StatusCode: http.StatusNotFound}, nil
	}
	return generated.GetSalesOrder200JSONResponse{Code: 0, Message: "success", Salesorder: orders[0].api(org, true)}, nil
}

func (s *server) ListPurchaseOrders(ctx context.Context, request generated.ListPurchaseOrdersRequestObject) (generated.ListPurchaseOrdersResponseObject, error) {
	if status, body, ok, err := s.orgFailure(ctx, string(request.Params.OrganizationId)); ok || err != nil {
		if err != nil {
			return nil, err
		}
		return generated.ListPurchaseOrdersdefaultJSONResponse{Body: body, StatusCode: status}, nil
	}
	org, err := s.loadOrganization(ctx)
	if err != nil {
		return nil, err
	}
	orders, err := s.queryPurchaseOrders(ctx, "", request.Params.PurchaseorderNumber, request.Params.Status)
	if err != nil {
		return nil, err
	}
	out := []generated.PurchaseOrder{}
	for _, order := range orders {
		out = append(out, order.api(org, false))
	}
	filter := "Status.All"
	if request.Params.Status != nil && *request.Params.Status != "" {
		filter = *request.Params.Status
	}
	page, contextPage := paginate(out, request.Params.Page, request.Params.PerPage, filter, "purchaseorder_number")
	return generated.ListPurchaseOrders200JSONResponse{Code: 0, Message: "success", Purchaseorders: page, PageContext: contextPage}, nil
}

func (s *server) GetPurchaseOrder(ctx context.Context, request generated.GetPurchaseOrderRequestObject) (generated.GetPurchaseOrderResponseObject, error) {
	if status, body, ok, err := s.orgFailure(ctx, string(request.Params.OrganizationId)); ok || err != nil {
		if err != nil {
			return nil, err
		}
		return generated.GetPurchaseOrderdefaultJSONResponse{Body: body, StatusCode: status}, nil
	}
	org, err := s.loadOrganization(ctx)
	if err != nil {
		return nil, err
	}
	orders, err := s.queryPurchaseOrders(ctx, string(request.PurchaseorderId), nil, nil)
	if err != nil {
		return nil, err
	}
	if len(orders) == 0 {
		return generated.GetPurchaseOrderdefaultJSONResponse{Body: notFound("Purchase order does not exist."), StatusCode: http.StatusNotFound}, nil
	}
	return generated.GetPurchaseOrder200JSONResponse{Code: 0, Message: "success", PurchaseOrder: orders[0].api(org, true)}, nil
}

func (s *server) ApprovePurchaseOrder(ctx context.Context, request generated.ApprovePurchaseOrderRequestObject) (generated.ApprovePurchaseOrderResponseObject, error) {
	if status, body, ok, err := s.orgFailure(ctx, string(request.Params.OrganizationId)); ok || err != nil {
		if err != nil {
			return nil, err
		}
		return generated.ApprovePurchaseOrderdefaultJSONResponse{Body: body, StatusCode: status}, nil
	}
	id := string(request.PurchaseorderId)
	var status, approver string
	err := s.db.QueryRowContext(ctx, `SELECT status, approver_name FROM purchase_orders WHERE purchaseorder_id = ?`, id).Scan(&status, &approver)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.ApprovePurchaseOrderdefaultJSONResponse{Body: notFound("Purchase order does not exist."), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	if status != string(generated.PurchaseOrderStatusPendingApproval) {
		return generated.ApprovePurchaseOrderdefaultJSONResponse{
			Body: generated.Error{Code: 5, Message: "Purchase order is not pending approval."}, StatusCode: http.StatusBadRequest,
		}, nil
	}
	commentID, err := s.ids.Next(ctx, "comment")
	if err != nil {
		return nil, fmt.Errorf("zohoinventory: allocate comment id: %w", err)
	}
	now := s.clock.Now().UTC()
	stamp := now.Format(time.RFC3339)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE purchase_orders SET status = ?, last_modified_time = ? WHERE purchaseorder_id = ?`,
		string(generated.PurchaseOrderStatusApproved), stamp, id); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO comments
		(comment_id, purchaseorder_id, commented_by, comment_type, operation_type, time, date_description)
		VALUES (?, ?, ?, 'system', 'approved', ?, ?)`,
		commentID, id, approver, stamp, now.Format("2006-01-02")); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return generated.ApprovePurchaseOrder200JSONResponse{Code: 0, Message: "success"}, nil
}

func (s *server) orgFailure(ctx context.Context, organizationID string) (int, generated.Error, bool, error) {
	var got string
	err := s.db.QueryRowContext(ctx, `SELECT organization_id FROM organization`).Scan(&got)
	if err != nil {
		return 0, generated.Error{}, false, err
	}
	if organizationID != got {
		return http.StatusBadRequest, generated.Error{Code: 5, Message: "Invalid organization id."}, true, nil
	}
	return 0, generated.Error{}, false, nil
}

type orgRow struct {
	fixtureOrganization
}

func (s *server) loadOrganization(ctx context.Context) (orgRow, error) {
	var row orgRow
	var defaultOrg, active int
	err := s.db.QueryRowContext(ctx, `SELECT organization_id, name, contact_name, email, is_default_org, language_code,
		fiscal_year_start_month, account_created_date, time_zone, is_org_active, currency_code, currency_symbol,
		currency_format, price_precision FROM organization`).Scan(
		&row.OrganizationID, &row.Name, &row.ContactName, &row.Email, &defaultOrg, &row.LanguageCode,
		&row.FiscalYearStartMonth, &row.AccountCreatedDate, &row.TimeZone, &active, &row.CurrencyCode,
		&row.CurrencySymbol, &row.CurrencyFormat, &row.PricePrecision)
	if err != nil {
		return orgRow{}, err
	}
	row.IsDefaultOrg = defaultOrg != 0
	row.IsOrgActive = active != 0
	return row, nil
}

func (row orgRow) api() generated.Organization {
	return generated.Organization{
		OrganizationId: row.OrganizationID, Name: row.Name, ContactName: row.ContactName,
		Email: openapi_types.Email(row.Email), IsDefaultOrg: row.IsDefaultOrg, LanguageCode: row.LanguageCode,
		FiscalYearStartMonth: row.FiscalYearStartMonth, AccountCreatedDate: row.AccountCreatedDate,
		TimeZone: row.TimeZone, IsOrgActive: row.IsOrgActive, CurrencyCode: row.CurrencyCode,
		CurrencySymbol: row.CurrencySymbol, CurrencyFormat: row.CurrencyFormat, PricePrecision: row.PricePrecision,
	}
}

func (row orgRow) symbol(currencyCode string) string {
	if currencyCode == row.CurrencyCode {
		return row.CurrencySymbol
	}
	return currencyCode
}

const warehouseColumns = `warehouse_id, warehouse_name, address, city, state, zip, country, phone, email, is_primary, status`

type warehouseRow struct {
	fixtureWarehouse
}

func scanWarehouse(row interface{ Scan(...any) error }) (warehouseRow, error) {
	var warehouse warehouseRow
	var primary int
	err := row.Scan(&warehouse.WarehouseID, &warehouse.WarehouseName, &warehouse.Address, &warehouse.City,
		&warehouse.State, &warehouse.Zip, &warehouse.Country, &warehouse.Phone, &warehouse.Email, &primary, &warehouse.Status)
	if err != nil {
		return warehouseRow{}, err
	}
	warehouse.IsPrimary = primary != 0
	return warehouse, nil
}

func (row warehouseRow) api() generated.Warehouse {
	return generated.Warehouse{
		WarehouseId: row.WarehouseID, WarehouseName: row.WarehouseName, Address: row.Address, City: row.City,
		State: row.State, Zip: row.Zip, Country: row.Country, Phone: row.Phone, Email: row.Email,
		IsPrimary: row.IsPrimary, Status: generated.WarehouseStatus(row.Status),
	}
}

const itemColumns = `item_id, name, sku, description, status, unit, rate, purchase_rate, stock_on_hand, product_type, item_type, warehouse_id`

type itemRow struct {
	fixtureItem
}

func scanItem(row interface{ Scan(...any) error }) (itemRow, error) {
	var item itemRow
	err := row.Scan(&item.ItemID, &item.Name, &item.SKU, &item.Description, &item.Status, &item.Unit, &item.Rate,
		&item.PurchaseRate, &item.StockOnHand, &item.ProductType, &item.ItemType, &item.WarehouseID)
	return item, err
}

func (row itemRow) api(withWarehouse bool, warehouse warehouseRow) generated.Item {
	description := row.Description
	item := generated.Item{
		ItemId: row.ItemID, Name: row.Name, Sku: row.SKU, Description: &description,
		Status: generated.ItemStatus(row.Status), Unit: row.Unit, Rate: float64(row.Rate),
		PurchaseRate: float64(row.PurchaseRate), StockOnHand: float64(row.StockOnHand),
		ProductType: generated.ItemProductType(row.ProductType), ItemType: generated.ItemItemType(row.ItemType),
	}
	if withWarehouse {
		warehouses := []generated.ItemWarehouse{{
			WarehouseId: warehouse.WarehouseID, WarehouseName: warehouse.WarehouseName,
			Status: generated.ItemWarehouseStatus(warehouse.Status), IsPrimary: warehouse.IsPrimary,
			WarehouseStockOnHand: float64(row.StockOnHand),
		}}
		item.Warehouses = &warehouses
	}
	return item
}

type salesOrderRow struct {
	fixtureSalesOrder
}

func (s *server) querySalesOrders(ctx context.Context, id string, number, reference *string) ([]salesOrderRow, error) {
	query := `SELECT salesorder_id, salesorder_number, reference_number, date, status, currency_code, total, created_time,
		warehouse_id, customer_id, customer_name, customer_email, customer_phone, ship_address, ship_city, ship_state, ship_zip, ship_country
		FROM sales_orders WHERE 1 = 1`
	args := []any{}
	if id != "" {
		query += ` AND salesorder_id = ?`
		args = append(args, id)
	}
	if number != nil && *number != "" {
		query += ` AND salesorder_number = ?`
		args = append(args, *number)
	}
	if reference != nil && *reference != "" {
		query += ` AND reference_number = ?`
		args = append(args, *reference)
	}
	query += ` ORDER BY salesorder_number, salesorder_id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	orders := []salesOrderRow{}
	for rows.Next() {
		var order salesOrderRow
		if err := rows.Scan(&order.SalesorderID, &order.SalesorderNumber, &order.ReferenceNumber, &order.Date, &order.Status,
			&order.CurrencyCode, &order.Total, &order.CreatedTime, &order.WarehouseID, &order.Customer.CustomerID,
			&order.Customer.Name, &order.Customer.Email, &order.Customer.Phone, &order.ShippingAddress.Address,
			&order.ShippingAddress.City, &order.ShippingAddress.State, &order.ShippingAddress.Zip, &order.ShippingAddress.Country); err != nil {
			return nil, err
		}
		order.LineItems = []fixtureSalesLine{}
		orders = append(orders, order)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range orders {
		lines, err := s.db.QueryContext(ctx, `SELECT l.line_item_id, l.item_id, l.name, l.sku, l.rate, l.quantity, l.item_total, l.item_order
			FROM sales_order_lines l WHERE l.salesorder_id = ? ORDER BY l.item_order, l.line_item_id`, orders[i].SalesorderID)
		if err != nil {
			return nil, err
		}
		for lines.Next() {
			var line fixtureSalesLine
			if err := lines.Scan(&line.LineItemID, &line.ItemID, &line.Name, &line.SKU, &line.Rate, &line.Quantity, &line.ItemTotal, &line.ItemOrder); err != nil {
				lines.Close()
				return nil, err
			}
			orders[i].LineItems = append(orders[i].LineItems, line)
		}
		if err := lines.Close(); err != nil {
			return nil, err
		}
	}
	return orders, nil
}

func (row salesOrderRow) api(org orgRow, detail bool) generated.SalesOrder {
	order := generated.SalesOrder{
		SalesorderId: row.SalesorderID, SalesorderNumber: row.SalesorderNumber, ReferenceNumber: row.ReferenceNumber,
		CustomerId: row.Customer.CustomerID, CustomerName: row.Customer.Name, Status: generated.SalesOrderStatus(row.Status),
		Date: row.Date, CurrencyCode: row.CurrencyCode, CurrencySymbol: org.symbol(row.CurrencyCode),
		Total: float64(row.Total), CreatedTime: row.CreatedTime,
	}
	if !detail {
		return order
	}
	lines := make([]generated.SalesOrderLine, 0, len(row.LineItems))
	for _, line := range row.LineItems {
		lines = append(lines, generated.SalesOrderLine{
			LineItemId: line.LineItemID, ItemId: line.ItemID, Name: line.Name, Sku: line.SKU,
			Rate: float64(line.Rate), Quantity: float64(line.Quantity), ItemTotal: float64(line.ItemTotal),
			ItemOrder: line.ItemOrder, Unit: "qty", WarehouseId: row.WarehouseID,
		})
	}
	order.LineItems = &lines
	order.ShippingAddress = &generated.Address{
		Address: row.ShippingAddress.Address, City: row.ShippingAddress.City, State: row.ShippingAddress.State,
		Zip: row.ShippingAddress.Zip, Country: row.ShippingAddress.Country,
	}
	people := []generated.ContactPerson{{
		ContactPersonId: row.Customer.CustomerID, ContactPersonName: row.Customer.Name,
		ContactPersonEmail: row.Customer.Email, Phone: row.Customer.Phone,
	}}
	order.ContactPersonsAssociated = &people
	return order
}

type purchaseOrderRow struct {
	fixturePurchaseOrder
}

func (s *server) queryPurchaseOrders(ctx context.Context, id string, number, status *string) ([]purchaseOrderRow, error) {
	query := `SELECT purchaseorder_id, purchaseorder_number, date, status, currency_code, total, created_time, last_modified_time,
		warehouse_id, vendor_id, vendor_name, approver_name, approver_email FROM purchase_orders WHERE 1 = 1`
	args := []any{}
	if id != "" {
		query += ` AND purchaseorder_id = ?`
		args = append(args, id)
	}
	if number != nil && *number != "" {
		query += ` AND purchaseorder_number = ?`
		args = append(args, *number)
	}
	if status != nil && *status != "" {
		query += ` AND status = ?`
		args = append(args, *status)
	}
	query += ` ORDER BY purchaseorder_number, purchaseorder_id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	orders := []purchaseOrderRow{}
	for rows.Next() {
		var order purchaseOrderRow
		if err := rows.Scan(&order.PurchaseorderID, &order.PurchaseorderNumber, &order.Date, &order.Status, &order.CurrencyCode,
			&order.Total, &order.CreatedTime, &order.LastModifiedTime, &order.WarehouseID, &order.Vendor.VendorID,
			&order.Vendor.Name, &order.ApproverName, &order.ApproverEmail); err != nil {
			return nil, err
		}
		order.LineItems = []fixturePurchaseLine{}
		order.Comments = []fixtureComment{}
		orders = append(orders, order)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range orders {
		lines, err := s.db.QueryContext(ctx, `SELECT line_item_id, item_id, name, sku, purchase_rate, quantity, item_total, item_order
			FROM purchase_order_lines WHERE purchaseorder_id = ? ORDER BY item_order, line_item_id`, orders[i].PurchaseorderID)
		if err != nil {
			return nil, err
		}
		for lines.Next() {
			var line fixturePurchaseLine
			if err := lines.Scan(&line.LineItemID, &line.ItemID, &line.Name, &line.SKU, &line.PurchaseRate, &line.Quantity, &line.ItemTotal, &line.ItemOrder); err != nil {
				lines.Close()
				return nil, err
			}
			orders[i].LineItems = append(orders[i].LineItems, line)
		}
		if err := lines.Close(); err != nil {
			return nil, err
		}
		comments, err := s.db.QueryContext(ctx, `SELECT comment_id, commented_by, comment_type, operation_type, time, date_description
			FROM comments WHERE purchaseorder_id = ? ORDER BY comment_id`, orders[i].PurchaseorderID)
		if err != nil {
			return nil, err
		}
		for comments.Next() {
			var comment fixtureComment
			if err := comments.Scan(&comment.CommentID, &comment.CommentedBy, &comment.CommentType, &comment.OperationType, &comment.Time, &comment.DateDescription); err != nil {
				comments.Close()
				return nil, err
			}
			orders[i].Comments = append(orders[i].Comments, comment)
		}
		if err := comments.Close(); err != nil {
			return nil, err
		}
	}
	return orders, nil
}

func (row purchaseOrderRow) api(org orgRow, detail bool) generated.PurchaseOrder {
	order := generated.PurchaseOrder{
		PurchaseorderId: row.PurchaseorderID, PurchaseorderNumber: row.PurchaseorderNumber,
		VendorId: row.Vendor.VendorID, VendorName: row.Vendor.Name, Status: generated.PurchaseOrderStatus(row.Status),
		Date: row.Date, CurrencyCode: row.CurrencyCode, CurrencySymbol: org.symbol(row.CurrencyCode),
		Total: float64(row.Total), CreatedTime: row.CreatedTime, LastModifiedTime: row.LastModifiedTime,
		CustomFields: []generated.CustomField{
			{CustomfieldId: "cf-approver-name", Label: "Approver", Value: row.ApproverName},
			{CustomfieldId: "cf-approver-email", Label: "Approver email", Value: row.ApproverEmail},
		},
	}
	if !detail {
		return order
	}
	lines := make([]generated.PurchaseOrderLine, 0, len(row.LineItems))
	for _, line := range row.LineItems {
		lines = append(lines, generated.PurchaseOrderLine{
			LineItemId: line.LineItemID, ItemId: line.ItemID, Name: line.Name, Sku: line.SKU,
			PurchaseRate: float64(line.PurchaseRate), Quantity: float64(line.Quantity), ItemTotal: float64(line.ItemTotal),
			ItemOrder: line.ItemOrder, Unit: "qty", WarehouseId: row.WarehouseID,
		})
	}
	comments := make([]generated.Comment, 0, len(row.Comments))
	for _, comment := range row.Comments {
		comments = append(comments, generated.Comment{
			CommentId: comment.CommentID, PurchaseorderId: row.PurchaseorderID, CommentedBy: comment.CommentedBy,
			CommentType: comment.CommentType, DateDescription: comment.DateDescription, Time: comment.Time,
			OperationType: comment.OperationType, TransactionId: row.PurchaseorderID, TransactionType: "purchaseorder",
		})
	}
	order.LineItems = &lines
	order.Comments = &comments
	return order
}

func paginate[T any](items []T, page, perPage *int, filter, sortColumn string) ([]T, generated.PageContext) {
	pageNum := 1
	if page != nil && *page > 0 {
		pageNum = *page
	}
	per := 200
	if perPage != nil && *perPage > 0 {
		per = *perPage
	}
	start := (pageNum - 1) * per
	if start > len(items) {
		start = len(items)
	}
	end := start + per
	if end > len(items) {
		end = len(items)
	}
	window := items[start:end]
	if window == nil {
		window = []T{}
	}
	return window, generated.PageContext{
		Page: pageNum, PerPage: per, HasMorePage: end < len(items),
		AppliedFilter: filter, SortColumn: sortColumn, SortOrder: "A",
	}
}

func notFound(message string) generated.Error {
	return generated.Error{Code: 1002, Message: message}
}

func writeError(w http.ResponseWriter, status, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(generated.Error{Code: code, Message: message})
}

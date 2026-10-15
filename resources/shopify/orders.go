package shopify

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/dumbmachine/fabricate/resources/shopify/generated"
)

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

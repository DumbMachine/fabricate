package shopify

import (
	"context"
	"fmt"
	"strings"

	"github.com/dumbmachine/fabricate/resources/shopify/generated"
)

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

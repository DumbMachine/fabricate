package redshift

import "github.com/dumbmachine/fabricate/resources/redshift/generated"

// columnDef is one seeded column. The SQL surface reads only these columns.
type columnDef struct {
	Name     string
	TypeName string
	Nullable bool
}

type tableDef struct {
	Name    string
	Columns []columnDef
}

const (
	typeVarchar   = "varchar"
	typeInt8      = "int8"
	typeTimestamp = "timestamp"
	typeBool      = "bool"
)

var (
	orderColumns = []columnDef{
		{Name: "order_id", TypeName: typeVarchar},
		{Name: "placed_at", TypeName: typeTimestamp},
		{Name: "customer_name", TypeName: typeVarchar},
		{Name: "customer_email", TypeName: typeVarchar},
		{Name: "customer_phone", TypeName: typeVarchar},
		{Name: "ship_to", TypeName: typeVarchar},
		{Name: "lines", TypeName: typeVarchar},
		{Name: "merchandise_inr", TypeName: typeInt8},
		{Name: "merchandise_paise", TypeName: typeInt8},
		{Name: "currency", TypeName: typeVarchar},
		{Name: "shop", TypeName: typeVarchar},
		{Name: "status", TypeName: typeVarchar},
	}
	paymentColumns = []columnDef{
		{Name: "payment_id", TypeName: typeVarchar},
		{Name: "order_id", TypeName: typeVarchar},
		{Name: "razorpay_order_id", TypeName: typeVarchar, Nullable: true},
		{Name: "status", TypeName: typeVarchar},
		{Name: "method", TypeName: typeVarchar, Nullable: true},
		{Name: "amount_paise", TypeName: typeInt8},
		{Name: "currency", TypeName: typeVarchar},
		{Name: "refund_id", TypeName: typeVarchar, Nullable: true},
		{Name: "captured", TypeName: typeBool},
	}
	shipmentColumns = []columnDef{
		{Name: "order_id", TypeName: typeVarchar},
		{Name: "awb", TypeName: typeVarchar, Nullable: true},
		{Name: "courier", TypeName: typeVarchar, Nullable: true},
		{Name: "status", TypeName: typeVarchar},
		{Name: "ndr_reason", TypeName: typeVarchar, Nullable: true},
		{Name: "return_id", TypeName: typeVarchar, Nullable: true},
		{Name: "channel", TypeName: typeVarchar},
	}
	saasInvoiceColumns = []columnDef{
		{Name: "invoice_id", TypeName: typeVarchar},
		{Name: "customer_name", TypeName: typeVarchar},
		{Name: "contact_email", TypeName: typeVarchar},
		{Name: "amount_usd", TypeName: typeInt8},
		{Name: "currency", TypeName: typeVarchar},
		{Name: "status", TypeName: typeVarchar},
		{Name: "payment_id", TypeName: typeVarchar},
		{Name: "duplicate_payment_id", TypeName: typeVarchar},
		{Name: "hubspot_deal_id", TypeName: typeVarchar},
		{Name: "chargebee_subscription", TypeName: typeVarchar},
		{Name: "chargebee_customer", TypeName: typeVarchar},
	}
)

func seededTables() map[string]tableDef {
	tables := []tableDef{
		{Name: "orders", Columns: orderColumns},
		{Name: "payments", Columns: paymentColumns},
		{Name: "shipments", Columns: shipmentColumns},
		{Name: "saas_invoices", Columns: saasInvoiceColumns},
	}
	out := make(map[string]tableDef, len(tables))
	for _, table := range tables {
		out[table.Name] = table
	}
	return out
}

func (c columnDef) metadata(schemaName, tableName string) generated.ColumnMetadata {
	nullable := 0
	if c.Nullable {
		nullable = 1
	}
	meta := generated.ColumnMetadata{
		IsCaseSensitive: false,
		IsCurrency:      false,
		IsSigned:        false,
		Label:           c.Name,
		Name:            c.Name,
		Nullable:        nullable,
		SchemaName:      schemaName,
		TableName:       tableName,
		TypeName:        c.TypeName,
	}
	switch c.TypeName {
	case typeVarchar:
		meta.IsCaseSensitive = true
		meta.Length = 256
		meta.Precision = 256
	case typeTimestamp:
		meta.Length = 29
		meta.Precision = 29
		meta.Scale = 6
	case typeInt8:
		meta.IsSigned = true
		meta.Length = 8
		meta.Precision = 19
	case typeBool:
		meta.Length = 1
		meta.Precision = 1
	}
	return meta
}

type orderRow struct {
	OrderID          string `json:"order_id"`
	PlacedAt         string `json:"placed_at"`
	CustomerName     string `json:"customer_name"`
	CustomerEmail    string `json:"customer_email"`
	CustomerPhone    string `json:"customer_phone"`
	ShipTo           string `json:"ship_to"`
	Lines            string `json:"lines"`
	MerchandiseINR   int64  `json:"merchandise_inr"`
	MerchandisePaise int64  `json:"merchandise_paise"`
	Currency         string `json:"currency"`
	Shop             string `json:"shop"`
	Status           string `json:"status"`
}

type paymentRow struct {
	PaymentID       string  `json:"payment_id"`
	OrderID         string  `json:"order_id"`
	RazorpayOrderID *string `json:"razorpay_order_id"`
	Status          string  `json:"status"`
	Method          *string `json:"method"`
	AmountPaise     int64   `json:"amount_paise"`
	Currency        string  `json:"currency"`
	RefundID        *string `json:"refund_id"`
	Captured        bool    `json:"captured"`
}

type shipmentRow struct {
	OrderID   string  `json:"order_id"`
	AWB       *string `json:"awb"`
	Courier   *string `json:"courier"`
	Status    string  `json:"status"`
	NDRReason *string `json:"ndr_reason"`
	ReturnID  *string `json:"return_id"`
	Channel   string  `json:"channel"`
}

type saasInvoiceRow struct {
	InvoiceID             string `json:"invoice_id"`
	CustomerName          string `json:"customer_name"`
	ContactEmail          string `json:"contact_email"`
	AmountUSD             int64  `json:"amount_usd"`
	Currency              string `json:"currency"`
	Status                string `json:"status"`
	PaymentID             string `json:"payment_id"`
	DuplicatePaymentID    string `json:"duplicate_payment_id"`
	HubspotDealID         string `json:"hubspot_deal_id"`
	ChargebeeSubscription string `json:"chargebee_subscription"`
	ChargebeeCustomer     string `json:"chargebee_customer"`
}

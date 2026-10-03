package shiprocket

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/mail"
	"strings"

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
		panic(fmt.Sprintf("shiprocket: parse embedded scenario schema: %v", err))
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("shiprocket-scenario.json", parsed); err != nil {
		panic(fmt.Sprintf("shiprocket: add embedded scenario schema: %v", err))
	}
	compiled, err := compiler.Compile("shiprocket-scenario.json")
	if err != nil {
		panic(fmt.Sprintf("shiprocket: compile embedded scenario schema: %v", err))
	}
	return compiled
}()

type scenarioCodec struct{}

type fixtureState struct {
	CompanyID int              `json:"companyId"`
	User      fixtureUser      `json:"user"`
	Couriers  []fixtureCourier `json:"couriers"`
	Pickups   []fixturePickup  `json:"pickups"`
	Orders    []fixtureOrder   `json:"orders"`
	Returns   []fixtureReturn  `json:"returns"`
}

type fixtureUser struct {
	ID        int    `json:"id"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	Email     string `json:"email"`
	CreatedAt string `json:"createdAt"`
}

type fixtureCourier struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type fixturePickup struct {
	ID       string `json:"id"`
	Location string `json:"location"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Phone    string `json:"phone"`
	Address  string `json:"address"`
	Address2 string `json:"address2"`
	City     string `json:"city"`
	State    string `json:"state"`
	Country  string `json:"country"`
	Pincode  string `json:"pincode"`
}

type fixtureItem struct {
	Name         string `json:"name"`
	SKU          string `json:"sku"`
	Units        int    `json:"units"`
	SellingPrice int    `json:"sellingPrice"`
}

type fixtureOrder struct {
	ID             string        `json:"id"`
	ChannelOrderID string        `json:"channelOrderId"`
	ChannelName    string        `json:"channelName"`
	Status         string        `json:"status"`
	Awb            string        `json:"awb"`
	CourierID      int           `json:"courierId"`
	CourierName    string        `json:"courierName"`
	ShipmentID     string        `json:"shipmentId"`
	CustomerName   string        `json:"customerName"`
	CustomerEmail  string        `json:"customerEmail"`
	CustomerPhone  string        `json:"customerPhone"`
	Address        string        `json:"address"`
	Address2       string        `json:"address2"`
	City           string        `json:"city"`
	State          string        `json:"state"`
	Pincode        string        `json:"pincode"`
	Country        string        `json:"country"`
	PickupLocation string        `json:"pickupLocation"`
	PaymentMethod  string        `json:"paymentMethod"`
	PaymentStatus  string        `json:"paymentStatus"`
	SubTotal       int           `json:"subTotal"`
	WeightGrams    int           `json:"weightGrams,omitempty"`
	LengthCm       int           `json:"lengthCm,omitempty"`
	BreadthCm      int           `json:"breadthCm,omitempty"`
	HeightCm       int           `json:"heightCm,omitempty"`
	OrderDate      string        `json:"orderDate"`
	NdrReason      string        `json:"ndrReason"`
	NdrAttempts    int           `json:"ndrAttempts"`
	NdrAction      string        `json:"ndrAction,omitempty"`
	NdrComments    string        `json:"ndrComments,omitempty"`
	AssignedAt     string        `json:"assignedAt,omitempty"`
	Items          []fixtureItem `json:"items"`
}

type fixtureReturn struct {
	ID                    string        `json:"id"`
	OrderID               string        `json:"orderId"`
	ShipmentID            string        `json:"shipmentId"`
	ChannelName           string        `json:"channelName"`
	Status                string        `json:"status"`
	ForwardShipmentStatus string        `json:"forwardShipmentStatus"`
	CustomerName          string        `json:"customerName"`
	CustomerEmail         string        `json:"customerEmail"`
	CustomerPhone         string        `json:"customerPhone"`
	Address               string        `json:"address"`
	Address2              string        `json:"address2"`
	City                  string        `json:"city"`
	State                 string        `json:"state"`
	Pincode               string        `json:"pincode"`
	Country               string        `json:"country"`
	PickupLocation        string        `json:"pickupLocation"`
	SubTotal              int           `json:"subTotal"`
	OrderDate             string        `json:"orderDate"`
	Awb                   string        `json:"awb"`
	CourierName           string        `json:"courierName"`
	Items                 []fixtureItem `json:"items"`
}

func (scenarioCodec) Validate(_ context.Context, doc scenario.Document) error {
	if err := doc.ValidateEnvelope(); err != nil {
		return err
	}
	if doc.Resource != "shiprocket" || doc.ResourceVersion != "v1" {
		return fmt.Errorf("shiprocket scenario: expected resource shiprocket v1, got %s %s", doc.Resource, doc.ResourceVersion)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc.State))
	if err != nil {
		return fmt.Errorf("shiprocket scenario: decode state for schema validation: %w", err)
	}
	if err := compiledScenarioSchema.Validate(instance); err != nil {
		return fmt.Errorf("shiprocket scenario: schema validation: %w", err)
	}
	state, err := decodeState(doc.State)
	if err != nil {
		return err
	}
	if err := parseEmail("user.email", state.User.Email); err != nil {
		return err
	}
	couriers := map[int]string{}
	courierNames := map[string]struct{}{}
	for _, courier := range state.Couriers {
		if _, exists := couriers[courier.ID]; exists {
			return fmt.Errorf("shiprocket scenario: duplicate courier id %d", courier.ID)
		}
		if _, exists := courierNames[courier.Name]; exists {
			return fmt.Errorf("shiprocket scenario: duplicate courier name %q", courier.Name)
		}
		couriers[courier.ID] = courier.Name
		courierNames[courier.Name] = struct{}{}
	}
	pickups := map[string]struct{}{}
	pickupIDs := map[string]struct{}{}
	for _, pickup := range state.Pickups {
		if _, exists := pickupIDs[pickup.ID]; exists {
			return fmt.Errorf("shiprocket scenario: duplicate pickup id %q", pickup.ID)
		}
		if _, exists := pickups[pickup.Location]; exists {
			return fmt.Errorf("shiprocket scenario: duplicate pickup location %q", pickup.Location)
		}
		if err := parseEmail("pickups.email", pickup.Email); err != nil {
			return err
		}
		pickupIDs[pickup.ID] = struct{}{}
		pickups[pickup.Location] = struct{}{}
	}
	seenOrders := map[string]struct{}{}
	seenChannels := map[string]struct{}{}
	seenShipments := map[string]struct{}{}
	seenAWB := map[string]struct{}{}
	for i, order := range state.Orders {
		if _, exists := seenOrders[order.ID]; exists {
			return fmt.Errorf("shiprocket scenario: duplicate order id %q", order.ID)
		}
		if _, exists := seenChannels[order.ChannelOrderID]; exists {
			return fmt.Errorf("shiprocket scenario: duplicate channel order id %q", order.ChannelOrderID)
		}
		if _, exists := seenShipments[order.ShipmentID]; exists {
			return fmt.Errorf("shiprocket scenario: duplicate shipment id %q", order.ShipmentID)
		}
		seenOrders[order.ID] = struct{}{}
		seenChannels[order.ChannelOrderID] = struct{}{}
		seenShipments[order.ShipmentID] = struct{}{}
		if order.Awb != "" {
			if _, exists := seenAWB[order.Awb]; exists {
				return fmt.Errorf("shiprocket scenario: duplicate awb %q", order.Awb)
			}
			seenAWB[order.Awb] = struct{}{}
		}
		if err := parseEmail(fmt.Sprintf("orders[%d].customerEmail", i), order.CustomerEmail); err != nil {
			return err
		}
		if _, ok := pickups[order.PickupLocation]; !ok && len(state.Pickups) > 0 {
			return fmt.Errorf("shiprocket scenario: order %q references unknown pickup %q", order.ID, order.PickupLocation)
		}
		name, ok := couriers[order.CourierID]
		switch {
		case order.CourierID == 0:
			if order.CourierName != "" || order.Awb != "" {
				return fmt.Errorf("shiprocket scenario: order %q has a courier or AWB without courierId", order.ID)
			}
		case !ok:
			return fmt.Errorf("shiprocket scenario: order %q references unknown courier id %d", order.ID, order.CourierID)
		case name != order.CourierName:
			return fmt.Errorf("shiprocket scenario: order %q courier name %q does not match id %d", order.ID, order.CourierName, order.CourierID)
		}
		if err := validateOrderStatus(order); err != nil {
			return err
		}
		if err := validateItems(fmt.Sprintf("orders[%d]", i), order.Items, order.SubTotal); err != nil {
			return err
		}
	}
	seenReturns := map[string]struct{}{}
	seenReturnOrders := map[string]struct{}{}
	for i, item := range state.Returns {
		if _, exists := seenReturns[item.ID]; exists {
			return fmt.Errorf("shiprocket scenario: duplicate return id %q", item.ID)
		}
		if _, exists := seenReturnOrders[item.OrderID]; exists {
			return fmt.Errorf("shiprocket scenario: duplicate return order id %q", item.OrderID)
		}
		if _, exists := seenShipments[item.ShipmentID]; exists {
			return fmt.Errorf("shiprocket scenario: duplicate shipment id %q", item.ShipmentID)
		}
		seenReturns[item.ID] = struct{}{}
		seenReturnOrders[item.OrderID] = struct{}{}
		seenShipments[item.ShipmentID] = struct{}{}
		if item.Awb != "" {
			if _, exists := seenAWB[item.Awb]; exists {
				return fmt.Errorf("shiprocket scenario: duplicate awb %q", item.Awb)
			}
			seenAWB[item.Awb] = struct{}{}
		}
		if item.Status != "initiated" {
			return fmt.Errorf("shiprocket scenario: return %q status must be initiated", item.ID)
		}
		if item.ForwardShipmentStatus != "" && item.ForwardShipmentStatus != "cancelled" {
			return fmt.Errorf("shiprocket scenario: return %q forwardShipmentStatus must be cancelled or empty", item.ID)
		}
		if err := parseEmail(fmt.Sprintf("returns[%d].customerEmail", i), item.CustomerEmail); err != nil {
			return err
		}
		if _, ok := pickups[item.PickupLocation]; !ok && len(state.Pickups) > 0 {
			return fmt.Errorf("shiprocket scenario: return %q references unknown pickup %q", item.ID, item.PickupLocation)
		}
		if err := validateItems(fmt.Sprintf("returns[%d]", i), item.Items, item.SubTotal); err != nil {
			return err
		}
	}
	return nil
}

func validateOrderStatus(order fixtureOrder) error {
	switch order.Status {
	case "in_transit", "delivered", "ready_to_ship":
		if order.Awb == "" || order.CourierID == 0 {
			return fmt.Errorf("shiprocket scenario: order %q status %s requires an AWB and courier", order.ID, order.Status)
		}
	case "ndr":
		if order.Awb == "" || order.CourierID == 0 {
			return fmt.Errorf("shiprocket scenario: order %q status ndr requires an AWB and courier", order.ID)
		}
		if order.NdrReason == "" || order.NdrAttempts < 1 {
			return fmt.Errorf("shiprocket scenario: order %q status ndr requires a reason and attempts", order.ID)
		}
	case "NEW":
		if order.Awb != "" || order.CourierID != 0 || order.CourierName != "" {
			return fmt.Errorf("shiprocket scenario: order %q status NEW has no AWB", order.ID)
		}
	case "cancelled":
	default:
		return fmt.Errorf("shiprocket scenario: order %q has unknown status %q", order.ID, order.Status)
	}
	if order.Status != "ndr" && (order.NdrReason != "" || order.NdrAttempts != 0 || order.NdrAction != "") {
		return fmt.Errorf("shiprocket scenario: order %q records an NDR without status ndr", order.ID)
	}
	if order.PaymentMethod != "Prepaid" && order.PaymentMethod != "COD" {
		return fmt.Errorf("shiprocket scenario: order %q payment method must be Prepaid or COD", order.ID)
	}
	return nil
}

func validateItems(where string, items []fixtureItem, subTotal int) error {
	if len(items) == 0 {
		return fmt.Errorf("shiprocket scenario: %s requires items", where)
	}
	sum := 0
	for i, item := range items {
		if item.Name == "" || item.SKU == "" || item.Units < 1 || item.SellingPrice < 0 {
			return fmt.Errorf("shiprocket scenario: %s.items[%d] is invalid", where, i)
		}
		sum += item.Units * item.SellingPrice
	}
	if sum != subTotal {
		return fmt.Errorf("shiprocket scenario: %s subTotal %d does not match line items %d", where, subTotal, sum)
	}
	return nil
}

func (scenarioCodec) Initialize(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("shiprocket scenario: initialize: %w", err)
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
		return fmt.Errorf("shiprocket scenario: begin load: %w", err)
	}
	defer tx.Rollback()
	for _, table := range []string{"orders", "returns", "pickups", "couriers", "account"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("shiprocket scenario: clear %s: %w", table, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO account
		(id, company_id, user_id, first_name, last_name, email, created_at)
		VALUES (1, ?, ?, ?, ?, ?, ?)`, state.CompanyID, state.User.ID, state.User.FirstName, state.User.LastName, state.User.Email, state.User.CreatedAt); err != nil {
		return fmt.Errorf("shiprocket scenario: insert account: %w", err)
	}
	for _, courier := range state.Couriers {
		if _, err := tx.ExecContext(ctx, "INSERT INTO couriers(id, name) VALUES(?, ?)", courier.ID, courier.Name); err != nil {
			return fmt.Errorf("shiprocket scenario: insert courier %d: %w", courier.ID, err)
		}
	}
	for _, pickup := range state.Pickups {
		if _, err := tx.ExecContext(ctx, `INSERT INTO pickups
			(id, location, name, email, phone, address, address_2, city, state, country, pincode)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, pickup.ID, pickup.Location, pickup.Name, pickup.Email, pickup.Phone,
			pickup.Address, pickup.Address2, pickup.City, pickup.State, pickup.Country, pickup.Pincode); err != nil {
			return fmt.Errorf("shiprocket scenario: insert pickup %s: %w", pickup.ID, err)
		}
	}
	for _, order := range state.Orders {
		if err := insertOrder(ctx, tx, order); err != nil {
			return err
		}
	}
	for _, item := range state.Returns {
		if err := insertReturn(ctx, tx, item); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("shiprocket scenario: commit load: %w", err)
	}
	return nil
}

func (scenarioCodec) Dump(ctx context.Context, db *sql.DB, metadata scenario.Metadata) (scenario.Document, error) {
	var state fixtureState
	err := db.QueryRowContext(ctx, `SELECT company_id, user_id, first_name, last_name, email, created_at FROM account WHERE id=1`).
		Scan(&state.CompanyID, &state.User.ID, &state.User.FirstName, &state.User.LastName, &state.User.Email, &state.User.CreatedAt)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("shiprocket scenario: dump account: %w", err)
	}
	state.Couriers, err = loadCouriers(ctx, db)
	if err != nil {
		return scenario.Document{}, err
	}
	state.Pickups, err = loadPickups(ctx, db)
	if err != nil {
		return scenario.Document{}, err
	}
	state.Orders, err = loadOrders(ctx, db)
	if err != nil {
		return scenario.Document{}, err
	}
	state.Returns, err = loadReturns(ctx, db)
	if err != nil {
		return scenario.Document{}, err
	}
	if state.Couriers == nil {
		state.Couriers = []fixtureCourier{}
	}
	if state.Pickups == nil {
		state.Pickups = []fixturePickup{}
	}
	if state.Orders == nil {
		state.Orders = []fixtureOrder{}
	}
	if state.Returns == nil {
		state.Returns = []fixtureReturn{}
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return scenario.Document{}, err
	}
	return scenario.Document{
		Contract: scenario.Contract, ContractVersion: 1, ID: metadata.ID,
		Resource: "shiprocket", ResourceVersion: "v1", State: raw,
	}, nil
}

func decodeState(raw []byte) (fixtureState, error) {
	var state fixtureState
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return fixtureState{}, fmt.Errorf("shiprocket scenario: decode state: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fixtureState{}, fmt.Errorf("shiprocket scenario: state has trailing data")
	}
	if state.Couriers == nil || state.Pickups == nil || state.Orders == nil || state.Returns == nil {
		return fixtureState{}, fmt.Errorf("shiprocket scenario: couriers, pickups, orders, and returns are required arrays")
	}
	for i := range state.Orders {
		if state.Orders[i].Items == nil {
			return fixtureState{}, fmt.Errorf("shiprocket scenario: orders[%d].items is required", i)
		}
	}
	for i := range state.Returns {
		if state.Returns[i].Items == nil {
			return fixtureState{}, fmt.Errorf("shiprocket scenario: returns[%d].items is required", i)
		}
	}
	return state, nil
}

type sqlExec interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

const orderColumns = `id, channel_order_id, channel_name, status, awb, courier_id, courier_name, shipment_id,
	customer_name, customer_email, customer_phone, address, address_2, city, state, pincode, country,
	pickup_location, payment_method, payment_status, sub_total, weight_grams, length_cm, breadth_cm, height_cm,
	order_date, ndr_reason, ndr_attempts, ndr_action, ndr_comments, assigned_at, items_json`

func insertOrder(ctx context.Context, db sqlExec, order fixtureOrder) error {
	if order.Items == nil {
		order.Items = []fixtureItem{}
	}
	items, err := json.Marshal(order.Items)
	if err != nil {
		return fmt.Errorf("shiprocket scenario: encode order %s items: %w", order.ID, err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO orders (`+orderColumns+`) VALUES (`+placeholders(32)+`)`,
		order.ID, order.ChannelOrderID, order.ChannelName, order.Status, order.Awb, order.CourierID, order.CourierName, order.ShipmentID,
		order.CustomerName, order.CustomerEmail, order.CustomerPhone, order.Address, order.Address2, order.City, order.State, order.Pincode, order.Country,
		order.PickupLocation, order.PaymentMethod, order.PaymentStatus, order.SubTotal, order.WeightGrams, order.LengthCm, order.BreadthCm, order.HeightCm,
		order.OrderDate, order.NdrReason, order.NdrAttempts, order.NdrAction, order.NdrComments, order.AssignedAt, string(items))
	if err != nil {
		return fmt.Errorf("shiprocket scenario: insert order %s: %w", order.ID, err)
	}
	return nil
}

func loadOrders(ctx context.Context, db sqlExec) ([]fixtureOrder, error) {
	rows, err := db.QueryContext(ctx, "SELECT "+orderColumns+" FROM orders ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("shiprocket scenario: dump orders: %w", err)
	}
	defer rows.Close()
	out := []fixtureOrder{}
	for rows.Next() {
		order, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, order)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func scanOrder(row interface{ Scan(...any) error }) (fixtureOrder, error) {
	var order fixtureOrder
	var items string
	err := row.Scan(&order.ID, &order.ChannelOrderID, &order.ChannelName, &order.Status, &order.Awb, &order.CourierID, &order.CourierName, &order.ShipmentID,
		&order.CustomerName, &order.CustomerEmail, &order.CustomerPhone, &order.Address, &order.Address2, &order.City, &order.State, &order.Pincode, &order.Country,
		&order.PickupLocation, &order.PaymentMethod, &order.PaymentStatus, &order.SubTotal, &order.WeightGrams, &order.LengthCm, &order.BreadthCm, &order.HeightCm,
		&order.OrderDate, &order.NdrReason, &order.NdrAttempts, &order.NdrAction, &order.NdrComments, &order.AssignedAt, &items)
	if err != nil {
		return fixtureOrder{}, err
	}
	if err := json.Unmarshal([]byte(items), &order.Items); err != nil {
		return fixtureOrder{}, fmt.Errorf("shiprocket scenario: decode order %s items: %w", order.ID, err)
	}
	if order.Items == nil {
		order.Items = []fixtureItem{}
	}
	return order, nil
}

func insertReturn(ctx context.Context, db sqlExec, item fixtureReturn) error {
	if item.Items == nil {
		item.Items = []fixtureItem{}
	}
	items, err := json.Marshal(item.Items)
	if err != nil {
		return fmt.Errorf("shiprocket scenario: encode return %s items: %w", item.ID, err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO returns
		(id, order_id, shipment_id, channel_name, status, forward_shipment_status, customer_name, customer_email, customer_phone,
		 address, address_2, city, state, pincode, country, pickup_location, sub_total, order_date, awb, courier_name, items_json)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID, item.OrderID, item.ShipmentID, item.ChannelName, item.Status, item.ForwardShipmentStatus, item.CustomerName, item.CustomerEmail, item.CustomerPhone,
		item.Address, item.Address2, item.City, item.State, item.Pincode, item.Country, item.PickupLocation, item.SubTotal, item.OrderDate, item.Awb, item.CourierName, string(items))
	if err != nil {
		return fmt.Errorf("shiprocket scenario: insert return %s: %w", item.ID, err)
	}
	return nil
}

func loadReturns(ctx context.Context, db sqlExec) ([]fixtureReturn, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, order_id, shipment_id, channel_name, status, forward_shipment_status,
		customer_name, customer_email, customer_phone, address, address_2, city, state, pincode, country, pickup_location,
		sub_total, order_date, awb, courier_name, items_json FROM returns ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("shiprocket scenario: dump returns: %w", err)
	}
	defer rows.Close()
	out := []fixtureReturn{}
	for rows.Next() {
		item, err := scanReturn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func scanReturn(row interface{ Scan(...any) error }) (fixtureReturn, error) {
	var item fixtureReturn
	var items string
	err := row.Scan(&item.ID, &item.OrderID, &item.ShipmentID, &item.ChannelName, &item.Status, &item.ForwardShipmentStatus,
		&item.CustomerName, &item.CustomerEmail, &item.CustomerPhone, &item.Address, &item.Address2, &item.City, &item.State, &item.Pincode, &item.Country, &item.PickupLocation,
		&item.SubTotal, &item.OrderDate, &item.Awb, &item.CourierName, &items)
	if err != nil {
		return fixtureReturn{}, err
	}
	if err := json.Unmarshal([]byte(items), &item.Items); err != nil {
		return fixtureReturn{}, fmt.Errorf("shiprocket scenario: decode return %s items: %w", item.ID, err)
	}
	if item.Items == nil {
		item.Items = []fixtureItem{}
	}
	return item, nil
}

func loadCouriers(ctx context.Context, db sqlExec) ([]fixtureCourier, error) {
	rows, err := db.QueryContext(ctx, "SELECT id, name FROM couriers ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("shiprocket scenario: dump couriers: %w", err)
	}
	defer rows.Close()
	out := []fixtureCourier{}
	for rows.Next() {
		var courier fixtureCourier
		if err := rows.Scan(&courier.ID, &courier.Name); err != nil {
			return nil, err
		}
		out = append(out, courier)
	}
	return out, rows.Err()
}

func loadPickups(ctx context.Context, db sqlExec) ([]fixturePickup, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, location, name, email, phone, address, address_2, city, state, country, pincode
		FROM pickups ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("shiprocket scenario: dump pickups: %w", err)
	}
	defer rows.Close()
	out := []fixturePickup{}
	for rows.Next() {
		var pickup fixturePickup
		if err := rows.Scan(&pickup.ID, &pickup.Location, &pickup.Name, &pickup.Email, &pickup.Phone, &pickup.Address, &pickup.Address2, &pickup.City, &pickup.State, &pickup.Country, &pickup.Pincode); err != nil {
			return nil, err
		}
		out = append(out, pickup)
	}
	return out, rows.Err()
}

func parseEmail(field, value string) error {
	if _, err := mail.ParseAddress(value); err != nil {
		return fmt.Errorf("shiprocket scenario: %s: %w", field, err)
	}
	return nil
}

func placeholders(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = "?"
	}
	return strings.Join(parts, ", ")
}

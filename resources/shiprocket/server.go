package shiprocket

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/mail"
	"strconv"
	"strings"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/shiprocket/generated"
	"github.com/getkin/kin-openapi/openapi3filter"
	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

type server struct {
	db      *sql.DB
	clock   httpresource.Clock
	ids     httpresource.IDGenerator
	token   string
	handler http.Handler
}

var _ generated.StrictServerInterface = (*server)(nil)

type clientError struct {
	status  int
	message string
}

func (e clientError) Error() string { return e.message }

func newServer(ctx context.Context, dependencies httpresource.ServerDependencies) (*server, error) {
	if dependencies.DB == nil || dependencies.Clock == nil || dependencies.IDs == nil || dependencies.Secrets == nil {
		return nil, fmt.Errorf("shiprocket: database, clock, ID generator, and secrets are required")
	}
	token, err := dependencies.Secrets.Get(ctx, "token")
	if err != nil {
		return nil, fmt.Errorf("shiprocket: load synthetic token: %w", err)
	}
	if token == "" {
		return nil, fmt.Errorf("shiprocket: synthetic token is empty")
	}
	spec, err := generated.GetSwagger()
	if err != nil {
		return nil, fmt.Errorf("shiprocket: load OpenAPI: %w", err)
	}
	impl := &server{db: dependencies.DB, clock: dependencies.Clock, ids: dependencies.IDs, token: token}
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
			message := err.Error()
			if status == http.StatusUnauthorized {
				message = "Unauthorized"
			}
			writeError(w, status, message)
		},
	})
	impl.handler = validator(generatedHandler)
	return impl, nil
}

func (s *server) Handler() http.Handler       { return s.handler }
func (s *server) Close(context.Context) error { return nil }

func (s *server) Login(ctx context.Context, request generated.LoginRequestObject) (generated.LoginResponseObject, error) {
	if request.Body == nil {
		return generated.LogindefaultJSONResponse{Body: errorBody(http.StatusBadRequest, "request body is required"), StatusCode: http.StatusBadRequest}, nil
	}
	companyID, user, err := loadAccount(ctx, s.db)
	if err != nil {
		return nil, err
	}
	return generated.Login200JSONResponse{
		Id: user.ID, CompanyId: companyID, Email: openapi_types.Email(user.Email),
		FirstName: user.FirstName, LastName: user.LastName, CreatedAt: user.CreatedAt, Token: s.token,
	}, nil
}

func (s *server) ListOrders(ctx context.Context, request generated.ListOrdersRequestObject) (generated.ListOrdersResponseObject, error) {
	orders, err := loadOrders(ctx, s.db)
	if err != nil {
		return nil, err
	}
	search := ""
	if request.Params.Search != nil {
		search = *request.Params.Search
	}
	filtered := make([]fixtureOrder, 0, len(orders))
	for _, order := range orders {
		if matchesSearch(order, search) {
			filtered = append(filtered, order)
		}
	}
	page, perPage := pageParams(request.Params.Page, request.Params.PerPage)
	window, meta := paginate(filtered, page, perPage)
	data := make([]generated.Order, 0, len(window))
	for _, order := range window {
		data = append(data, orderView(order))
	}
	return generated.ListOrders200JSONResponse{Data: data, Meta: generated.Meta{Pagination: meta}}, nil
}

func (s *server) GetOrder(ctx context.Context, request generated.GetOrderRequestObject) (generated.GetOrderResponseObject, error) {
	order, err := findOrder(ctx, s.db, string(request.Id))
	if errors.Is(err, sql.ErrNoRows) {
		return generated.GetOrderdefaultJSONResponse{Body: errorBody(http.StatusNotFound, "order not found"), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.GetOrder200JSONResponse{Data: orderView(order)}, nil
}

func (s *server) CreateAdhocOrder(ctx context.Context, request generated.CreateAdhocOrderRequestObject) (generated.CreateAdhocOrderResponseObject, error) {
	if request.Body == nil {
		return generated.CreateAdhocOrderdefaultJSONResponse{Body: errorBody(http.StatusBadRequest, "request body is required"), StatusCode: http.StatusBadRequest}, nil
	}
	if err := s.ensureChannelOrderAvailable(ctx, request.Body.OrderId); err != nil {
		var client clientError
		if errors.As(err, &client) {
			return generated.CreateAdhocOrderdefaultJSONResponse{Body: errorBody(client.status, client.message), StatusCode: client.status}, nil
		}
		return nil, err
	}
	order, err := s.newOrder(ctx, request.Body)
	if err != nil {
		var client clientError
		if errors.As(err, &client) {
			return generated.CreateAdhocOrderdefaultJSONResponse{Body: errorBody(client.status, client.message), StatusCode: client.status}, nil
		}
		return nil, err
	}
	if err := insertOrder(ctx, s.db, order); err != nil {
		return nil, err
	}
	return generated.CreateAdhocOrder200JSONResponse{
		OrderId: order.ID, ChannelOrderId: order.ChannelOrderID, ShipmentId: order.ShipmentID,
		Status: order.Status, StatusCode: statusCode(order.Status), OnboardingCompletedNow: 0,
		AwbCode: "", CourierCompanyId: 0, CourierName: "",
	}, nil
}

func (s *server) CancelOrders(ctx context.Context, request generated.CancelOrdersRequestObject) (generated.CancelOrdersResponseObject, error) {
	if request.Body == nil || len(request.Body.Ids) == 0 {
		return generated.CancelOrdersdefaultJSONResponse{Body: errorBody(http.StatusBadRequest, "ids is required"), StatusCode: http.StatusBadRequest}, nil
	}
	orders := make([]fixtureOrder, 0, len(request.Body.Ids))
	for _, id := range request.Body.Ids {
		order, err := findOrder(ctx, s.db, id)
		if errors.Is(err, sql.ErrNoRows) {
			return generated.CancelOrdersdefaultJSONResponse{Body: errorBody(http.StatusNotFound, "order not found"), StatusCode: http.StatusNotFound}, nil
		}
		if err != nil {
			return nil, err
		}
		orders = append(orders, order)
	}
	for _, order := range orders {
		if _, err := s.db.ExecContext(ctx, "UPDATE orders SET status=? WHERE id=?", "cancelled", order.ID); err != nil {
			return nil, err
		}
	}
	return generated.CancelOrders200JSONResponse{StatusCode: http.StatusOK, Message: "Order cancelled successfully."}, nil
}

func (s *server) CreateReturnOrder(ctx context.Context, request generated.CreateReturnOrderRequestObject) (generated.CreateReturnOrderResponseObject, error) {
	if request.Body == nil {
		return generated.CreateReturnOrderdefaultJSONResponse{Body: errorBody(http.StatusBadRequest, "request body is required"), StatusCode: http.StatusBadRequest}, nil
	}
	if err := s.ensureReturnOrderAvailable(ctx, request.Body.OrderId); err != nil {
		var client clientError
		if errors.As(err, &client) {
			return generated.CreateReturnOrderdefaultJSONResponse{Body: errorBody(client.status, client.message), StatusCode: client.status}, nil
		}
		return nil, err
	}
	item, err := s.newReturn(ctx, request.Body)
	if err != nil {
		var client clientError
		if errors.As(err, &client) {
			return generated.CreateReturnOrderdefaultJSONResponse{Body: errorBody(client.status, client.message), StatusCode: client.status}, nil
		}
		return nil, err
	}
	if err := insertReturn(ctx, s.db, item); err != nil {
		return nil, err
	}
	return generated.CreateReturnOrder200JSONResponse{
		Id: item.ID, OrderId: item.OrderID, ShipmentId: item.ShipmentID,
		Status: item.Status, StatusCode: statusCode(item.Status), ForwardShipmentStatus: item.ForwardShipmentStatus,
	}, nil
}

func (s *server) ListReturnOrders(ctx context.Context, request generated.ListReturnOrdersRequestObject) (generated.ListReturnOrdersResponseObject, error) {
	items, err := loadReturns(ctx, s.db)
	if err != nil {
		return nil, err
	}
	page, perPage := pageParams(request.Params.Page, request.Params.PerPage)
	window, meta := paginate(items, page, perPage)
	data := make([]generated.ReturnOrder, 0, len(window))
	for _, item := range window {
		data = append(data, returnView(item))
	}
	return generated.ListReturnOrders200JSONResponse{Data: data, Meta: generated.Meta{Pagination: meta}}, nil
}

func (s *server) ListCouriers(ctx context.Context, request generated.ListCouriersRequestObject) (generated.ListCouriersResponseObject, error) {
	couriers, err := loadCouriers(ctx, s.db)
	if err != nil {
		return nil, err
	}
	include := true
	if request.Params.Type != nil && *request.Params.Type == generated.Inactive {
		include = false
	}
	data := []generated.Courier{}
	if include {
		for _, courier := range couriers {
			data = append(data, generated.Courier{Id: courier.ID, Name: courier.Name, Status: 1})
		}
	}
	return generated.ListCouriers200JSONResponse{TotalCourierCount: len(data), CourierCount: len(data), CourierData: data}, nil
}

func (s *server) CheckServiceability(ctx context.Context, _ generated.CheckServiceabilityRequestObject) (generated.CheckServiceabilityResponseObject, error) {
	couriers, err := loadCouriers(ctx, s.db)
	if err != nil {
		return nil, err
	}
	companies := []generated.ServiceableCourier{}
	for _, courier := range couriers {
		rate := float32(45)
		days := "3"
		if courier.Name == "Bluedart" {
			rate = 80
			days = "2"
		}
		companies = append(companies, generated.ServiceableCourier{
			CourierCompanyId: courier.ID, CourierName: courier.Name, Rate: rate, EstimatedDeliveryDays: days,
		})
	}
	response := generated.CheckServiceability200JSONResponse{Status: http.StatusOK}
	response.Data.AvailableCourierCompanies = companies
	return response, nil
}

func (s *server) AssignAwb(ctx context.Context, request generated.AssignAwbRequestObject) (generated.AssignAwbResponseObject, error) {
	if request.Body == nil || request.Body.ShipmentId == "" {
		return generated.AssignAwbdefaultJSONResponse{Body: errorBody(http.StatusBadRequest, "shipment_id is required"), StatusCode: http.StatusBadRequest}, nil
	}
	order, err := orderByShipment(ctx, s.db, request.Body.ShipmentId)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.AssignAwbdefaultJSONResponse{Body: errorBody(http.StatusNotFound, "shipment not found"), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	if order.Awb != "" {
		return generated.AssignAwbdefaultJSONResponse{Body: errorBody(http.StatusUnprocessableEntity, "AWB already assigned"), StatusCode: http.StatusUnprocessableEntity}, nil
	}
	couriers, err := loadCouriers(ctx, s.db)
	if err != nil {
		return nil, err
	}
	courierID := 0
	if request.Body.CourierId != nil {
		courierID = *request.Body.CourierId
	}
	courier, err := pickCourier(couriers, courierID)
	if err != nil {
		return generated.AssignAwbdefaultJSONResponse{Body: errorBody(http.StatusUnprocessableEntity, err.Error()), StatusCode: http.StatusUnprocessableEntity}, nil
	}
	awb, err := s.ids.Next(ctx, "shiprocket.awb")
	if err != nil {
		return nil, fmt.Errorf("shiprocket: allocate AWB: %w", err)
	}
	assigned := s.clock.Now().UTC().Format("2006-01-02 15:04")
	if _, err := s.db.ExecContext(ctx, `UPDATE orders SET awb=?, courier_id=?, courier_name=?, status=?, assigned_at=? WHERE id=?`,
		awb, courier.ID, courier.Name, "ready_to_ship", assigned, order.ID); err != nil {
		return nil, err
	}
	response := generated.AssignAwb200JSONResponse{AwbAssignStatus: 1}
	response.Response.Data = generated.AwbAssignment{
		AwbCode: awb, CourierCompanyId: courier.ID, CourierName: courier.Name,
		ShipmentId: order.ShipmentID, OrderId: order.ID, ChannelOrderId: order.ChannelOrderID,
	}
	return response, nil
}

func (s *server) TrackByAwb(ctx context.Context, request generated.TrackByAwbRequestObject) (generated.TrackByAwbResponseObject, error) {
	order, err := orderByAwb(ctx, s.db, string(request.AwbCode))
	if errors.Is(err, sql.ErrNoRows) {
		return generated.TrackByAwbdefaultJSONResponse{Body: errorBody(http.StatusNotFound, "AWB not found"), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	view, err := s.trackingView(ctx, order)
	if err != nil {
		return nil, err
	}
	return generated.TrackByAwb200JSONResponse(view), nil
}

func (s *server) TrackByShipment(ctx context.Context, request generated.TrackByShipmentRequestObject) (generated.TrackByShipmentResponseObject, error) {
	order, err := orderByShipment(ctx, s.db, string(request.ShipmentId))
	if errors.Is(err, sql.ErrNoRows) {
		return generated.TrackByShipmentdefaultJSONResponse{Body: errorBody(http.StatusNotFound, "shipment not found"), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	view, err := s.trackingView(ctx, order)
	if err != nil {
		return nil, err
	}
	return generated.TrackByShipment200JSONResponse(view), nil
}

func (s *server) ListNdr(ctx context.Context, request generated.ListNdrRequestObject) (generated.ListNdrResponseObject, error) {
	orders, err := loadOrders(ctx, s.db)
	if err != nil {
		return nil, err
	}
	matched := []fixtureOrder{}
	for _, order := range orders {
		if order.Status == "ndr" {
			matched = append(matched, order)
		}
	}
	page, perPage := pageParams(request.Params.Page, request.Params.PerPage)
	window, meta := paginate(matched, page, perPage)
	data := make([]generated.NdrShipment, 0, len(window))
	for _, order := range window {
		data = append(data, ndrView(order))
	}
	return generated.ListNdr200JSONResponse{Data: data, Meta: generated.Meta{Pagination: meta}}, nil
}

func (s *server) GetNdr(ctx context.Context, request generated.GetNdrRequestObject) (generated.GetNdrResponseObject, error) {
	order, err := s.ndrByAwb(ctx, string(request.Awb))
	if errors.Is(err, sql.ErrNoRows) {
		return generated.GetNdrdefaultJSONResponse{Body: errorBody(http.StatusNotFound, "NDR shipment not found"), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	return generated.GetNdr200JSONResponse{Data: ndrView(order)}, nil
}

func (s *server) ActOnNdr(ctx context.Context, request generated.ActOnNdrRequestObject) (generated.ActOnNdrResponseObject, error) {
	if request.Body == nil {
		return generated.ActOnNdrdefaultJSONResponse{Body: errorBody(http.StatusBadRequest, "request body is required"), StatusCode: http.StatusBadRequest}, nil
	}
	order, err := orderByAwb(ctx, s.db, string(request.Awb))
	if errors.Is(err, sql.ErrNoRows) {
		return generated.ActOnNdrdefaultJSONResponse{Body: errorBody(http.StatusNotFound, "NDR shipment not found"), StatusCode: http.StatusNotFound}, nil
	}
	if err != nil {
		return nil, err
	}
	if order.Status != "ndr" {
		return generated.ActOnNdrdefaultJSONResponse{Body: errorBody(http.StatusUnprocessableEntity, "shipment is not in NDR"), StatusCode: http.StatusUnprocessableEntity}, nil
	}
	comments := ""
	if request.Body.Comments != nil {
		comments = *request.Body.Comments
	}
	attempts := order.NdrAttempts
	if request.Body.Action == generated.ReAttempt {
		attempts++
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE orders SET ndr_action=?, ndr_comments=?, ndr_attempts=? WHERE id=?`,
		string(request.Body.Action), comments, attempts, order.ID); err != nil {
		return nil, err
	}
	order.NdrAction = string(request.Body.Action)
	order.NdrComments = comments
	order.NdrAttempts = attempts
	return generated.ActOnNdr200JSONResponse{Data: ndrView(order)}, nil
}

func (s *server) ListPickups(ctx context.Context, _ generated.ListPickupsRequestObject) (generated.ListPickupsResponseObject, error) {
	pickups, err := loadPickups(ctx, s.db)
	if err != nil {
		return nil, err
	}
	addresses := []generated.PickupAddress{}
	for _, pickup := range pickups {
		addresses = append(addresses, generated.PickupAddress{
			Id: pickup.ID, PickupLocation: pickup.Location, Name: pickup.Name, Email: pickup.Email, Phone: pickup.Phone,
			Address: pickup.Address, Address2: pickup.Address2, City: pickup.City, State: pickup.State, Country: pickup.Country, PinCode: pickup.Pincode,
		})
	}
	response := generated.ListPickups200JSONResponse{}
	response.Data.ShippingAddress = addresses
	return response, nil
}

func (s *server) newOrder(ctx context.Context, body *generated.CreateOrderRequest) (fixtureOrder, error) {
	draft, err := s.draftShipment(ctx, body)
	if err != nil {
		return fixtureOrder{}, err
	}
	id, err := s.ids.Next(ctx, "shiprocket.order")
	if err != nil {
		return fixtureOrder{}, fmt.Errorf("shiprocket: allocate order ID: %w", err)
	}
	shipmentID, err := s.ids.Next(ctx, "shiprocket.shipment")
	if err != nil {
		return fixtureOrder{}, fmt.Errorf("shiprocket: allocate shipment ID: %w", err)
	}
	draft.order.ID = id
	draft.order.ShipmentID = shipmentID
	draft.order.ChannelOrderID = body.OrderId
	draft.order.ChannelName = "CUSTOM"
	draft.order.Status = "NEW"
	return draft.order, nil
}

func (s *server) newReturn(ctx context.Context, body *generated.CreateOrderRequest) (fixtureReturn, error) {
	draft, err := s.draftShipment(ctx, body)
	if err != nil {
		return fixtureReturn{}, err
	}
	id, err := s.ids.Next(ctx, "shiprocket.return")
	if err != nil {
		return fixtureReturn{}, fmt.Errorf("shiprocket: allocate return ID: %w", err)
	}
	shipmentID, err := s.ids.Next(ctx, "shiprocket.shipment")
	if err != nil {
		return fixtureReturn{}, fmt.Errorf("shiprocket: allocate shipment ID: %w", err)
	}
	return fixtureReturn{
		ID: id, OrderID: body.OrderId, ShipmentID: shipmentID, ChannelName: "CUSTOM", Status: "initiated",
		CustomerName: draft.order.CustomerName, CustomerEmail: draft.order.CustomerEmail, CustomerPhone: draft.order.CustomerPhone,
		Address: draft.order.Address, Address2: draft.order.Address2, City: draft.order.City, State: draft.order.State,
		Pincode: draft.order.Pincode, Country: draft.order.Country, PickupLocation: draft.order.PickupLocation,
		SubTotal: draft.order.SubTotal, OrderDate: draft.order.OrderDate, Items: draft.order.Items,
	}, nil
}

type shipmentDraft struct {
	order fixtureOrder
}

func (s *server) draftShipment(ctx context.Context, body *generated.CreateOrderRequest) (shipmentDraft, error) {
	dest, err := destination(body)
	if err != nil {
		return shipmentDraft{}, err
	}
	pickups, err := loadPickups(ctx, s.db)
	if err != nil {
		return shipmentDraft{}, err
	}
	items := make([]fixtureItem, 0, len(body.OrderItems))
	for _, item := range body.OrderItems {
		items = append(items, fixtureItem{
			Name: item.Name, SKU: item.Sku, Units: item.Units, SellingPrice: int(math.Round(float64(item.SellingPrice))),
		})
	}
	grams := int(math.Round(float64(body.Weight) * 1000))
	if grams <= 0 {
		return shipmentDraft{}, clientError{status: http.StatusUnprocessableEntity, message: "weight must be greater than 0"}
	}
	length := int(math.Round(float64(body.Length)))
	breadth := int(math.Round(float64(body.Breadth)))
	height := int(math.Round(float64(body.Height)))
	paymentStatus := "paid"
	if body.PaymentMethod == generated.COD {
		paymentStatus = "pending"
	}
	return shipmentDraft{order: fixtureOrder{
		CustomerName: dest.name, CustomerEmail: dest.email, CustomerPhone: dest.phone,
		Address: dest.address, Address2: dest.address2, City: dest.city, State: dest.state, Pincode: dest.pincode, Country: dest.country,
		PickupLocation: pickupLocation(body.PickupLocation, pickups), PaymentMethod: string(body.PaymentMethod), PaymentStatus: paymentStatus,
		SubTotal: body.SubTotal, WeightGrams: grams, LengthCm: length, BreadthCm: breadth, HeightCm: height, OrderDate: body.OrderDate, Items: items,
	}}, nil
}

func (s *server) ensureChannelOrderAvailable(ctx context.Context, orderID string) error {
	var existing string
	err := s.db.QueryRowContext(ctx, "SELECT id FROM orders WHERE channel_order_id=?", orderID).Scan(&existing)
	if err == nil {
		return clientError{status: http.StatusUnprocessableEntity, message: "order_id already exists"}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}

func (s *server) ensureReturnOrderAvailable(ctx context.Context, orderID string) error {
	var existing string
	err := s.db.QueryRowContext(ctx, "SELECT id FROM returns WHERE order_id=?", orderID).Scan(&existing)
	if err == nil {
		return clientError{status: http.StatusUnprocessableEntity, message: "return already exists for order_id"}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}

func (s *server) ndrByAwb(ctx context.Context, awb string) (fixtureOrder, error) {
	order, err := orderByAwb(ctx, s.db, awb)
	if err != nil {
		return fixtureOrder{}, err
	}
	if order.Status != "ndr" {
		return fixtureOrder{}, sql.ErrNoRows
	}
	return order, nil
}

func (s *server) trackingView(ctx context.Context, order fixtureOrder) (generated.TrackingResponse, error) {
	pickups, err := loadPickups(ctx, s.db)
	if err != nil {
		return generated.TrackingResponse{}, err
	}
	origin := ""
	for _, pickup := range pickups {
		if pickup.Location == order.PickupLocation {
			origin = pickup.City
			break
		}
	}
	activity, location := activityFor(order, origin, order.City)
	when := order.OrderDate
	if order.AssignedAt != "" {
		when = order.AssignedAt
	}
	trackStatus := 0
	trackURL := ""
	if order.Awb != "" {
		trackStatus = 1
		trackURL = "https://shiprocket.co/tracking/" + order.Awb
	}
	track := generated.ShipmentTrack{
		Id: order.ShipmentID, AwbCode: order.Awb, CourierCompanyId: order.CourierID, CourierName: order.CourierName,
		ShipmentId: order.ShipmentID, OrderId: order.ID, ChannelOrderId: order.ChannelOrderID, ChannelName: order.ChannelName,
		Packages: 1, CurrentStatus: order.Status, Destination: order.City, ConsigneeName: order.CustomerName, Origin: origin,
	}
	if order.WeightGrams > 0 {
		weight := fmt.Sprintf("%.2f", float64(order.WeightGrams)/1000)
		track.Weight = &weight
	}
	return generated.TrackingResponse{TrackingData: generated.TrackingData{
		TrackStatus: trackStatus, ShipmentStatus: statusCode(order.Status),
		ShipmentTrack: []generated.ShipmentTrack{track},
		ShipmentTrackActivities: []generated.ShipmentTrackActivity{{
			Date: when, Status: order.Status, Activity: activity, Location: location,
			SrStatus: strconv.Itoa(statusCode(order.Status)), SrStatusLabel: order.Status,
		}},
		TrackUrl: trackURL,
	}}, nil
}

type party struct {
	name, email, phone, address, address2, city, state, pincode, country string
}

func destination(body *generated.CreateOrderRequest) (party, error) {
	if body.ShippingIsBilling {
		email := string(body.BillingEmail)
		if _, err := mail.ParseAddress(email); err != nil {
			return party{}, clientError{status: http.StatusUnprocessableEntity, message: "billing_email is invalid"}
		}
		return party{
			name: joinName(body.BillingCustomerName, body.BillingLastName), email: email, phone: body.BillingPhone,
			address: body.BillingAddress, address2: deref(body.BillingAddress2), city: body.BillingCity,
			state: body.BillingState, pincode: body.BillingPincode, country: body.BillingCountry,
		}, nil
	}
	if deref(body.ShippingCustomerName) == "" || deref(body.ShippingAddress) == "" || deref(body.ShippingCity) == "" ||
		deref(body.ShippingPincode) == "" || deref(body.ShippingState) == "" || deref(body.ShippingCountry) == "" ||
		deref(body.ShippingEmail) == "" || deref(body.ShippingPhone) == "" {
		return party{}, clientError{status: http.StatusUnprocessableEntity, message: "shipping address is required when shipping_is_billing is false"}
	}
	if _, err := mail.ParseAddress(deref(body.ShippingEmail)); err != nil {
		return party{}, clientError{status: http.StatusUnprocessableEntity, message: "shipping_email is invalid"}
	}
	return party{
		name: joinName(deref(body.ShippingCustomerName), body.ShippingLastName), email: deref(body.ShippingEmail), phone: deref(body.ShippingPhone),
		address: deref(body.ShippingAddress), address2: deref(body.ShippingAddress2), city: deref(body.ShippingCity),
		state: deref(body.ShippingState), pincode: deref(body.ShippingPincode), country: deref(body.ShippingCountry),
	}, nil
}

func pickupLocation(requested *string, pickups []fixturePickup) string {
	if requested != nil && *requested != "" {
		return *requested
	}
	for _, pickup := range pickups {
		if pickup.Location == "BLR-1" {
			return pickup.Location
		}
	}
	if len(pickups) > 0 {
		return pickups[0].Location
	}
	return "BLR-1"
}

func pickCourier(couriers []fixtureCourier, id int) (fixtureCourier, error) {
	if id == 0 {
		for _, courier := range couriers {
			if courier.Name == "Delhivery" {
				return courier, nil
			}
		}
		if len(couriers) == 0 {
			return fixtureCourier{}, fmt.Errorf("no couriers configured")
		}
		return couriers[0], nil
	}
	for _, courier := range couriers {
		if courier.ID == id {
			return courier, nil
		}
	}
	return fixtureCourier{}, fmt.Errorf("unknown courier id %d", id)
}

func orderView(order fixtureOrder) generated.Order {
	shipment := generated.Shipment{
		Id: order.ShipmentID, OrderId: order.ID, Awb: order.Awb, Courier: order.CourierName,
		CourierId: order.CourierID, Status: order.Status,
	}
	if order.WeightGrams > 0 {
		weight := float32(order.WeightGrams) / 1000
		shipment.Weight = &weight
	}
	return generated.Order{
		Id: order.ID, ChannelOrderId: order.ChannelOrderID, ChannelName: order.ChannelName,
		CustomerName: order.CustomerName, CustomerEmail: order.CustomerEmail, CustomerPhone: order.CustomerPhone,
		CustomerAddress: order.Address, CustomerCity: order.City, CustomerState: order.State,
		CustomerPincode: order.Pincode, CustomerCountry: order.Country, PickupLocation: order.PickupLocation,
		PaymentStatus: order.PaymentStatus, Total: rupees(order.SubTotal), Status: order.Status,
		StatusCode: statusCode(order.Status), PaymentMethod: order.PaymentMethod, CreatedAt: order.OrderDate,
		Products: productsOf(order.Items), Shipments: []generated.Shipment{shipment},
	}
}

func returnView(item fixtureReturn) generated.ReturnOrder {
	return generated.ReturnOrder{
		Id: item.ID, OrderId: item.OrderID, ShipmentId: item.ShipmentID, ChannelName: item.ChannelName,
		Status: item.Status, ForwardShipmentStatus: item.ForwardShipmentStatus, CustomerName: item.CustomerName,
		CustomerEmail: item.CustomerEmail, CustomerPhone: item.CustomerPhone, CustomerAddress: item.Address,
		CustomerCity: item.City, CustomerState: item.State, CustomerPincode: item.Pincode, CustomerCountry: item.Country,
		PickupLocation: item.PickupLocation, Total: rupees(item.SubTotal), CreatedAt: item.OrderDate,
		Products: productsOf(item.Items), Awb: item.Awb, Courier: item.CourierName,
	}
}

func ndrView(order fixtureOrder) generated.NdrShipment {
	return generated.NdrShipment{
		Id: order.ID, ShipmentId: order.ShipmentID, ChannelOrderId: order.ChannelOrderID, ChannelName: order.ChannelName,
		CustomerName: order.CustomerName, CustomerEmail: order.CustomerEmail, CustomerPhone: order.CustomerPhone,
		CustomerAddress: order.Address, CustomerCity: order.City, CustomerState: order.State, CustomerPincode: order.Pincode,
		Status: order.Status, StatusCode: statusCode(order.Status), Reason: order.NdrReason, Attempts: order.NdrAttempts,
		NdrAction: order.NdrAction, Comments: order.NdrComments, Courier: order.CourierName, AwbCode: order.Awb,
	}
}

func productsOf(items []fixtureItem) []generated.Product {
	out := make([]generated.Product, 0, len(items))
	for _, item := range items {
		out = append(out, generated.Product{Name: item.Name, Sku: item.SKU, Units: item.Units, SellingPrice: rupees(item.SellingPrice)})
	}
	return out
}

func activityFor(order fixtureOrder, origin, destination string) (string, string) {
	switch order.Status {
	case "NEW":
		return "Order created", origin
	case "ready_to_ship":
		return "AWB assigned", origin
	case "in_transit":
		return "In transit", origin
	case "ndr":
		return order.NdrReason, destination
	case "delivered":
		return "Delivered", destination
	case "cancelled":
		return "Cancelled", origin
	default:
		return order.Status, origin
	}
}

func statusCode(status string) int {
	switch status {
	case "NEW", "initiated":
		return 1
	case "ready_to_ship":
		return 3
	case "cancelled":
		return 5
	case "delivered":
		return 7
	case "in_transit":
		return 18
	case "ndr":
		return 21
	default:
		return 0
	}
}

func findOrder(ctx context.Context, db sqlExec, id string) (fixtureOrder, error) {
	order, err := orderByID(ctx, db, id)
	if errors.Is(err, sql.ErrNoRows) {
		return orderByChannel(ctx, db, id)
	}
	return order, err
}

func orderByID(ctx context.Context, db sqlExec, id string) (fixtureOrder, error) {
	return scanOrder(db.QueryRowContext(ctx, "SELECT "+orderColumns+" FROM orders WHERE id=?", id))
}

func orderByChannel(ctx context.Context, db sqlExec, id string) (fixtureOrder, error) {
	return scanOrder(db.QueryRowContext(ctx, "SELECT "+orderColumns+" FROM orders WHERE channel_order_id=?", id))
}

func orderByShipment(ctx context.Context, db sqlExec, id string) (fixtureOrder, error) {
	return scanOrder(db.QueryRowContext(ctx, "SELECT "+orderColumns+" FROM orders WHERE shipment_id=?", id))
}

func orderByAwb(ctx context.Context, db sqlExec, awb string) (fixtureOrder, error) {
	return scanOrder(db.QueryRowContext(ctx, "SELECT "+orderColumns+" FROM orders WHERE awb=?", awb))
}

func loadAccount(ctx context.Context, db sqlExec) (int, fixtureUser, error) {
	var companyID int
	var user fixtureUser
	err := db.QueryRowContext(ctx, `SELECT company_id, user_id, first_name, last_name, email, created_at FROM account WHERE id=1`).
		Scan(&companyID, &user.ID, &user.FirstName, &user.LastName, &user.Email, &user.CreatedAt)
	if err != nil {
		return 0, fixtureUser{}, fmt.Errorf("shiprocket: load account: %w", err)
	}
	return companyID, user, nil
}

func matchesSearch(order fixtureOrder, search string) bool {
	if strings.TrimSpace(search) == "" {
		return true
	}
	needle := strings.ToLower(search)
	haystack := strings.ToLower(strings.Join([]string{
		order.ID, order.ChannelOrderID, order.Awb, order.CustomerName, order.CourierName, order.ChannelName, order.Status, order.ShipmentID,
	}, " "))
	return strings.Contains(haystack, needle)
}

func pageParams(page *int, perPage *int) (int, int) {
	resolvedPage := 1
	if page != nil && *page > 0 {
		resolvedPage = *page
	}
	resolvedPerPage := 15
	if perPage != nil && *perPage > 0 {
		resolvedPerPage = *perPage
	}
	return resolvedPage, resolvedPerPage
}

func paginate[T any](items []T, page, perPage int) ([]T, generated.Pagination) {
	total := len(items)
	start := (page - 1) * perPage
	if start > total {
		start = total
	}
	if start < 0 {
		start = 0
	}
	end := start + perPage
	if end > total {
		end = total
	}
	window := items[start:end]
	if window == nil {
		window = []T{}
	}
	pages := 0
	if total > 0 {
		pages = (total + perPage - 1) / perPage
	}
	return window, generated.Pagination{Total: total, Count: len(window), PerPage: perPage, CurrentPage: page, TotalPages: pages}
}

func rupees(amount int) string { return fmt.Sprintf("%d.00", amount) }

func joinName(first string, last *string) string {
	if last == nil || strings.TrimSpace(*last) == "" {
		return first
	}
	return first + " " + strings.TrimSpace(*last)
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func errorBody(status int, message string) generated.ErrorBody {
	return generated.ErrorBody{Message: message, StatusCode: status}
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody(status, message))
}

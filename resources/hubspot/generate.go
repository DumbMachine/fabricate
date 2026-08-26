package hubspot

// Official HubSpot specs are one file per CRM object. prepare merges them and
// strips codegen-incompatible keywords; the stored JSON files stay unmodified.
//
//go:generate go run ../../internal/openapi/prepare -strip-examples -in contacts.json -in companies.json -in deals.json -in tickets.json -out generated/openapi.prepared.json
//go:generate go tool oapi-codegen -config oapi-codegen.yaml generated/openapi.prepared.json

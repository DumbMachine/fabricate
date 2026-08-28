package intercom

// Official vendor specs often contain allOf/readOnly combinations that
// oapi-codegen cannot merge. prepare strips only those codegen-incompatible
// keywords; the stored openapi.yaml stays the unmodified vendor document.
//
//go:generate go run ../../internal/openapi/prepare -in openapi.yaml -out generated/openapi.prepared.json
//go:generate go tool oapi-codegen -config oapi-codegen.yaml generated/openapi.prepared.json

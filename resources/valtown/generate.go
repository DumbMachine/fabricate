package valtown

//go:generate go run ../../internal/openapi/prepare -strip-examples -openapi-version 3.0.3 -keep-operation-ids valsList2 -keep-operation-ids valsGet2 -keep-operation-ids valsCreate2 -in openapi.yaml -out generated/openapi.prepared.json
//go:generate go tool oapi-codegen -config oapi-codegen.yaml generated/openapi.prepared.json

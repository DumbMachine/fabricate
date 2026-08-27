package resend

//go:generate go run ../../internal/openapi/prepare -strip-examples -openapi-version 3.0.3 -keep-operation-ids emails/send -keep-operation-ids emails/list -keep-operation-ids emails/get -keep-operation-ids domains/list -in openapi.yaml -out generated/openapi.prepared.json
//go:generate go tool oapi-codegen -config oapi-codegen.yaml generated/openapi.prepared.json

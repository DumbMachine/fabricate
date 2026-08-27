package sendgrid

//go:generate go run ../../internal/openapi/prepare -strip-examples -openapi-version 3.0.3 -keep-operation-ids SendMail -keep-operation-ids CreateMailBatch -keep-operation-ids GetMailBatch -in openapi.yaml -out generated/openapi.prepared.json
//go:generate go tool oapi-codegen -config oapi-codegen.yaml generated/openapi.prepared.json

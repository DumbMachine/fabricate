package digitalocean

//go:generate go run ../../internal/openapi/prepare -strip-examples -openapi-version 3.0.3 -keep-operation-ids listDroplets -keep-operation-ids getDroplet -keep-operation-ids createDroplet -in openapi.yaml -out generated/openapi.prepared.json
//go:generate go tool oapi-codegen -config oapi-codegen.yaml generated/openapi.prepared.json

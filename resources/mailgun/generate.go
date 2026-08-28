package mailgun

//go:generate go run ../../internal/openapi/prepare -strip-examples -openapi-version 3.0.3 -keep-operation-ids POST-v3--domain-name--messages -keep-operation-ids get-v3-domain_name-events -keep-operation-ids GET-v4-domains -in openapi.yaml -out generated/openapi.prepared.json
//go:generate go tool oapi-codegen -config oapi-codegen.yaml generated/openapi.prepared.json

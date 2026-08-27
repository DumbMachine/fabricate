package close

//go:generate go run ../../internal/openapi/prepare -strip-examples -openapi-version 3.0.3 -keep-operation-ids users_get_me -keep-operation-ids leads_list -keep-operation-ids leads_create -keep-operation-ids leads_get -keep-operation-ids leads_update -keep-operation-ids contacts_list -keep-operation-ids contacts_create -keep-operation-ids contacts_get -keep-operation-ids opportunities_list -keep-operation-ids opportunities_create -keep-operation-ids opportunities_get -keep-operation-ids opportunities_update -in openapi.json -out generated/openapi.prepared.json
//go:generate go tool oapi-codegen -config oapi-codegen.yaml generated/openapi.prepared.json

package pipedrive

//go:generate go run ../../internal/openapi/prepare -strip-examples -keep-operation-ids getPersons -keep-operation-ids addPerson -keep-operation-ids getPerson -keep-operation-ids updatePerson -keep-operation-ids getOrganizations -keep-operation-ids addOrganization -keep-operation-ids getOrganization -keep-operation-ids getDeals -keep-operation-ids addDeal -keep-operation-ids getDeal -keep-operation-ids updateDeal -in openapi.yaml -out generated/openapi.prepared.json
//go:generate go tool oapi-codegen -config oapi-codegen.yaml generated/openapi.prepared.json

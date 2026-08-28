package attio

// Official Attio OpenAPI 3.1 has no operationIds. prepare assigns them and
// rewrites the version for oapi-codegen; the stored JSON stays unmodified.
//
//go:generate go run ../../internal/openapi/prepare -strip-examples -assign-operation-ids -openapi-version 3.0.3 -keep-operation-ids get_v2_workspace_members -keep-operation-ids get_v2_objects -keep-operation-ids post_v2_objects_object_records_query -keep-operation-ids post_v2_objects_object_records -keep-operation-ids get_v2_objects_object_records_record_id -keep-operation-ids patch_v2_objects_object_records_record_id -in openapi.json -out generated/openapi.prepared.json
//go:generate go tool oapi-codegen -config oapi-codegen.yaml generated/openapi.prepared.json

package mailchimp

// Official Mailchimp Marketing spec is Swagger 2. prepare emits JSON for
// oapi-codegen; the stored document stays unmodified.
//
//go:generate go run ../../internal/openapi/prepare -strip-examples -keep-operation-ids getPing -keep-operation-ids getLists -keep-operation-ids postLists -keep-operation-ids getListsId -keep-operation-ids getListsIdMembers -keep-operation-ids postListsIdMembers -keep-operation-ids getListsIdMembersId -keep-operation-ids getCampaigns -keep-operation-ids postCampaigns -keep-operation-ids getCampaignsId -keep-operation-ids postCampaignsIdActionsSend -in openapi.json -out generated/openapi.prepared.json
//go:generate go tool oapi-codegen -config oapi-codegen.yaml generated/openapi.prepared.json

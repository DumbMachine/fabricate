package gainsight

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/dumbmachine/fabricate/resources/gainsight/generated"
)

func companyWire(company fixtureCompany) generated.Record {
	record := generated.Record{
		"Gsid":                 company.Gsid,
		"Name":                 company.Name,
		"Industry":             company.Industry,
		"OriginalContractDate": company.OriginalContractDate,
		"RenewalDate":          company.RenewalDate,
		"Stage":                company.Stage,
		"Status":               company.Status,
		"CSMFirstName":         company.CSMFirstName,
		"CSMLastName":          company.CSMLastName,
		"Health__gc":           company.Health,
		"Domain__gc":           company.Domain,
		"CreatedDate":          company.CreatedDate,
		"ModifiedDate":         company.ModifiedDate,
		"csm__gr.email":        company.CSMEmail,
		"Csm":                  userID(company.CSMEmail),
	}
	if company.ARR != nil {
		record["ARR"] = jsonNumber(*company.ARR)
	} else {
		record["ARR"] = nil
	}
	if company.Employees != nil {
		record["Employees"] = *company.Employees
	} else {
		record["Employees"] = nil
	}
	if company.LifecycleInWeeks != nil {
		record["LifecycleInWeeks"] = *company.LifecycleInWeeks
	} else {
		record["LifecycleInWeeks"] = nil
	}
	return record
}

func ctaWire(cta fixtureCTA, company fixtureCompany) generated.Record {
	return generated.Record{
		"Gsid":                cta.Gsid,
		"Name":                cta.Name,
		"CompanyId":           cta.CompanyID,
		"CompanyId__gr.Name":  company.Name,
		"DueDate":             cta.DueDate,
		"Comments":            cta.Comments,
		"EntityType":          cta.EntityType,
		"IsClosed":            cta.IsClosed,
		"IsImportant":         cta.IsImportant,
		"OwnerId":             userID(cta.OwnerEmail),
		"OwnerId__gr.Email":   cta.OwnerEmail,
		"OwnerId__gr.Name":    cta.OwnerName,
		"StatusId__gr.Name":   cta.Status,
		"TypeId__gr.Name":     cta.Type,
		"PriorityId__gr.Name": cta.Priority,
		"ReasonId__gr.Name":   cta.Reason,
		"status":              cta.Status,
		"type":                cta.Type,
		"priority":            cta.Priority,
		"reason":              cta.Reason,
	}
}

func planWire(plan fixturePlan, company fixtureCompany) generated.Record {
	return generated.Record{
		"Gsid":                       plan.Gsid,
		"Name":                       plan.Name,
		"CompanyId":                  plan.CompanyID,
		"CompanyId__gr.Name":         company.Name,
		"DueDate":                    plan.DueDate,
		"ActionPlan":                 plan.ActionPlan,
		"EntityType":                 plan.EntityType,
		"OwnerId":                    userID(plan.OwnerEmail),
		"OwnerId__gr.Email":          plan.OwnerEmail,
		"OwnerId__gr.Name":           plan.OwnerName,
		"StatusId__gr.Name":          plan.Status,
		"status":                     plan.Status,
		"SuccessPlanTypeId__gr.Name": plan.Type,
		"SuccessPlanType":            plan.Type,
	}
}

func activityWire(activity fixtureActivity) generated.Record {
	return generated.Record{
		"Gsid":         activity.Gsid,
		"ExternalId":   activity.ExternalID,
		"ContextName":  activity.ContextName,
		"contextname":  activity.ContextName,
		"ContextId":    activity.ContextID,
		"GsCompanyId":  activity.CompanyID,
		"Author":       activity.Author,
		"AuthorId":     activity.AuthorID,
		"TypeName":     activity.TypeName,
		"Subject":      activity.Subject,
		"Notes":        activity.Notes,
		"ActivityDate": activity.ActivityDate,
	}
}

func companyFromWrite(record generated.CompanyRecord, now int64) (fixtureCompany, error) {
	name := sval(record.Name)
	if name == "" {
		return fixtureCompany{}, errors.New("Name is required")
	}
	company := fixtureCompany{
		Name: name, Industry: sval(record.Industry), OriginalContractDate: sval(record.OriginalContractDate),
		RenewalDate: sval(record.RenewalDate), Stage: sval(record.Stage), Status: sval(record.Status),
		Health: healthString(record.HealthGc), CSMFirstName: sval(record.CSMFirstName), CSMLastName: sval(record.CSMLastName),
		Domain: sval(record.DomainGc), CreatedDate: now, ModifiedDate: now,
	}
	if company.Status == "" {
		company.Status = "Active"
	}
	if company.Stage == "" {
		company.Stage = "New Customer"
	}
	if record.ARR != nil {
		value := float64(*record.ARR)
		company.ARR = &value
	}
	if record.Employees != nil {
		value := int(*record.Employees)
		company.Employees = &value
	}
	if record.LifecycleInWeeks != nil {
		company.LifecycleInWeeks = record.LifecycleInWeeks
	} else if record.LifeCycleInWeeks != nil {
		company.LifecycleInWeeks = record.LifeCycleInWeeks
	}
	return company, nil
}

func companyMatchesKeys(company fixtureCompany, record generated.CompanyRecord, keys []string) (bool, error) {
	for _, key := range keys {
		want, ok := writeKey(record, key)
		if !ok {
			return false, fmt.Errorf("key %s is missing from the record", key)
		}
		got, known := storedKey(company, key)
		if !known {
			return false, fmt.Errorf("unsupported key %s", key)
		}
		if got != want {
			return false, nil
		}
	}
	return true, nil
}

func applyCompanyPatch(company *fixtureCompany, record generated.CompanyRecord, keys []string, now int64) error {
	keySet := map[string]struct{}{}
	for _, key := range keys {
		keySet[strings.ToLower(key)] = struct{}{}
	}
	set := func(name string) bool {
		_, skip := keySet[strings.ToLower(name)]
		return !skip
	}
	if record.Name != nil && set("Name") {
		company.Name = *record.Name
	}
	if record.Industry != nil && set("Industry") {
		company.Industry = *record.Industry
	}
	if record.ARR != nil && set("ARR") {
		value := float64(*record.ARR)
		company.ARR = &value
	}
	if record.Employees != nil && set("Employees") {
		value := int(*record.Employees)
		company.Employees = &value
	}
	if record.LifecycleInWeeks != nil && set("LifecycleInWeeks") {
		company.LifecycleInWeeks = record.LifecycleInWeeks
	}
	if record.LifeCycleInWeeks != nil && set("LifeCycleInWeeks") {
		company.LifecycleInWeeks = record.LifeCycleInWeeks
	}
	if record.OriginalContractDate != nil && set("OriginalContractDate") {
		company.OriginalContractDate = *record.OriginalContractDate
	}
	if record.RenewalDate != nil && set("RenewalDate") {
		company.RenewalDate = *record.RenewalDate
	}
	if record.Stage != nil && set("Stage") {
		company.Stage = *record.Stage
	}
	if record.Status != nil && set("Status") {
		company.Status = *record.Status
	}
	if record.HealthGc != nil && set("Health__gc") {
		company.Health = string(*record.HealthGc)
	}
	if record.CSMFirstName != nil && set("CSMFirstName") {
		company.CSMFirstName = *record.CSMFirstName
	}
	if record.CSMLastName != nil && set("CSMLastName") {
		company.CSMLastName = *record.CSMLastName
	}
	if record.DomainGc != nil && set("Domain__gc") {
		company.Domain = *record.DomainGc
	}
	company.ModifiedDate = now
	return nil
}

func storedKey(company fixtureCompany, key string) (string, bool) {
	switch strings.ToLower(key) {
	case "gsid":
		return company.Gsid, true
	case "name":
		return company.Name, true
	case "industry":
		return company.Industry, true
	case "status":
		return company.Status, true
	case "stage":
		return company.Stage, true
	case "health__gc":
		return company.Health, true
	case "domain__gc":
		return company.Domain, true
	case "csmfirstname":
		return company.CSMFirstName, true
	case "csmlastname":
		return company.CSMLastName, true
	case "arr":
		if company.ARR == nil {
			return "", true
		}
		return fmt.Sprint(jsonNumber(*company.ARR)), true
	default:
		return "", false
	}
}

func writeKey(record generated.CompanyRecord, key string) (string, bool) {
	switch strings.ToLower(key) {
	case "gsid":
		if record.Gsid == nil {
			return "", false
		}
		return *record.Gsid, true
	case "name":
		if record.Name == nil {
			return "", false
		}
		return *record.Name, true
	case "industry":
		if record.Industry == nil {
			return "", false
		}
		return *record.Industry, true
	case "status":
		if record.Status == nil {
			return "", false
		}
		return *record.Status, true
	case "stage":
		if record.Stage == nil {
			return "", false
		}
		return *record.Stage, true
	case "health__gc":
		if record.HealthGc == nil {
			return "", false
		}
		return string(*record.HealthGc), true
	case "domain__gc":
		if record.DomainGc == nil {
			return "", false
		}
		return *record.DomainGc, true
	case "csmfirstname":
		if record.CSMFirstName == nil {
			return "", false
		}
		return *record.CSMFirstName, true
	case "csmlastname":
		if record.CSMLastName == nil {
			return "", false
		}
		return *record.CSMLastName, true
	case "arr":
		if record.ARR == nil {
			return "", false
		}
		return fmt.Sprint(jsonNumber(float64(*record.ARR))), true
	default:
		return "", false
	}
}

func resolveCompany(idOrName string, byID, byName map[string]fixtureCompany) (fixtureCompany, error) {
	if idOrName == "" {
		return fixtureCompany{}, errors.New("CompanyId is required")
	}
	if company, ok := byID[idOrName]; ok {
		return company, nil
	}
	if company, ok := byName[idOrName]; ok {
		return company, nil
	}
	return fixtureCompany{}, fmt.Errorf("company %s was not found", idOrName)
}

func ownerName(email string, company fixtureCompany) string {
	if email != "" && strings.EqualFold(email, company.CSMEmail) {
		return strings.TrimSpace(company.CSMFirstName + " " + company.CSMLastName)
	}
	if email == "sam@acme.example" {
		return "Sam Adeyemi"
	}
	return ""
}

func userID(email string) string {
	local, _, _ := strings.Cut(strings.ToLower(email), "@")
	var b strings.Builder
	for _, r := range local {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return "user-" + b.String()
}

func jsonNumber(value float64) any {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	if value == math.Trunc(value) && value <= 1<<53 && value >= -(1<<53) {
		return int64(value)
	}
	return value
}

func listEnvelope(requestID string, result bool, code, desc string, data []generated.Record) generated.RecordListEnvelope {
	if data == nil {
		data = []generated.Record{}
	}
	return generated.RecordListEnvelope{
		Result: result, ErrorCode: strPtrOrNil(code), ErrorDesc: strPtrOrNil(desc), RequestId: requestID, Data: data,
	}
}

func batchEnvelope(requestID string, result bool, code, desc string, records []generated.Record) generated.RecordBatchEnvelope {
	if records == nil {
		records = []generated.Record{}
	}
	count := len(records)
	envelope := generated.RecordBatchEnvelope{
		Result: result, ErrorCode: strPtrOrNil(code), ErrorDesc: strPtrOrNil(desc), RequestId: requestID,
	}
	envelope.Data.Count = &count
	envelope.Data.Records = records
	return envelope
}

func deleteEnvelope(requestID string, result bool, code, desc string, data *string) generated.DeleteEnvelope {
	return generated.DeleteEnvelope{
		Result: result, ErrorCode: strPtrOrNil(code), ErrorDesc: strPtrOrNil(desc), RequestId: requestID, Data: data,
	}
}

func bulkEnvelope(requestID string, result bool, code, desc string, success, failure []generated.StringMap) generated.BulkResultEnvelope {
	if success == nil {
		success = []generated.StringMap{}
	}
	if failure == nil {
		failure = []generated.StringMap{}
	}
	return generated.BulkResultEnvelope{
		Result: result, ErrorCode: strPtrOrNil(code), ErrorDesc: strPtrOrNil(desc), RequestId: requestID,
		Data: generated.BulkResult{Success: success, Failure: failure},
	}
}

func activityEnvelope(result bool, failures map[string][]generated.ActivityFailure, success map[string][]generated.ActivitySuccess) generated.ActivitySaveEnvelope {
	if failures == nil {
		failures = map[string][]generated.ActivityFailure{}
	}
	if success == nil {
		success = map[string][]generated.ActivitySuccess{}
	}
	return generated.ActivitySaveEnvelope{
		Result: result,
		Data:   generated.ActivitySaveData{Failures: failures, Success: success},
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(generated.ErrorEnvelope{
		Result: false, ErrorCode: &code, ErrorDesc: &message, RequestId: "",
	})
}

func splitKeys(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func sval(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func strPtrOrNil(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func healthString(value *generated.CompanyRecordHealthGc) string {
	if value == nil {
		return ""
	}
	return string(*value)
}

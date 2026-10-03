package greenhouse

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/mail"
	"time"

	"github.com/dumbmachine/fabricate/scenario"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed schema.sql
var schemaSQL string

//go:embed scenario.schema.json
var scenarioSchema []byte

//go:embed scenarios/*.json
var builtInScenarios embed.FS

const (
	collectionUsers        = "users"
	collectionOffices      = "offices"
	collectionDepartments  = "departments"
	collectionJobs         = "jobs"
	collectionJobStages    = "jobStages"
	collectionCandidates   = "candidates"
	collectionApplications = "applications"
	collectionOffers       = "offers"
	collectionNotes        = "notes"
	collectionScorecards   = "scorecards"
)

var compiledScenarioSchema = func() *jsonschema.Schema {
	parsed, err := jsonschema.UnmarshalJSON(bytes.NewReader(scenarioSchema))
	if err != nil {
		panic(fmt.Sprintf("greenhouse: parse embedded scenario schema: %v", err))
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("greenhouse-scenario.json", parsed); err != nil {
		panic(fmt.Sprintf("greenhouse: add embedded scenario schema: %v", err))
	}
	compiled, err := compiler.Compile("greenhouse-scenario.json")
	if err != nil {
		panic(fmt.Sprintf("greenhouse: compile embedded scenario schema: %v", err))
	}
	return compiled
}()

type scenarioCodec struct{}

type fixtureState struct {
	Users        []fixtureUser        `json:"users"`
	Offices      []fixtureOffice      `json:"offices"`
	Departments  []fixtureDepartment  `json:"departments"`
	Jobs         []fixtureJob         `json:"jobs"`
	JobStages    []fixtureJobStage    `json:"jobStages"`
	Candidates   []fixtureCandidate   `json:"candidates"`
	Applications []fixtureApplication `json:"applications"`
	Offers       []fixtureOffer       `json:"offers"`
	Notes        []fixtureNote        `json:"notes"`
	Scorecards   []fixtureScorecard   `json:"scorecards"`
}

type fixtureUser struct {
	ID         string   `json:"id"`
	FirstName  string   `json:"firstName"`
	LastName   string   `json:"lastName"`
	Name       string   `json:"name"`
	Email      string   `json:"email"`
	Emails     []string `json:"emails"`
	EmployeeID string   `json:"employeeId"`
	Disabled   bool     `json:"disabled"`
	SiteAdmin  bool     `json:"siteAdmin"`
	CreatedAt  string   `json:"createdAt"`
	UpdatedAt  string   `json:"updatedAt"`
}

type fixtureOffice struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	LocationName string `json:"locationName"`
}

type fixtureDepartment struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type fixtureJob struct {
	ID                     string   `json:"id"`
	Name                   string   `json:"name"`
	RequisitionID          string   `json:"requisitionId"`
	Notes                  string   `json:"notes"`
	Confidential           bool     `json:"confidential"`
	Status                 string   `json:"status"`
	CreatedAt              string   `json:"createdAt"`
	OpenedAt               string   `json:"openedAt"`
	ClosedAt               string   `json:"closedAt"`
	UpdatedAt              string   `json:"updatedAt"`
	DepartmentID           string   `json:"departmentId"`
	OfficeID               string   `json:"officeId"`
	HiringManagers         []string `json:"hiringManagers"`
	Recruiters             []string `json:"recruiters"`
	Coordinators           []string `json:"coordinators"`
	Sourcers               []string `json:"sourcers"`
	ResponsibleRecruiterID string   `json:"responsibleRecruiterId"`
}

type fixtureJobStage struct {
	ID        string `json:"id"`
	JobID     string `json:"jobId"`
	Name      string `json:"name"`
	Priority  int    `json:"priority"`
	Active    bool   `json:"active"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

type fixtureCandidate struct {
	ID           string              `json:"id"`
	FirstName    string              `json:"firstName"`
	LastName     string              `json:"lastName"`
	Company      string              `json:"company"`
	Title        string              `json:"title"`
	CreatedAt    string              `json:"createdAt"`
	UpdatedAt    string              `json:"updatedAt"`
	LastActivity string              `json:"lastActivity"`
	IsPrivate    bool                `json:"isPrivate"`
	CanEmail     bool                `json:"canEmail"`
	RecruiterID  string              `json:"recruiterId"`
	Emails       []fixtureContact    `json:"emails"`
	Phones       []fixtureContact    `json:"phones"`
	Tags         []string            `json:"tags"`
	Attachments  []fixtureAttachment `json:"attachments"`
}

type fixtureContact struct {
	Value string `json:"value"`
	Type  string `json:"type"`
}

type fixtureAttachment struct {
	Filename  string `json:"filename"`
	URL       string `json:"url"`
	Type      string `json:"type"`
	CreatedAt string `json:"createdAt"`
}

type fixtureApplication struct {
	ID             string `json:"id"`
	CandidateID    string `json:"candidateId"`
	JobID          string `json:"jobId"`
	Prospect       bool   `json:"prospect"`
	Status         string `json:"status"`
	StageID        string `json:"stageId"`
	AppliedAt      string `json:"appliedAt"`
	RejectedAt     string `json:"rejectedAt"`
	LastActivityAt string `json:"lastActivityAt"`
	RecruiterID    string `json:"recruiterId"`
	SourceID       string `json:"sourceId"`
	SourceName     string `json:"sourceName"`
}

type fixtureOffer struct {
	ID            string            `json:"id"`
	Version       int               `json:"version"`
	ApplicationID string            `json:"applicationId"`
	JobID         string            `json:"jobId"`
	CandidateID   string            `json:"candidateId"`
	Status        string            `json:"status"`
	CreatedAt     string            `json:"createdAt"`
	UpdatedAt     string            `json:"updatedAt"`
	SentAt        string            `json:"sentAt"`
	ResolvedAt    string            `json:"resolvedAt"`
	StartsAt      string            `json:"startsAt"`
	CustomFields  map[string]string `json:"customFields"`
}

type fixtureNote struct {
	ID          string `json:"id"`
	CandidateID string `json:"candidateId"`
	UserID      string `json:"userId"`
	Body        string `json:"body"`
	Visibility  string `json:"visibility"`
	CreatedAt   string `json:"createdAt"`
}

type fixtureScorecard struct {
	ID                    string             `json:"id"`
	CandidateID           string             `json:"candidateId"`
	ApplicationID         string             `json:"applicationId"`
	Interview             string             `json:"interview"`
	InterviewStepID       string             `json:"interviewStepId"`
	InterviewStepName     string             `json:"interviewStepName"`
	InterviewedAt         string             `json:"interviewedAt"`
	SubmittedBy           string             `json:"submittedBy"`
	InterviewerID         string             `json:"interviewerId"`
	SubmittedAt           string             `json:"submittedAt"`
	OverallRecommendation string             `json:"overallRecommendation"`
	CreatedAt             string             `json:"createdAt"`
	UpdatedAt             string             `json:"updatedAt"`
	Attributes            []fixtureAttribute `json:"attributes"`
	Questions             []fixtureQuestion  `json:"questions"`
}

type fixtureAttribute struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Note   string `json:"note"`
	Rating string `json:"rating"`
}

type fixtureQuestion struct {
	ID       string `json:"id"`
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

type sqlQuery interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (scenarioCodec) Validate(_ context.Context, doc scenario.Document) error {
	if err := doc.ValidateEnvelope(); err != nil {
		return err
	}
	if doc.Resource != "greenhouse" || doc.ResourceVersion != "v1" {
		return fmt.Errorf("greenhouse scenario: expected resource greenhouse v1, got %s %s", doc.Resource, doc.ResourceVersion)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc.State))
	if err != nil {
		return fmt.Errorf("greenhouse scenario: decode state for schema validation: %w", err)
	}
	if err := compiledScenarioSchema.Validate(instance); err != nil {
		return fmt.Errorf("greenhouse scenario: schema validation: %w", err)
	}
	state, err := decodeState(doc.State)
	if err != nil {
		return err
	}
	return validateState(state)
}

func (scenarioCodec) Initialize(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("greenhouse scenario: initialize: %w", err)
	}
	return nil
}

func (codec scenarioCodec) Load(ctx context.Context, db *sql.DB, doc scenario.Document) error {
	if err := codec.Validate(ctx, doc); err != nil {
		return err
	}
	state, err := decodeState(doc.State)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("greenhouse scenario: begin load: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM records`); err != nil {
		return fmt.Errorf("greenhouse scenario: clear records: %w", err)
	}
	if err := insertAll(ctx, tx, state); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("greenhouse scenario: commit load: %w", err)
	}
	return nil
}

func (scenarioCodec) Dump(ctx context.Context, db *sql.DB, metadata scenario.Metadata) (scenario.Document, error) {
	state, err := readState(ctx, db)
	if err != nil {
		return scenario.Document{}, err
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return scenario.Document{}, err
	}
	return scenario.Document{
		Contract: ContractName(), ContractVersion: 1, ID: metadata.ID,
		Resource: "greenhouse", ResourceVersion: "v1", State: raw,
	}, nil
}

func insertAll(ctx context.Context, tx *sql.Tx, state fixtureState) error {
	for _, item := range state.Users {
		if err := insertDocument(ctx, tx, collectionUsers, item.ID, item); err != nil {
			return err
		}
	}
	for _, item := range state.Offices {
		if err := insertDocument(ctx, tx, collectionOffices, item.ID, item); err != nil {
			return err
		}
	}
	for _, item := range state.Departments {
		if err := insertDocument(ctx, tx, collectionDepartments, item.ID, item); err != nil {
			return err
		}
	}
	for _, item := range state.Jobs {
		if err := insertDocument(ctx, tx, collectionJobs, item.ID, item); err != nil {
			return err
		}
	}
	for _, item := range state.JobStages {
		if err := insertDocument(ctx, tx, collectionJobStages, item.ID, item); err != nil {
			return err
		}
	}
	for _, item := range state.Candidates {
		if err := insertDocument(ctx, tx, collectionCandidates, item.ID, item); err != nil {
			return err
		}
	}
	for _, item := range state.Applications {
		if err := insertDocument(ctx, tx, collectionApplications, item.ID, item); err != nil {
			return err
		}
	}
	for _, item := range state.Offers {
		if err := insertDocument(ctx, tx, collectionOffers, item.ID, item); err != nil {
			return err
		}
	}
	for _, item := range state.Notes {
		if err := insertDocument(ctx, tx, collectionNotes, item.ID, item); err != nil {
			return err
		}
	}
	for _, item := range state.Scorecards {
		if err := insertDocument(ctx, tx, collectionScorecards, item.ID, item); err != nil {
			return err
		}
	}
	return nil
}

func insertDocument(ctx context.Context, q sqlQuery, collection, id string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("greenhouse scenario: encode %s %s: %w", collection, id, err)
	}
	if _, err := q.ExecContext(ctx, `INSERT INTO records(collection, id, document) VALUES(?, ?, ?)`, collection, id, string(raw)); err != nil {
		return fmt.Errorf("greenhouse scenario: insert %s %s: %w", collection, id, err)
	}
	return nil
}

func saveDocument(ctx context.Context, q sqlQuery, collection, id string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("greenhouse scenario: encode %s %s: %w", collection, id, err)
	}
	result, err := q.ExecContext(ctx, `UPDATE records SET document=? WHERE collection=? AND id=?`, string(raw), collection, id)
	if err != nil {
		return fmt.Errorf("greenhouse scenario: update %s %s: %w", collection, id, err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("greenhouse scenario: update %s %s affected %d rows", collection, id, n)
	}
	return nil
}

func loadDocument(ctx context.Context, q sqlQuery, collection, id string, dest any) error {
	var raw string
	err := q.QueryRowContext(ctx, `SELECT document FROM records WHERE collection=? AND id=?`, collection, id).Scan(&raw)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(raw), dest); err != nil {
		return fmt.Errorf("greenhouse scenario: decode %s %s: %w", collection, id, err)
	}
	return nil
}

func readState(ctx context.Context, db sqlQuery) (fixtureState, error) {
	var state fixtureState
	var err error
	if state.Users, err = readCollection[fixtureUser](ctx, db, collectionUsers); err != nil {
		return fixtureState{}, err
	}
	if state.Offices, err = readCollection[fixtureOffice](ctx, db, collectionOffices); err != nil {
		return fixtureState{}, err
	}
	if state.Departments, err = readCollection[fixtureDepartment](ctx, db, collectionDepartments); err != nil {
		return fixtureState{}, err
	}
	if state.Jobs, err = readCollection[fixtureJob](ctx, db, collectionJobs); err != nil {
		return fixtureState{}, err
	}
	if state.JobStages, err = readCollection[fixtureJobStage](ctx, db, collectionJobStages); err != nil {
		return fixtureState{}, err
	}
	if state.Candidates, err = readCollection[fixtureCandidate](ctx, db, collectionCandidates); err != nil {
		return fixtureState{}, err
	}
	if state.Applications, err = readCollection[fixtureApplication](ctx, db, collectionApplications); err != nil {
		return fixtureState{}, err
	}
	if state.Offers, err = readCollection[fixtureOffer](ctx, db, collectionOffers); err != nil {
		return fixtureState{}, err
	}
	if state.Notes, err = readCollection[fixtureNote](ctx, db, collectionNotes); err != nil {
		return fixtureState{}, err
	}
	if state.Scorecards, err = readCollection[fixtureScorecard](ctx, db, collectionScorecards); err != nil {
		return fixtureState{}, err
	}
	normalizeState(&state)
	return state, nil
}

func readCollection[T any](ctx context.Context, db sqlQuery, collection string) ([]T, error) {
	rows, err := db.QueryContext(ctx, `SELECT document FROM records WHERE collection=? ORDER BY id`, collection)
	if err != nil {
		return nil, fmt.Errorf("greenhouse scenario: read %s: %w", collection, err)
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var item T
		if err := json.Unmarshal([]byte(raw), &item); err != nil {
			return nil, fmt.Errorf("greenhouse scenario: decode %s: %w", collection, err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func normalizeState(state *fixtureState) {
	if state.Users == nil {
		state.Users = []fixtureUser{}
	}
	if state.Offices == nil {
		state.Offices = []fixtureOffice{}
	}
	if state.Departments == nil {
		state.Departments = []fixtureDepartment{}
	}
	if state.Jobs == nil {
		state.Jobs = []fixtureJob{}
	}
	if state.JobStages == nil {
		state.JobStages = []fixtureJobStage{}
	}
	if state.Candidates == nil {
		state.Candidates = []fixtureCandidate{}
	}
	if state.Applications == nil {
		state.Applications = []fixtureApplication{}
	}
	if state.Offers == nil {
		state.Offers = []fixtureOffer{}
	}
	if state.Notes == nil {
		state.Notes = []fixtureNote{}
	}
	if state.Scorecards == nil {
		state.Scorecards = []fixtureScorecard{}
	}
	for i := range state.Users {
		if state.Users[i].Emails == nil {
			state.Users[i].Emails = []string{}
		}
	}
	for i := range state.Jobs {
		job := &state.Jobs[i]
		if job.HiringManagers == nil {
			job.HiringManagers = []string{}
		}
		if job.Recruiters == nil {
			job.Recruiters = []string{}
		}
		if job.Coordinators == nil {
			job.Coordinators = []string{}
		}
		if job.Sourcers == nil {
			job.Sourcers = []string{}
		}
	}
	for i := range state.Candidates {
		candidate := &state.Candidates[i]
		if candidate.Emails == nil {
			candidate.Emails = []fixtureContact{}
		}
		if candidate.Phones == nil {
			candidate.Phones = []fixtureContact{}
		}
		if candidate.Tags == nil {
			candidate.Tags = []string{}
		}
		if candidate.Attachments == nil {
			candidate.Attachments = []fixtureAttachment{}
		}
	}
	for i := range state.Offers {
		if state.Offers[i].CustomFields == nil {
			state.Offers[i].CustomFields = map[string]string{}
		}
	}
	for i := range state.Scorecards {
		card := &state.Scorecards[i]
		if card.Attributes == nil {
			card.Attributes = []fixtureAttribute{}
		}
		if card.Questions == nil {
			card.Questions = []fixtureQuestion{}
		}
	}
}

func decodeState(raw []byte) (fixtureState, error) {
	var state fixtureState
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return fixtureState{}, fmt.Errorf("greenhouse scenario: decode state: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fixtureState{}, fmt.Errorf("greenhouse scenario: state has trailing data")
	}
	if state.Users == nil || state.Offices == nil || state.Departments == nil || state.Jobs == nil || state.JobStages == nil || state.Candidates == nil || state.Applications == nil || state.Offers == nil || state.Notes == nil || state.Scorecards == nil {
		return fixtureState{}, fmt.Errorf("greenhouse scenario: users, offices, departments, jobs, jobStages, candidates, applications, offers, notes, and scorecards are required arrays")
	}
	normalizeState(&state)
	return state, nil
}

func validateState(state fixtureState) error {
	users := map[string]fixtureUser{}
	seen := map[string]struct{}{}
	for i, user := range state.Users {
		if err := seenID(seen, user.ID, "user"); err != nil {
			return err
		}
		if user.FirstName == "" || user.LastName == "" {
			return fmt.Errorf("greenhouse scenario: users[%d] requires firstName and lastName", i)
		}
		if user.Name != user.FirstName+" "+user.LastName {
			return fmt.Errorf("greenhouse scenario: users[%d].name must be the first and last name", i)
		}
		if err := parseEmail(fmt.Sprintf("users[%d].email", i), user.Email); err != nil {
			return err
		}
		if len(user.Emails) == 0 {
			return fmt.Errorf("greenhouse scenario: users[%d].emails is required", i)
		}
		found := false
		emailSeen := map[string]struct{}{}
		for j, email := range user.Emails {
			if err := parseEmail(fmt.Sprintf("users[%d].emails[%d]", i, j), email); err != nil {
				return err
			}
			if _, ok := emailSeen[email]; ok {
				return fmt.Errorf("greenhouse scenario: users[%d] repeats email %q", i, email)
			}
			emailSeen[email] = struct{}{}
			if email == user.Email {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("greenhouse scenario: users[%d].emails must include %s", i, user.Email)
		}
		if err := requireTime(fmt.Sprintf("users[%d].createdAt", i), user.CreatedAt); err != nil {
			return err
		}
		if err := requireTime(fmt.Sprintf("users[%d].updatedAt", i), user.UpdatedAt); err != nil {
			return err
		}
		users[user.ID] = user
	}
	seen = map[string]struct{}{}
	offices := map[string]fixtureOffice{}
	for i, office := range state.Offices {
		if err := seenID(seen, office.ID, "office"); err != nil {
			return err
		}
		if office.Name == "" || office.LocationName == "" {
			return fmt.Errorf("greenhouse scenario: offices[%d] requires name and locationName", i)
		}
		offices[office.ID] = office
	}
	seen = map[string]struct{}{}
	departments := map[string]fixtureDepartment{}
	for i, department := range state.Departments {
		if err := seenID(seen, department.ID, "department"); err != nil {
			return err
		}
		if department.Name == "" {
			return fmt.Errorf("greenhouse scenario: departments[%d] requires name", i)
		}
		departments[department.ID] = department
	}
	seen = map[string]struct{}{}
	jobs := map[string]fixtureJob{}
	for i, job := range state.Jobs {
		if err := seenID(seen, job.ID, "job"); err != nil {
			return err
		}
		if job.Name == "" {
			return fmt.Errorf("greenhouse scenario: jobs[%d] requires name", i)
		}
		if err := oneOf(fmt.Sprintf("jobs[%d].status", i), job.Status, jobStatuses); err != nil {
			return err
		}
		if _, ok := departments[job.DepartmentID]; !ok {
			return fmt.Errorf("greenhouse scenario: jobs[%d] references unknown department %q", i, job.DepartmentID)
		}
		if _, ok := offices[job.OfficeID]; !ok {
			return fmt.Errorf("greenhouse scenario: jobs[%d] references unknown office %q", i, job.OfficeID)
		}
		if err := requireTime(fmt.Sprintf("jobs[%d].createdAt", i), job.CreatedAt); err != nil {
			return err
		}
		if err := requireTime(fmt.Sprintf("jobs[%d].openedAt", i), job.OpenedAt); err != nil {
			return err
		}
		if err := optionalTime(fmt.Sprintf("jobs[%d].closedAt", i), job.ClosedAt); err != nil {
			return err
		}
		if err := requireTime(fmt.Sprintf("jobs[%d].updatedAt", i), job.UpdatedAt); err != nil {
			return err
		}
		if err := userList(users, job.HiringManagers, fmt.Sprintf("jobs[%d].hiringManagers", i)); err != nil {
			return err
		}
		if err := userList(users, job.Recruiters, fmt.Sprintf("jobs[%d].recruiters", i)); err != nil {
			return err
		}
		if err := userList(users, job.Coordinators, fmt.Sprintf("jobs[%d].coordinators", i)); err != nil {
			return err
		}
		if err := userList(users, job.Sourcers, fmt.Sprintf("jobs[%d].sourcers", i)); err != nil {
			return err
		}
		if len(job.Recruiters) == 0 {
			if job.ResponsibleRecruiterID != "" {
				return fmt.Errorf("greenhouse scenario: jobs[%d].responsibleRecruiterId is set without recruiters", i)
			}
		} else if !containsString(job.Recruiters, job.ResponsibleRecruiterID) {
			return fmt.Errorf("greenhouse scenario: jobs[%d].responsibleRecruiterId must be one of recruiters", i)
		}
		jobs[job.ID] = job
	}
	seen = map[string]struct{}{}
	stages := map[string]fixtureJobStage{}
	for i, stage := range state.JobStages {
		if err := seenID(seen, stage.ID, "job stage"); err != nil {
			return err
		}
		if stage.Name == "" {
			return fmt.Errorf("greenhouse scenario: jobStages[%d] requires name", i)
		}
		if _, ok := jobs[stage.JobID]; !ok {
			return fmt.Errorf("greenhouse scenario: jobStages[%d] references unknown job %q", i, stage.JobID)
		}
		if stage.Priority < 0 {
			return fmt.Errorf("greenhouse scenario: jobStages[%d].priority must be non-negative", i)
		}
		if err := requireTime(fmt.Sprintf("jobStages[%d].createdAt", i), stage.CreatedAt); err != nil {
			return err
		}
		if err := requireTime(fmt.Sprintf("jobStages[%d].updatedAt", i), stage.UpdatedAt); err != nil {
			return err
		}
		stages[stage.ID] = stage
	}
	seen = map[string]struct{}{}
	candidates := map[string]fixtureCandidate{}
	for i, candidate := range state.Candidates {
		if err := seenID(seen, candidate.ID, "candidate"); err != nil {
			return err
		}
		if candidate.FirstName == "" || candidate.LastName == "" {
			return fmt.Errorf("greenhouse scenario: candidates[%d] requires firstName and lastName", i)
		}
		if _, ok := users[candidate.RecruiterID]; !ok {
			return fmt.Errorf("greenhouse scenario: candidates[%d] references unknown recruiter %q", i, candidate.RecruiterID)
		}
		if len(candidate.Emails) == 0 {
			return fmt.Errorf("greenhouse scenario: candidates[%d].emails is required", i)
		}
		emailSeen := map[string]struct{}{}
		for j, email := range candidate.Emails {
			if err := parseEmail(fmt.Sprintf("candidates[%d].emails[%d].value", i, j), email.Value); err != nil {
				return err
			}
			if err := oneOf(fmt.Sprintf("candidates[%d].emails[%d].type", i, j), email.Type, emailTypes); err != nil {
				return err
			}
			if _, ok := emailSeen[email.Value]; ok {
				return fmt.Errorf("greenhouse scenario: candidates[%d] repeats email %q", i, email.Value)
			}
			emailSeen[email.Value] = struct{}{}
		}
		phoneSeen := map[string]struct{}{}
		for j, phone := range candidate.Phones {
			if phone.Value == "" {
				return fmt.Errorf("greenhouse scenario: candidates[%d].phones[%d].value is required", i, j)
			}
			if err := oneOf(fmt.Sprintf("candidates[%d].phones[%d].type", i, j), phone.Type, phoneTypes); err != nil {
				return err
			}
			if _, ok := phoneSeen[phone.Value]; ok {
				return fmt.Errorf("greenhouse scenario: candidates[%d] repeats phone %q", i, phone.Value)
			}
			phoneSeen[phone.Value] = struct{}{}
		}
		for j, tag := range candidate.Tags {
			if tag == "" {
				return fmt.Errorf("greenhouse scenario: candidates[%d].tags[%d] is empty", i, j)
			}
		}
		for j, attachment := range candidate.Attachments {
			if attachment.Filename == "" || attachment.URL == "" {
				return fmt.Errorf("greenhouse scenario: candidates[%d].attachments[%d] requires filename and url", i, j)
			}
			if err := oneOf(fmt.Sprintf("candidates[%d].attachments[%d].type", i, j), attachment.Type, attachmentTypes); err != nil {
				return err
			}
			if err := requireTime(fmt.Sprintf("candidates[%d].attachments[%d].createdAt", i, j), attachment.CreatedAt); err != nil {
				return err
			}
		}
		if err := requireTime(fmt.Sprintf("candidates[%d].createdAt", i), candidate.CreatedAt); err != nil {
			return err
		}
		if err := requireTime(fmt.Sprintf("candidates[%d].updatedAt", i), candidate.UpdatedAt); err != nil {
			return err
		}
		if err := requireTime(fmt.Sprintf("candidates[%d].lastActivity", i), candidate.LastActivity); err != nil {
			return err
		}
		candidates[candidate.ID] = candidate
	}
	seen = map[string]struct{}{}
	applications := map[string]fixtureApplication{}
	candidateJobs := map[string]struct{}{}
	for i, app := range state.Applications {
		if err := seenID(seen, app.ID, "application"); err != nil {
			return err
		}
		if _, ok := candidates[app.CandidateID]; !ok {
			return fmt.Errorf("greenhouse scenario: applications[%d] references unknown candidate %q", i, app.CandidateID)
		}
		if _, ok := jobs[app.JobID]; !ok {
			return fmt.Errorf("greenhouse scenario: applications[%d] references unknown job %q", i, app.JobID)
		}
		stage, ok := stages[app.StageID]
		if !ok {
			return fmt.Errorf("greenhouse scenario: applications[%d] references unknown stage %q", i, app.StageID)
		}
		if stage.JobID != app.JobID {
			return fmt.Errorf("greenhouse scenario: applications[%d] stage %q belongs to another job", i, app.StageID)
		}
		if _, ok := users[app.RecruiterID]; !ok {
			return fmt.Errorf("greenhouse scenario: applications[%d] references unknown recruiter %q", i, app.RecruiterID)
		}
		if app.SourceID == "" || app.SourceName == "" {
			return fmt.Errorf("greenhouse scenario: applications[%d] requires sourceId and sourceName", i)
		}
		if err := oneOf(fmt.Sprintf("applications[%d].status", i), app.Status, applicationStatuses); err != nil {
			return err
		}
		if app.Status == "rejected" && app.RejectedAt == "" {
			return fmt.Errorf("greenhouse scenario: applications[%d].rejectedAt is required when status is rejected", i)
		}
		if app.Status != "rejected" && app.RejectedAt != "" {
			return fmt.Errorf("greenhouse scenario: applications[%d].rejectedAt must be empty unless status is rejected", i)
		}
		if err := requireTime(fmt.Sprintf("applications[%d].appliedAt", i), app.AppliedAt); err != nil {
			return err
		}
		if err := optionalTime(fmt.Sprintf("applications[%d].rejectedAt", i), app.RejectedAt); err != nil {
			return err
		}
		if err := requireTime(fmt.Sprintf("applications[%d].lastActivityAt", i), app.LastActivityAt); err != nil {
			return err
		}
		pair := app.CandidateID + "\x00" + app.JobID
		if _, ok := candidateJobs[pair]; ok {
			return fmt.Errorf("greenhouse scenario: candidate %q already has an application on job %q", app.CandidateID, app.JobID)
		}
		candidateJobs[pair] = struct{}{}
		applications[app.ID] = app
	}
	seen = map[string]struct{}{}
	for i, offer := range state.Offers {
		if err := seenID(seen, offer.ID, "offer"); err != nil {
			return err
		}
		app, ok := applications[offer.ApplicationID]
		if !ok {
			return fmt.Errorf("greenhouse scenario: offers[%d] references unknown application %q", i, offer.ApplicationID)
		}
		if offer.CandidateID != app.CandidateID || offer.JobID != app.JobID {
			return fmt.Errorf("greenhouse scenario: offers[%d] candidate and job must match application %s", i, app.ID)
		}
		if offer.Version < 1 {
			return fmt.Errorf("greenhouse scenario: offers[%d].version must be at least 1", i)
		}
		if err := oneOf(fmt.Sprintf("offers[%d].status", i), offer.Status, offerStatuses); err != nil {
			return err
		}
		if offer.CustomFields == nil {
			return fmt.Errorf("greenhouse scenario: offers[%d].customFields is required", i)
		}
		if err := requireTime(fmt.Sprintf("offers[%d].createdAt", i), offer.CreatedAt); err != nil {
			return err
		}
		if err := requireTime(fmt.Sprintf("offers[%d].updatedAt", i), offer.UpdatedAt); err != nil {
			return err
		}
		if err := optionalDate(fmt.Sprintf("offers[%d].sentAt", i), offer.SentAt); err != nil {
			return err
		}
		if err := optionalTime(fmt.Sprintf("offers[%d].resolvedAt", i), offer.ResolvedAt); err != nil {
			return err
		}
		if err := optionalDate(fmt.Sprintf("offers[%d].startsAt", i), offer.StartsAt); err != nil {
			return err
		}
		if offer.Status == "accepted" && (offer.SentAt == "" || offer.ResolvedAt == "" || offer.StartsAt == "") {
			return fmt.Errorf("greenhouse scenario: offers[%d] accepted offers require sentAt, resolvedAt, and startsAt", i)
		}
	}
	seen = map[string]struct{}{}
	for i, note := range state.Notes {
		if err := seenID(seen, note.ID, "note"); err != nil {
			return err
		}
		if _, ok := candidates[note.CandidateID]; !ok {
			return fmt.Errorf("greenhouse scenario: notes[%d] references unknown candidate %q", i, note.CandidateID)
		}
		if _, ok := users[note.UserID]; !ok {
			return fmt.Errorf("greenhouse scenario: notes[%d] references unknown user %q", i, note.UserID)
		}
		if note.Body == "" {
			return fmt.Errorf("greenhouse scenario: notes[%d].body is required", i)
		}
		if err := oneOf(fmt.Sprintf("notes[%d].visibility", i), note.Visibility, visibilities); err != nil {
			return err
		}
		if err := requireTime(fmt.Sprintf("notes[%d].createdAt", i), note.CreatedAt); err != nil {
			return err
		}
	}
	seen = map[string]struct{}{}
	for i, card := range state.Scorecards {
		if err := seenID(seen, card.ID, "scorecard"); err != nil {
			return err
		}
		app, ok := applications[card.ApplicationID]
		if !ok {
			return fmt.Errorf("greenhouse scenario: scorecards[%d] references unknown application %q", i, card.ApplicationID)
		}
		if card.CandidateID != app.CandidateID {
			return fmt.Errorf("greenhouse scenario: scorecards[%d].candidateId must match application %s", i, app.ID)
		}
		if card.Interview == "" || card.InterviewStepName == "" {
			return fmt.Errorf("greenhouse scenario: scorecards[%d] requires interview and interviewStepName", i)
		}
		stage, ok := stages[card.InterviewStepID]
		if !ok || stage.JobID != app.JobID {
			return fmt.Errorf("greenhouse scenario: scorecards[%d].interviewStepId must be a stage on the application's job", i)
		}
		if _, ok := users[card.SubmittedBy]; !ok {
			return fmt.Errorf("greenhouse scenario: scorecards[%d] references unknown submittedBy %q", i, card.SubmittedBy)
		}
		if _, ok := users[card.InterviewerID]; !ok {
			return fmt.Errorf("greenhouse scenario: scorecards[%d] references unknown interviewer %q", i, card.InterviewerID)
		}
		if err := oneOf(fmt.Sprintf("scorecards[%d].overallRecommendation", i), card.OverallRecommendation, recommendations); err != nil {
			return err
		}
		if err := requireTime(fmt.Sprintf("scorecards[%d].interviewedAt", i), card.InterviewedAt); err != nil {
			return err
		}
		if err := requireTime(fmt.Sprintf("scorecards[%d].submittedAt", i), card.SubmittedAt); err != nil {
			return err
		}
		if err := requireTime(fmt.Sprintf("scorecards[%d].createdAt", i), card.CreatedAt); err != nil {
			return err
		}
		if err := requireTime(fmt.Sprintf("scorecards[%d].updatedAt", i), card.UpdatedAt); err != nil {
			return err
		}
		for j, attr := range card.Attributes {
			if attr.Name == "" || attr.Type == "" {
				return fmt.Errorf("greenhouse scenario: scorecards[%d].attributes[%d] requires name and type", i, j)
			}
			if err := oneOf(fmt.Sprintf("scorecards[%d].attributes[%d].rating", i, j), attr.Rating, attributeRatings); err != nil {
				return err
			}
		}
		for j, question := range card.Questions {
			if question.ID == "" || question.Question == "" {
				return fmt.Errorf("greenhouse scenario: scorecards[%d].questions[%d] requires id and question", i, j)
			}
		}
	}
	return nil
}

func userList(users map[string]fixtureUser, ids []string, field string) error {
	seen := map[string]struct{}{}
	for _, id := range ids {
		if _, ok := users[id]; !ok {
			return fmt.Errorf("greenhouse scenario: %s references unknown user %q", field, id)
		}
		if _, ok := seen[id]; ok {
			return fmt.Errorf("greenhouse scenario: %s repeats user %q", field, id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func seenID(ids map[string]struct{}, id, label string) error {
	if id == "" {
		return fmt.Errorf("greenhouse scenario: %s id is required", label)
	}
	if _, ok := ids[id]; ok {
		return fmt.Errorf("greenhouse scenario: duplicate %s id %q", label, id)
	}
	ids[id] = struct{}{}
	return nil
}

func parseEmail(field, value string) error {
	addr, err := mail.ParseAddress(value)
	if err != nil {
		return fmt.Errorf("greenhouse scenario: %s: %w", field, err)
	}
	if addr.Address != value {
		return fmt.Errorf("greenhouse scenario: %s must be a bare email address", field)
	}
	return nil
}

func requireTime(field, value string) error {
	if _, err := time.Parse(time.RFC3339, value); err != nil {
		return fmt.Errorf("greenhouse scenario: %s: %w", field, err)
	}
	return nil
}

func optionalTime(field, value string) error {
	if value == "" {
		return nil
	}
	return requireTime(field, value)
}

func optionalDate(field, value string) error {
	if value == "" {
		return nil
	}
	if _, err := time.Parse("2006-01-02", value); err != nil {
		return fmt.Errorf("greenhouse scenario: %s: %w", field, err)
	}
	return nil
}

func oneOf(field, value string, allowed map[string]struct{}) error {
	if _, ok := allowed[value]; !ok {
		return fmt.Errorf("greenhouse scenario: %s %q is not allowed", field, value)
	}
	return nil
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

var (
	jobStatuses         = map[string]struct{}{"open": {}, "closed": {}, "draft": {}}
	applicationStatuses = map[string]struct{}{"active": {}, "rejected": {}, "hired": {}, "converted": {}}
	offerStatuses       = map[string]struct{}{"unresolved": {}, "accepted": {}, "rejected": {}, "deprecated": {}}
	visibilities        = map[string]struct{}{"admin_only": {}, "private": {}, "public": {}}
	recommendations     = map[string]struct{}{"definitely_not": {}, "no": {}, "yes": {}, "strong_yes": {}, "no_decision": {}}
	attributeRatings    = map[string]struct{}{"definitely_not": {}, "no": {}, "mixed": {}, "yes": {}, "strong_yes": {}, "no_decision": {}}
	emailTypes          = map[string]struct{}{"personal": {}, "work": {}, "other": {}}
	phoneTypes          = map[string]struct{}{"home": {}, "work": {}, "mobile": {}, "skype": {}, "other": {}}
	attachmentTypes     = map[string]struct{}{"resume": {}, "cover_letter": {}, "offer_packet": {}, "offer_letter": {}, "take_home_test": {}, "other": {}}
)

func ContractName() string { return scenario.Contract }

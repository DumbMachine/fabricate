package outreach

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

var compiledScenarioSchema = func() *jsonschema.Schema {
	parsed, err := jsonschema.UnmarshalJSON(bytes.NewReader(scenarioSchema))
	if err != nil {
		panic(fmt.Sprintf("outreach: parse embedded scenario schema: %v", err))
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("outreach-scenario.json", parsed); err != nil {
		panic(fmt.Sprintf("outreach: add embedded scenario schema: %v", err))
	}
	compiled, err := compiler.Compile("outreach-scenario.json")
	if err != nil {
		panic(fmt.Sprintf("outreach: compile embedded scenario schema: %v", err))
	}
	return compiled
}()

type scenarioCodec struct{}

type fixtureState struct {
	Sequences      []fixtureSequence      `json:"sequences"`
	Prospects      []fixtureProspect      `json:"prospects"`
	SequenceStates []fixtureSequenceState `json:"sequenceStates"`
	Tasks          []fixtureTask          `json:"tasks"`
}

type fixtureSequence struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Enabled      bool     `json:"enabled"`
	SequenceType string   `json:"sequenceType"`
	ShareType    string   `json:"shareType"`
	Tags         []string `json:"tags"`
	CreatedAt    string   `json:"createdAt"`
	UpdatedAt    string   `json:"updatedAt"`
}

type fixtureProspect struct {
	ID           string   `json:"id"`
	FirstName    string   `json:"firstName"`
	LastName     string   `json:"lastName"`
	Name         string   `json:"name"`
	Emails       []string `json:"emails"`
	Title        string   `json:"title"`
	Company      string   `json:"company"`
	MobilePhones []string `json:"mobilePhones"`
	Tags         []string `json:"tags"`
	CreatedAt    string   `json:"createdAt"`
	UpdatedAt    string   `json:"updatedAt"`
}

type fixtureSequenceState struct {
	ID             string `json:"id"`
	State          string `json:"state"`
	ProspectID     string `json:"prospectId"`
	SequenceID     string `json:"sequenceId"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
	StateChangedAt string `json:"stateChangedAt"`
}

type fixtureTask struct {
	ID             string `json:"id"`
	Action         string `json:"action"`
	Note           string `json:"note"`
	Completed      bool   `json:"completed"`
	State          string `json:"state"`
	TaskType       string `json:"taskType"`
	DueAt          string `json:"dueAt"`
	CompletedAt    string `json:"completedAt"`
	StateChangedAt string `json:"stateChangedAt"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
	ProspectID     string `json:"prospectId"`
	SequenceID     string `json:"sequenceId"`
}

func (scenarioCodec) Validate(_ context.Context, doc scenario.Document) error {
	if err := doc.ValidateEnvelope(); err != nil {
		return err
	}
	if doc.Resource != "outreach" || doc.ResourceVersion != "v2" {
		return fmt.Errorf("outreach scenario: expected resource outreach v2, got %s %s", doc.Resource, doc.ResourceVersion)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc.State))
	if err != nil {
		return fmt.Errorf("outreach scenario: decode state for schema validation: %w", err)
	}
	if err := compiledScenarioSchema.Validate(instance); err != nil {
		return fmt.Errorf("outreach scenario: schema validation: %w", err)
	}
	state, err := decodeState(doc.State)
	if err != nil {
		return err
	}
	sequences := map[string]struct{}{}
	for i, sequence := range state.Sequences {
		if _, exists := sequences[sequence.ID]; exists {
			return fmt.Errorf("outreach scenario: duplicate sequence id %q", sequence.ID)
		}
		sequences[sequence.ID] = struct{}{}
		if _, err := time.Parse(time.RFC3339, sequence.CreatedAt); err != nil {
			return fmt.Errorf("outreach scenario: sequences[%d].createdAt: %w", i, err)
		}
		if _, err := time.Parse(time.RFC3339, sequence.UpdatedAt); err != nil {
			return fmt.Errorf("outreach scenario: sequences[%d].updatedAt: %w", i, err)
		}
	}
	prospects := map[string]struct{}{}
	emails := map[string]struct{}{}
	for i, prospect := range state.Prospects {
		if _, exists := prospects[prospect.ID]; exists {
			return fmt.Errorf("outreach scenario: duplicate prospect id %q", prospect.ID)
		}
		prospects[prospect.ID] = struct{}{}
		for j, email := range prospect.Emails {
			if _, err := mail.ParseAddress(email); err != nil {
				return fmt.Errorf("outreach scenario: prospects[%d].emails[%d]: %w", i, j, err)
			}
			if _, exists := emails[email]; exists {
				return fmt.Errorf("outreach scenario: duplicate prospect email %q", email)
			}
			emails[email] = struct{}{}
		}
		if _, err := time.Parse(time.RFC3339, prospect.CreatedAt); err != nil {
			return fmt.Errorf("outreach scenario: prospects[%d].createdAt: %w", i, err)
		}
		if _, err := time.Parse(time.RFC3339, prospect.UpdatedAt); err != nil {
			return fmt.Errorf("outreach scenario: prospects[%d].updatedAt: %w", i, err)
		}
	}
	states := map[string]struct{}{}
	pairs := map[string]struct{}{}
	for i, item := range state.SequenceStates {
		if _, exists := states[item.ID]; exists {
			return fmt.Errorf("outreach scenario: duplicate sequence state id %q", item.ID)
		}
		states[item.ID] = struct{}{}
		if _, ok := prospects[item.ProspectID]; !ok {
			return fmt.Errorf("outreach scenario: sequenceStates[%d] references unknown prospect %q", i, item.ProspectID)
		}
		if _, ok := sequences[item.SequenceID]; !ok {
			return fmt.Errorf("outreach scenario: sequenceStates[%d] references unknown sequence %q", i, item.SequenceID)
		}
		pair := item.ProspectID + "\x00" + item.SequenceID
		if _, exists := pairs[pair]; exists {
			return fmt.Errorf("outreach scenario: prospect %q is already in sequence %q", item.ProspectID, item.SequenceID)
		}
		pairs[pair] = struct{}{}
		if _, err := time.Parse(time.RFC3339, item.CreatedAt); err != nil {
			return fmt.Errorf("outreach scenario: sequenceStates[%d].createdAt: %w", i, err)
		}
		if _, err := time.Parse(time.RFC3339, item.UpdatedAt); err != nil {
			return fmt.Errorf("outreach scenario: sequenceStates[%d].updatedAt: %w", i, err)
		}
		if _, err := time.Parse(time.RFC3339, item.StateChangedAt); err != nil {
			return fmt.Errorf("outreach scenario: sequenceStates[%d].stateChangedAt: %w", i, err)
		}
	}
	tasks := map[string]struct{}{}
	for i, task := range state.Tasks {
		if _, exists := tasks[task.ID]; exists {
			return fmt.Errorf("outreach scenario: duplicate task id %q", task.ID)
		}
		tasks[task.ID] = struct{}{}
		if _, ok := prospects[task.ProspectID]; !ok {
			return fmt.Errorf("outreach scenario: tasks[%d] references unknown prospect %q", i, task.ProspectID)
		}
		if task.SequenceID != "" {
			if _, ok := sequences[task.SequenceID]; !ok {
				return fmt.Errorf("outreach scenario: tasks[%d] references unknown sequence %q", i, task.SequenceID)
			}
			if _, ok := pairs[task.ProspectID+"\x00"+task.SequenceID]; !ok {
				return fmt.Errorf("outreach scenario: tasks[%d] prospect %q is not in sequence %q", i, task.ProspectID, task.SequenceID)
			}
		}
		if task.Completed != (task.State == "complete") {
			return fmt.Errorf("outreach scenario: tasks[%d] completed and state disagree", i)
		}
		if task.Completed && task.CompletedAt == "" {
			return fmt.Errorf("outreach scenario: tasks[%d] completed task requires completedAt", i)
		}
		if !task.Completed && task.CompletedAt != "" {
			return fmt.Errorf("outreach scenario: tasks[%d] incomplete task must not set completedAt", i)
		}
		for _, stamp := range []struct {
			name  string
			value string
		}{
			{"dueAt", task.DueAt},
			{"stateChangedAt", task.StateChangedAt},
			{"createdAt", task.CreatedAt},
			{"updatedAt", task.UpdatedAt},
		} {
			if _, err := time.Parse(time.RFC3339, stamp.value); err != nil {
				return fmt.Errorf("outreach scenario: tasks[%d].%s: %w", i, stamp.name, err)
			}
		}
		if task.CompletedAt != "" {
			if _, err := time.Parse(time.RFC3339, task.CompletedAt); err != nil {
				return fmt.Errorf("outreach scenario: tasks[%d].completedAt: %w", i, err)
			}
		}
	}
	return nil
}

func (scenarioCodec) Initialize(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("outreach scenario: initialize: %w", err)
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
		return fmt.Errorf("outreach scenario: begin load: %w", err)
	}
	defer tx.Rollback()
	for _, table := range []string{"tasks", "sequence_states", "prospects", "sequences"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("outreach scenario: clear %s: %w", table, err)
		}
	}
	for i, sequence := range state.Sequences {
		tags, err := json.Marshal(sequence.Tags)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO sequences
			(id, name, description, enabled, sequence_type, share_type, tags, created_at, updated_at, position)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			sequence.ID, sequence.Name, sequence.Description, boolInt(sequence.Enabled), sequence.SequenceType,
			sequence.ShareType, string(tags), sequence.CreatedAt, sequence.UpdatedAt, i); err != nil {
			return fmt.Errorf("outreach scenario: insert sequence %s: %w", sequence.ID, err)
		}
	}
	for i, prospect := range state.Prospects {
		emails, err := json.Marshal(prospect.Emails)
		if err != nil {
			return err
		}
		phones, err := json.Marshal(prospect.MobilePhones)
		if err != nil {
			return err
		}
		tags, err := json.Marshal(prospect.Tags)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO prospects
			(id, first_name, last_name, name, emails, title, company, mobile_phones, tags, created_at, updated_at, position)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			prospect.ID, prospect.FirstName, prospect.LastName, prospect.Name, string(emails), prospect.Title,
			prospect.Company, string(phones), string(tags), prospect.CreatedAt, prospect.UpdatedAt, i); err != nil {
			return fmt.Errorf("outreach scenario: insert prospect %s: %w", prospect.ID, err)
		}
	}
	for i, item := range state.SequenceStates {
		if _, err := tx.ExecContext(ctx, `INSERT INTO sequence_states
			(id, state, prospect_id, sequence_id, created_at, updated_at, state_changed_at, position)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
			item.ID, item.State, item.ProspectID, item.SequenceID, item.CreatedAt, item.UpdatedAt, item.StateChangedAt, i); err != nil {
			return fmt.Errorf("outreach scenario: insert sequence state %s: %w", item.ID, err)
		}
	}
	for i, task := range state.Tasks {
		if _, err := tx.ExecContext(ctx, `INSERT INTO tasks
			(id, action, note, completed, state, task_type, due_at, completed_at, state_changed_at, created_at, updated_at, prospect_id, sequence_id, position)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			task.ID, task.Action, task.Note, boolInt(task.Completed), task.State, task.TaskType, task.DueAt,
			task.CompletedAt, task.StateChangedAt, task.CreatedAt, task.UpdatedAt, task.ProspectID, task.SequenceID, i); err != nil {
			return fmt.Errorf("outreach scenario: insert task %s: %w", task.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("outreach scenario: commit load: %w", err)
	}
	return nil
}

func (scenarioCodec) Dump(ctx context.Context, db *sql.DB, metadata scenario.Metadata) (scenario.Document, error) {
	var state fixtureState
	sequenceRows, err := db.QueryContext(ctx, `SELECT id, name, description, enabled, sequence_type, share_type, tags, created_at, updated_at
		FROM sequences ORDER BY position, id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("outreach scenario: dump sequences: %w", err)
	}
	for sequenceRows.Next() {
		var sequence fixtureSequence
		var enabled int
		var tags string
		if err := sequenceRows.Scan(&sequence.ID, &sequence.Name, &sequence.Description, &enabled, &sequence.SequenceType,
			&sequence.ShareType, &tags, &sequence.CreatedAt, &sequence.UpdatedAt); err != nil {
			sequenceRows.Close()
			return scenario.Document{}, err
		}
		sequence.Enabled = enabled != 0
		sequence.Tags, err = decodeStrings(tags)
		if err != nil {
			sequenceRows.Close()
			return scenario.Document{}, fmt.Errorf("outreach scenario: dump sequence %s tags: %w", sequence.ID, err)
		}
		state.Sequences = append(state.Sequences, sequence)
	}
	if err := sequenceRows.Close(); err != nil {
		return scenario.Document{}, err
	}
	if err := sequenceRows.Err(); err != nil {
		return scenario.Document{}, err
	}
	prospectRows, err := db.QueryContext(ctx, `SELECT id, first_name, last_name, name, emails, title, company, mobile_phones, tags, created_at, updated_at
		FROM prospects ORDER BY position, id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("outreach scenario: dump prospects: %w", err)
	}
	for prospectRows.Next() {
		var prospect fixtureProspect
		var emails, phones, tags string
		if err := prospectRows.Scan(&prospect.ID, &prospect.FirstName, &prospect.LastName, &prospect.Name, &emails,
			&prospect.Title, &prospect.Company, &phones, &tags, &prospect.CreatedAt, &prospect.UpdatedAt); err != nil {
			prospectRows.Close()
			return scenario.Document{}, err
		}
		if prospect.Emails, err = decodeStrings(emails); err != nil {
			prospectRows.Close()
			return scenario.Document{}, err
		}
		if prospect.MobilePhones, err = decodeStrings(phones); err != nil {
			prospectRows.Close()
			return scenario.Document{}, err
		}
		if prospect.Tags, err = decodeStrings(tags); err != nil {
			prospectRows.Close()
			return scenario.Document{}, err
		}
		state.Prospects = append(state.Prospects, prospect)
	}
	if err := prospectRows.Close(); err != nil {
		return scenario.Document{}, err
	}
	if err := prospectRows.Err(); err != nil {
		return scenario.Document{}, err
	}
	stateRows, err := db.QueryContext(ctx, `SELECT id, state, prospect_id, sequence_id, created_at, updated_at, state_changed_at
		FROM sequence_states ORDER BY position, id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("outreach scenario: dump sequence states: %w", err)
	}
	for stateRows.Next() {
		var item fixtureSequenceState
		if err := stateRows.Scan(&item.ID, &item.State, &item.ProspectID, &item.SequenceID, &item.CreatedAt, &item.UpdatedAt, &item.StateChangedAt); err != nil {
			stateRows.Close()
			return scenario.Document{}, err
		}
		state.SequenceStates = append(state.SequenceStates, item)
	}
	if err := stateRows.Close(); err != nil {
		return scenario.Document{}, err
	}
	if err := stateRows.Err(); err != nil {
		return scenario.Document{}, err
	}
	taskRows, err := db.QueryContext(ctx, `SELECT id, action, note, completed, state, task_type, due_at, completed_at, state_changed_at,
		created_at, updated_at, prospect_id, sequence_id FROM tasks ORDER BY position, id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("outreach scenario: dump tasks: %w", err)
	}
	for taskRows.Next() {
		var task fixtureTask
		var completed int
		if err := taskRows.Scan(&task.ID, &task.Action, &task.Note, &completed, &task.State, &task.TaskType, &task.DueAt,
			&task.CompletedAt, &task.StateChangedAt, &task.CreatedAt, &task.UpdatedAt, &task.ProspectID, &task.SequenceID); err != nil {
			taskRows.Close()
			return scenario.Document{}, err
		}
		task.Completed = completed != 0
		state.Tasks = append(state.Tasks, task)
	}
	if err := taskRows.Close(); err != nil {
		return scenario.Document{}, err
	}
	if err := taskRows.Err(); err != nil {
		return scenario.Document{}, err
	}
	if state.Sequences == nil {
		state.Sequences = []fixtureSequence{}
	}
	if state.Prospects == nil {
		state.Prospects = []fixtureProspect{}
	}
	if state.SequenceStates == nil {
		state.SequenceStates = []fixtureSequenceState{}
	}
	if state.Tasks == nil {
		state.Tasks = []fixtureTask{}
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return scenario.Document{}, err
	}
	return scenario.Document{
		Contract: ContractName(), ContractVersion: 1, ID: metadata.ID,
		Resource: "outreach", ResourceVersion: "v2", State: raw,
	}, nil
}

func decodeState(raw []byte) (fixtureState, error) {
	var state fixtureState
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return fixtureState{}, fmt.Errorf("outreach scenario: decode state: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fixtureState{}, fmt.Errorf("outreach scenario: state has trailing data")
	}
	if state.Sequences == nil || state.Prospects == nil || state.SequenceStates == nil || state.Tasks == nil {
		return fixtureState{}, fmt.Errorf("outreach scenario: sequences, prospects, sequenceStates, and tasks are required arrays")
	}
	return state, nil
}

func decodeStrings(raw string) ([]string, error) {
	var items []string
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil, err
	}
	if items == nil {
		return []string{}, nil
	}
	return items, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func ContractName() string { return scenario.Contract }

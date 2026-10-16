package airbyte

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/dumbmachine/fabricate/scenario"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed schema.sql
var schemaSQL string

//go:embed scenario.schema.json
var scenarioSchema []byte

// builtInScenarios keeps the reference worlds inside the fab binary so
// foreground runs do not depend on the source checkout being present.
//
//go:embed scenarios/*.json
var builtInScenarios embed.FS

var compiledScenarioSchema = func() *jsonschema.Schema {
	parsed, err := jsonschema.UnmarshalJSON(bytes.NewReader(scenarioSchema))
	if err != nil {
		panic(fmt.Sprintf("airbyte: parse embedded scenario schema: %v", err))
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("airbyte-scenario.json", parsed); err != nil {
		panic(fmt.Sprintf("airbyte: add embedded scenario schema: %v", err))
	}
	compiled, err := compiler.Compile("airbyte-scenario.json")
	if err != nil {
		panic(fmt.Sprintf("airbyte: compile embedded scenario schema: %v", err))
	}
	return compiled
}()

type scenarioCodec struct{}

type fixtureState struct {
	WorkspaceID  string               `json:"workspaceId"`
	Sources      []fixtureSource      `json:"sources"`
	Destinations []fixtureDestination `json:"destinations"`
	Connections  []fixtureConnection  `json:"connections"`
	Jobs         []fixtureJob         `json:"jobs"`
}

type fixtureSource struct {
	SourceID      string              `json:"sourceId"`
	Name          string              `json:"name"`
	SourceType    string              `json:"sourceType"`
	WorkspaceID   string              `json:"workspaceId"`
	Configuration sourceConfiguration `json:"configuration"`
}

type sourceConfiguration struct {
	SourceType string `json:"sourceType"`
	Shop       string `json:"shop,omitempty"`
}

type fixtureDestination struct {
	DestinationID   string                   `json:"destinationId"`
	Name            string                   `json:"name"`
	DestinationType string                   `json:"destinationType"`
	WorkspaceID     string                   `json:"workspaceId"`
	Configuration   destinationConfiguration `json:"configuration"`
}

type destinationConfiguration struct {
	DestinationType string `json:"destinationType"`
	Host            string `json:"host"`
	Database        string `json:"database"`
	Schema          string `json:"schema"`
}

type fixtureConnection struct {
	ConnectionID   string               `json:"connectionId"`
	Name           string               `json:"name"`
	SourceID       string               `json:"sourceId"`
	DestinationID  string               `json:"destinationId"`
	WorkspaceID    string               `json:"workspaceId"`
	Status         string               `json:"status"`
	Schedule       connectionSchedule   `json:"schedule"`
	DataResidency  string               `json:"dataResidency"`
	Configurations streamConfigurations `json:"configurations"`
	CreatedAt      int64                `json:"createdAt"`
}

type connectionSchedule struct {
	ScheduleType   string `json:"scheduleType"`
	CronExpression string `json:"cronExpression,omitempty"`
	BasicTiming    string `json:"basicTiming,omitempty"`
}

type streamConfigurations struct {
	Streams []streamConfig `json:"streams"`
}

type streamConfig struct {
	Name     string `json:"name"`
	SyncMode string `json:"syncMode,omitempty"`
}

type fixtureJob struct {
	JobID         string `json:"jobId"`
	Status        string `json:"status"`
	JobType       string `json:"jobType"`
	StartTime     string `json:"startTime"`
	ConnectionID  string `json:"connectionId,omitempty"`
	SourceID      string `json:"sourceId,omitempty"`
	LastUpdatedAt string `json:"lastUpdatedAt,omitempty"`
	Duration      string `json:"duration,omitempty"`
	BytesSynced   *int64 `json:"bytesSynced,omitempty"`
	RowsSynced    *int64 `json:"rowsSynced,omitempty"`
	FailureReason string `json:"failureReason,omitempty"`
}

func (scenarioCodec) Validate(_ context.Context, doc scenario.Document) error {
	if err := doc.ValidateEnvelope(); err != nil {
		return err
	}
	if doc.Resource != "airbyte" || doc.ResourceVersion != "v1" {
		return fmt.Errorf("airbyte scenario: expected resource airbyte v1, got %s %s", doc.Resource, doc.ResourceVersion)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc.State))
	if err != nil {
		return fmt.Errorf("airbyte scenario: decode state for schema validation: %w", err)
	}
	if err := compiledScenarioSchema.Validate(instance); err != nil {
		return fmt.Errorf("airbyte scenario: schema validation: %w", err)
	}
	state, err := decodeState(doc.State)
	if err != nil {
		return err
	}
	return validateState(state)
}

func validateState(state fixtureState) error {
	sources := map[string]fixtureSource{}
	for i, source := range state.Sources {
		if source.WorkspaceID != state.WorkspaceID {
			return fmt.Errorf("airbyte scenario: sources[%d] workspaceId must be %s", i, state.WorkspaceID)
		}
		if source.Configuration.SourceType != source.SourceType {
			return fmt.Errorf("airbyte scenario: sources[%d] configuration.sourceType must match sourceType", i)
		}
		if _, exists := sources[source.SourceID]; exists {
			return fmt.Errorf("airbyte scenario: duplicate source id %q", source.SourceID)
		}
		sources[source.SourceID] = source
	}
	destinations := map[string]fixtureDestination{}
	for i, destination := range state.Destinations {
		if destination.WorkspaceID != state.WorkspaceID {
			return fmt.Errorf("airbyte scenario: destinations[%d] workspaceId must be %s", i, state.WorkspaceID)
		}
		if destination.Configuration.DestinationType != destination.DestinationType {
			return fmt.Errorf("airbyte scenario: destinations[%d] configuration.destinationType must match destinationType", i)
		}
		if _, exists := destinations[destination.DestinationID]; exists {
			return fmt.Errorf("airbyte scenario: duplicate destination id %q", destination.DestinationID)
		}
		destinations[destination.DestinationID] = destination
	}
	connections := map[string]fixtureConnection{}
	for i, connection := range state.Connections {
		if connection.WorkspaceID != state.WorkspaceID {
			return fmt.Errorf("airbyte scenario: connections[%d] workspaceId must be %s", i, state.WorkspaceID)
		}
		if _, ok := sources[connection.SourceID]; !ok {
			return fmt.Errorf("airbyte scenario: connections[%d] references unknown source %q", i, connection.SourceID)
		}
		if _, ok := destinations[connection.DestinationID]; !ok {
			return fmt.Errorf("airbyte scenario: connections[%d] references unknown destination %q", i, connection.DestinationID)
		}
		if _, exists := connections[connection.ConnectionID]; exists {
			return fmt.Errorf("airbyte scenario: duplicate connection id %q", connection.ConnectionID)
		}
		connections[connection.ConnectionID] = connection
	}
	jobs := map[string]struct{}{}
	for i, job := range state.Jobs {
		if _, exists := jobs[job.JobID]; exists {
			return fmt.Errorf("airbyte scenario: duplicate job id %q", job.JobID)
		}
		jobs[job.JobID] = struct{}{}
		if job.ConnectionID == "" && job.SourceID == "" {
			return fmt.Errorf("airbyte scenario: jobs[%d] requires connectionId or sourceId", i)
		}
		if job.ConnectionID != "" {
			if _, ok := connections[job.ConnectionID]; !ok {
				return fmt.Errorf("airbyte scenario: jobs[%d] references unknown connection %q", i, job.ConnectionID)
			}
		}
		if job.SourceID != "" {
			if _, ok := sources[job.SourceID]; !ok {
				return fmt.Errorf("airbyte scenario: jobs[%d] references unknown source %q", i, job.SourceID)
			}
		}
		if _, err := time.Parse(time.RFC3339, job.StartTime); err != nil {
			return fmt.Errorf("airbyte scenario: jobs[%d].startTime: %w", i, err)
		}
		if job.LastUpdatedAt != "" {
			if _, err := time.Parse(time.RFC3339, job.LastUpdatedAt); err != nil {
				return fmt.Errorf("airbyte scenario: jobs[%d].lastUpdatedAt: %w", i, err)
			}
		}
		if job.Status == "failed" && job.FailureReason == "" {
			return fmt.Errorf("airbyte scenario: jobs[%d] failed without failureReason", i)
		}
		if job.Status != "failed" && job.FailureReason != "" {
			return fmt.Errorf("airbyte scenario: jobs[%d] failureReason is only valid when status is failed", i)
		}
	}
	return nil
}

func (scenarioCodec) Initialize(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("airbyte scenario: initialize: %w", err)
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
		return fmt.Errorf("airbyte scenario: begin load: %w", err)
	}
	defer tx.Rollback()
	for _, table := range []string{"sources", "destinations", "connections", "jobs", "metadata"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("airbyte scenario: clear %s: %w", table, err)
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO metadata(key, value) VALUES('workspaceId', ?)", state.WorkspaceID); err != nil {
		return fmt.Errorf("airbyte scenario: insert workspace: %w", err)
	}
	for i, item := range state.Sources {
		if err := insertBody(ctx, tx, "sources", i+1, item.SourceID, item); err != nil {
			return fmt.Errorf("airbyte scenario: insert source %s: %w", item.SourceID, err)
		}
	}
	for i, item := range state.Destinations {
		if err := insertBody(ctx, tx, "destinations", i+1, item.DestinationID, item); err != nil {
			return fmt.Errorf("airbyte scenario: insert destination %s: %w", item.DestinationID, err)
		}
	}
	for i, item := range state.Connections {
		if item.Configurations.Streams == nil {
			item.Configurations.Streams = []streamConfig{}
		}
		if err := insertBody(ctx, tx, "connections", i+1, item.ConnectionID, item); err != nil {
			return fmt.Errorf("airbyte scenario: insert connection %s: %w", item.ConnectionID, err)
		}
	}
	for i, item := range state.Jobs {
		if err := insertBody(ctx, tx, "jobs", i+1, item.JobID, item); err != nil {
			return fmt.Errorf("airbyte scenario: insert job %s: %w", item.JobID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("airbyte scenario: commit load: %w", err)
	}
	return nil
}

func (scenarioCodec) Dump(ctx context.Context, db *sql.DB, metadata scenario.Metadata) (scenario.Document, error) {
	var workspace string
	if err := db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='workspaceId'").Scan(&workspace); err != nil {
		return scenario.Document{}, fmt.Errorf("airbyte scenario: dump workspace: %w", err)
	}
	sources, err := loadAll[fixtureSource](ctx, db, "sources")
	if err != nil {
		return scenario.Document{}, err
	}
	destinations, err := loadAll[fixtureDestination](ctx, db, "destinations")
	if err != nil {
		return scenario.Document{}, err
	}
	connections, err := loadAll[fixtureConnection](ctx, db, "connections")
	if err != nil {
		return scenario.Document{}, err
	}
	for i := range connections {
		if connections[i].Configurations.Streams == nil {
			connections[i].Configurations.Streams = []streamConfig{}
		}
	}
	jobs, err := loadAll[fixtureJob](ctx, db, "jobs")
	if err != nil {
		return scenario.Document{}, err
	}
	raw, err := json.Marshal(fixtureState{
		WorkspaceID:  workspace,
		Sources:      sources,
		Destinations: destinations,
		Connections:  connections,
		Jobs:         jobs,
	})
	if err != nil {
		return scenario.Document{}, err
	}
	return scenario.Document{
		Contract: scenario.Contract, ContractVersion: 1, ID: metadata.ID,
		Resource: "airbyte", ResourceVersion: "v1", State: raw,
	}, nil
}

func decodeState(raw []byte) (fixtureState, error) {
	var state fixtureState
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return fixtureState{}, fmt.Errorf("airbyte scenario: decode state: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fixtureState{}, fmt.Errorf("airbyte scenario: state has trailing data")
	}
	if state.Sources == nil || state.Destinations == nil || state.Connections == nil || state.Jobs == nil {
		return fixtureState{}, fmt.Errorf("airbyte scenario: sources, destinations, connections, and jobs are required arrays")
	}
	return state, nil
}

func knownTable(table string) error {
	switch table {
	case "sources", "destinations", "connections", "jobs":
		return nil
	default:
		return fmt.Errorf("airbyte: unknown table %s", table)
	}
}

func insertBody(ctx context.Context, tx *sql.Tx, table string, seq int, id string, value any) error {
	if err := knownTable(table); err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO "+table+"(seq, id, body) VALUES(?, ?, ?)", seq, id, string(raw))
	return err
}

func loadAll[T any](ctx context.Context, db *sql.DB, table string) ([]T, error) {
	if err := knownTable(table); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, "SELECT body FROM "+table+" ORDER BY seq, id")
	if err != nil {
		return nil, fmt.Errorf("airbyte: list %s: %w", table, err)
	}
	defer rows.Close()
	items := []T{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var item T
		if err := json.Unmarshal([]byte(raw), &item); err != nil {
			return nil, fmt.Errorf("airbyte: decode %s: %w", table, err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func loadOne[T any](ctx context.Context, db *sql.DB, table, id string) (T, error) {
	var zero T
	if err := knownTable(table); err != nil {
		return zero, err
	}
	var raw string
	err := db.QueryRowContext(ctx, "SELECT body FROM "+table+" WHERE id=?", id).Scan(&raw)
	if err != nil {
		return zero, err
	}
	var item T
	if err := json.Unmarshal([]byte(raw), &item); err != nil {
		return zero, fmt.Errorf("airbyte: decode %s %s: %w", table, id, err)
	}
	return item, nil
}

package googledrive

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

const folderMimeType = "application/vnd.google-apps.folder"

//go:embed schema.sql
var schemaSQL string

//go:embed scenario.schema.json
var scenarioSchema []byte

//go:embed scenarios/*.json
var builtInScenarios embed.FS

var compiledScenarioSchema = func() *jsonschema.Schema {
	parsed, err := jsonschema.UnmarshalJSON(bytes.NewReader(scenarioSchema))
	if err != nil {
		panic(fmt.Sprintf("googledrive: parse embedded scenario schema: %v", err))
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("googledrive-scenario.json", parsed); err != nil {
		panic(fmt.Sprintf("googledrive: add embedded scenario schema: %v", err))
	}
	compiled, err := compiler.Compile("googledrive-scenario.json")
	if err != nil {
		panic(fmt.Sprintf("googledrive: compile embedded scenario schema: %v", err))
	}
	return compiled
}()

type scenarioCodec struct{}

type fixtureState struct {
	User        fixtureUser         `json:"user"`
	Files       []fixtureFile       `json:"files"`
	Permissions []fixturePermission `json:"permissions"`
}

type fixtureUser struct {
	DisplayName  string `json:"displayName"`
	EmailAddress string `json:"emailAddress"`
	PermissionID string `json:"permissionId"`
}

type fixtureFile struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	MimeType       string   `json:"mimeType"`
	Description    string   `json:"description"`
	Body           string   `json:"body"`
	Parents        []string `json:"parents"`
	Starred        bool     `json:"starred"`
	Trashed        bool     `json:"trashed"`
	CreatedTime    string   `json:"createdTime"`
	ModifiedTime   string   `json:"modifiedTime"`
	FolderColorRgb string   `json:"folderColorRgb,omitempty"`
}

type fixturePermission struct {
	ID                 string `json:"id"`
	FileID             string `json:"fileId"`
	Type               string `json:"type"`
	Role               string `json:"role"`
	EmailAddress       string `json:"emailAddress"`
	DisplayName        string `json:"displayName"`
	AllowFileDiscovery bool   `json:"allowFileDiscovery,omitempty"`
}

func (scenarioCodec) Validate(_ context.Context, doc scenario.Document) error {
	if err := doc.ValidateEnvelope(); err != nil {
		return err
	}
	if doc.Resource != "googledrive" || doc.ResourceVersion != "v3" {
		return fmt.Errorf("googledrive scenario: expected resource googledrive v3, got %s %s", doc.Resource, doc.ResourceVersion)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc.State))
	if err != nil {
		return fmt.Errorf("googledrive scenario: decode state for schema validation: %w", err)
	}
	if err := compiledScenarioSchema.Validate(instance); err != nil {
		return fmt.Errorf("googledrive scenario: schema validation: %w", err)
	}
	state, err := decodeState(doc.State)
	if err != nil {
		return err
	}
	if _, err := mail.ParseAddress(state.User.EmailAddress); err != nil {
		return fmt.Errorf("googledrive scenario: user.emailAddress: %w", err)
	}
	files := make(map[string]fixtureFile, len(state.Files))
	for i, file := range state.Files {
		if _, exists := files[file.ID]; exists {
			return fmt.Errorf("googledrive scenario: duplicate file id %q", file.ID)
		}
		if _, err := time.Parse(time.RFC3339, file.CreatedTime); err != nil {
			return fmt.Errorf("googledrive scenario: files[%d].createdTime: %w", i, err)
		}
		if _, err := time.Parse(time.RFC3339, file.ModifiedTime); err != nil {
			return fmt.Errorf("googledrive scenario: files[%d].modifiedTime: %w", i, err)
		}
		files[file.ID] = file
	}
	for _, file := range state.Files {
		if len(file.Parents) > 1 {
			return fmt.Errorf("googledrive scenario: file %q has multiple parents", file.ID)
		}
		for _, parentID := range file.Parents {
			parent, ok := files[parentID]
			if !ok {
				return fmt.Errorf("googledrive scenario: file %q references unknown parent %q", file.ID, parentID)
			}
			if parent.MimeType != folderMimeType {
				return fmt.Errorf("googledrive scenario: file %q parent %q is not a folder", file.ID, parentID)
			}
		}
	}
	visiting := map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if visiting[id] {
			return fmt.Errorf("googledrive scenario: parent cycle includes %q", id)
		}
		visiting[id] = true
		for _, parentID := range files[id].Parents {
			if err := visit(parentID); err != nil {
				return err
			}
		}
		visiting[id] = false
		return nil
	}
	for id := range files {
		if err := visit(id); err != nil {
			return err
		}
	}
	owners := map[string]int{}
	seenPerm := map[string]struct{}{}
	for i, perm := range state.Permissions {
		if _, ok := files[perm.FileID]; !ok {
			return fmt.Errorf("googledrive scenario: permissions[%d] references unknown file %q", i, perm.FileID)
		}
		key := perm.FileID + "\x00" + perm.ID
		if _, exists := seenPerm[key]; exists {
			return fmt.Errorf("googledrive scenario: duplicate permission %q on file %q", perm.ID, perm.FileID)
		}
		seenPerm[key] = struct{}{}
		if perm.Type == "user" {
			if _, err := mail.ParseAddress(perm.EmailAddress); err != nil {
				return fmt.Errorf("googledrive scenario: permissions[%d].emailAddress: %w", i, err)
			}
		}
		if perm.Role == "owner" {
			owners[perm.FileID]++
			if perm.Type != "user" || perm.ID != state.User.PermissionID || perm.EmailAddress != state.User.EmailAddress {
				return fmt.Errorf("googledrive scenario: file %q owner must be %s (%s)", perm.FileID, state.User.EmailAddress, state.User.PermissionID)
			}
		}
	}
	for id := range files {
		if owners[id] != 1 {
			return fmt.Errorf("googledrive scenario: file %q must have exactly one owner permission", id)
		}
	}
	return nil
}

func (scenarioCodec) Initialize(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("googledrive scenario: initialize: %w", err)
	}
	return nil
}

func (codec scenarioCodec) Load(ctx context.Context, db *sql.DB, doc scenario.Document) error {
	if err := codec.Validate(ctx, doc); err != nil {
		return err
	}
	state, _ := decodeState(doc.State)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("googledrive scenario: begin load: %w", err)
	}
	defer tx.Rollback()
	for _, table := range []string{"permissions", "files", "metadata"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("googledrive scenario: clear %s: %w", table, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO metadata(key, value) VALUES
		('displayName', ?), ('emailAddress', ?), ('permissionId', ?)`,
		state.User.DisplayName, state.User.EmailAddress, state.User.PermissionID); err != nil {
		return fmt.Errorf("googledrive scenario: insert user: %w", err)
	}
	for _, file := range state.Files {
		parents := file.Parents
		if parents == nil {
			parents = []string{}
		}
		encoded, err := json.Marshal(parents)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO files
			(id, name, mime_type, description, body, parents, starred, trashed, explicitly_trashed, created_time, modified_time, folder_color_rgb, version)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)`,
			file.ID, file.Name, file.MimeType, file.Description, file.Body, string(encoded),
			boolInt(file.Starred), boolInt(file.Trashed), boolInt(file.Trashed),
			file.CreatedTime, file.ModifiedTime, file.FolderColorRgb); err != nil {
			return fmt.Errorf("googledrive scenario: insert file %s: %w", file.ID, err)
		}
	}
	for _, perm := range state.Permissions {
		if _, err := tx.ExecContext(ctx, `INSERT INTO permissions
			(file_id, id, type, role, email_address, display_name, allow_file_discovery)
			VALUES(?, ?, ?, ?, ?, ?, ?)`,
			perm.FileID, perm.ID, perm.Type, perm.Role, perm.EmailAddress, perm.DisplayName, boolInt(perm.AllowFileDiscovery)); err != nil {
			return fmt.Errorf("googledrive scenario: insert permission %s: %w", perm.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("googledrive scenario: commit load: %w", err)
	}
	return nil
}

func (scenarioCodec) Dump(ctx context.Context, db *sql.DB, metadata scenario.Metadata) (scenario.Document, error) {
	var state fixtureState
	rows, err := db.QueryContext(ctx, "SELECT key, value FROM metadata WHERE key IN ('displayName', 'emailAddress', 'permissionId')")
	if err != nil {
		return scenario.Document{}, fmt.Errorf("googledrive scenario: dump user: %w", err)
	}
	values := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			rows.Close()
			return scenario.Document{}, err
		}
		values[key] = value
	}
	if err := rows.Close(); err != nil {
		return scenario.Document{}, err
	}
	state.User = fixtureUser{DisplayName: values["displayName"], EmailAddress: values["emailAddress"], PermissionID: values["permissionId"]}
	fileRows, err := db.QueryContext(ctx, `SELECT id, name, mime_type, description, body, parents, starred, trashed, created_time, modified_time, folder_color_rgb
		FROM files ORDER BY id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("googledrive scenario: dump files: %w", err)
	}
	for fileRows.Next() {
		var file fixtureFile
		var parents string
		var starred, trashed int
		if err := fileRows.Scan(&file.ID, &file.Name, &file.MimeType, &file.Description, &file.Body, &parents, &starred, &trashed, &file.CreatedTime, &file.ModifiedTime, &file.FolderColorRgb); err != nil {
			fileRows.Close()
			return scenario.Document{}, err
		}
		if err := json.Unmarshal([]byte(parents), &file.Parents); err != nil {
			fileRows.Close()
			return scenario.Document{}, fmt.Errorf("googledrive scenario: dump file %s parents: %w", file.ID, err)
		}
		if file.Parents == nil {
			file.Parents = []string{}
		}
		file.Starred = starred != 0
		file.Trashed = trashed != 0
		state.Files = append(state.Files, file)
	}
	if err := fileRows.Close(); err != nil {
		return scenario.Document{}, err
	}
	permRows, err := db.QueryContext(ctx, `SELECT id, file_id, type, role, email_address, display_name, allow_file_discovery
		FROM permissions ORDER BY file_id, id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("googledrive scenario: dump permissions: %w", err)
	}
	defer permRows.Close()
	for permRows.Next() {
		var perm fixturePermission
		var allow int
		if err := permRows.Scan(&perm.ID, &perm.FileID, &perm.Type, &perm.Role, &perm.EmailAddress, &perm.DisplayName, &allow); err != nil {
			return scenario.Document{}, err
		}
		perm.AllowFileDiscovery = allow != 0
		state.Permissions = append(state.Permissions, perm)
	}
	if err := permRows.Err(); err != nil {
		return scenario.Document{}, err
	}
	if state.Files == nil {
		state.Files = []fixtureFile{}
	}
	if state.Permissions == nil {
		state.Permissions = []fixturePermission{}
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return scenario.Document{}, err
	}
	return scenario.Document{
		Contract: ContractName(), ContractVersion: 1, ID: metadata.ID,
		Resource: "googledrive", ResourceVersion: "v3", State: raw,
	}, nil
}

func decodeState(raw []byte) (fixtureState, error) {
	var state fixtureState
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return fixtureState{}, fmt.Errorf("googledrive scenario: decode state: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fixtureState{}, fmt.Errorf("googledrive scenario: state has trailing data")
	}
	if state.Files == nil || state.Permissions == nil {
		return fixtureState{}, fmt.Errorf("googledrive scenario: files and permissions are required arrays")
	}
	return state, nil
}

func ContractName() string { return scenario.Contract }

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

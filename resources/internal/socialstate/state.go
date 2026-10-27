package socialstate

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/dumbmachine/fabricate/scenario"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

type Object struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Payload string `json:"payload"`
}
type State struct {
	Account map[string]string `json:"account"`
	Objects []Object          `json:"objects"`
}
type Codec struct {
	Resource string
	Schema   []byte
}

func (c Codec) Validate(ctx context.Context, d scenario.Document) error {
	if err := d.ValidateEnvelope(); err != nil {
		return err
	}
	if d.Resource != c.Resource || d.ResourceVersion != "v1" {
		return fmt.Errorf("wrong resource/version")
	}
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(c.Schema))
	if err != nil {
		return err
	}
	compiler := jsonschema.NewCompiler()
	if err = compiler.AddResource("state.json", v); err != nil {
		return err
	}
	s, err := compiler.Compile("state.json")
	if err != nil {
		return err
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(d.State))
	if err != nil {
		return err
	}
	if err = s.Validate(value); err != nil {
		return err
	}
	var state State
	if err = json.Unmarshal(d.State, &state); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, o := range state.Objects {
		key := o.Kind + ":" + o.ID
		if seen[key] || !json.Valid([]byte(o.Payload)) {
			return fmt.Errorf("invalid object")
		}
		seen[key] = true
	}
	return nil
}
func (c Codec) Initialize(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS social_account(payload TEXT NOT NULL);CREATE TABLE IF NOT EXISTS social_objects(kind TEXT NOT NULL,id TEXT NOT NULL,payload TEXT NOT NULL,PRIMARY KEY(kind,id));`)
	return err
}
func (c Codec) Load(ctx context.Context, db *sql.DB, d scenario.Document) error {
	if err := c.Validate(ctx, d); err != nil {
		return err
	}
	var s State
	json.Unmarshal(d.State, &s)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM social_objects;DELETE FROM social_account;"); err != nil {
		return err
	}
	b, _ := json.Marshal(s.Account)
	if _, err = tx.ExecContext(ctx, "INSERT INTO social_account VALUES(?)", string(b)); err != nil {
		return err
	}
	for _, o := range s.Objects {
		if _, err = tx.ExecContext(ctx, "INSERT INTO social_objects VALUES(?,?,?)", o.Kind, o.ID, o.Payload); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (c Codec) Dump(ctx context.Context, db *sql.DB, m scenario.Metadata) (scenario.Document, error) {
	s := State{Objects: []Object{}}
	var a string
	if err := db.QueryRowContext(ctx, "SELECT payload FROM social_account").Scan(&a); err != nil {
		return scenario.Document{}, err
	}
	json.Unmarshal([]byte(a), &s.Account)
	rows, err := db.QueryContext(ctx, "SELECT kind,id,payload FROM social_objects ORDER BY kind,id")
	if err != nil {
		return scenario.Document{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var o Object
		if err = rows.Scan(&o.Kind, &o.ID, &o.Payload); err != nil {
			return scenario.Document{}, err
		}
		s.Objects = append(s.Objects, o)
	}
	if err = rows.Err(); err != nil {
		return scenario.Document{}, err
	}
	b, err := json.Marshal(s)
	return scenario.Document{Contract: scenario.Contract, ContractVersion: 1, ID: m.ID, Resource: m.Resource, ResourceVersion: m.ResourceVersion, State: b}, err
}

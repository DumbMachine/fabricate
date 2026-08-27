package specserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

func getDocument(ctx context.Context, db *sql.DB, collection, id string) (map[string]any, error) {
	var raw string
	err := db.QueryRowContext(ctx, `SELECT body FROM documents WHERE collection=? AND id=?`, collection, id).Scan(&raw)
	if err != nil {
		return nil, err
	}
	return decodeObject(raw)
}

func listDocuments(ctx context.Context, db *sql.DB, collection string, params map[string]string) ([]map[string]any, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, body FROM documents WHERE collection=? ORDER BY id`, collection)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		item, err := decodeObject(raw)
		if err != nil {
			return nil, err
		}
		if !matchesParams(item, params) {
			continue
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func upsertDocument(ctx context.Context, db *sql.DB, collection, id string, item map[string]any) error {
	raw, err := json.Marshal(item)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT INTO documents(collection, id, body) VALUES(?, ?, ?) ON CONFLICT(collection, id) DO UPDATE SET body=excluded.body`, collection, id, string(raw))
	return err
}

func deleteDocument(ctx context.Context, db *sql.DB, collection, id string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM documents WHERE collection=? AND id=?`, collection, id)
	return err
}

func decodeObject(raw string) (map[string]any, error) {
	item := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &item); err != nil {
		return nil, err
	}
	return item, nil
}

func matchesParams(item map[string]any, params map[string]string) bool {
	for key, want := range params {
		if strings.EqualFold(key, "id") {
			continue
		}
		got, ok := item[key]
		if !ok {
			continue
		}
		if fmt.Sprint(got) != want {
			return false
		}
	}
	return true
}

func NestedID(body map[string]any) string {
	if id := DocumentID(body); id != "" {
		return id
	}
	for _, key := range []string{"result", "data", "project", "droplet", "customer", "val", "task", "output", "webhook", "zone"} {
		inner, ok := body[key].(map[string]any)
		if !ok {
			continue
		}
		if id := DocumentID(inner); id != "" {
			return id
		}
	}
	return ""
}

func DocumentID(item map[string]any) string {
	for _, key := range []string{"id", "gid", "number", "zone_id", "project_id", "slug"} {
		if id := scalarID(item[key]); id != "" {
			return id
		}
	}
	return scalarID(item["id"])
}

func scalarID(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	case float64:
		return fmt.Sprintf("%.0f", typed)
	case int:
		return fmt.Sprintf("%d", typed)
	case int64:
		return fmt.Sprintf("%d", typed)
	case int32:
		return fmt.Sprintf("%d", typed)
	default:
		return ""
	}
}

package googledrive

import (
	"context"
	"encoding/json"
	"fmt"
)

func loadAccount(ctx context.Context, conn sqlConn) (account, error) {
	rows, err := conn.QueryContext(ctx, "SELECT key, value FROM metadata WHERE key IN ('displayName', 'emailAddress', 'permissionId')")
	if err != nil {
		return account{}, err
	}
	defer rows.Close()
	values := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return account{}, err
		}
		values[key] = value
	}
	if err := rows.Err(); err != nil {
		return account{}, err
	}
	user := account{DisplayName: values["displayName"], EmailAddress: values["emailAddress"], PermissionID: values["permissionId"]}
	if user.EmailAddress == "" || user.PermissionID == "" {
		return account{}, fmt.Errorf("googledrive: account metadata is incomplete")
	}
	return user, nil
}

func loadFiles(ctx context.Context, conn sqlConn) ([]storedFile, error) {
	rows, err := conn.QueryContext(ctx, `SELECT id, name, mime_type, description, body, parents, starred, trashed, explicitly_trashed,
		created_time, modified_time, folder_color_rgb, version FROM files ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storedFile{}
	for rows.Next() {
		file, err := scanFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, file)
	}
	return out, rows.Err()
}

func getFile(ctx context.Context, conn sqlConn, id string) (storedFile, error) {
	row := conn.QueryRowContext(ctx, `SELECT id, name, mime_type, description, body, parents, starred, trashed, explicitly_trashed,
		created_time, modified_time, folder_color_rgb, version FROM files WHERE id=?`, id)
	return scanFile(row)
}

func scanFile(row interface{ Scan(...any) error }) (storedFile, error) {
	var file storedFile
	var parents string
	var starred, trashed, explicit int
	err := row.Scan(&file.ID, &file.Name, &file.MimeType, &file.Description, &file.Body, &parents, &starred, &trashed, &explicit,
		&file.CreatedTime, &file.ModifiedTime, &file.FolderColorRgb, &file.Version)
	if err != nil {
		return storedFile{}, err
	}
	if err := json.Unmarshal([]byte(parents), &file.Parents); err != nil {
		return storedFile{}, err
	}
	if file.Parents == nil {
		file.Parents = []string{}
	}
	file.Starred = starred != 0
	file.Trashed = trashed != 0
	file.ExplicitlyTrashed = explicit != 0
	return file, nil
}

func insertFile(ctx context.Context, conn sqlConn, file storedFile, owner account) error {
	parents, err := json.Marshal(file.Parents)
	if err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO files
		(id, name, mime_type, description, body, parents, starred, trashed, explicitly_trashed, created_time, modified_time, folder_color_rgb, version)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		file.ID, file.Name, file.MimeType, file.Description, file.Body, string(parents),
		boolInt(file.Starred), boolInt(file.Trashed), boolInt(file.ExplicitlyTrashed),
		file.CreatedTime, file.ModifiedTime, file.FolderColorRgb, file.Version); err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, `INSERT INTO permissions
		(file_id, id, type, role, email_address, display_name, allow_file_discovery)
		VALUES(?, ?, 'user', 'owner', ?, ?, 0)`, file.ID, owner.PermissionID, owner.EmailAddress, owner.DisplayName)
	return err
}

func updateFile(ctx context.Context, conn sqlConn, file storedFile) error {
	parents, err := json.Marshal(nonNilStrings(file.Parents))
	if err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, `UPDATE files SET name=?, mime_type=?, description=?, parents=?, starred=?, trashed=?,
		explicitly_trashed=?, created_time=?, modified_time=?, folder_color_rgb=?, version=? WHERE id=?`,
		file.Name, file.MimeType, file.Description, string(parents), boolInt(file.Starred), boolInt(file.Trashed),
		boolInt(file.ExplicitlyTrashed), file.CreatedTime, file.ModifiedTime, file.FolderColorRgb, file.Version, file.ID)
	return err
}

func loadPermissions(ctx context.Context, conn sqlConn, fileID string) ([]storedPermission, error) {
	rows, err := conn.QueryContext(ctx, `SELECT file_id, id, type, role, email_address, display_name, allow_file_discovery
		FROM permissions WHERE file_id=? ORDER BY id`, fileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storedPermission{}
	for rows.Next() {
		perm, err := scanPermission(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, perm)
	}
	return out, rows.Err()
}

func getPermission(ctx context.Context, conn sqlConn, fileID, permissionID string) (storedPermission, error) {
	row := conn.QueryRowContext(ctx, `SELECT file_id, id, type, role, email_address, display_name, allow_file_discovery
		FROM permissions WHERE file_id=? AND id=?`, fileID, permissionID)
	return scanPermission(row)
}

func scanPermission(row interface{ Scan(...any) error }) (storedPermission, error) {
	var perm storedPermission
	var allow int
	err := row.Scan(&perm.FileID, &perm.ID, &perm.Type, &perm.Role, &perm.EmailAddress, &perm.DisplayName, &allow)
	if err != nil {
		return storedPermission{}, err
	}
	perm.AllowFileDiscovery = allow != 0
	return perm, nil
}

package googledrive

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/googledrive/generated"
	"github.com/getkin/kin-openapi/openapi3filter"
	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"
)

const (
	storageLimit   = "16106127360"
	maxUploadSize  = "5242880000"
	fileKind       = "drive#file"
	fileListKind   = "drive#fileList"
	permissionKind = "drive#permission"
	aboutKind      = "drive#about"
	userKind       = "drive#user"
)

var (
	folderColors = []string{
		"#ac725e", "#d06b64", "#f83a22", "#fa573c", "#ff7537", "#ffad46",
		"#42d692", "#16a765", "#7bd148", "#b3dc6c", "#fbe983", "#fad165",
		"#92e1c0", "#9fe1e7", "#9fc6e7", "#4986e7", "#9a9cff", "#b99aff",
		"#c2c2c2", "#cabdbf", "#cca6ac", "#f691b2", "#cd74e6", "#a47ae2",
	}
	folderColorPattern = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
	permissionRoles    = map[string]struct{}{
		"owner": {}, "organizer": {}, "fileOrganizer": {}, "writer": {}, "commenter": {}, "reader": {},
	}
	permissionTypes = map[string]struct{}{
		"user": {}, "group": {}, "domain": {}, "anyone": {},
	}
)

type server struct {
	db      *sql.DB
	clock   httpresource.Clock
	ids     httpresource.IDGenerator
	handler http.Handler
}

type sqlConn interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type account struct {
	DisplayName  string
	EmailAddress string
	PermissionID string
}

type storedFile struct {
	ID                string
	Name              string
	MimeType          string
	Description       string
	Body              string
	Parents           []string
	Starred           bool
	Trashed           bool
	ExplicitlyTrashed bool
	CreatedTime       string
	ModifiedTime      string
	FolderColorRgb    string
	Version           int
}

type storedPermission struct {
	FileID             string
	ID                 string
	Type               string
	Role               string
	EmailAddress       string
	DisplayName        string
	AllowFileDiscovery bool
}

type httpError struct {
	Status  int
	Code    string
	Message string
}

func (e httpError) Error() string { return e.Message }

func badRequest(message string) error {
	return httpError{Status: http.StatusBadRequest, Code: "INVALID_ARGUMENT", Message: message}
}

func notFound(message string) error {
	return httpError{Status: http.StatusNotFound, Code: "NOT_FOUND", Message: message}
}

func conflict(message string) error {
	return httpError{Status: http.StatusConflict, Code: "ALREADY_EXISTS", Message: message}
}

var _ generated.StrictServerInterface = (*server)(nil)

func newServer(ctx context.Context, dependencies httpresource.ServerDependencies) (*server, error) {
	if dependencies.DB == nil || dependencies.Clock == nil || dependencies.IDs == nil || dependencies.Secrets == nil {
		return nil, fmt.Errorf("googledrive: database, clock, ID generator, and secrets are required")
	}
	token, err := dependencies.Secrets.Get(ctx, "token")
	if err != nil {
		return nil, fmt.Errorf("googledrive: load synthetic token: %w", err)
	}
	if token == "" {
		return nil, fmt.Errorf("googledrive: synthetic token is empty")
	}
	spec, err := generated.GetSwagger()
	if err != nil {
		return nil, fmt.Errorf("googledrive: load OpenAPI: %w", err)
	}
	impl := &server{db: dependencies.DB, clock: dependencies.Clock, ids: dependencies.IDs}
	strict := generated.NewStrictHandler(impl, nil)
	generatedHandler := generated.Handler(strict)
	validator := nethttpmiddleware.OapiRequestValidatorWithOptions(spec, &nethttpmiddleware.Options{
		DoNotValidateServers: true,
		Options: openapi3filter.Options{AuthenticationFunc: func(_ context.Context, input *openapi3filter.AuthenticationInput) error {
			header := input.RequestValidationInput.Request.Header.Get("Authorization")
			if header != "Bearer "+token {
				return input.NewError(errors.New("invalid synthetic bearer token"))
			}
			return nil
		}},
		ErrorHandlerWithOpts: func(_ context.Context, err error, w http.ResponseWriter, _ *http.Request, opts nethttpmiddleware.ErrorHandlerOpts) {
			status := opts.StatusCode
			code := "INVALID_ARGUMENT"
			if status == http.StatusUnauthorized {
				code = "UNAUTHENTICATED"
			}
			if status == 0 {
				status = http.StatusBadRequest
			}
			writeError(w, status, code, err.Error())
		},
	})
	impl.handler = validator(generatedHandler)
	return impl, nil
}

func (s *server) Handler() http.Handler       { return s.handler }
func (s *server) Close(context.Context) error { return nil }

func (s *server) DriveAboutGet(ctx context.Context, _ generated.DriveAboutGetRequestObject) (generated.DriveAboutGetResponseObject, error) {
	account, err := loadAccount(ctx, s.db)
	if err != nil {
		return nil, err
	}
	files, err := loadFiles(ctx, s.db)
	if err != nil {
		return nil, err
	}
	usage, trash := 0, 0
	for _, file := range files {
		size := len(file.Body)
		usage += size
		if file.Trashed {
			trash += size
		}
	}
	palette := append([]string{}, folderColors...)
	return generated.DriveAboutGet200JSONResponse{
		Kind: aboutKind, MaxUploadSize: maxUploadSize, CanCreateDrives: true, AppInstalled: true,
		FolderColorPalette: palette,
		User: generated.User{
			Kind: userKind, DisplayName: account.DisplayName, EmailAddress: account.EmailAddress,
			PermissionId: account.PermissionID, Me: true,
		},
		StorageQuota: generated.StorageQuota{
			Limit: storageLimit, Usage: strconv.Itoa(usage), UsageInDrive: strconv.Itoa(usage), UsageInDriveTrash: strconv.Itoa(trash),
		},
	}, nil
}

func (s *server) DriveFilesList(ctx context.Context, request generated.DriveFilesListRequestObject) (generated.DriveFilesListResponseObject, error) {
	fail := fileListErr
	if err := rejectSharedDrive(request.Params.Corpora, request.Params.DriveId); err != nil {
		return fail(err)
	}
	spaces := ""
	if request.Params.Spaces != nil {
		spaces = string(*request.Params.Spaces)
	}
	if !spacesIncludeDrive(spaces) {
		return fileListResponse(nil), nil
	}
	files, err := loadFiles(ctx, s.db)
	if err != nil {
		return nil, err
	}
	query := ""
	if request.Params.Q != nil {
		query = string(*request.Params.Q)
	}
	filtered := make([]storedFile, 0, len(files))
	for _, file := range files {
		ok, err := matchQuery(file, query)
		if err != nil {
			return fail(badRequest(err.Error()))
		}
		if ok {
			filtered = append(filtered, file)
		}
	}
	orderRaw := ""
	if request.Params.OrderBy != nil {
		orderRaw = string(*request.Params.OrderBy)
	}
	keys, err := parseOrderBy(orderRaw)
	if err != nil {
		return fail(badRequest(err.Error()))
	}
	sort.SliceStable(filtered, func(i, j int) bool { return lessFile(filtered[i], filtered[j], keys) })
	var pageSize *int
	if request.Params.PageSize != nil {
		size := int(*request.Params.PageSize)
		pageSize = &size
	}
	var token *string
	if request.Params.PageToken != nil {
		value := string(*request.Params.PageToken)
		token = &value
	}
	start, end, err := pageWindow(token, pageSize, len(filtered))
	if err != nil {
		return fail(badRequest(err.Error()))
	}
	account, err := loadAccount(ctx, s.db)
	if err != nil {
		return nil, err
	}
	rendered := make([]generated.File, 0, end-start)
	for _, file := range filtered[start:end] {
		item, err := s.renderFile(ctx, file, account)
		if err != nil {
			return nil, err
		}
		rendered = append(rendered, item)
	}
	var next *string
	if end < len(filtered) {
		value := strconv.Itoa(end)
		next = &value
	}
	return fileListResponse(next, rendered...), nil
}

func (s *server) DriveFilesGet(ctx context.Context, request generated.DriveFilesGetRequestObject) (generated.DriveFilesGetResponseObject, error) {
	file, account, err := s.loadOwned(ctx, string(request.FileId))
	if err != nil {
		return fileErr(err)
	}
	rendered, err := s.renderFile(ctx, file, account)
	if err != nil {
		return nil, err
	}
	return generated.DriveFilesGet200JSONResponse(rendered), nil
}

func (s *server) DriveFilesCreate(ctx context.Context, request generated.DriveFilesCreateRequestObject) (generated.DriveFilesCreateResponseObject, error) {
	if request.Body == nil {
		return fileCreateErr(badRequest("Request body is required"))
	}
	account, err := loadAccount(ctx, s.db)
	if err != nil {
		return nil, err
	}
	var created storedFile
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		file, err := s.newFile(ctx, tx, *request.Body, account, "")
		if err != nil {
			return err
		}
		if err := insertFile(ctx, tx, file, account); err != nil {
			return err
		}
		created = file
		return nil
	})
	if err != nil {
		return fileCreateErr(err)
	}
	rendered, err := s.renderFile(ctx, created, account)
	if err != nil {
		return nil, err
	}
	return generated.DriveFilesCreate200JSONResponse(rendered), nil
}

func (s *server) DriveFilesUpdate(ctx context.Context, request generated.DriveFilesUpdateRequestObject) (generated.DriveFilesUpdateResponseObject, error) {
	if request.Body == nil {
		return fileUpdateErr(badRequest("Request body is required"))
	}
	account, err := loadAccount(ctx, s.db)
	if err != nil {
		return nil, err
	}
	var updated storedFile
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		file, err := getFile(ctx, tx, string(request.FileId))
		if errors.Is(err, sql.ErrNoRows) {
			return notFound(fmt.Sprintf("File not found: %s.", request.FileId))
		}
		if err != nil {
			return err
		}
		originalMime := file.MimeType
		changed, err := applyFilePatch(&file, *request.Body)
		if err != nil {
			return err
		}
		addParents, removeParents := "", ""
		if request.Params.AddParents != nil {
			addParents = string(*request.Params.AddParents)
		}
		if request.Params.RemoveParents != nil {
			removeParents = string(*request.Params.RemoveParents)
		}
		if addParents != "" || removeParents != "" {
			next := applyParentEdits(file.Parents, addParents, removeParents)
			if err := s.validateParents(ctx, tx, string(request.FileId), next); err != nil {
				return err
			}
			if !sameStrings(file.Parents, next) {
				file.Parents = next
				changed = true
			}
		}
		if originalMime == folderMimeType && file.MimeType != folderMimeType {
			existing, err := loadFiles(ctx, tx)
			if err != nil {
				return err
			}
			for _, other := range existing {
				if containsString(other.Parents, file.ID) {
					return badRequest("Folder is not empty.")
				}
			}
		}
		if changed {
			if request.Body.ModifiedTime == nil {
				file.ModifiedTime = s.now()
			}
			file.Version++
			if err := updateFile(ctx, tx, file); err != nil {
				return err
			}
		}
		updated = file
		return nil
	})
	if err != nil {
		return fileUpdateErr(err)
	}
	rendered, err := s.renderFile(ctx, updated, account)
	if err != nil {
		return nil, err
	}
	return generated.DriveFilesUpdate200JSONResponse(rendered), nil
}

func (s *server) DriveFilesDelete(ctx context.Context, request generated.DriveFilesDeleteRequestObject) (generated.DriveFilesDeleteResponseObject, error) {
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		files, err := loadFiles(ctx, tx)
		if err != nil {
			return err
		}
		ids, ok := descendantIDs(files, string(request.FileId))
		if !ok {
			return notFound(fmt.Sprintf("File not found: %s.", request.FileId))
		}
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx, "DELETE FROM permissions WHERE file_id=?", id); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "DELETE FROM files WHERE id=?", id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fileDeleteErr(err)
	}
	return generated.DriveFilesDelete204Response{}, nil
}

func (s *server) DriveFilesCopy(ctx context.Context, request generated.DriveFilesCopyRequestObject) (generated.DriveFilesCopyResponseObject, error) {
	if request.Body == nil {
		return fileCopyErr(badRequest("Request body is required"))
	}
	account, err := loadAccount(ctx, s.db)
	if err != nil {
		return nil, err
	}
	var copied storedFile
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		source, err := getFile(ctx, tx, string(request.FileId))
		if errors.Is(err, sql.ErrNoRows) {
			return notFound(fmt.Sprintf("File not found: %s.", request.FileId))
		}
		if err != nil {
			return err
		}
		if source.MimeType == folderMimeType {
			return badRequest("Copying folders is not supported")
		}
		body := *request.Body
		if body.Name == nil || strings.TrimSpace(*body.Name) == "" {
			name := "Copy of " + source.Name
			body.Name = &name
		}
		if body.MimeType == nil {
			body.MimeType = &source.MimeType
		}
		if body.Description == nil {
			body.Description = &source.Description
		}
		if body.Parents == nil {
			empty := []string{}
			body.Parents = &empty
		}
		file, err := s.newFile(ctx, tx, body, account, source.Body)
		if err != nil {
			return err
		}
		file.MimeType = source.MimeType
		file.Body = source.Body
		if body.FolderColorRgb == nil {
			file.FolderColorRgb = source.FolderColorRgb
		}
		if err := insertFile(ctx, tx, file, account); err != nil {
			return err
		}
		copied = file
		return nil
	})
	if err != nil {
		return fileCopyErr(err)
	}
	rendered, err := s.renderFile(ctx, copied, account)
	if err != nil {
		return nil, err
	}
	return generated.DriveFilesCopy200JSONResponse(rendered), nil
}

func (s *server) DrivePermissionsList(ctx context.Context, request generated.DrivePermissionsListRequestObject) (generated.DrivePermissionsListResponseObject, error) {
	if _, err := getFile(ctx, s.db, string(request.FileId)); errors.Is(err, sql.ErrNoRows) {
		return permissionListErr(notFound(fmt.Sprintf("File not found: %s.", request.FileId)))
	} else if err != nil {
		return nil, err
	}
	perms, err := loadPermissions(ctx, s.db, string(request.FileId))
	if err != nil {
		return nil, err
	}
	var pageSize *int
	if request.Params.PageSize != nil {
		size := int(*request.Params.PageSize)
		pageSize = &size
	}
	var token *string
	if request.Params.PageToken != nil {
		value := string(*request.Params.PageToken)
		token = &value
	}
	start, end, err := pageWindow(token, pageSize, len(perms))
	if err != nil {
		return permissionListErr(badRequest(err.Error()))
	}
	rendered := make([]generated.Permission, 0, end-start)
	for _, perm := range perms[start:end] {
		rendered = append(rendered, buildPermission(perm))
	}
	response := generated.PermissionList{Kind: "drive#permissionList", Permissions: rendered}
	if end < len(perms) {
		value := strconv.Itoa(end)
		response.NextPageToken = &value
	}
	return generated.DrivePermissionsList200JSONResponse(response), nil
}

func (s *server) DrivePermissionsGet(ctx context.Context, request generated.DrivePermissionsGetRequestObject) (generated.DrivePermissionsGetResponseObject, error) {
	if _, err := getFile(ctx, s.db, string(request.FileId)); errors.Is(err, sql.ErrNoRows) {
		return permissionGetErr(notFound(fmt.Sprintf("File not found: %s.", request.FileId)))
	} else if err != nil {
		return nil, err
	}
	perm, err := getPermission(ctx, s.db, string(request.FileId), string(request.PermissionId))
	if errors.Is(err, sql.ErrNoRows) {
		return permissionGetErr(notFound(fmt.Sprintf("Permission not found: %s.", request.PermissionId)))
	}
	if err != nil {
		return nil, err
	}
	return generated.DrivePermissionsGet200JSONResponse(buildPermission(perm)), nil
}

func (s *server) DrivePermissionsCreate(ctx context.Context, request generated.DrivePermissionsCreateRequestObject) (generated.DrivePermissionsCreateResponseObject, error) {
	if request.Body == nil {
		return permissionCreateErr(badRequest("Request body is required"))
	}
	var created storedPermission
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := getFile(ctx, tx, string(request.FileId)); errors.Is(err, sql.ErrNoRows) {
			return notFound(fmt.Sprintf("File not found: %s.", request.FileId))
		} else if err != nil {
			return err
		}
		perm, err := s.permissionFromRequest(ctx, string(request.FileId), *request.Body)
		if err != nil {
			return err
		}
		existing, err := loadPermissions(ctx, tx, string(request.FileId))
		if err != nil {
			return err
		}
		for _, current := range existing {
			if current.Type == perm.Type && current.EmailAddress == perm.EmailAddress && perm.EmailAddress != "" {
				return conflict("A permission for that grantee already exists.")
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO permissions
			(file_id, id, type, role, email_address, display_name, allow_file_discovery)
			VALUES(?, ?, ?, ?, ?, ?, ?)`,
			perm.FileID, perm.ID, perm.Type, perm.Role, perm.EmailAddress, perm.DisplayName, boolInt(perm.AllowFileDiscovery)); err != nil {
			return err
		}
		created = perm
		return nil
	})
	if err != nil {
		return permissionCreateErr(err)
	}
	return generated.DrivePermissionsCreate200JSONResponse(buildPermission(created)), nil
}

func (s *server) newFile(ctx context.Context, tx *sql.Tx, body generated.File, account account, bodyText string) (storedFile, error) {
	name := strValue(body.Name)
	if strings.TrimSpace(name) == "" {
		return storedFile{}, badRequest("name is required")
	}
	mimeType := strValue(body.MimeType)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	id := strValue(body.Id)
	if id == "" {
		next, err := s.ids.Next(ctx, "file")
		if err != nil {
			return storedFile{}, fmt.Errorf("googledrive: allocate file ID: %w", err)
		}
		id = next
	}
	if !validID(id) {
		return storedFile{}, badRequest("id is invalid")
	}
	if _, err := getFile(ctx, tx, id); err == nil {
		return storedFile{}, conflict(fmt.Sprintf("File already exists: %s.", id))
	} else if !errors.Is(err, sql.ErrNoRows) {
		return storedFile{}, err
	}
	parents := []string{}
	if body.Parents != nil {
		parents = append(parents, (*body.Parents)...)
	}
	if err := s.validateParents(ctx, tx, id, parents); err != nil {
		return storedFile{}, err
	}
	created := s.now()
	if body.CreatedTime != nil {
		if !validTime(*body.CreatedTime) {
			return storedFile{}, badRequest("createdTime must be RFC 3339")
		}
		created = *body.CreatedTime
	}
	modified := created
	if body.ModifiedTime != nil {
		if !validTime(*body.ModifiedTime) {
			return storedFile{}, badRequest("modifiedTime must be RFC 3339")
		}
		modified = *body.ModifiedTime
	}
	color := strValue(body.FolderColorRgb)
	if color != "" && !folderColorPattern.MatchString(color) {
		return storedFile{}, badRequest("folderColorRgb must be an RGB hex string")
	}
	trashed := body.Trashed != nil && *body.Trashed
	return storedFile{
		ID: id, Name: name, MimeType: mimeType, Description: strValue(body.Description), Body: bodyText,
		Parents: parents, Starred: body.Starred != nil && *body.Starred, Trashed: trashed, ExplicitlyTrashed: trashed,
		CreatedTime: created, ModifiedTime: modified, FolderColorRgb: color, Version: 1,
	}, nil
}

func applyFilePatch(file *storedFile, body generated.File) (bool, error) {
	changed := false
	if body.Name != nil && *body.Name != file.Name {
		if strings.TrimSpace(*body.Name) == "" {
			return false, badRequest("name is required")
		}
		file.Name = *body.Name
		changed = true
	}
	if body.MimeType != nil && *body.MimeType != file.MimeType {
		if strings.TrimSpace(*body.MimeType) == "" {
			return false, badRequest("mimeType is invalid")
		}
		file.MimeType = *body.MimeType
		changed = true
	}
	if body.Description != nil && *body.Description != file.Description {
		file.Description = *body.Description
		changed = true
	}
	if body.Starred != nil && *body.Starred != file.Starred {
		file.Starred = *body.Starred
		changed = true
	}
	if body.Trashed != nil && *body.Trashed != file.Trashed {
		file.Trashed = *body.Trashed
		file.ExplicitlyTrashed = *body.Trashed
		changed = true
	}
	if body.FolderColorRgb != nil && *body.FolderColorRgb != file.FolderColorRgb {
		if *body.FolderColorRgb != "" && !folderColorPattern.MatchString(*body.FolderColorRgb) {
			return false, badRequest("folderColorRgb must be an RGB hex string")
		}
		file.FolderColorRgb = *body.FolderColorRgb
		changed = true
	}
	if body.ModifiedTime != nil && *body.ModifiedTime != file.ModifiedTime {
		if !validTime(*body.ModifiedTime) {
			return false, badRequest("modifiedTime must be RFC 3339")
		}
		file.ModifiedTime = *body.ModifiedTime
		changed = true
	}
	if body.CreatedTime != nil && *body.CreatedTime != file.CreatedTime {
		if !validTime(*body.CreatedTime) {
			return false, badRequest("createdTime must be RFC 3339")
		}
		file.CreatedTime = *body.CreatedTime
		changed = true
	}
	return changed, nil
}

func applyParentEdits(current []string, addRaw, removeRaw string) []string {
	remove := splitCSV(removeRaw)
	add := splitCSV(addRaw)
	next := []string{}
	for _, id := range current {
		if !containsString(remove, id) {
			next = append(next, id)
		}
	}
	for _, id := range add {
		if !containsString(next, id) {
			next = append(next, id)
		}
	}
	return next
}

func (s *server) validateParents(ctx context.Context, conn sqlConn, fileID string, parents []string) error {
	if len(parents) > 1 {
		return badRequest("A file can only have one parent.")
	}
	if len(parents) == 0 {
		return nil
	}
	parentID := parents[0]
	if parentID == fileID {
		return badRequest("A file cannot be its own parent.")
	}
	parent, err := getFile(ctx, conn, parentID)
	if errors.Is(err, sql.ErrNoRows) {
		return notFound(fmt.Sprintf("File not found: %s.", parentID))
	}
	if err != nil {
		return err
	}
	if parent.MimeType != folderMimeType {
		return badRequest("The specified parent is not a folder.")
	}
	files, err := loadFiles(ctx, conn)
	if err != nil {
		return err
	}
	if parentCycle(files, fileID, parentID) {
		return badRequest("The specified parent would create a cycle.")
	}
	return nil
}

func (s *server) permissionFromRequest(ctx context.Context, fileID string, body generated.Permission) (storedPermission, error) {
	permType := strValue(body.Type)
	role := strValue(body.Role)
	if _, ok := permissionTypes[permType]; !ok {
		return storedPermission{}, badRequest("type must be user, group, domain, or anyone")
	}
	if _, ok := permissionRoles[role]; !ok {
		return storedPermission{}, badRequest("role must be owner, organizer, fileOrganizer, writer, commenter, or reader")
	}
	if role == "owner" {
		return storedPermission{}, badRequest("Ownership transfer is not supported.")
	}
	email := strValue(body.EmailAddress)
	if permType == "user" || permType == "group" {
		if email == "" {
			return storedPermission{}, badRequest("emailAddress is required")
		}
	}
	if permType == "user" {
		if _, err := mail.ParseAddress(email); err != nil {
			return storedPermission{}, badRequest("emailAddress is invalid")
		}
	}
	id, err := s.ids.Next(ctx, "permission")
	if err != nil {
		return storedPermission{}, fmt.Errorf("googledrive: allocate permission ID: %w", err)
	}
	return storedPermission{
		FileID: fileID, ID: id, Type: permType, Role: role, EmailAddress: email,
		DisplayName: strValue(body.DisplayName), AllowFileDiscovery: body.AllowFileDiscovery != nil && *body.AllowFileDiscovery,
	}, nil
}

func (s *server) loadOwned(ctx context.Context, id string) (storedFile, account, error) {
	file, err := getFile(ctx, s.db, id)
	if errors.Is(err, sql.ErrNoRows) {
		return storedFile{}, account{}, notFound(fmt.Sprintf("File not found: %s.", id))
	}
	if err != nil {
		return storedFile{}, account{}, err
	}
	user, err := loadAccount(ctx, s.db)
	if err != nil {
		return storedFile{}, account{}, err
	}
	return file, user, nil
}

func (s *server) renderFile(ctx context.Context, file storedFile, account account) (generated.File, error) {
	perms, err := loadPermissions(ctx, s.db, file.ID)
	if err != nil {
		return generated.File{}, err
	}
	return buildFile(file, account, perms), nil
}

func (s *server) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *server) now() string { return s.clock.Now().UTC().Format(time.RFC3339) }

func buildFile(file storedFile, owner account, perms []storedPermission) generated.File {
	parents := append([]string{}, file.Parents...)
	ids := make([]string, 0, len(perms))
	owners := []generated.User{}
	for _, perm := range perms {
		ids = append(ids, perm.ID)
		if perm.Role == "owner" {
			owners = append(owners, generated.User{
				Kind: userKind, DisplayName: perm.DisplayName, EmailAddress: perm.EmailAddress,
				PermissionId: perm.ID, Me: perm.EmailAddress == owner.EmailAddress,
			})
		}
	}
	sort.Strings(ids)
	size := strconv.Itoa(len(file.Body))
	shared := len(perms) > 1
	out := generated.File{
		Kind: strPtr(fileKind), Id: strPtr(file.ID), Name: strPtr(file.Name), MimeType: strPtr(file.MimeType),
		Description: strPtr(file.Description), Parents: &parents, Starred: boolPtr(file.Starred), Trashed: boolPtr(file.Trashed),
		ExplicitlyTrashed: boolPtr(file.ExplicitlyTrashed), CreatedTime: strPtr(file.CreatedTime), ModifiedTime: strPtr(file.ModifiedTime),
		ModifiedByMeTime: strPtr(file.ModifiedTime), QuotaBytesUsed: strPtr(size), Version: strPtr(strconv.Itoa(file.Version)),
		OwnedByMe: boolPtr(true), Shared: boolPtr(shared), WritersCanShare: boolPtr(true), CopyRequiresWriterPermission: boolPtr(false),
		ModifiedByMe: boolPtr(true), ViewedByMe: boolPtr(false), Spaces: &[]string{"drive"}, PermissionIds: &ids, Owners: &owners,
		LastModifyingUser: &generated.User{
			Kind: userKind, DisplayName: owner.DisplayName, EmailAddress: owner.EmailAddress, PermissionId: owner.PermissionID, Me: true,
		},
		WebViewLink: strPtr(webViewLink(file)),
	}
	if file.MimeType != folderMimeType {
		out.Size = strPtr(size)
		sum := md5.Sum([]byte(file.Body))
		checksum := hex.EncodeToString(sum[:])
		out.Md5Checksum = &checksum
		if ext := fileExtension(file.Name); ext != "" {
			out.FileExtension = &ext
			out.FullFileExtension = &ext
		}
		out.OriginalFilename = strPtr(file.Name)
	}
	if file.FolderColorRgb != "" {
		out.FolderColorRgb = strPtr(file.FolderColorRgb)
	}
	return out
}

func buildPermission(perm storedPermission) generated.Permission {
	return generated.Permission{
		Kind: strPtr(permissionKind), Id: strPtr(perm.ID), Type: strPtr(perm.Type), Role: strPtr(perm.Role),
		EmailAddress: strPtr(perm.EmailAddress), DisplayName: strPtr(perm.DisplayName),
		Deleted: boolPtr(false), AllowFileDiscovery: boolPtr(perm.AllowFileDiscovery),
	}
}

func webViewLink(file storedFile) string {
	if file.MimeType == folderMimeType {
		return "https://drive.google.com/drive/folders/" + file.ID
	}
	return "https://drive.google.com/file/d/" + file.ID + "/view"
}

func fileExtension(name string) string {
	dot := strings.LastIndex(name, ".")
	if dot < 0 || dot == len(name)-1 {
		return ""
	}
	return name[dot+1:]
}

func fileListResponse(next *string, files ...generated.File) generated.DriveFilesList200JSONResponse {
	if files == nil {
		files = []generated.File{}
	}
	return generated.DriveFilesList200JSONResponse{Kind: fileListKind, Files: files, IncompleteSearch: false, NextPageToken: next}
}

func rejectSharedDrive(corpora *generated.Corpora, driveID *generated.DriveId) error {
	corpus := ""
	if corpora != nil {
		corpus = string(*corpora)
	}
	shared := ""
	if driveID != nil {
		shared = string(*driveID)
	}
	switch corpus {
	case "", "user":
		if shared != "" {
			return badRequest("driveId requires corpora=drive")
		}
	case "drive":
		if shared == "" {
			return badRequest("driveId is required when corpora is drive")
		}
		return notFound(fmt.Sprintf("Shared drive not found: %s.", shared))
	default:
		return badRequest("unsupported corpora")
	}
	return nil
}

func parentCycle(files []storedFile, fileID, parentID string) bool {
	byID := make(map[string]storedFile, len(files))
	for _, file := range files {
		byID[file.ID] = file
	}
	seen := map[string]bool{}
	for cur := parentID; cur != ""; {
		if cur == fileID || seen[cur] {
			return cur == fileID
		}
		seen[cur] = true
		file, ok := byID[cur]
		if !ok || len(file.Parents) == 0 {
			return false
		}
		cur = file.Parents[0]
	}
	return false
}

func descendantIDs(files []storedFile, root string) ([]string, bool) {
	found := false
	children := map[string][]string{}
	for _, file := range files {
		if file.ID == root {
			found = true
		}
		for _, parent := range file.Parents {
			children[parent] = append(children[parent], file.ID)
		}
	}
	if !found {
		return nil, false
	}
	var out []string
	seen := map[string]bool{}
	var walk func(string)
	walk = func(id string) {
		if seen[id] {
			return
		}
		seen[id] = true
		for _, child := range children[id] {
			walk(child)
		}
		out = append(out, id)
	}
	walk(root)
	return out, true
}

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

func respondErr[R any](err error, wrap func(generated.ErrorEnvelope, int) R) (R, error) {
	var zero R
	var httpErr httpError
	if errors.As(err, &httpErr) {
		return wrap(errorEnvelope(httpErr.Status, httpErr.Code, httpErr.Message), httpErr.Status), nil
	}
	return zero, err
}

func fileErr(err error) (generated.DriveFilesGetResponseObject, error) {
	return respondErr(err, func(body generated.ErrorEnvelope, status int) generated.DriveFilesGetResponseObject {
		return generated.DriveFilesGetdefaultJSONResponse{Body: body, StatusCode: status}
	})
}

func fileListErr(err error) (generated.DriveFilesListResponseObject, error) {
	return respondErr(err, func(body generated.ErrorEnvelope, status int) generated.DriveFilesListResponseObject {
		return generated.DriveFilesListdefaultJSONResponse{Body: body, StatusCode: status}
	})
}

func fileCreateErr(err error) (generated.DriveFilesCreateResponseObject, error) {
	return respondErr(err, func(body generated.ErrorEnvelope, status int) generated.DriveFilesCreateResponseObject {
		return generated.DriveFilesCreatedefaultJSONResponse{Body: body, StatusCode: status}
	})
}

func fileUpdateErr(err error) (generated.DriveFilesUpdateResponseObject, error) {
	return respondErr(err, func(body generated.ErrorEnvelope, status int) generated.DriveFilesUpdateResponseObject {
		return generated.DriveFilesUpdatedefaultJSONResponse{Body: body, StatusCode: status}
	})
}

func fileDeleteErr(err error) (generated.DriveFilesDeleteResponseObject, error) {
	return respondErr(err, func(body generated.ErrorEnvelope, status int) generated.DriveFilesDeleteResponseObject {
		return generated.DriveFilesDeletedefaultJSONResponse{Body: body, StatusCode: status}
	})
}

func fileCopyErr(err error) (generated.DriveFilesCopyResponseObject, error) {
	return respondErr(err, func(body generated.ErrorEnvelope, status int) generated.DriveFilesCopyResponseObject {
		return generated.DriveFilesCopydefaultJSONResponse{Body: body, StatusCode: status}
	})
}

func permissionListErr(err error) (generated.DrivePermissionsListResponseObject, error) {
	return respondErr(err, func(body generated.ErrorEnvelope, status int) generated.DrivePermissionsListResponseObject {
		return generated.DrivePermissionsListdefaultJSONResponse{Body: body, StatusCode: status}
	})
}

func permissionGetErr(err error) (generated.DrivePermissionsGetResponseObject, error) {
	return respondErr(err, func(body generated.ErrorEnvelope, status int) generated.DrivePermissionsGetResponseObject {
		return generated.DrivePermissionsGetdefaultJSONResponse{Body: body, StatusCode: status}
	})
}

func permissionCreateErr(err error) (generated.DrivePermissionsCreateResponseObject, error) {
	return respondErr(err, func(body generated.ErrorEnvelope, status int) generated.DrivePermissionsCreateResponseObject {
		return generated.DrivePermissionsCreatedefaultJSONResponse{Body: body, StatusCode: status}
	})
}

func errorEnvelope(status int, code, message string) generated.ErrorEnvelope {
	reason := "invalid"
	switch code {
	case "NOT_FOUND":
		reason = "notFound"
	case "UNAUTHENTICATED":
		reason = "authError"
	case "ALREADY_EXISTS":
		reason = "duplicate"
	}
	return generated.ErrorEnvelope{Error: generated.ErrorBody{
		Code: status, Message: message, Status: code,
		Errors: []generated.ErrorDetail{{Domain: "global", Reason: reason, Message: message}},
	}}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorEnvelope(status, code, message))
}

func validID(id string) bool {
	return id != "" && !strings.Contains(id, "/") && strings.TrimSpace(id) == id
}

func validTime(value string) bool {
	if _, err := time.Parse(time.RFC3339, value); err == nil {
		return true
	}
	_, err := time.Parse(time.RFC3339Nano, value)
	return err == nil
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func strPtr(value string) *string { return &value }
func boolPtr(value bool) *bool    { return &value }
func strValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

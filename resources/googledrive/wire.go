package googledrive

import (
	"crypto/md5"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"

	"github.com/dumbmachine/fabricate/resources/googledrive/generated"
)

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

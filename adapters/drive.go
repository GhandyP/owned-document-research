package adapters

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/GhandyP/owned-document-research/internal/research"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
)

const (
	// MyDriveTenant names the tenant of a file that lives in a personal Drive
	// rather than in a shared drive. The tenant boundary is what keeps two
	// customers' documents apart even when a permission is misconfigured.
	MyDriveTenant = "my-drive"

	// defaultMaxFiles bounds how many files one listing or search considers.
	defaultMaxFiles = 25
	// defaultMaxContentBytes bounds how much of a file is read into a passage.
	defaultMaxContentBytes = 1 << 20

	// fileFields and permissionFields are the only Drive fields this adapter
	// asks for: no field is fetched that the document model does not carry.
	fileFields       = "id,name,version,mimeType,owners(emailAddress),driveId,modifiedTime,trashed"
	permissionFields = "permissions(id,type,role,emailAddress)"
)

// DriveSource serves owned documents from Google Drive through the same port as
// the local corpus.
//
// Two limits are worth stating plainly, because they are the difference between
// this adapter and the local one:
//
//   - A Drive version is fetched through the current revision. A caller asking
//     for an older version of a file is answered exactly as if the document did
//     not exist, rather than being served the current body under an old
//     reference.
//   - Supersession is a corpus concept this adapter cannot see. Drive exposes a
//     revision counter, not a statement that one revision replaced another, so
//     the adapter maps the counter into Version and leaves Supersedes empty
//     instead of inventing a link between files.
//
// Google-native files are exported as text; other files are downloaded only
// when they are text, and a binary file contributes metadata and no body.
type DriveSource struct {
	service *drive.Service

	maxFiles        int
	maxContentBytes int64
	exportMimeType  string
}

// NewDriveSource builds the adapter over an authenticated Drive service.
func NewDriveSource(service *drive.Service) *DriveSource {
	return &DriveSource{
		service:         service,
		maxFiles:        defaultMaxFiles,
		maxContentBytes: defaultMaxContentBytes,
		exportMimeType:  "text/plain",
	}
}

// List returns the metadata of the files the principal may read.
func (s *DriveSource) List(ctx context.Context, p research.Principal) ([]research.DocumentMeta, error) {
	files, err := s.listFiles(ctx, "trashed = false")
	if err != nil {
		return nil, err
	}
	metas := make([]research.DocumentMeta, 0, len(files))
	for _, file := range files {
		document, err := s.document(ctx, file, false)
		if err != nil {
			if errors.Is(err, research.ErrUnavailable) {
				continue
			}
			return nil, err
		}
		if !document.ReadableBy(p) {
			continue
		}
		metas = append(metas, document.Meta())
	}
	sort.Slice(metas, func(i, j int) bool {
		if metas[i].ID != metas[j].ID {
			return metas[i].ID < metas[j].ID
		}
		return metas[i].Version < metas[j].Version
	})
	return metas, nil
}

// Get returns one file with its body.
func (s *DriveSource) Get(ctx context.Context, p research.Principal, id string, version int) (research.Document, error) {
	file, err := s.service.Files.Get(id).
		Fields(fileFields).
		SupportsAllDrives(true).
		Context(ctx).
		Do()
	if err != nil {
		if isNotFound(err) {
			return research.Document{}, research.ErrUnavailable
		}
		return research.Document{}, fmt.Errorf("drive: get %s: %w", id, err)
	}
	document, err := s.document(ctx, file, true)
	if err != nil {
		if errors.Is(err, research.ErrUnavailable) {
			return research.Document{}, research.ErrUnavailable
		}
		return research.Document{}, err
	}
	if !document.ReadableBy(p) || (version != 0 && version != document.Version) {
		// A forbidden file, an absent file and an unavailable revision are one
		// answer on purpose: any difference would be an existence oracle.
		return research.Document{}, research.ErrUnavailable
	}
	return document, nil
}

// Search asks Drive for the files that contain the query words, reads their
// bodies and ranks the passages with the same rule the local corpus uses.
func (s *DriveSource) Search(ctx context.Context, p research.Principal, query string, limit int) ([]research.Passage, error) {
	tokens := research.QueryTokens(query)
	if len(tokens) == 0 {
		return nil, nil
	}
	files, err := s.listFiles(ctx, "trashed = false and ("+fullTextClause(tokens)+")")
	if err != nil {
		return nil, err
	}
	documents := make([]research.Document, 0, len(files))
	for _, file := range files {
		document, err := s.document(ctx, file, true)
		if err != nil {
			if errors.Is(err, research.ErrUnavailable) {
				continue
			}
			return nil, err
		}
		if !document.ReadableBy(p) {
			continue
		}
		documents = append(documents, document)
	}
	return research.SearchDocuments(query, documents, limit), nil
}

// listFiles runs one Drive query, bounded by the adapter's file limit.
func (s *DriveSource) listFiles(ctx context.Context, query string) ([]*drive.File, error) {
	response, err := s.service.Files.List().
		Q(query).
		Fields("nextPageToken,files(" + fileFields + ")").
		PageSize(int64(s.maxFiles)).
		SupportsAllDrives(true).
		IncludeItemsFromAllDrives(true).
		Context(ctx).
		Do()
	if err != nil {
		return nil, fmt.Errorf("drive: list files: %w", err)
	}
	files := response.Files
	if len(files) > s.maxFiles {
		files = files[:s.maxFiles]
	}
	return files, nil
}

// document maps a Drive file into the document model.
func (s *DriveSource) document(ctx context.Context, file *drive.File, withContent bool) (research.Document, error) {
	permissions, err := s.permissions(ctx, file.Id)
	if err != nil {
		return research.Document{}, err
	}
	version := int(file.Version)
	if version < 1 {
		// A file always has a revision counter; refusing to invent one keeps
		// the model's promise that a version is 1 or greater.
		version = 1
	}
	document := research.Document{
		ID:        file.Id,
		Title:     file.Name,
		Owner:     strings.ToLower(ownerOf(file)),
		Tenant:    tenantOf(file),
		Allow:     allowFromPermissions(permissions),
		Version:   version,
		UpdatedAt: modifiedAt(file),
	}
	if withContent {
		body, err := s.content(ctx, file)
		if err != nil {
			return research.Document{}, err
		}
		document.Text = body
	}
	return document, nil
}

// permissions reads who a file is shared with.
func (s *DriveSource) permissions(ctx context.Context, fileID string) ([]*drive.Permission, error) {
	response, err := s.service.Permissions.List(fileID).
		Fields(permissionFields).
		SupportsAllDrives(true).
		Context(ctx).
		Do()
	if err != nil {
		if isNotFound(err) {
			return nil, research.ErrUnavailable
		}
		return nil, fmt.Errorf("drive: permissions of %s: %w", fileID, err)
	}
	return response.Permissions, nil
}

// content reads a file body, bounded by the adapter's byte limit.
func (s *DriveSource) content(ctx context.Context, file *drive.File) (string, error) {
	var (
		response *http.Response
		err      error
	)
	switch {
	case strings.HasPrefix(file.MimeType, "application/vnd.google-apps."):
		response, err = s.service.Files.Export(file.Id, s.exportMimeType).Context(ctx).Download()
	case strings.HasPrefix(file.MimeType, "text/"):
		response, err = s.service.Files.Get(file.Id).Context(ctx).Download()
	default:
		// A binary file is listed and can be cited by title, but it has no body
		// to quote, and pretending otherwise would be an invented passage.
		return "", nil
	}
	if err != nil {
		if isNotFound(err) {
			return "", research.ErrUnavailable
		}
		return "", fmt.Errorf("drive: content of %s: %w", file.Id, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, s.maxContentBytes))
	if err != nil {
		return "", fmt.Errorf("drive: read content of %s: %w", file.Id, err)
	}
	return string(body), nil
}

// allowFromPermissions turns Drive sharing into the allow list the permission
// rule understands.
//
// "anyone" and "domain" both open the document inside its tenant, which is why
// they map to the wildcard rather than to a list of identities: the tenant
// boundary, not the sharing entry, is what keeps another customer out.
func allowFromPermissions(permissions []*drive.Permission) []string {
	seen := make(map[string]bool, len(permissions))
	allow := make([]string, 0, len(permissions))
	for _, permission := range permissions {
		if permission == nil {
			continue
		}
		address := strings.ToLower(strings.TrimSpace(permission.EmailAddress))
		var entry string
		switch permission.Type {
		case "user":
			if address != "" {
				entry = "user:" + address
			}
		case "group":
			if address != "" {
				entry = "group:" + address
			}
		case "anyone", "domain":
			entry = "*"
		}
		if entry == "" || seen[entry] {
			continue
		}
		seen[entry] = true
		allow = append(allow, entry)
	}
	sort.Strings(allow)
	return allow
}

// ownerOf returns the file's owner.
func ownerOf(file *drive.File) string {
	for _, owner := range file.Owners {
		if owner != nil && owner.EmailAddress != "" {
			return owner.EmailAddress
		}
	}
	return ""
}

// tenantOf returns the shared drive the file lives in, or the personal-drive
// tenant when it lives in none.
func tenantOf(file *drive.File) string {
	if file.DriveId != "" {
		return file.DriveId
	}
	return MyDriveTenant
}

// modifiedAt parses Drive's modification time, reporting the zero time when it
// cannot be read rather than failing the whole search.
func modifiedAt(file *drive.File) time.Time {
	if file.ModifiedTime == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, file.ModifiedTime)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

// fullTextClause builds Drive's full-text filter from the query tokens. Drive
// matches single words, which is why the query is tokenized here rather than
// passed through whole.
func fullTextClause(tokens []string) string {
	clauses := make([]string, 0, len(tokens))
	for _, token := range tokens {
		clauses = append(clauses, fmt.Sprintf("fullText contains '%s'", strings.ReplaceAll(token, "'", "")))
	}
	return strings.Join(clauses, " and ")
}

// isNotFound reports whether the API answered that the file is not there, which
// is also what Drive answers for a file this caller may not see.
func isNotFound(err error) bool {
	var apiErr *googleapi.Error
	if errors.As(err, &apiErr) {
		return apiErr.Code == http.StatusNotFound
	}
	return false
}

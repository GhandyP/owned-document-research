package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/GhandyP/owned-document-research/internal/research"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

// fakeFile is one file the fake Drive serves.
type fakeFile struct {
	file        *drive.File
	permissions []*drive.Permission
	content     string
}

// fakeDrive answers the three Drive calls this adapter makes, without a
// network. It records every request, so a test can check which query was asked.
type fakeDrive struct {
	files    map[string]fakeFile
	requests []string
}

func (f *fakeDrive) RoundTrip(req *http.Request) (*http.Response, error) {
	f.requests = append(f.requests, req.URL.Path+"?"+req.URL.RawQuery)
	path := strings.TrimSuffix(req.URL.Path, "/")
	segments := strings.Split(strings.TrimPrefix(path, "/drive/v3/"), "/")
	switch {
	case len(segments) == 1 && segments[0] == "files":
		list := &drive.FileList{}
		for _, entry := range f.files {
			list.Files = append(list.Files, entry.file)
		}
		return jsonResponse(list)
	case len(segments) == 3 && segments[2] == "permissions":
		entry, ok := f.files[segments[1]]
		if !ok {
			return notFoundResponse(), nil
		}
		return jsonResponse(&drive.PermissionList{Permissions: entry.permissions})
	case len(segments) == 3 && segments[2] == "export":
		entry, ok := f.files[segments[1]]
		if !ok {
			return notFoundResponse(), nil
		}
		return textResponse(entry.content), nil
	case len(segments) == 2 && segments[0] == "files":
		entry, ok := f.files[segments[1]]
		if !ok {
			return notFoundResponse(), nil
		}
		if req.URL.Query().Get("alt") == "media" {
			return textResponse(entry.content), nil
		}
		return jsonResponse(entry.file)
	}
	return nil, fmt.Errorf("fake drive: unexpected request %s", req.URL)
}

func (f *fakeDrive) queries() []string {
	return append([]string{}, f.requests...)
}

func jsonResponse(payload any) (*http.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
	}, nil
}

func textResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/plain"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func notFoundResponse() *http.Response {
	body := `{"error":{"code":404,"message":"File not found.","errors":[{"reason":"notFound"}]}}`
	return &http.Response{
		StatusCode: http.StatusNotFound,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// testDrive serves three files: one the principal owns, one restricted to
// another group in the same tenant, and one in another tenant entirely.
func testDrive(t *testing.T) (*fakeDrive, *DriveSource) {
	t.Helper()
	fake := &fakeDrive{files: map[string]fakeFile{
		"file-pilot": {
			file: &drive.File{
				Id: "file-pilot", Name: "Intake pilot notes", Version: 4,
				MimeType: "application/vnd.google-apps.document", DriveId: "drive-acme",
				ModifiedTime: "2026-03-14T09:00:00Z",
				Owners:       []*drive.User{{EmailAddress: "Ana@Acme.test"}},
			},
			permissions: []*drive.Permission{
				{Type: "user", Role: "writer", EmailAddress: "Ana@Acme.test"},
				{Type: "group", Role: "reader", EmailAddress: "ops@acme.test"},
			},
			content: "The intake pilot reduced handling time by a third across the first quarter.",
		},
		"file-audit": {
			file: &drive.File{
				Id: "file-audit", Name: "Internal audit 2026", Version: 2,
				MimeType: "application/vnd.google-apps.document", DriveId: "drive-acme",
				ModifiedTime: "2026-04-01T09:00:00Z",
				Owners:       []*drive.User{{EmailAddress: "dan@acme.test"}},
			},
			permissions: []*drive.Permission{
				{Type: "group", Role: "reader", EmailAddress: "legal@acme.test"},
			},
			content: "The audit found no evidence that handling time fell during the pilot window.",
		},
		"file-globex": {
			file: &drive.File{
				Id: "file-globex", Name: "Globex benchmark", Version: 1,
				MimeType: "text/plain", DriveId: "drive-globex",
				ModifiedTime: "2026-03-30T09:00:00Z",
				Owners:       []*drive.User{{EmailAddress: "eve@globex.test"}},
			},
			permissions: []*drive.Permission{
				{Type: "anyone", Role: "reader"},
			},
			content: "Globex reports handling time is not measured the same way.",
		},
	}}

	service, err := drive.NewService(context.Background(),
		option.WithHTTPClient(&http.Client{Transport: fake}),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("drive.NewService: %v", err)
	}
	return fake, NewDriveSource(service)
}

var (
	anaOps = research.Principal{ID: "ana@acme.test", Groups: []string{"ops"}, Tenant: "drive-acme"}
)

func TestDriveSourceMapsOwnershipAndFiltersByPermission(t *testing.T) {
	_, source := testDrive(t)

	metas, err := source.List(context.Background(), anaOps)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(metas) != 1 {
		t.Fatalf("List returned %+v, want only the file the principal may read", metas)
	}
	meta := metas[0]
	if meta.ID != "file-pilot" {
		t.Errorf("List returned %s, want file-pilot", meta.Ref())
	}
	if meta.Tenant != "drive-acme" {
		t.Errorf("tenant = %q, want the shared drive id", meta.Tenant)
	}
	if meta.Owner != "ana@acme.test" {
		t.Errorf("owner = %q, want the lower-cased owner address", meta.Owner)
	}
	if meta.Version != 4 {
		t.Errorf("version = %d, want Drive's revision counter", meta.Version)
	}
	if meta.Supersedes != "" {
		t.Errorf("the adapter invented a supersedes link: %q", meta.Supersedes)
	}
	if got := strings.Join(meta.Allow, ","); got != "group:ops@acme.test,user:ana@acme.test" {
		t.Errorf("allow = %q, want the sharing entries in a stable order", got)
	}
	if meta.UpdatedAt.IsZero() {
		t.Error("the modification time was not carried over")
	}
}

func TestDriveSourceHidesForbiddenAbsentAndStaleRevisionsAlike(t *testing.T) {
	_, source := testDrive(t)
	ctx := context.Background()

	document, err := source.Get(ctx, anaOps, "file-pilot", 0)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !strings.Contains(document.Text, "reduced handling time") {
		t.Errorf("the body was not exported: %q", document.Text)
	}
	if document.Version != 4 {
		t.Errorf("version = %d, want 4", document.Version)
	}

	forbiddenDocument, forbiddenErr := source.Get(ctx, anaOps, "file-audit", 0)
	absentDocument, absentErr := source.Get(ctx, anaOps, "no-such-file", 0)
	oldRevision, oldErr := source.Get(ctx, anaOps, "file-pilot", 1)

	if !errors.Is(forbiddenErr, research.ErrUnavailable) {
		t.Errorf("a forbidden file returned %v, want ErrUnavailable", forbiddenErr)
	}
	if !errors.Is(absentErr, research.ErrUnavailable) || !errors.Is(oldErr, research.ErrUnavailable) {
		t.Errorf("an absent file returned %v and an old revision %v, want ErrUnavailable", absentErr, oldErr)
	}
	if forbiddenErr.Error() != absentErr.Error() || oldErr.Error() != absentErr.Error() {
		t.Errorf("the three refusals differ: %q, %q, %q", forbiddenErr, absentErr, oldErr)
	}
	if forbiddenDocument.ID != "" || absentDocument.ID != "" || oldRevision.ID != "" {
		t.Error("a refused Get returned a document")
	}
}

func TestDriveSourceSearchesDriveAndRanksLocally(t *testing.T) {
	fake, source := testDrive(t)

	passages, err := source.Search(context.Background(), anaOps, "did the pilot reduce handling time", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(passages) != 1 {
		t.Fatalf("Search returned %+v, want the one readable passage", passages)
	}
	passage := passages[0]
	if passage.Ref() != "file-pilot@4" {
		t.Errorf("passage ref = %q, want file-pilot@4", passage.Ref())
	}
	if passage.Title != "Intake pilot notes" || passage.Owner != "ana@acme.test" {
		t.Errorf("passage lost its ownership: %+v", passage)
	}

	// The quote has to be addressable in the body the adapter served.
	document, err := source.Get(context.Background(), anaOps, "file-pilot", 0)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	runes := []rune(document.Text)
	if passage.Start < 0 || passage.Start+len([]rune(passage.Text)) > len(runes) {
		t.Fatalf("passage offsets %d..%d are outside the body", passage.Start, passage.Start+len([]rune(passage.Text)))
	}
	if got := string(runes[passage.Start : passage.Start+len([]rune(passage.Text))]); got != passage.Text {
		t.Errorf("rune offset %d addresses %q, want %q", passage.Start, got, passage.Text)
	}

	var asked bool
	for _, request := range fake.queries() {
		if strings.Contains(request, "fullText+contains") || strings.Contains(request, "fullText%20contains") {
			asked = true
		}
	}
	if !asked {
		t.Errorf("the adapter never asked Drive for the query words: %v", fake.queries())
	}
}

func TestDriveSourceNeverReturnsAnotherTenant(t *testing.T) {
	_, source := testDrive(t)
	globex := research.Principal{ID: "eve@globex.test", Tenant: "drive-globex"}

	metas, err := source.List(context.Background(), globex)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(metas) != 1 || metas[0].ID != "file-globex" {
		t.Fatalf("another tenant listed %+v, want only its own file", metas)
	}

	// The audit file is in the operations tenant and is open to "anyone" there;
	// the tenant boundary, not the sharing entry, has to keep it out.
	document, err := source.Get(context.Background(), globex, "file-audit", 0)
	if !errors.Is(err, research.ErrUnavailable) {
		t.Errorf("Get across tenants returned %v, want ErrUnavailable", err)
	}
	if document.ID != "" {
		t.Error("Get across tenants returned a document")
	}
}

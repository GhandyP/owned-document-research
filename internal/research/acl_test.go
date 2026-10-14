package research

import "testing"

func doc(id, tenant, owner string, allow ...string) Document {
	return Document{
		ID:      id,
		Title:   id,
		Owner:   owner,
		Tenant:  tenant,
		Allow:   allow,
		Version: 1,
		Text:    "text of " + id,
	}
}

func TestReadableByEnforcesTenantThenOwnershipThenAllow(t *testing.T) {
	tests := []struct {
		name      string
		document  Document
		principal Principal
		want      bool
	}{
		{
			name:      "the owner reads their own document without an allow list",
			document:  doc("notes", "acme", "ana"),
			principal: Principal{ID: "ana", Tenant: "acme"},
			want:      true,
		},
		{
			name:      "another tenant is refused even when named in the allow list",
			document:  doc("notes", "acme", "ana", "user:bob"),
			principal: Principal{ID: "bob", Tenant: "globex"},
			want:      false,
		},
		{
			name:      "a named user in the same tenant is allowed",
			document:  doc("notes", "acme", "ana", "user:bob"),
			principal: Principal{ID: "bob", Tenant: "acme"},
			want:      true,
		},
		{
			name:      "a group member in the same tenant is allowed",
			document:  doc("notes", "acme", "ana", "group:legal"),
			principal: Principal{ID: "bob", Tenant: "acme", Groups: []string{"sales", "legal"}},
			want:      true,
		},
		{
			name:      "a group the principal does not hold is refused",
			document:  doc("notes", "acme", "ana", "group:legal"),
			principal: Principal{ID: "bob", Tenant: "acme", Groups: []string{"sales"}},
			want:      false,
		},
		{
			name:      "the wildcard opens the document to the whole tenant",
			document:  doc("notes", "acme", "ana", "*"),
			principal: Principal{ID: "bob", Tenant: "acme"},
			want:      true,
		},
		{
			name:      "the wildcard does not cross the tenant boundary",
			document:  doc("notes", "acme", "ana", "*"),
			principal: Principal{ID: "bob", Tenant: "globex"},
			want:      false,
		},
		{
			name:      "an unlisted principal in the same tenant is refused",
			document:  doc("notes", "acme", "ana", "user:carol"),
			principal: Principal{ID: "bob", Tenant: "acme"},
			want:      false,
		},
		{
			name:      "an anonymous principal is refused",
			document:  doc("notes", "acme", "ana", "user:bob"),
			principal: Principal{},
			want:      false,
		},
		{
			name:      "the owner of another tenant is refused",
			document:  doc("notes", "acme", "ana"),
			principal: Principal{ID: "ana", Tenant: "globex"},
			want:      false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.document.ReadableBy(tt.principal); got != tt.want {
				t.Errorf("ReadableBy(%+v) = %v, want %v", tt.principal, got, tt.want)
			}
		})
	}
}

func TestValidAllowEntryRejectsMalformedEntries(t *testing.T) {
	valid := []string{"*", "user:ana", "group:legal", "user:a.b-c_d"}
	for _, entry := range valid {
		if err := ValidAllowEntry(entry); err != nil {
			t.Errorf("ValidAllowEntry(%q) = %v, want nil", entry, err)
		}
	}
	invalid := []string{"", "ana", "team:legal", "user:", "group:", "user:ana ", "USER:ana"}
	for _, entry := range invalid {
		if err := ValidAllowEntry(entry); err == nil {
			t.Errorf("ValidAllowEntry(%q) = nil, want an error", entry)
		}
	}
}

func TestParseRefAcceptsAnOptionalVersion(t *testing.T) {
	tests := []struct {
		in          string
		wantID      string
		wantVersion int
		wantErr     bool
	}{
		{in: "ops-handbook", wantID: "ops-handbook", wantVersion: 0},
		{in: "ops-handbook@2", wantID: "ops-handbook", wantVersion: 2},
		{in: "ops-handbook@17", wantID: "ops-handbook", wantVersion: 17},
		{in: "", wantErr: true},
		{in: "@2", wantErr: true},
		{in: "ops-handbook@", wantErr: true},
		{in: "ops-handbook@0", wantErr: true},
		{in: "ops-handbook@-1", wantErr: true},
		{in: "ops-handbook@two", wantErr: true},
		{in: "ops@handbook@2", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			id, version, err := ParseRef(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseRef(%q) = %q, %d, nil; want an error", tt.in, id, version)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseRef(%q): %v", tt.in, err)
			}
			if id != tt.wantID || version != tt.wantVersion {
				t.Errorf("ParseRef(%q) = %q, %d; want %q, %d", tt.in, id, version, tt.wantID, tt.wantVersion)
			}
		})
	}
}

func TestDocumentRefAndValidation(t *testing.T) {
	d := doc("ops-handbook", "acme", "ana")
	d.Version = 3
	if got := d.Ref(); got != "ops-handbook@3" {
		t.Errorf("Ref() = %q, want %q", got, "ops-handbook@3")
	}
	if err := d.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}

	missingID := d
	missingID.ID = ""
	if err := missingID.Validate(); err == nil {
		t.Error("Validate() accepted an empty document id")
	}

	missingTenant := d
	missingTenant.Tenant = ""
	if err := missingTenant.Validate(); err == nil {
		t.Error("Validate() accepted an empty tenant")
	}

	invalidVersion := d
	invalidVersion.Version = 0
	if err := invalidVersion.Validate(); err == nil {
		t.Error("Validate() accepted a version below 1")
	}

	badAllow := d
	badAllow.Allow = []string{"nobody"}
	if err := badAllow.Validate(); err == nil {
		t.Error("Validate() accepted a malformed allow entry")
	}

	empty := d
	empty.Text = ""
	if err := empty.Validate(); err != nil {
		t.Errorf("Validate() rejected an empty document body: %v", err)
	}
}

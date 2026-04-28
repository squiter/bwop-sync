package transformer

import (
	"testing"

	"github.com/squiter/bwop-sync/internal/bitwarden"
	"github.com/squiter/bwop-sync/internal/onepassword"
)

func loginItem(name, user, pass, totp string, uris []string) bitwarden.Item {
	bwURIs := make([]bitwarden.URI, len(uris))
	for i, u := range uris {
		bwURIs[i] = bitwarden.URI{URI: u}
	}
	return bitwarden.Item{
		ID:   "bw-1",
		Type: bitwarden.TypeLogin,
		Name: name,
		Login: &bitwarden.Login{
			Username: user,
			Password: pass,
			TOTP:     totp,
			URIs:     bwURIs,
		},
	}
}

func TestTransform_login(t *testing.T) {
	item := loginItem("GitHub", "user@example.com", "s3cr3t", "", []string{"https://github.com"})
	result := Transform(item, "vault-1")

	if result.Skipped {
		t.Fatalf("expected not skipped, got: %s", result.SkipReason)
	}
	if result.OPItem == nil {
		t.Fatal("expected OPItem to be non-nil")
	}
	if result.OPItem.Title != "GitHub" {
		t.Errorf("expected title 'GitHub', got %q", result.OPItem.Title)
	}
	if result.OPItem.Category != onepassword.CategoryLogin {
		t.Errorf("expected category LOGIN, got %q", result.OPItem.Category)
	}
	if result.OPItem.Vault.ID != "vault-1" {
		t.Errorf("expected vault 'vault-1', got %q", result.OPItem.Vault.ID)
	}

	fieldMap := fieldsByID(result.OPItem.Fields)
	if fieldMap["username"].Value != "user@example.com" {
		t.Errorf("unexpected username: %q", fieldMap["username"].Value)
	}
	if fieldMap["password"].Value != "s3cr3t" {
		t.Errorf("unexpected password value")
	}
	if _, ok := fieldMap["otp"]; ok {
		t.Error("did not expect otp field when TOTP is empty")
	}
}

func TestTransform_login_withTOTP(t *testing.T) {
	item := loginItem("AWS", "user", "pass", "otpauth://totp/AWS:user?secret=BASE32SECRET", nil)
	result := Transform(item, "vault-1")

	if result.Skipped {
		t.Fatalf("unexpected skip: %s", result.SkipReason)
	}

	fieldMap := fieldsByID(result.OPItem.Fields)
	otp, ok := fieldMap["otp"]
	if !ok {
		t.Fatal("expected otp field")
	}
	if otp.Type != onepassword.FieldTypeOTP {
		t.Errorf("expected OTP type, got %q", otp.Type)
	}
	if otp.Value != "otpauth://totp/AWS:user?secret=BASE32SECRET" {
		t.Errorf("unexpected otp value: %q", otp.Value)
	}
}

func TestTransform_passkey_skipped(t *testing.T) {
	item := bitwarden.Item{
		ID:   "bw-pk",
		Type: bitwarden.TypeLogin,
		Name: "Apple ID",
		Login: &bitwarden.Login{
			Fido2Credentials: []bitwarden.Fido2Credential{{CredentialID: "cred1"}},
		},
	}
	result := Transform(item, "vault-1")

	if !result.Skipped {
		t.Fatal("expected item with passkey to be skipped")
	}
	if result.OPItem != nil {
		t.Error("expected OPItem to be nil for skipped item")
	}
}

func TestTransform_secureNote(t *testing.T) {
	item := bitwarden.Item{
		ID:         "bw-note",
		Type:       bitwarden.TypeSecureNote,
		Name:       "WiFi Password",
		Notes:      "SSID: MyWifi\nPass: hunter2",
		SecureNote: &bitwarden.SecureNote{},
	}
	result := Transform(item, "vault-1")

	if result.Skipped {
		t.Fatalf("unexpected skip: %s", result.SkipReason)
	}
	if result.OPItem.Category != onepassword.CategorySecureNote {
		t.Errorf("expected SECURE_NOTE, got %q", result.OPItem.Category)
	}
	fieldMap := fieldsByID(result.OPItem.Fields)
	if fieldMap["notesPlain"].Value != "SSID: MyWifi\nPass: hunter2" {
		t.Errorf("unexpected notes value: %q", fieldMap["notesPlain"].Value)
	}
}

func TestTransform_card(t *testing.T) {
	item := bitwarden.Item{
		ID:   "bw-card",
		Type: bitwarden.TypeCard,
		Name: "Visa",
		Card: &bitwarden.Card{
			CardholderName: "John Doe",
			Number:         "4111111111111111",
			Code:           "123",
			ExpMonth:       "12",
			ExpYear:        "2028",
			Brand:          "Visa",
		},
	}
	result := Transform(item, "vault-1")

	if result.Skipped {
		t.Fatalf("unexpected skip: %s", result.SkipReason)
	}
	if result.OPItem.Category != onepassword.CategoryCreditCard {
		t.Errorf("expected CREDIT_CARD, got %q", result.OPItem.Category)
	}
	fieldMap := fieldsByID(result.OPItem.Fields)
	if fieldMap["ccnum"].Value != "4111111111111111" {
		t.Errorf("unexpected card number: %q", fieldMap["ccnum"].Value)
	}
	if fieldMap["expiry"].Value != "12/2028" {
		t.Errorf("unexpected expiry: %q", fieldMap["expiry"].Value)
	}
}

func TestTransform_customFields(t *testing.T) {
	item := loginItem("Example", "user", "pass", "", nil)
	item.Fields = []bitwarden.Field{
		{Name: "API Key", Value: "abc123", Type: bitwarden.FieldTypeHidden},
		{Name: "Notes", Value: "some text", Type: bitwarden.FieldTypeText},
	}
	result := Transform(item, "vault-1")

	if result.Skipped {
		t.Fatalf("unexpected skip: %s", result.SkipReason)
	}

	var customFields []onepassword.Field
	for _, f := range result.OPItem.Fields {
		if f.Section != nil && f.Section.ID == "custom_fields" {
			customFields = append(customFields, f)
		}
	}
	if len(customFields) != 2 {
		t.Fatalf("expected 2 custom fields, got %d", len(customFields))
	}
	if customFields[0].Type != onepassword.FieldTypeConcealed {
		t.Errorf("expected CONCEALED for hidden field, got %q", customFields[0].Type)
	}
	if customFields[1].Type != onepassword.FieldTypeString {
		t.Errorf("expected STRING for text field, got %q", customFields[1].Type)
	}
}

func TestComputeHash_changesOnPasswordUpdate(t *testing.T) {
	item1 := loginItem("GitHub", "user", "old-pass", "", nil)
	item2 := loginItem("GitHub", "user", "new-pass", "", nil)

	h1 := computeHash(item1)
	h2 := computeHash(item2)

	if h1 == h2 {
		t.Error("expected different hashes for different passwords")
	}
}

func TestComputeHash_stableForSameContent(t *testing.T) {
	item := loginItem("GitHub", "user", "pass", "", nil)
	if computeHash(item) != computeHash(item) {
		t.Error("hash must be deterministic")
	}
}

// fieldsByID indexes a slice of fields by their ID for easy lookup in tests.
func fieldsByID(fields []onepassword.Field) map[string]onepassword.Field {
	m := make(map[string]onepassword.Field)
	for _, f := range fields {
		m[f.ID] = f
	}
	return m
}

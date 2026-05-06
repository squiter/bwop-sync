package onepassword

import "testing"

func TestItem_HasPasskey_passkeyCategory(t *testing.T) {
	item := &Item{Category: CategoryPasskey}
	if !item.HasPasskey() {
		t.Error("expected PASSKEY-category item to report HasPasskey true")
	}
}

func TestItem_HasPasskey_passkeySection(t *testing.T) {
	item := &Item{
		Category: CategoryLogin,
		Fields: []Field{
			{ID: "username", Label: "username", Type: FieldTypeString},
			{ID: "pk_key", Label: "private key", Type: FieldTypeConcealed, Section: &SectionRef{ID: "passkey", Label: "Passkey"}},
		},
	}
	if !item.HasPasskey() {
		t.Error("expected item with Passkey section to report HasPasskey true")
	}
}

func TestItem_HasPasskey_passkeySection_caseInsensitive(t *testing.T) {
	item := &Item{
		Category: CategoryLogin,
		Fields: []Field{
			{ID: "pk_key", Label: "key", Type: FieldTypeConcealed, Section: &SectionRef{ID: "passkey", Label: "PASSKEY"}},
		},
	}
	if !item.HasPasskey() {
		t.Error("expected case-insensitive section label match to report HasPasskey true")
	}
}

func TestItem_HasPasskey_noPasskey(t *testing.T) {
	item := &Item{
		Category: CategoryLogin,
		Fields: []Field{
			{ID: "username", Label: "username", Type: FieldTypeString},
			{ID: "password", Label: "password", Type: FieldTypeConcealed},
		},
	}
	if item.HasPasskey() {
		t.Error("expected login item without passkey section to report HasPasskey false")
	}
}

func TestItem_HasPasskey_sectionNil(t *testing.T) {
	item := &Item{
		Category: CategoryLogin,
		Fields: []Field{
			{ID: "username", Label: "username", Type: FieldTypeString, Section: nil},
		},
	}
	if item.HasPasskey() {
		t.Error("expected field with nil section to not trigger HasPasskey")
	}
}

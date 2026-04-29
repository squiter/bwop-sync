// Package transformer converts Bitwarden vault items into 1Password item templates.
package transformer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/squiter/bwop-sync/internal/bitwarden"
	"github.com/squiter/bwop-sync/internal/onepassword"
)

// Result is the output of transforming one Bitwarden item.
type Result struct {
	// OPItem is the 1Password template ready for creation/update.
	// It is nil when Skipped is true.
	OPItem *onepassword.Item

	// Skipped is true when the item cannot be synced.
	Skipped bool

	// SkipReason describes why the item was skipped.
	SkipReason string

	// HasPasskey is true when the item contains a FIDO2 passkey that cannot be
	// synced. The item itself may still be synced if it has other credentials.
	HasPasskey bool

	// Hash is a deterministic fingerprint of the BW item's content.
	// The sync engine stores this to detect future changes.
	Hash string
}

// Transform converts a Bitwarden item to a 1Password item template.
// opVaultID must be resolved by the caller from the collection→vault mapping.
func Transform(item bitwarden.Item, opVaultID string) Result {
	hash := computeHash(item)

	if item.HasOnlyPasskey() {
		return Result{
			Skipped:    true,
			SkipReason: "contains passkey (FIDO2 credential) only — manual action required",
			HasPasskey: true,
			Hash:       hash,
		}
	}

	var opItem *onepassword.Item
	switch item.Type {
	case bitwarden.TypeLogin:
		opItem = transformLogin(item, opVaultID)
	case bitwarden.TypeSecureNote:
		opItem = transformSecureNote(item, opVaultID)
	case bitwarden.TypeCard:
		opItem = transformCard(item, opVaultID)
	case bitwarden.TypeIdentity:
		opItem = transformIdentity(item, opVaultID)
	default:
		return Result{
			Skipped:    true,
			SkipReason: fmt.Sprintf("unsupported item type %d", item.Type),
			Hash:       hash,
		}
	}

	return Result{OPItem: opItem, HasPasskey: item.HasPasskey(), Hash: hash}
}

func transformLogin(item bitwarden.Item, vaultID string) *onepassword.Item {
	fields := []onepassword.Field{
		{ID: "username", Label: "username", Type: onepassword.FieldTypeString, Purpose: onepassword.PurposeUsername},
		{ID: "password", Label: "password", Type: onepassword.FieldTypeConcealed, Purpose: onepassword.PurposePassword},
	}

	if item.Login != nil {
		fields[0].Value = item.Login.Username
		fields[1].Value = item.Login.Password

		if item.Login.TOTP != "" {
			fields = append(fields, onepassword.Field{
				ID:    "otp",
				Label: "one-time password",
				Type:  onepassword.FieldTypeOTP,
				Value: item.Login.TOTP,
			})
		}
	}

	if item.Notes != "" {
		fields = append(fields, onepassword.Field{
			ID:      "notesPlain",
			Label:   "notesPlain",
			Type:    onepassword.FieldTypeString,
			Purpose: onepassword.PurposeNotes,
			Value:   item.Notes,
		})
	}

	fields = append(fields, convertCustomFields(item.Fields)...)

	fields = append(fields, BwIDField(item.ID))

	return &onepassword.Item{
		Title:    item.Name,
		Category: onepassword.CategoryLogin,
		Vault:    onepassword.VaultRef{ID: vaultID},
		Fields:   fields,
		URLs:     convertURLs(item),
	}
}

func transformSecureNote(item bitwarden.Item, vaultID string) *onepassword.Item {
	fields := []onepassword.Field{
		{
			ID:      "notesPlain",
			Label:   "notesPlain",
			Type:    onepassword.FieldTypeString,
			Purpose: onepassword.PurposeNotes,
			Value:   item.Notes,
		},
	}
	fields = append(fields, convertCustomFields(item.Fields)...)

	fields = append(fields, BwIDField(item.ID))

	return &onepassword.Item{
		Title:    item.Name,
		Category: onepassword.CategorySecureNote,
		Vault:    onepassword.VaultRef{ID: vaultID},
		Fields:   fields,
	}
}

func transformCard(item bitwarden.Item, vaultID string) *onepassword.Item {
	var fields []onepassword.Field

	if item.Card != nil {
		c := item.Card
		fields = []onepassword.Field{
			{ID: "cardholder", Label: "cardholder name", Type: onepassword.FieldTypeString, Value: c.CardholderName},
			{ID: "ccnum", Label: "number", Type: onepassword.FieldTypeCCNumber, Value: c.Number},
			{ID: "cvv", Label: "verification number", Type: onepassword.FieldTypeConcealed, Value: c.Code},
			{ID: "expiry", Label: "expiry date", Type: onepassword.FieldTypeMonthYear, Value: cardExpiry(c.ExpMonth, c.ExpYear)},
			{ID: "type", Label: "type", Type: onepassword.FieldTypeString, Value: c.Brand},
		}
	}

	if item.Notes != "" {
		fields = append(fields, onepassword.Field{
			ID: "notesPlain", Label: "notesPlain",
			Type: onepassword.FieldTypeString, Purpose: onepassword.PurposeNotes,
			Value: item.Notes,
		})
	}

	fields = append(fields, convertCustomFields(item.Fields)...)

	fields = append(fields, BwIDField(item.ID))

	return &onepassword.Item{
		Title:    item.Name,
		Category: onepassword.CategoryCreditCard,
		Vault:    onepassword.VaultRef{ID: vaultID},
		Fields:   fields,
	}
}

func transformIdentity(item bitwarden.Item, vaultID string) *onepassword.Item {
	var fields []onepassword.Field

	if item.Identity != nil {
		id := item.Identity
		section := &onepassword.SectionRef{ID: "name", Label: "Identification"}
		fields = []onepassword.Field{
			{ID: "firstname", Label: "first name", Type: onepassword.FieldTypeString, Section: section, Value: id.FirstName},
			{ID: "lastname", Label: "last name", Type: onepassword.FieldTypeString, Section: section, Value: id.LastName},
			{ID: "username", Label: "username", Type: onepassword.FieldTypeString, Value: id.Username},
			{ID: "company", Label: "company", Type: onepassword.FieldTypeString, Value: id.Company},
			{ID: "email", Label: "email", Type: onepassword.FieldTypeString, Value: id.Email},
			{ID: "phone", Label: "phone", Type: onepassword.FieldTypePhone, Value: id.Phone},
			{ID: "address1", Label: "address", Type: onepassword.FieldTypeString, Value: id.Address1},
			{ID: "city", Label: "city", Type: onepassword.FieldTypeString, Value: id.City},
			{ID: "state", Label: "state", Type: onepassword.FieldTypeString, Value: id.State},
			{ID: "zip", Label: "zip", Type: onepassword.FieldTypeString, Value: id.PostalCode},
			{ID: "country", Label: "country", Type: onepassword.FieldTypeString, Value: id.Country},
		}
	}

	if item.Notes != "" {
		fields = append(fields, onepassword.Field{
			ID: "notesPlain", Label: "notesPlain",
			Type: onepassword.FieldTypeString, Purpose: onepassword.PurposeNotes,
			Value: item.Notes,
		})
	}

	fields = append(fields, convertCustomFields(item.Fields)...)

	fields = append(fields, BwIDField(item.ID))

	return &onepassword.Item{
		Title:    item.Name,
		Category: onepassword.CategoryIdentity,
		Vault:    onepassword.VaultRef{ID: vaultID},
		Fields:   fields,
	}
}

func convertCustomFields(fields []bitwarden.Field) []onepassword.Field {
	var out []onepassword.Field
	section := &onepassword.SectionRef{ID: "custom_fields", Label: "Custom Fields"}

	for i, f := range fields {
		var ft onepassword.FieldType
		switch f.Type {
		case bitwarden.FieldTypeHidden:
			ft = onepassword.FieldTypeConcealed
		default:
			// Text, Boolean, and Linked all map to STRING.
			// Boolean values are "true"/"false" strings; Linked fields copy the value.
			ft = onepassword.FieldTypeString
		}
		out = append(out, onepassword.Field{
			ID:      fmt.Sprintf("custom_%d", i),
			Label:   f.Name,
			Type:    ft,
			Value:   f.Value,
			Section: section,
		})
	}
	return out
}

func convertURLs(item bitwarden.Item) []onepassword.URL {
	if item.Login == nil {
		return nil
	}
	var urls []onepassword.URL
	for i, u := range item.Login.URIs {
		urls = append(urls, onepassword.URL{Href: u.URI, Primary: i == 0})
	}
	return urls
}

func cardExpiry(month, year string) string {
	if month == "" && year == "" {
		return ""
	}
	// 1Password monthYear fields require YYYY/MM format.
	return fmt.Sprintf("%s/%02s", year, month)
}

// BWIDFieldID is the ID of the hidden field stamped on every 1Password item
// created by bwop-sync. The field stores the Bitwarden item ID and is used by
// `bwop-sync recover` to rebuild state.json without needing the original state file.
// It is a concealed (hidden) field so it does not clutter the 1Password UI.
const BWIDFieldID = "bwop_sync_bw_id"

// BwIDField returns the hidden field that encodes the Bitwarden item ID.
func BwIDField(bwID string) onepassword.Field {
	return onepassword.Field{
		ID:    BWIDFieldID,
		Label: "bwop-sync source ID",
		Type:  onepassword.FieldTypeConcealed,
		Value: bwID,
	}
}

// computeHash returns a SHA-256 fingerprint of the item's content fields so the
// sync engine can detect changes without comparing every field individually.
func computeHash(item bitwarden.Item) string {
	type hashable struct {
		Name     string
		Notes    string
		Fields   []bitwarden.Field
		Login    *bitwarden.Login
		Card     *bitwarden.Card
		Identity *bitwarden.Identity
	}

	h := hashable{
		Name:     item.Name,
		Notes:    item.Notes,
		Fields:   item.Fields,
		Login:    item.Login,
		Card:     item.Card,
		Identity: item.Identity,
	}

	data, _ := json.Marshal(h)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

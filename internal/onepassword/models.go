package onepassword

import "strings"

// Category is the 1Password item category identifier.
type Category string

const (
	CategoryLogin      Category = "LOGIN"
	CategorySecureNote Category = "SECURE_NOTE"
	CategoryCreditCard Category = "CREDIT_CARD"
	CategoryIdentity   Category = "IDENTITY"
	CategorySSHKey     Category = "SSH_KEY"
	CategoryPasskey    Category = "PASSKEY"
)

// FieldType is the 1Password field type identifier.
type FieldType string

const (
	FieldTypeString    FieldType = "STRING"
	FieldTypeConcealed FieldType = "CONCEALED"
	FieldTypeOTP       FieldType = "OTP"
	FieldTypeURL       FieldType = "URL"
	FieldTypeCCNumber  FieldType = "CREDIT_CARD_NUMBER"
	FieldTypeMonthYear FieldType = "MONTH_YEAR"
	FieldTypeDate      FieldType = "DATE"
	FieldTypePhone     FieldType = "PHONE"
)

// FieldPurpose marks well-known fields on login items.
type FieldPurpose string

const (
	PurposeUsername FieldPurpose = "USERNAME"
	PurposePassword FieldPurpose = "PASSWORD"
	PurposeNotes    FieldPurpose = "NOTES"
)

// Item is the 1Password item structure used for both creation (via template) and
// the result returned by the CLI after create/edit.
type Item struct {
	ID       string   `json:"id,omitempty"`
	Title    string   `json:"title"`
	Category Category `json:"category"`
	Vault    VaultRef `json:"vault"`
	Fields   []Field  `json:"fields,omitempty"`
	URLs     []URL    `json:"urls,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Files    []File   `json:"files,omitempty"`
}

// File represents a file attachment on a 1Password item, as returned by
// `op item get --format json`.
type File struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name,omitempty"`
	Size        int64  `json:"size,omitempty"`
	ContentPath string `json:"content_path,omitempty"`
}

// VaultRef is the vault reference embedded inside an Item.
type VaultRef struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// Field is a single field inside a 1Password item.
type Field struct {
	ID      string       `json:"id"`
	Label   string       `json:"label"`
	Type    FieldType    `json:"type"`
	Purpose FieldPurpose `json:"purpose,omitempty"`
	Value   string       `json:"value,omitempty"`
	Section *SectionRef  `json:"section,omitempty"`
}

// SectionRef groups fields together inside an item.
type SectionRef struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
}

// URL is a website URL associated with a login item.
type URL struct {
	Href    string `json:"href"`
	Primary bool   `json:"primary,omitempty"`
}

// VaultInfo is the shape returned by `op vault list`.
type VaultInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ListItem is the minimal shape returned by `op item list`.
type ListItem struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Category Category `json:"category"`
	Vault    VaultRef `json:"vault"`
}

// AccountInfo is the shape returned by `op account list`.
type AccountInfo struct {
	URL       string `json:"url"`
	Email     string `json:"email"`
	UserUUID  string `json:"user_uuid"`
	Shorthand string `json:"shorthand"`
}

// HasPasskey reports whether the item contains a passkey credential.
// 1Password stores passkeys either as dedicated PASSKEY-category items or as
// fields grouped in a section labelled "Passkey" inside an existing LOGIN item.
func (item *Item) HasPasskey() bool {
	if item.Category == CategoryPasskey {
		return true
	}
	for _, f := range item.Fields {
		if f.Section != nil && strings.EqualFold(f.Section.Label, "passkey") {
			return true
		}
	}
	return false
}

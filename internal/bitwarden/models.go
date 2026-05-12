package bitwarden

import "encoding/json"

// ItemType is the numeric type identifier used in Bitwarden's JSON export.
type ItemType int

const (
	TypeLogin      ItemType = 1
	TypeSecureNote ItemType = 2
	TypeCard       ItemType = 3
	TypeIdentity   ItemType = 4
	TypeSSHKey     ItemType = 5
)

// FieldType is the type of a Bitwarden custom field.
type FieldType int

const (
	FieldTypeText    FieldType = 0
	FieldTypeHidden  FieldType = 1
	FieldTypeBoolean FieldType = 2
	FieldTypeLinked  FieldType = 3
)

// Export is the top-level structure of a Bitwarden JSON vault export.
type Export struct {
	Encrypted bool     `json:"encrypted"`
	Folders   []Folder `json:"folders"`
	Items     []Item   `json:"items"`
}

// Folder is a Bitwarden folder.
type Folder struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Item is a single Bitwarden vault item.
type Item struct {
	ID             string       `json:"id"`
	OrganizationID string       `json:"organizationId"`
	FolderID       string       `json:"folderId"`
	Type           ItemType     `json:"type"`
	Name           string       `json:"name"`
	Notes          string       `json:"notes"`
	Favorite       bool         `json:"favorite"`
	Fields         []Field      `json:"fields"`
	Login          *Login       `json:"login"`
	SecureNote     *SecureNote  `json:"secureNote"`
	Card           *Card        `json:"card"`
	Identity       *Identity    `json:"identity"`
	CollectionIDs  []string     `json:"collectionIds"`
	Attachments    []Attachment `json:"attachments"`
	RevisionDate   string       `json:"revisionDate"`
	CreationDate   string       `json:"creationDate"`
	DeletedDate    *string      `json:"deletedDate"`
}

// Attachment is a file attached to a Bitwarden item.
// Size is the raw byte count as a string in the BW JSON (e.g. "12345");
// SizeName is the human form (e.g. "12 KB"). Both come straight from the CLI.
type Attachment struct {
	ID       string `json:"id"`
	FileName string `json:"fileName"`
	Size     string `json:"size"`
	SizeName string `json:"sizeName"`
	URL      string `json:"url"`
}

// HasPasskey returns true when the item contains FIDO2 passkey credentials that
// cannot be transferred to 1Password via the CLI.
func (item *Item) HasPasskey() bool {
	return item.Login != nil && len(item.Login.Fido2Credentials) > 0
}

// HasOnlyPasskey returns true when the item contains FIDO2 credentials but no
// other syncable content (no username, password, TOTP, URIs, notes, or custom fields).
func (item *Item) HasOnlyPasskey() bool {
	if !item.HasPasskey() {
		return false
	}
	l := item.Login
	return l.Username == "" && l.Password == "" && l.TOTP == "" &&
		len(l.URIs) == 0 && item.Notes == "" && len(item.Fields) == 0
}

// PrimaryURL returns the first URI associated with a login item, or empty string.
func (item *Item) PrimaryURL() string {
	if item.Login == nil || len(item.Login.URIs) == 0 {
		return ""
	}
	return item.Login.URIs[0].URI
}

// Field is a custom field on a Bitwarden item.
type Field struct {
	Name     string    `json:"name"`
	Value    string    `json:"value"`
	Type     FieldType `json:"type"`
	LinkedID *string   `json:"linkedId"`
}

// Login holds login-specific data.
type Login struct {
	URIs             []URI             `json:"uris"`
	Username         string            `json:"username"`
	Password         string            `json:"password"`
	TOTP             string            `json:"totp"`
	Fido2Credentials []Fido2Credential `json:"fido2Credentials"`
}

// URI is a URL associated with a login item.
type URI struct {
	Match json.RawMessage `json:"match"` // null or int enum (0–5); unused by bwop-sync
	URI   string          `json:"uri"`
}

// Fido2Credential holds passkey data. These cannot be synced between password managers
// via CLI — they are logged for manual action.
type Fido2Credential struct {
	CredentialID    string          `json:"credentialId"`
	KeyType         string          `json:"keyType"`
	KeyAlgorithm    string          `json:"keyAlgorithm"`
	KeyCurve        string          `json:"keyCurve"`
	KeyValue        string          `json:"keyValue"`
	RPId            string          `json:"rpId"`
	UserHandle      string          `json:"userHandle"`
	UserName        string          `json:"userName"`
	Counter         json.RawMessage `json:"counter"` // BW returns this as string or int depending on version
	RPName          string          `json:"rpName"`
	UserDisplayName string          `json:"userDisplayName"`
	Discoverable    string          `json:"discoverable"`
	CreationDate    string          `json:"creationDate"`
}

// SecureNote holds the secure note sub-type.
type SecureNote struct {
	Type int `json:"type"`
}

// Card holds credit card data.
type Card struct {
	CardholderName string `json:"cardholderName"`
	Brand          string `json:"brand"`
	Number         string `json:"number"`
	ExpMonth       string `json:"expMonth"`
	ExpYear        string `json:"expYear"`
	Code           string `json:"code"`
}

// Identity holds personal identity data.
type Identity struct {
	Title          string `json:"title"`
	FirstName      string `json:"firstName"`
	MiddleName     string `json:"middleName"`
	LastName       string `json:"lastName"`
	Address1       string `json:"address1"`
	Address2       string `json:"address2"`
	Address3       string `json:"address3"`
	City           string `json:"city"`
	State          string `json:"state"`
	PostalCode     string `json:"postalCode"`
	Country        string `json:"country"`
	Company        string `json:"company"`
	Email          string `json:"email"`
	Phone          string `json:"phone"`
	SSN            string `json:"ssn"`
	Username       string `json:"username"`
	PassportNumber string `json:"passportNumber"`
	LicenseNumber  string `json:"licenseNumber"`
}

// Collection is a Bitwarden organization collection.
type Collection struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organizationId"`
	Name           string `json:"name"`
}

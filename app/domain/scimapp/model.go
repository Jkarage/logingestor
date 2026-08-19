// Package scimapp implements SCIM 2.0 provisioning for organization membership.
package scimapp

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/jkarage/logingestor/business/domain/orgbus"
	"github.com/jkarage/logingestor/business/domain/userbus"
)

// SCIM schema URNs used in requests and responses.
const (
	schemaUser        = "urn:ietf:params:scim:schemas:core:2.0:User"
	schemaListResp    = "urn:ietf:params:scim:api:messages:2.0:ListResponse"
	schemaError       = "urn:ietf:params:scim:api:messages:2.0:Error"
	schemaPatchOp     = "urn:ietf:params:scim:api:messages:2.0:PatchOp"
	contentTypeSCIM   = "application/scim+json"
	scimResourceUsers = "Users"
)

// Name is the SCIM complex name attribute.
type Name struct {
	Formatted  string `json:"formatted,omitempty"`
	GivenName  string `json:"givenName,omitempty"`
	FamilyName string `json:"familyName,omitempty"`
}

// Email is one entry of the SCIM multi-valued emails attribute.
type Email struct {
	Value   string `json:"value"`
	Primary bool   `json:"primary,omitempty"`
	Type    string `json:"type,omitempty"`
}

// Meta is the SCIM common resource metadata.
type Meta struct {
	ResourceType string `json:"resourceType"`
	Created      string `json:"created,omitempty"`
	LastModified string `json:"lastModified,omitempty"`
	Location     string `json:"location,omitempty"`
}

// User is a SCIM core User resource.
type User struct {
	Schemas    []string `json:"schemas"`
	ID         string   `json:"id"`
	ExternalID string   `json:"externalId,omitempty"`
	UserName   string   `json:"userName"`
	Name       *Name    `json:"name,omitempty"`
	Emails     []Email  `json:"emails,omitempty"`
	Active     bool     `json:"active"`
	Meta       Meta     `json:"meta"`
}

// Decode implements the decoder interface.
func (u *User) Decode(data []byte) error {
	return json.Unmarshal(data, u)
}

// Encode implements the encoder interface.
func (u User) Encode() ([]byte, string, error) {
	data, err := json.Marshal(u)
	return data, contentTypeSCIM, err
}

// PrimaryEmail returns the email a provisioning request intends, preferring the
// primary entry and falling back to userName, which most IdPs set to the email.
func (u User) PrimaryEmail() string {
	for _, e := range u.Emails {
		if e.Primary && e.Value != "" {
			return strings.ToLower(strings.TrimSpace(e.Value))
		}
	}
	for _, e := range u.Emails {
		if e.Value != "" {
			return strings.ToLower(strings.TrimSpace(e.Value))
		}
	}
	return strings.ToLower(strings.TrimSpace(u.UserName))
}

// DisplayName returns the best available human name.
func (u User) DisplayName() string {
	if u.Name != nil {
		if u.Name.Formatted != "" {
			return u.Name.Formatted
		}
		if joined := strings.TrimSpace(u.Name.GivenName + " " + u.Name.FamilyName); joined != "" {
			return joined
		}
	}
	return u.UserName
}

// toSCIMUser renders a local user plus their membership state in this org.
func toSCIMUser(usr userbus.User, member *orgbus.OrgMember, baseURL string) User {
	// "active" in SCIM means provisioned in this organization. A user disabled at
	// the platform level is also inactive here.
	active := member != nil && usr.Enabled

	return User{
		Schemas:  []string{schemaUser},
		ID:       usr.ID.String(),
		UserName: usr.Email.Address,
		Name:     &Name{Formatted: usr.Name.String()},
		Emails:   []Email{{Value: usr.Email.Address, Primary: true, Type: "work"}},
		Active:   active,
		Meta: Meta{
			ResourceType: "User",
			Created:      usr.DateCreated.UTC().Format(time.RFC3339),
			LastModified: usr.DateUpdated.UTC().Format(time.RFC3339),
			Location:     baseURL + "/Users/" + usr.ID.String(),
		},
	}
}

// ListResponse is a SCIM list result.
type ListResponse struct {
	Schemas      []string `json:"schemas"`
	TotalResults int      `json:"totalResults"`
	StartIndex   int      `json:"startIndex"`
	ItemsPerPage int      `json:"itemsPerPage"`
	Resources    []User   `json:"Resources"`
}

// Encode implements the encoder interface.
func (l ListResponse) Encode() ([]byte, string, error) {
	data, err := json.Marshal(l)
	return data, contentTypeSCIM, err
}

// PatchRequest is a SCIM PATCH document.
type PatchRequest struct {
	Schemas    []string    `json:"schemas"`
	Operations []Operation `json:"Operations"`
}

// Operation is one PATCH operation.
type Operation struct {
	Op    string          `json:"op"`
	Path  string          `json:"path"`
	Value json.RawMessage `json:"value"`
}

// Decode implements the decoder interface.
func (p *PatchRequest) Decode(data []byte) error {
	return json.Unmarshal(data, p)
}

// Error is a SCIM error response. SCIM clients parse this shape rather than the
// API's normal error envelope.
type Error struct {
	Schemas  []string `json:"schemas"`
	Status   string   `json:"status"`
	SCIMType string   `json:"scimType,omitempty"`
	Detail   string   `json:"detail"`

	status int
}

// Encode implements the encoder interface.
func (e Error) Encode() ([]byte, string, error) {
	data, err := json.Marshal(e)
	return data, contentTypeSCIM, err
}

// HTTPStatus implements the web package status interface.
func (e Error) HTTPStatus() int { return e.status }

// scimErr builds a SCIM-shaped error response.
func scimErr(status int, scimType, detail string) Error {
	return Error{
		Schemas:  []string{schemaError},
		Status:   itoa(status),
		SCIMType: scimType,
		Detail:   detail,
		status:   status,
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

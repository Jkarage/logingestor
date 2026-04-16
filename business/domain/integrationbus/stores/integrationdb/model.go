package integrationdb

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/integrationbus"
)

// integrationDB is the database representation of a configured integration.
type integrationDB struct {
	ID             uuid.UUID `db:"id"`
	OrgID          uuid.UUID `db:"org_id"`
	ProviderID     string    `db:"provider_id"`
	Name           string    `db:"name"`
	CredentialsEnc []byte    `db:"credentials_enc"`
	CredentialsIV  []byte    `db:"credentials_iv"`
	Enabled        bool      `db:"enabled"`
	DateCreated    time.Time `db:"date_created"`
	DateUpdated    time.Time `db:"date_updated"`
}

// providerDB is the database representation of an integration provider definition.
type providerDB struct {
	ID          string          `db:"id"`
	Name        string          `db:"name"`
	Icon        string          `db:"icon"`
	Type        string          `db:"type"`
	Description string          `db:"description"`
	FieldsJSON  json.RawMessage `db:"fields"`
}

// providerFieldDB mirrors the JSON structure stored in integration_providers.fields.
type providerFieldDB struct {
	Key         string `json:"k"`
	Label       string `json:"label"`
	Placeholder string `json:"ph,omitempty"`
}

func toDBIntegration(bus integrationbus.Integration, enc, iv []byte) integrationDB {
	return integrationDB{
		ID:             bus.ID,
		OrgID:          bus.OrgID,
		ProviderID:     bus.ProviderID,
		Name:           bus.Name,
		CredentialsEnc: enc,
		CredentialsIV:  iv,
		Enabled:        bus.Enabled,
		DateCreated:    bus.DateCreated.UTC(),
		DateUpdated:    bus.DateUpdated.UTC(),
	}
}

func toBusIntegration(db integrationDB, creds map[string]string) integrationbus.Integration {
	return integrationbus.Integration{
		ID:          db.ID,
		OrgID:       db.OrgID,
		ProviderID:  db.ProviderID,
		Name:        db.Name,
		Credentials: creds,
		Enabled:     db.Enabled,
		DateCreated: db.DateCreated.In(time.Local),
		DateUpdated: db.DateUpdated.In(time.Local),
	}
}

func toBusProvider(db providerDB) (integrationbus.Provider, error) {
	var raw []providerFieldDB
	if err := json.Unmarshal(db.FieldsJSON, &raw); err != nil {
		return integrationbus.Provider{}, err
	}

	fields := make([]integrationbus.ProviderField, len(raw))
	for i, f := range raw {
		fields[i] = integrationbus.ProviderField{
			Key:         f.Key,
			Label:       f.Label,
			Placeholder: f.Placeholder,
		}
	}

	return integrationbus.Provider{
		ID:          db.ID,
		Name:        db.Name,
		Icon:        db.Icon,
		Type:        db.Type,
		Description: db.Description,
		Fields:      fields,
	}, nil
}

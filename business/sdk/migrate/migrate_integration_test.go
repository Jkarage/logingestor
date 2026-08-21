package migrate_test

import (
	"testing"

	"github.com/jkarage/logingestor/business/domain/integrationbus/providers"
	"github.com/jkarage/logingestor/business/sdk/dbtest"
)

// The provider catalog has to arrive from the migrations alone. It used to be
// inserted only by seed.sql, which also inserts sample users, so a deployment
// that migrated without seeding came up with no integrations available at all
// and no error to explain why.
func Test_Migrate_Integration_SeedsTheProviderCatalog(t *testing.T) {
	db := dbtest.New(t)

	var rows []struct {
		ID      string `db:"id"`
		Name    string `db:"name"`
		Type    string `db:"type"`
		Enabled bool   `db:"enabled"`
	}
	if err := db.DB.Select(&rows, `SELECT id, name, type, enabled FROM integration_providers ORDER BY sort_order`); err != nil {
		t.Fatalf("read catalog: %v", err)
	}

	callers := providers.All(nil)

	if len(rows) != len(callers) {
		t.Errorf("catalog has %d providers, the registry has %d", len(rows), len(callers))
	}

	for _, r := range rows {
		if _, ok := callers[r.ID]; !ok {
			t.Errorf("catalog offers %q with no caller behind it", r.ID)
		}
		if !r.Enabled {
			t.Errorf("provider %q arrived disabled", r.ID)
		}
		if r.Name == "" || r.Type == "" {
			t.Errorf("provider %q is missing a name or type", r.ID)
		}
	}

	for id := range callers {
		found := false
		for _, r := range rows {
			if r.ID == id {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("caller %q is registered but the migrations never insert it", id)
		}
	}

	// Every field descriptor has to carry the two keys the form is built from.
	var bad int
	if err := db.DB.Get(&bad, `
		SELECT count(1) FROM integration_providers p, jsonb_array_elements(p.fields) f
		WHERE f->>'k' IS NULL OR f->>'label' IS NULL`); err != nil {
		t.Fatalf("check fields: %v", err)
	}
	if bad != 0 {
		t.Errorf("%d field descriptors are missing a key or label", bad)
	}
}

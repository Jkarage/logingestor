package migrate

import (
	"regexp"
	"strings"
	"testing"

	"github.com/jkarage/logingestor/business/domain/integrationbus/providers"
)

// providerIDPattern finds the id of each row inserted into integration_providers.
// The id is the first value in every VALUES tuple, on its own line.
var providerIDPattern = regexp.MustCompile(`(?m)^\s*'([a-z]+)',\s*$`)

// catalogIDs extracts the provider ids inserted by a SQL document, looking only
// at INSERT INTO integration_providers statements.
func catalogIDs(t *testing.T, doc string) map[string]struct{} {
	t.Helper()

	ids := map[string]struct{}{}

	for _, chunk := range strings.Split(doc, "INSERT INTO integration_providers")[1:] {
		// A statement ends at the first semicolon; anything after it belongs to a
		// different table.
		if end := strings.Index(chunk, ";"); end >= 0 {
			chunk = chunk[:end]
		}

		for _, m := range providerIDPattern.FindAllStringSubmatch(chunk, -1) {
			ids[m[1]] = struct{}{}
		}
	}

	if len(ids) == 0 {
		t.Fatal("found no provider ids in the document; the extraction is broken, not the catalog")
	}

	return ids
}

// Every provider the catalog offers must have a caller registered, and every
// caller must be offered.
//
// The two halves live in different places — SQL rows here, Go constructors in
// the providers package — and nothing at compile time ties them together.
// A row without a caller is worse than a missing feature: the UI lists the
// provider, the customer fills in credentials, and Create rejects it as an
// unknown provider.
func Test_IntegrationProviderCatalog_MatchesCallers(t *testing.T) {
	callers := providers.All(nil)

	for _, doc := range []struct {
		name string
		sql  string
	}{
		{"migrations", migrateDoc},
		{"seed", seedDoc},
	} {
		t.Run(doc.name, func(t *testing.T) {
			ids := catalogIDs(t, doc.sql)

			for id := range ids {
				if _, ok := callers[id]; !ok {
					t.Errorf("catalog offers %q but no caller is registered for it", id)
				}
			}

			for id := range callers {
				if _, ok := ids[id]; !ok {
					t.Errorf("caller %q is registered but the catalog never offers it", id)
				}
			}
		})
	}
}

// A nil caller in the map would pass the test above and panic on the first
// alert.
func Test_IntegrationProviderCallers_AreNotNil(t *testing.T) {
	for id, caller := range providers.All(nil) {
		if caller == nil {
			t.Errorf("caller %q is nil", id)
		}
	}
}

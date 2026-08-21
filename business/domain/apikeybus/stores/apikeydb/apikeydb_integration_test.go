package apikeydb_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jkarage/logingestor/business/domain/apikeybus"
	"github.com/jkarage/logingestor/business/domain/apikeybus/stores/apikeydb"
	"github.com/jkarage/logingestor/business/sdk/dbtest"
)

func newBus(t *testing.T) (*apikeybus.Business, *dbtest.Database, dbtest.Fixture) {
	t.Helper()

	db := dbtest.New(t)
	f := db.SeedFixture(t, "pro")

	return apikeybus.NewBusiness(db.Log, apikeydb.NewStore(db.Log, db.DB)), db, f
}

// Authentication is a hash lookup, so the raw key must never be stored and the
// hash must be what the lookup matches on.
func Test_APIKey_Integration_AuthenticateByHash(t *testing.T) {
	bus, db, f := newBus(t)
	ctx := context.Background()

	key, raw, err := bus.Create(ctx, f.OrgID, f.UserID, apikeybus.NewAPIKey{Name: "ci"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	var stored struct {
		Hash   string `db:"key_hash"`
		Prefix string `db:"key_prefix"`
	}
	if err := db.DB.Get(&stored, `SELECT key_hash, key_prefix FROM api_keys WHERE id = $1`, key.ID); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if stored.Hash == raw {
		t.Fatalf("the raw key was stored")
	}
	if stored.Hash != apikeybus.HashKey(raw) {
		t.Errorf("stored hash does not match the key's hash")
	}

	got, err := bus.Authenticate(ctx, raw)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if got.ID != key.ID || got.OrgID != f.OrgID {
		t.Errorf("authenticated the wrong key: %s in %s", got.ID, got.OrgID)
	}

	// A key that was never issued, and one that is nearly right, both fail the
	// same way.
	for _, bad := range []string{
		apikeybus.KeyScheme + "0000000000000000000000000000000000000000000000000000000000000000",
		raw + "a",
		raw[:len(raw)-1],
		"ls_src_live_deadbeef",
		"",
	} {
		if _, err := bus.Authenticate(ctx, bad); !errors.Is(err, apikeybus.ErrNotFound) {
			t.Errorf("Authenticate(%q) = %v, want ErrNotFound", bad, err)
		}
	}
}

// A revoked key stops working immediately, and an expired one says so distinctly
// so a script can be told to rotate rather than that its key is wrong.
func Test_APIKey_Integration_RevokeAndExpiry(t *testing.T) {
	bus, db, f := newBus(t)
	ctx := context.Background()

	key, raw, err := bus.Create(ctx, f.OrgID, f.UserID, apikeybus.NewAPIKey{Name: "temp"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := bus.Authenticate(ctx, raw); err != nil {
		t.Fatalf("authenticate before revoke: %v", err)
	}

	if err := bus.Revoke(ctx, key.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := bus.Authenticate(ctx, raw); !errors.Is(err, apikeybus.ErrNotFound) {
		t.Errorf("a revoked key still authenticates: %v", err)
	}

	// Expiry in the past is refused at creation, so build the lapsed state the
	// only way a real key reaches it: time passing.
	future := time.Now().Add(time.Hour)
	expiring, expiringRaw, err := bus.Create(ctx, f.OrgID, f.UserID, apikeybus.NewAPIKey{Name: "expiring", ExpiresAt: &future})
	if err != nil {
		t.Fatalf("create expiring: %v", err)
	}

	if _, err := bus.Authenticate(ctx, expiringRaw); err != nil {
		t.Fatalf("authenticate an unexpired key: %v", err)
	}

	// Backdate it the way time would: creating a key already expired is refused.
	past := time.Now().Add(-time.Minute)
	if _, err := db.DB.Exec(`UPDATE api_keys SET expires_at = $1 WHERE id = $2`, past, expiring.ID); err != nil {
		t.Fatalf("backdate expiry: %v", err)
	}
	if _, err := bus.Authenticate(ctx, expiringRaw); !errors.Is(err, apikeybus.ErrKeyExpired) {
		t.Errorf("an expired key = %v, want ErrKeyExpired", err)
	}

	if _, _, err := bus.Create(ctx, f.OrgID, f.UserID, apikeybus.NewAPIKey{Name: "backdated", ExpiresAt: &past}); !errors.Is(err, apikeybus.ErrExpiryPast) {
		t.Errorf("creating a key that is already expired = %v, want ErrExpiryPast", err)
	}
}

// Listing is per org, and a key pinned to a project keeps that pin, since it is
// what scopes every query the key can run.
func Test_APIKey_Integration_ScopeAndListing(t *testing.T) {
	bus, db, f := newBus(t)
	ctx := context.Background()

	orgWide, _, err := bus.Create(ctx, f.OrgID, f.UserID, apikeybus.NewAPIKey{Name: "org wide"})
	if err != nil {
		t.Fatalf("create org key: %v", err)
	}
	if orgWide.ProjectID != nil {
		t.Errorf("an org-wide key came back pinned to %s", orgWide.ProjectID)
	}

	pinned, _, err := bus.Create(ctx, f.OrgID, f.UserID, apikeybus.NewAPIKey{Name: "pinned", ProjectID: &f.ProjectID})
	if err != nil {
		t.Fatalf("create pinned key: %v", err)
	}
	if pinned.ProjectID == nil || *pinned.ProjectID != f.ProjectID {
		t.Errorf("pinned key project = %v, want %s", pinned.ProjectID, f.ProjectID)
	}

	keys, err := bus.QueryByOrg(ctx, f.OrgID)
	if err != nil {
		t.Fatalf("query by org: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("got %d keys, want 2", len(keys))
	}

	// Another org's keys are invisible here.
	other := db.SeedFixture(t, "free")
	if _, _, err := bus.Create(ctx, other.OrgID, other.UserID, apikeybus.NewAPIKey{Name: "theirs"}); err != nil {
		t.Fatalf("create in other org: %v", err)
	}
	keys, err = bus.QueryByOrg(ctx, f.OrgID)
	if err != nil {
		t.Fatalf("query by org again: %v", err)
	}
	if len(keys) != 2 {
		t.Errorf("got %d keys, want 2: another org's key leaked into the listing", len(keys))
	}

	// Deleting the project a key is pinned to takes the key with it, rather than
	// leaving a credential scoped to nothing.
	if _, err := db.DB.Exec(`DELETE FROM projects WHERE id = $1`, f.ProjectID); err != nil {
		t.Fatalf("delete project: %v", err)
	}
	keys, err = bus.QueryByOrg(ctx, f.OrgID)
	if err != nil {
		t.Fatalf("query after project delete: %v", err)
	}
	if len(keys) != 1 || keys[0].ID != orgWide.ID {
		t.Errorf("after deleting the project, keys = %d, want only the org-wide one", len(keys))
	}
}

// last_used_at is how an operator finds keys nothing uses any more.
func Test_APIKey_Integration_TouchLastUsed(t *testing.T) {
	bus, _, f := newBus(t)
	ctx := context.Background()

	key, _, err := bus.Create(ctx, f.OrgID, f.UserID, apikeybus.NewAPIKey{Name: "used"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if key.LastUsedAt != nil {
		t.Errorf("a new key already has a last-used time")
	}

	when := time.Now().UTC().Truncate(time.Second)
	bus.TouchLastUsed(ctx, key.ID, when)

	got, err := bus.QueryByID(ctx, key.ID)
	if err != nil {
		t.Fatalf("query by id: %v", err)
	}
	if got.LastUsedAt == nil {
		t.Fatalf("last used was not recorded")
	}
	if !got.LastUsedAt.UTC().Truncate(time.Second).Equal(when) {
		t.Errorf("last used = %v, want %v", got.LastUsedAt.UTC(), when)
	}
}

// A key carries its own query API budget, so one customer can be raised or
// throttled with an UPDATE rather than a deploy. Zero means the service default,
// which is what every key created before limits existed carries.
func Test_APIKey_Integration_RateLimits(t *testing.T) {
	bus, db, f := newBus(t)
	ctx := context.Background()

	onDefault, _, err := bus.Create(ctx, f.OrgID, f.UserID, apikeybus.NewAPIKey{Name: "default"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if onDefault.RateLimitPerMin != 0 || onDefault.RateLimitBurst != 0 {
		t.Errorf("a key with no limits came back with %d/%d", onDefault.RateLimitPerMin, onDefault.RateLimitBurst)
	}

	throttled, _, err := bus.Create(ctx, f.OrgID, f.UserID, apikeybus.NewAPIKey{
		Name: "throttled", RateLimitPerMin: 30, RateLimitBurst: 5,
	})
	if err != nil {
		t.Fatalf("create throttled: %v", err)
	}

	// The limits have to survive the round trip, since the middleware reads them
	// from the authenticated key rather than from the create response.
	reread, err := bus.QueryByID(ctx, throttled.ID)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if reread.RateLimitPerMin != 30 || reread.RateLimitBurst != 5 {
		t.Errorf("limits = %d/%d, want 30/5", reread.RateLimitPerMin, reread.RateLimitBurst)
	}

	// Authenticate is the path the middleware actually uses.
	_, raw, err := bus.Create(ctx, f.OrgID, f.UserID, apikeybus.NewAPIKey{Name: "authed", RateLimitPerMin: 90})
	if err != nil {
		t.Fatalf("create authed: %v", err)
	}
	authed, err := bus.Authenticate(ctx, raw)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if authed.RateLimitPerMin != 90 {
		t.Errorf("authenticated key reports %d/min, want 90", authed.RateLimitPerMin)
	}

	// A negative limit is refused rather than stored as a value nothing can mean.
	if _, _, err := bus.Create(ctx, f.OrgID, f.UserID, apikeybus.NewAPIKey{Name: "bad", RateLimitPerMin: -1}); !errors.Is(err, apikeybus.ErrRateLimitNegative) {
		t.Errorf("err = %v, want ErrRateLimitNegative", err)
	}

	// And the database refuses it too, so no path can write one.
	if _, err := db.DB.Exec(`
		UPDATE api_keys SET rate_limit_per_min = -5 WHERE id = $1`, throttled.ID); err == nil {
		t.Errorf("the database accepted a negative rate limit")
	}
}

// Package userdb contains user related CRUD functionality.
package userdb

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/mail"
	"time"

	"github.com/google/uuid"
	"github.com/jkarage/logingestor/business/domain/userbus"
	"github.com/jkarage/logingestor/business/sdk/order"
	"github.com/jkarage/logingestor/business/sdk/page"
	"github.com/jkarage/logingestor/business/sdk/sqldb"
	"github.com/jkarage/logingestor/foundation/logger"
	"github.com/jmoiron/sqlx"
)

// Store manages the set of APIs for user database access.
type Store struct {
	log *logger.Logger
	db  sqlx.ExtContext
}

// NewStore constructs the api for data access.
func NewStore(log *logger.Logger, db *sqlx.DB) *Store {
	return &Store{
		log: log,
		db:  db,
	}
}

// NewWithTx constructs a new Store value replacing the sqlx DB
// value with a sqlx DB value that is currently inside a transaction.
func (s *Store) NewWithTx(tx sqldb.CommitRollbacker) (userbus.Storer, error) {
	ec, err := sqldb.GetExtContext(tx)
	if err != nil {
		return nil, err
	}

	store := Store{
		log: s.log,
		db:  ec,
	}

	return &store, nil
}

// Create inserts a new user into the database.
func (s *Store) Create(ctx context.Context, usr userbus.User) error {
	const q = `
	INSERT INTO users
		(id, name, email, password_hash, roles, enabled, date_created, date_updated)
	VALUES
		(:id, :name, :email, :password_hash, :roles, :enabled, :date_created, :date_updated)`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, toDBUser(usr)); err != nil {
		if errors.Is(err, sqldb.ErrDBDuplicatedEntry) {
			return fmt.Errorf("namedexeccontext: %w", userbus.ErrUniqueEmail)
		}
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// Update replaces a user document in the database.
func (s *Store) Update(ctx context.Context, usr userbus.User) error {
	const q = `
	UPDATE
		users
	SET 
		"name" = :name,
		"email" = :email,
		"roles" = :roles,
		"password_hash" = :password_hash,
		"enabled" = :enabled,
		"date_updated" = :date_updated
	WHERE
		id = :id`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, toDBUser(usr)); err != nil {
		if errors.Is(err, sqldb.ErrDBDuplicatedEntry) {
			return userbus.ErrUniqueEmail
		}
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// Delete removes a user from the database.
func (s *Store) Delete(ctx context.Context, usr userbus.User) error {
	const q = `
	DELETE FROM
		users
	WHERE
		id = :id`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, toDBUser(usr)); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// Query retrieves a list of existing users from the database.
func (s *Store) Query(ctx context.Context, filter userbus.QueryFilter, orderBy order.By, page page.Page) ([]userbus.User, error) {
	data := map[string]any{
		"offset":        (page.Number() - 1) * page.RowsPerPage(),
		"rows_per_page": page.RowsPerPage(),
	}

	const q = `
	SELECT
		id, name, email, password_hash, roles, enabled, date_created, date_updated
	FROM
		users`

	buf := bytes.NewBufferString(q)
	applyFilter(filter, data, buf)

	orderByClause, err := orderByClause(orderBy)
	if err != nil {
		return nil, err
	}

	buf.WriteString(orderByClause)
	buf.WriteString(" OFFSET :offset ROWS FETCH NEXT :rows_per_page ROWS ONLY")

	var dbUsrs []userDB
	if err := sqldb.NamedQuerySlice(ctx, s.log, s.db, buf.String(), data, &dbUsrs); err != nil {
		return nil, fmt.Errorf("namedqueryslice: %w", err)
	}

	return toBusUsers(dbUsrs)
}

// Count returns the total number of users in the DB.
func (s *Store) Count(ctx context.Context, filter userbus.QueryFilter) (int, error) {
	data := map[string]any{}

	const q = `
	SELECT
		count(1)
	FROM
		users`

	buf := bytes.NewBufferString(q)
	applyFilter(filter, data, buf)

	var count struct {
		Count int `db:"count"`
	}
	if err := sqldb.NamedQueryStruct(ctx, s.log, s.db, buf.String(), data, &count); err != nil {
		return 0, fmt.Errorf("db: %w", err)
	}

	return count.Count, nil
}

// QueryByID gets the specified user from the database.
func (s *Store) QueryByID(ctx context.Context, userID uuid.UUID) (userbus.User, error) {
	data := struct {
		ID string `db:"id"`
	}{
		ID: userID.String(),
	}

	const q = `
	SELECT
        id, name, email, password_hash, roles, enabled, date_created, date_updated
	FROM
		users
	WHERE 
		id = :id`

	var dbUsr userDB
	if err := sqldb.NamedQueryStruct(ctx, s.log, s.db, q, data, &dbUsr); err != nil {
		if errors.Is(err, sqldb.ErrDBNotFound) {
			return userbus.User{}, fmt.Errorf("db: %w", userbus.ErrNotFound)
		}
		return userbus.User{}, fmt.Errorf("db: %w", err)
	}

	return toBusUser(dbUsr)
}

// StoreVerifyToken inserts a new verification token row.
func (s *Store) StoreVerifyToken(ctx context.Context, userID uuid.UUID, token string, expiresAt time.Time) error {
	data := struct {
		Token     string    `db:"token"`
		UserID    string    `db:"user_id"`
		ExpiresAt time.Time `db:"expires_at"`
	}{
		Token:     token,
		UserID:    userID.String(),
		ExpiresAt: expiresAt.UTC(),
	}

	const q = `
	INSERT INTO verification_tokens (token, user_id, expires_at)
	VALUES (:token, :user_id, :expires_at)`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, q, data); err != nil {
		return fmt.Errorf("namedexeccontext: %w", err)
	}

	return nil
}

// ConsumeVerifyToken validates a verification token and marks it as used.
// Returns ErrTokenNotFound, ErrTokenExpired, or ErrTokenUsed on failure.
func (s *Store) ConsumeVerifyToken(ctx context.Context, token string) (uuid.UUID, error) {
	data := struct {
		Token string `db:"token"`
	}{Token: token}

	const selectQ = `
	SELECT user_id, expires_at, used_at
	FROM verification_tokens
	WHERE token = :token`

	var row struct {
		UserID    uuid.UUID    `db:"user_id"`
		ExpiresAt time.Time    `db:"expires_at"`
		UsedAt    sql.NullTime `db:"used_at"`
	}

	if err := sqldb.NamedQueryStruct(ctx, s.log, s.db, selectQ, data, &row); err != nil {
		if errors.Is(err, sqldb.ErrDBNotFound) {
			return uuid.UUID{}, fmt.Errorf("db: %w", userbus.ErrTokenNotFound)
		}
		return uuid.UUID{}, fmt.Errorf("db: %w", err)
	}

	if row.UsedAt.Valid {
		return uuid.UUID{}, userbus.ErrTokenUsed
	}

	if time.Now().After(row.ExpiresAt) {
		return uuid.UUID{}, userbus.ErrTokenExpired
	}

	markData := struct {
		Token  string    `db:"token"`
		UsedAt time.Time `db:"used_at"`
	}{
		Token:  token,
		UsedAt: time.Now().UTC(),
	}

	const updateQ = `UPDATE verification_tokens SET used_at = :used_at WHERE token = :token`

	if err := sqldb.NamedExecContext(ctx, s.log, s.db, updateQ, markData); err != nil {
		return uuid.UUID{}, fmt.Errorf("mark used: %w", err)
	}

	return row.UserID, nil
}

// QueryByEmail gets the specified user from the database by email.
func (s *Store) QueryByEmail(ctx context.Context, email mail.Address) (userbus.User, error) {
	data := struct {
		Email string `db:"email"`
	}{
		Email: email.Address,
	}

	const q = `
	SELECT
        id, name, email, password_hash, roles, enabled, date_created, date_updated
	FROM
		users
	WHERE
		email = :email`

	var dbUsr userDB
	if err := sqldb.NamedQueryStruct(ctx, s.log, s.db, q, data, &dbUsr); err != nil {
		if errors.Is(err, sqldb.ErrDBNotFound) {
			return userbus.User{}, fmt.Errorf("db: %w", userbus.ErrNotFound)
		}
		return userbus.User{}, fmt.Errorf("db: %w", err)
	}

	return toBusUser(dbUsr)
}

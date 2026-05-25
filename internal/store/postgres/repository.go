// Package postgres implements store.Repository against a real Postgres
// database. The actual driver wiring (pgxpool, transactions) lands when
// a Postgres dependency is added to go.mod; for now this file documents
// the structure handlers can compile against once that lands.
//
// To finish wiring (next session):
//
//   1. go get github.com/jackc/pgx/v5/pgxpool
//   2. Add *pgxpool.Pool field to Repository
//   3. Implement Init/Get/Consume using the queries in queries.go
//   4. Add an integration test that runs against a Postgres testcontainer
//
// Until then, edge runs with the in-memory store (see internal/store/memory/).
package postgres

import (
	"context"

	"github.com/layerid/edge/internal/store"
)

// Repository implements store.Repository against Postgres. Field is
// intentionally unexported and untyped here — fill in *pgxpool.Pool
// (or *sql.DB) once the driver dependency is committed.
type Repository struct {
	// pool *pgxpool.Pool   // TODO: wire pgx
	region string
}

// New constructs a Repository. region is the edge POP identifier, written
// into lookups.edge_region for cross-region debugging.
func New(region string) *Repository {
	return &Repository{region: region}
}

// Init upserts a Lookup via queryInit. Idempotent on (req_id, tenant_id).
func (r *Repository) Init(ctx context.Context, l store.Lookup) (store.Lookup, error) {
	// TODO: execute queryInit with l fields; map signals_json via json.Marshal.
	return store.Lookup{}, errNotImplemented
}

// Get returns a Lookup by req_id. Cross-tenant lookups return ErrCrossTenant
// to mirror the in-memory Repository's behaviour.
func (r *Repository) Get(ctx context.Context, reqID string, tenantID int64) (store.Lookup, error) {
	// TODO: execute queryGet, then enforce tenant scoping in code (the
	// query intentionally doesn't filter on tenant so we can distinguish
	// "not found" from "wrong tenant" without leaking req_id existence).
	return store.Lookup{}, errNotImplemented
}

// Consume executes queryConsume + queryDebitQuota + queryAuditConsume in
// a single transaction.
func (r *Repository) Consume(ctx context.Context, reqID string, tenantID int64, consumedBy string) (store.ConsumeResult, error) {
	// TODO: BEGIN; queryConsume; if !replayed { queryDebitQuota }; queryAuditConsume; COMMIT
	return store.ConsumeResult{}, errNotImplemented
}

// errNotImplemented marks methods waiting on the pgx driver wiring.
// Production callers must use internal/store/memory/ until this is done.
var errNotImplemented = errPgxPending{}

type errPgxPending struct{}

func (errPgxPending) Error() string {
	return "postgres: driver wiring pending — see internal/store/postgres/repository.go TODO"
}

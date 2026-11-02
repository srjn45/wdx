package db

import (
	"context"
	"database/sql"
	"sync"
)

// Statements keeps the queries asked of it prepared: parsing SQL is much of what
// running a large query costs. The queries must be a fixed set, as each is kept
// for as long as the database is open.
type Statements struct {
	db       *sql.DB
	mu       sync.Mutex
	prepared map[string]*sql.Stmt
}

// NewStatements returns the statements of db.
func NewStatements(db *sql.DB) *Statements {
	return &Statements{db: db, prepared: map[string]*sql.Stmt{}}
}

// Query runs query, prepared the first time.
func (s *Statements) Query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	stmt, err := s.statement(ctx, query)
	if err != nil {
		return nil, err
	}
	return stmt.QueryContext(ctx, args...)
}

func (s *Statements) statement(ctx context.Context, query string) (*sql.Stmt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if stmt, ok := s.prepared[query]; ok {
		return stmt, nil
	}
	stmt, err := s.db.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	s.prepared[query] = stmt
	return stmt, nil
}

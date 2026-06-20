// Package clickhouse implements storage.Store against ClickHouse. ALL SQL in
// the hub lives in this package.
package clickhouse

import (
	"context"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// Config connects the store to a ClickHouse server.
type Config struct {
	Addr     string // host:port (native protocol)
	Database string // telemetry database (default "otel")
	Username string
	Password string
}

// Store implements storage.Store. Construct with New.
type Store struct {
	conn driver.Conn
	db   string
}

// New opens a connection pool and verifies connectivity.
func New(ctx context.Context, cfg Config) (*Store, error) {
	if cfg.Database == "" {
		cfg.Database = "otel"
	}
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{cfg.Addr},
		Auth: clickhouse.Auth{
			Database: cfg.Database,
			Username: cfg.Username,
			Password: cfg.Password,
		},
		DialTimeout:     5 * time.Second,
		MaxOpenConns:    8,
		MaxIdleConns:    4,
		ConnMaxLifetime: time.Hour,
	})
	if err != nil {
		return nil, fmt.Errorf("opening clickhouse at %s: %w", cfg.Addr, err)
	}
	s := &Store{conn: conn, db: cfg.Database}
	if err := s.Ping(ctx); err != nil {
		return nil, fmt.Errorf("pinging clickhouse at %s: %w", cfg.Addr, err)
	}
	return s, nil
}

// Ping reports backend connectivity.
func (s *Store) Ping(ctx context.Context) error {
	return s.conn.Ping(ctx)
}

// Close releases the connection pool.
func (s *Store) Close() error {
	return s.conn.Close()
}

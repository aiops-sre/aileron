package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	_ "github.com/lib/pq"
)

// PostgresConfig holds PostgreSQL configuration
type PostgresConfig struct {
	Host            string
	Port            int
	User            string
	Password        string
	Database        string
	SSLMode         string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

// Pool represents optimized database connection pool
type Pool struct {
	DB     *sql.DB
	Config *PostgresConfig
}

// NewPool creates an optimized PostgreSQL connection pool
func NewPool(config *PostgresConfig) (*Pool, error) {
	// Build connection string
	connStr := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		config.Host, config.Port, config.User, config.Password, config.Database, config.SSLMode,
	)

	// Open database connection
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Configure connection pool for optimal performance
	db.SetMaxOpenConns(config.MaxOpenConns)       // Maximum concurrent connections
	db.SetMaxIdleConns(config.MaxIdleConns)       // Keep idle connections ready
	db.SetConnMaxLifetime(config.ConnMaxLifetime) // Recycle connections
	db.SetConnMaxIdleTime(config.ConnMaxIdleTime) // Close idle connections

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	pool := &Pool{
		DB:     db,
		Config: config,
	}

	// Initialize performance monitoring
	go pool.monitorPerformance()

	return pool, nil
}

// GetDefaultConfig returns production-optimized configuration
func GetDefaultConfig() *PostgresConfig {
	password := os.Getenv("DB_PASSWORD")
	return &PostgresConfig{
		Host:            "postgres",
		Port:            5432,
		User:            "alerthub",
		Password:        password,
		Database:        "alerthub",
		SSLMode:         "prefer",
		MaxOpenConns:    100,              // Max concurrent connections
		MaxIdleConns:    25,               // Keep 25 idle for fast response
		ConnMaxLifetime: 5 * time.Minute,  // Recycle every 5 min
		ConnMaxIdleTime: 10 * time.Minute, // Close idle after 10 min
	}
}

// monitorPerformance monitors connection pool health
func (p *Pool) monitorPerformance() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		stats := p.DB.Stats()

		// Log connection pool metrics
		fmt.Printf("[DB Pool] Open: %d, InUse: %d, Idle: %d, WaitCount: %d, WaitDuration: %v\n",
			stats.OpenConnections,
			stats.InUse,
			stats.Idle,
			stats.WaitCount,
			stats.WaitDuration,
		)

		// Alert if connection pool is exhausted
		if stats.WaitCount > 10 {
			fmt.Printf("[DB Pool] High wait count: %d - consider increasing MaxOpenConns\n", stats.WaitCount)
		}

		// Alert if too many idle connections
		if stats.Idle > p.Config.MaxIdleConns*2 {
			fmt.Printf("[DB Pool] Too many idle connections: %d\n", stats.Idle)
		}
	}
}

// GetStats returns current pool statistics
func (p *Pool) GetStats() sql.DBStats {
	return p.DB.Stats()
}

// HealthCheck checks database health
func (p *Pool) HealthCheck(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	// Simple ping
	if err := p.DB.PingContext(ctx); err != nil {
		return fmt.Errorf("database ping failed: %w", err)
	}

	// Check if we can execute queries
	var result int
	if err := p.DB.QueryRowContext(ctx, "SELECT 1").Scan(&result); err != nil {
		return fmt.Errorf("database query failed: %w", err)
	}

	return nil
}

// Close closes the database connection pool
func (p *Pool) Close() error {
	return p.DB.Close()
}

// PreparedStatementCache caches prepared statements for performance
type PreparedStatementCache struct {
	stmts map[string]*sql.Stmt
	db    *sql.DB
}

// NewPreparedStatementCache creates a new prepared statement cache
func NewPreparedStatementCache(db *sql.DB) *PreparedStatementCache {
	return &PreparedStatementCache{
		stmts: make(map[string]*sql.Stmt),
		db:    db,
	}
}

// Prepare prepares and caches a statement
func (c *PreparedStatementCache) Prepare(name, query string) error {
	stmt, err := c.db.Prepare(query)
	if err != nil {
		return err
	}
	c.stmts[name] = stmt
	return nil
}

// Get retrieves a prepared statement
func (c *PreparedStatementCache) Get(name string) (*sql.Stmt, error) {
	stmt, ok := c.stmts[name]
	if !ok {
		return nil, fmt.Errorf("prepared statement '%s' not found", name)
	}
	return stmt, nil
}

// Close closes all prepared statements
func (c *PreparedStatementCache) Close() error {
	for _, stmt := range c.stmts {
		stmt.Close()
	}
	return nil
}

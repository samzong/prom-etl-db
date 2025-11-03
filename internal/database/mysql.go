package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/samzong/prom-etl-db/internal/models"
)

// DB represents a database connection
type DB struct {
	conn *sql.DB
}

// NewDB creates a new database connection
func NewDB(dsn string) (*DB, error) {
	conn, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Configure connection pool
	conn.SetMaxOpenConns(100)
	conn.SetMaxIdleConns(10)
	conn.SetConnMaxLifetime(time.Hour)

	// Test connection
	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return &DB{conn: conn}, nil
}

// Close closes the database connection
func (db *DB) Close() error {
	return db.conn.Close()
}

// GetConn returns the underlying *sql.DB connection
func (db *DB) GetConn() *sql.DB {
	return db.conn
}

// TestConnection tests the database connection
func (db *DB) TestConnection() error {
	return db.conn.Ping()
}

// InsertMetricRecord inserts a metric record into the database
func (db *DB) InsertMetricRecord(record *models.MetricRecord) error {
	// Convert labels to JSON
	labelsJSON, err := json.Marshal(record.Labels)
	if err != nil {
		return fmt.Errorf("failed to marshal labels: %w", err)
	}

	query := `
		INSERT INTO metrics_data 
		(query_id, metric_name, labels, value, timestamp, result_type, collected_at) 
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`

	_, err = db.conn.Exec(query,
		record.QueryID,
		record.MetricName,
		labelsJSON,
		record.Value,
		record.Timestamp,
		record.ResultType,
		record.CollectedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to insert metric record: %w", err)
	}

	return nil
}

// InsertMetricRecords inserts multiple metric records in a transaction
func (db *DB) InsertMetricRecords(records []*models.MetricRecord) error {
	if len(records) == 0 {
		return nil
	}

	// Begin transaction
	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Prepare statement
	stmt, err := tx.Prepare(`
		INSERT INTO metrics_data 
		(query_id, metric_name, labels, value, timestamp, result_type, collected_at) 
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	// Insert records
	for _, record := range records {
		// Convert labels to JSON
		labelsJSON, err := json.Marshal(record.Labels)
		if err != nil {
			return fmt.Errorf("failed to marshal labels: %w", err)
		}

		_, err = stmt.Exec(
			record.QueryID,
			record.MetricName,
			labelsJSON,
			record.Value,
			record.Timestamp,
			record.ResultType,
			record.CollectedAt,
		)
		if err != nil {
			return fmt.Errorf("failed to insert metric record: %w", err)
		}
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// InsertQueryExecution inserts a query execution record
func (db *DB) InsertQueryExecution(execution *models.QueryExecution) error {
	query := `
		INSERT INTO query_executions 
		(query_id, query_name, status, start_time, end_time, duration_ms, records_count, error_message, created_at) 
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err := db.conn.Exec(query,
		execution.QueryID,
		execution.QueryName,
		execution.Status,
		execution.StartTime,
		execution.EndTime,
		execution.DurationMs,
		execution.RecordsCount,
		execution.ErrorMessage,
		execution.CreatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to insert query execution: %w", err)
	}

	return nil
}

// GetLatestMetrics returns the latest metrics for a query
func (db *DB) GetLatestMetrics(queryID string, limit int) ([]*models.MetricRecord, error) {
	query := `
		SELECT id, query_id, metric_name, labels, value, timestamp, result_type, collected_at
		FROM metrics_data 
		WHERE query_id = ? 
		ORDER BY timestamp DESC 
		LIMIT ?
	`

	rows, err := db.conn.Query(query, queryID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query metrics: %w", err)
	}
	defer rows.Close()

	var records []*models.MetricRecord
	for rows.Next() {
		record := &models.MetricRecord{}
		var labelsJSON []byte

		err := rows.Scan(
			&record.ID,
			&record.QueryID,
			&record.MetricName,
			&labelsJSON,
			&record.Value,
			&record.Timestamp,
			&record.ResultType,
			&record.CollectedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan metric record: %w", err)
		}

		// Unmarshal labels
		if err := json.Unmarshal(labelsJSON, &record.Labels); err != nil {
			return nil, fmt.Errorf("failed to unmarshal labels: %w", err)
		}

		records = append(records, record)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return records, nil
}

// GetQueryExecutions returns query execution history
func (db *DB) GetQueryExecutions(queryID string, limit int) ([]*models.QueryExecution, error) {
	query := `
		SELECT id, query_id, query_name, status, start_time, end_time, duration_ms, records_count, error_message, created_at
		FROM query_executions 
		WHERE query_id = ? 
		ORDER BY start_time DESC 
		LIMIT ?
	`

	rows, err := db.conn.Query(query, queryID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query executions: %w", err)
	}
	defer rows.Close()

	var executions []*models.QueryExecution
	for rows.Next() {
		execution := &models.QueryExecution{}

		err := rows.Scan(
			&execution.ID,
			&execution.QueryID,
			&execution.QueryName,
			&execution.Status,
			&execution.StartTime,
			&execution.EndTime,
			&execution.DurationMs,
			&execution.RecordsCount,
			&execution.ErrorMessage,
			&execution.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan execution record: %w", err)
		}

		executions = append(executions, execution)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return executions, nil
}

// GetMetricsCount returns the count of metrics for a query
func (db *DB) GetMetricsCount(queryID string) (int64, error) {
	query := `SELECT COUNT(*) FROM metrics_data WHERE query_id = ?`

	var count int64
	err := db.conn.QueryRow(query, queryID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to get metrics count: %w", err)
	}

	return count, nil
}

// CleanupOldMetrics removes old metrics data
func (db *DB) CleanupOldMetrics(olderThan time.Time) (int64, error) {
	query := `DELETE FROM metrics_data WHERE collected_at < ?`

	result, err := db.conn.Exec(query, olderThan)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup old metrics: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected: %w", err)
	}

	return rowsAffected, nil
}

// GetDatabaseStats returns database statistics
func (db *DB) GetDatabaseStats() (map[string]interface{}, error) {
	stats := make(map[string]interface{})

	// Get connection stats
	dbStats := db.conn.Stats()
	stats["max_open_connections"] = dbStats.MaxOpenConnections
	stats["open_connections"] = dbStats.OpenConnections
	stats["in_use"] = dbStats.InUse
	stats["idle"] = dbStats.Idle

	// Get table counts
	// Use whitelist to prevent SQL injection
	allowedTables := map[string]bool{
		"metrics_data":     true,
		"query_executions": true,
	}
	tables := []string{"metrics_data", "query_executions"}
	for _, table := range tables {
		// Validate table name against whitelist
		if !allowedTables[table] {
			return nil, fmt.Errorf("table name not in whitelist: %s", table)
		}
		query := fmt.Sprintf("SELECT COUNT(*) FROM %s", table)
		var count int64
		err := db.conn.QueryRow(query).Scan(&count)
		if err != nil {
			return nil, fmt.Errorf("failed to get count for table %s: %w", table, err)
		}
		stats[table+"_count"] = count
	}

	return stats, nil
}

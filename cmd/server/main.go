package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/samzong/prom-etl-db/internal/config"
	"github.com/samzong/prom-etl-db/internal/database"
	"github.com/samzong/prom-etl-db/internal/executor"
	"github.com/samzong/prom-etl-db/internal/logger"
	"github.com/samzong/prom-etl-db/internal/models"
	"github.com/samzong/prom-etl-db/internal/prometheus"
)

// Version information (set by build flags)
var (
	version   = "dev"
	buildTime = "unknown"
	goVersion = "unknown"
)

func main() {
	// Print version information
	fmt.Printf("prom-etl-db %s (built: %s, go: %s)\n", version, buildTime, goVersion)

	// Load configuration (without queries)
	cfg, err := config.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	// Create logger
	log := logger.NewLogger(cfg.App.LogLevel)
	log.Info("Starting prom-etl-db",
		"version", version,
		"build_time", buildTime,
		"go_version", goVersion)

	// Create database connection
	dsn := config.GetMySQLDSN(&cfg.MySQL)

	db, err := database.NewDB(dsn)
	if err != nil {
		log.Error("Failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Error("Failed to close database", "error", err)
		}
	}()

	// Reload configuration with queries from database
	cfg, err = config.LoadConfigWithDB(db.GetConn())
	if err != nil {
		log.Error("Failed to load configuration from database", "error", err)
		os.Exit(1)
	}

	// Print configuration (mask sensitive data)
	printConfig(cfg)

	// Parse timeout duration
	timeoutDuration, err := time.ParseDuration(cfg.Prometheus.Timeout)
	if err != nil {
		log.Error("Failed to parse Prometheus timeout", "timeout", cfg.Prometheus.Timeout, "error", err)
		os.Exit(1)
	}

	// Create Prometheus client with logger
	promClient, err := prometheus.NewClientWithLogger(cfg.Prometheus.URL, timeoutDuration, log)
	if err != nil {
		log.Error("Failed to create Prometheus client", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := promClient.Close(); err != nil {
			log.Error("Failed to close Prometheus client", "error", err)
		}
	}()

	// Create executor
	exec := executor.NewExecutor(promClient, db, log)

	// Test connections
	testCtx, testCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer testCancel()

	if err := exec.TestConnections(testCtx); err != nil {
		log.Error("Connection test failed", "error", err)
		os.Exit(1)
	}

	// Run as long-running service
	serviceCtx := context.Background()
	if err := runService(serviceCtx, exec, cfg, log); err != nil {
		log.Error("Service execution failed", "error", err)
		os.Exit(1)
	}
}

// runService runs the application as a long-running service with scheduled queries
func runService(ctx context.Context, exec *executor.Executor, cfg *models.Config, log *slog.Logger) error {
	log.Info("Starting service mode with scheduled queries", "queries_count", len(cfg.Queries))

	// Create cron scheduler with second precision
	c := cron.New(cron.WithSeconds())

	// Schedule all queries
	for _, query := range cfg.Queries {
		if !query.Enabled {
			log.Info("Skipping disabled query", "query_id", query.ID)
			continue
		}

		// Capture query in closure
		q := query
		_, err := c.AddFunc(q.Schedule, func() {
			queryCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			log.Info("Executing scheduled query",
				"query_id", q.ID,
				"name", q.Name,
				"schedule", q.Schedule)

			if err := exec.ExecuteQuery(queryCtx, &q); err != nil {
				log.Error("Scheduled query execution failed",
					"query_id", q.ID,
					"error", err)
			} else {
				log.Info("Scheduled query executed successfully", "query_id", q.ID)
			}
		})

		if err != nil {
			return fmt.Errorf("failed to schedule query %s: %w", query.ID, err)
		}

		log.Info("Query scheduled successfully",
			"query_id", query.ID,
			"name", query.Name,
			"schedule", query.Schedule)
	}

	// Start the cron scheduler
	c.Start()
	log.Info("Cron scheduler started")

	// Run initial execution of all queries
	log.Info("Running initial query execution")
	for _, query := range cfg.Queries {
		if !query.Enabled {
			log.Info("Skipping disabled query", "query_id", query.ID)
			continue
		}

		log.Info("Executing query",
			"query_id", query.ID,
			"name", query.Name,
			"query", query.Query)

		// Create a timeout context for each query
		queryCtx, queryCancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer queryCancel()

		// Use retry mechanism if configured
		var err error
		if query.RetryCount > 0 {
			err = exec.ExecuteQueryWithRetry(queryCtx, &query)
		} else {
			err = exec.ExecuteQuery(queryCtx, &query)
		}

		if err != nil {
			log.Error("Query execution failed",
				"query_id", query.ID,
				"error", err)
		} else {
			log.Info("Query executed successfully", "query_id", query.ID)
		}
	}

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Wait for shutdown signal
	sig := <-sigChan
	log.Info("Received shutdown signal", "signal", sig)

	// Graceful shutdown
	log.Info("Shutting down cron scheduler...")
	shutdownCtx := c.Stop()

	// Wait for running jobs to complete (with timeout)
	select {
	case <-shutdownCtx.Done():
		log.Info("All scheduled jobs completed")
	case <-time.After(30 * time.Second):
		log.Warn("Timeout waiting for jobs to complete")
	}

	log.Info("Service shutdown completed")
	return nil
}

// printConfig prints the configuration (masking sensitive data)
func printConfig(cfg *models.Config) {
	fmt.Println("=== Configuration ===")
	fmt.Printf("Prometheus URL: %s\n", cfg.Prometheus.URL)
	fmt.Printf("Prometheus Timeout: %s\n", cfg.Prometheus.Timeout)
	fmt.Printf("MySQL Host: %s:%d\n", cfg.MySQL.Host, cfg.MySQL.Port)
	fmt.Printf("MySQL Database: %s\n", cfg.MySQL.Database)
	fmt.Printf("MySQL Username: %s\n", cfg.MySQL.Username)
	fmt.Printf("MySQL Password: %s\n", maskPassword(cfg.MySQL.Password))
	fmt.Printf("Log Level: %s\n", cfg.App.LogLevel)
	fmt.Printf("HTTP Port: %d\n", cfg.App.HTTPPort)
	fmt.Printf("Worker Pool: %d\n", cfg.App.WorkerPool)
	fmt.Printf("Queries Count: %d\n", len(cfg.Queries))
	fmt.Println("=====================")
}

// maskPassword masks the password for logging
func maskPassword(password string) string {
	if len(password) <= 2 {
		return "****"
	}
	return password[:2] + "****"
}

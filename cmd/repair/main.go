package main

import (
	"bufio"
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/samzong/prom-etl-db/internal/config"
	"github.com/samzong/prom-etl-db/internal/database"
	"github.com/samzong/prom-etl-db/internal/logger"
	"github.com/samzong/prom-etl-db/internal/models"
	"github.com/samzong/prom-etl-db/internal/prometheus"
	"github.com/samzong/prom-etl-db/internal/timeparser"
)

func main() {
	// Load .env file if it exists
	loadEnvFile(".env")

	// Parse command line flags
	var queryID string
	var forceRecompute bool
	var dryRun bool
	var skipIfNoData bool

	flag.StringVar(&queryID, "query-id", "", "Query ID from database (required)")
	flag.BoolVar(&forceRecompute, "force-recompute", false, "Force recompute even if data exists (will delete old data)")
	flag.BoolVar(&dryRun, "dry-run", false, "Preview mode: show what would be done without actually doing it")
	flag.BoolVar(&skipIfNoData, "skip-if-no-data", true, "Skip deletion if Prometheus has no data (keep old data)")
	flag.Parse()

	// Validate required parameter
	if queryID == "" {
		log.Fatalf("Error: --query-id is required\n\nUsage:\n  repair --query-id <query_id> <days>\n  repair --query-id <query_id> <start_date> <end_date>\n\nExamples:\n  repair --query-id gpu_utilization_daily 30\n  repair --query-id gpu_utilization_daily 2024-01-01 2025-01-31")
	}

	// Parse position arguments (days or date range)
	var startDate, endDate time.Time
	var err error

	now := time.Now()
	yesterday := now.AddDate(0, 0, -1)

	args := flag.Args()
	if len(args) == 0 {
		// No days or date range specified, prompt error
		log.Fatalf("Error: Please specify days or date range\n\nUsage:\n  repair --query-id <query_id> <days>\n  repair --query-id <query_id> <start_date> <end_date>\n\nExamples:\n  repair --query-id gpu_utilization_daily 30\n  repair --query-id gpu_utilization_daily 2024-01-01 2025-01-31")
	} else if len(args) >= 2 {
		// Manual date range specified
		startDateStr := args[0]
		endDateStr := args[1]

		startDate, err = time.Parse("2006-01-02", startDateStr)
		if err != nil {
			log.Fatalf("Failed to parse start date: %v", err)
		}

		endDate, err = time.Parse("2006-01-02", endDateStr)
		if err != nil {
			log.Fatalf("Failed to parse end date: %v", err)
		}

		if startDate.After(endDate) {
			log.Fatalf("Start date must be before end date")
		}
	} else if len(args) == 1 {
		// Days specified
		days, err := strconv.Atoi(args[0])
		if err != nil {
			log.Fatalf("Failed to parse days: %v. Usage: repair --query-id <id> <days> or repair --query-id <id> <start_date> <end_date>", err)
		}
		if days <= 0 {
			log.Fatalf("Days must be greater than 0")
		}
		endDate = yesterday
		startDate = yesterday.AddDate(0, 0, -days+1)
	}

	// Print configuration
	fmt.Printf("Data Repair Tool\n")
	fmt.Printf("================\n")
	fmt.Printf("Query ID: %s\n", queryID)
	fmt.Printf("Start Date: %s\n", startDate.Format("2006-01-02"))
	fmt.Printf("End Date: %s\n", endDate.Format("2006-01-02"))
	if len(args) == 1 {
		days := int(endDate.Sub(startDate).Hours()/24) + 1
		fmt.Printf("Days: %d\n", days)
	}
	fmt.Printf("Force Recompute: %v\n", forceRecompute)
	fmt.Printf("Dry Run: %v\n", dryRun)
	fmt.Printf("Skip If No Data: %v\n", skipIfNoData)
	fmt.Println()

	if dryRun {
		fmt.Println("⚠️  DRY RUN MODE: No changes will be made")
		fmt.Println()
	}

	// Load configuration
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Check if Prometheus URL is using default value
	if cfg.Prometheus.URL == "http://localhost:9090" {
		fmt.Println("\n⚠️  Warning: Prometheus URL is using default value (localhost:9090)")
		fmt.Println("Please set PROMETHEUS_URL environment variable or create .env file")
		os.Exit(1)
	}

	// Create logger
	logger := logger.NewLogger(cfg.App.LogLevel)
	logger.Info("Starting data repair",
		"query_id", queryID,
		"start_date", startDate.Format("2006-01-02"),
		"end_date", endDate.Format("2006-01-02"),
		"force_recompute", forceRecompute,
		"dry_run", dryRun,
		"skip_if_no_data", skipIfNoData)

	// Create database connection
	dsn := config.GetMySQLDSN(&cfg.MySQL)
	db, err := database.NewDB(dsn)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Load query configuration from database
	queryConfig, err := loadQueryConfigFromDB(db, queryID)
	if err != nil {
		log.Fatalf("Failed to load query config: %v", err)
	}

	fmt.Printf("Query: %s\n", queryConfig.Query)
	fmt.Printf("Name: %s\n", queryConfig.Name)
	fmt.Println()

	// Parse timeout duration
	timeoutDuration, err := time.ParseDuration(cfg.Prometheus.Timeout)
	if err != nil {
		log.Fatalf("Failed to parse Prometheus timeout: %v", err)
	}

	// Create Prometheus client
	promClient, err := prometheus.NewClientWithLogger(cfg.Prometheus.URL, timeoutDuration, logger)
	if err != nil {
		log.Fatalf("Failed to create Prometheus client: %v", err)
	}
	defer promClient.Close()

	// Create time resolver for calculating collected_at
	timeResolver := timeparser.NewRelativeTimeParser(time.Now())

	// Process each day
	currentDate := startDate
	totalProcessed := 0
	totalFailed := 0
	totalSkipped := 0
	totalDeleted := 0
	totalNoData := 0
	datesWithData := []string{}
	datesWithoutData := []string{}

	for !currentDate.After(endDate) {
		// Calculate yesterday_end for this date
		yesterdayEnd := time.Date(
			currentDate.Year(),
			currentDate.Month(),
			currentDate.Day(),
			23, 59, 59, 0,
			currentDate.Location(),
		)

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)

		logger.Info("Processing date",
			"date", currentDate.Format("2006-01-02"),
			"query_time", yesterdayEnd.Format(time.RFC3339))

		// Query Prometheus for this specific date
		response, err := promClient.QueryInstantWithTime(ctx, queryConfig.Query, yesterdayEnd)
		if err != nil {
			logger.Error("Failed to query Prometheus",
				"date", currentDate.Format("2006-01-02"),
				"error", err)

			if isRetentionError(err) {
				datesWithoutData = append(datesWithoutData, currentDate.Format("2006-01-02"))
				totalNoData++

				if dryRun {
					if forceRecompute {
						if skipIfNoData {
							fmt.Printf("  [DRY RUN] Would skip: %s (Prometheus has no data, would keep old data)\n", currentDate.Format("2006-01-02"))
							totalSkipped++
						} else {
							fmt.Printf("  [DRY RUN] Would delete: %s (Prometheus has no data, would delete old data)\n", currentDate.Format("2006-01-02"))
							totalDeleted++
						}
					} else {
						fmt.Printf("  [DRY RUN] Would skip: %s (Prometheus has no data)\n", currentDate.Format("2006-01-02"))
						totalSkipped++
					}
				} else {
					if forceRecompute {
						if skipIfNoData {
							logger.Info("Skipping: Prometheus has no data, keeping old data",
								"date", currentDate.Format("2006-01-02"))
							totalSkipped++
						} else {
							deletedCount, delErr := db.DeleteMetricsByDate(queryID, currentDate)
							if delErr != nil {
								logger.Error("Failed to delete old data", "error", delErr)
								totalFailed++
							} else {
								logger.Info("Deleted old data, no new data available",
									"date", currentDate.Format("2006-01-02"),
									"deleted_count", deletedCount)
								totalDeleted++
							}
						}
					} else {
						logger.Info("Skipping: Prometheus has no data",
							"date", currentDate.Format("2006-01-02"))
						totalSkipped++
					}
				}
			} else {
				totalFailed++
			}

			cancel()
			currentDate = currentDate.AddDate(0, 0, 1)
			continue
		}

		// Parse result
		var metricRecords []*models.MetricRecord
		if response.Data.ResultType == "vector" {
			vectorResult, err := response.ParseVectorResult()
			if err != nil {
				logger.Error("Failed to parse vector result",
					"date", currentDate.Format("2006-01-02"),
					"error", err)
				totalFailed++
				cancel()
				currentDate = currentDate.AddDate(0, 0, 1)
				continue
			}

			// Convert vector samples to metric records
			for _, sample := range vectorResult {
				record, err := convertSampleToRecord(&sample, queryConfig.ID, yesterdayEnd, queryConfig.TimeRange, timeResolver, currentDate)
				if err != nil {
					logger.Warn("Failed to convert sample to record, skipping",
						"error", err)
					continue
				}
				metricRecords = append(metricRecords, record)
			}
		}

		if len(metricRecords) == 0 {
			datesWithoutData = append(datesWithoutData, currentDate.Format("2006-01-02"))
			totalNoData++

			if dryRun {
				if forceRecompute {
					if skipIfNoData {
						fmt.Printf("  [DRY RUN] Would skip: %s (Prometheus returned no data, would keep old data)\n", currentDate.Format("2006-01-02"))
						totalSkipped++
					} else {
						fmt.Printf("  [DRY RUN] Would delete: %s (Prometheus returned no data, would delete old data)\n", currentDate.Format("2006-01-02"))
						totalDeleted++
					}
				} else {
					fmt.Printf("  [DRY RUN] Would skip: %s (Prometheus returned no data)\n", currentDate.Format("2006-01-02"))
					totalSkipped++
				}
			} else {
				if forceRecompute {
					if skipIfNoData {
						logger.Info("Skipping: Prometheus returned no data, keeping old data",
							"date", currentDate.Format("2006-01-02"))
						totalSkipped++
					} else {
						deletedCount, delErr := db.DeleteMetricsByDate(queryID, currentDate)
						if delErr != nil {
							logger.Error("Failed to delete old data", "error", delErr)
							totalFailed++
						} else {
							logger.Info("Deleted old data, no new data available",
								"date", currentDate.Format("2006-01-02"),
								"deleted_count", deletedCount)
							totalDeleted++
						}
					}
				} else {
					logger.Info("Skipping: Prometheus returned no data",
						"date", currentDate.Format("2006-01-02"))
					totalSkipped++
				}
			}
		} else {
			datesWithData = append(datesWithData, currentDate.Format("2006-01-02"))

			// Check if data already exists for this date
			existingCount, err := checkExistingData(db, queryID, currentDate)
			if err != nil {
				logger.Warn("Failed to check existing data",
					"date", currentDate.Format("2006-01-02"),
					"error", err)
			}

			if existingCount > 0 && !forceRecompute {
				if dryRun {
					fmt.Printf("  [DRY RUN] Would skip: %s (data already exists: %d records)\n",
						currentDate.Format("2006-01-02"), existingCount)
				} else {
					logger.Info("Data already exists, skipping",
						"date", currentDate.Format("2006-01-02"),
						"existing_count", existingCount,
						"new_count", len(metricRecords))
				}
				totalSkipped++
			} else {
				if dryRun {
					if forceRecompute && existingCount > 0 {
						fmt.Printf("  [DRY RUN] Would delete: %s (%d old records)\n",
							currentDate.Format("2006-01-02"), existingCount)
					}
					fmt.Printf("  [DRY RUN] Would insert: %s (%d new records)\n",
						currentDate.Format("2006-01-02"), len(metricRecords))
					totalProcessed++
				} else {
					// Delete old data if force recompute
					if forceRecompute && existingCount > 0 {
						deletedCount, delErr := db.DeleteMetricsByDate(queryID, currentDate)
						if delErr != nil {
							logger.Error("Failed to delete old data", "error", delErr)
							totalFailed++
							cancel()
							currentDate = currentDate.AddDate(0, 0, 1)
							continue
						}
						logger.Info("Deleted old data",
							"date", currentDate.Format("2006-01-02"),
							"deleted_count", deletedCount)
					}

					// Store metric records
					if err := db.InsertMetricRecords(metricRecords); err != nil {
						logger.Error("Failed to store metric records",
							"date", currentDate.Format("2006-01-02"),
							"error", err)
						totalFailed++
					} else {
						logger.Info("Successfully stored metric records",
							"date", currentDate.Format("2006-01-02"),
							"count", len(metricRecords))
						totalProcessed++
					}
				}
			}
		}

		cancel()
		currentDate = currentDate.AddDate(0, 0, 1)

		// Small delay to avoid overwhelming Prometheus
		time.Sleep(100 * time.Millisecond)
	}

	// Print summary
	fmt.Println()
	fmt.Println("Repair Summary:")
	fmt.Printf("  Processed: %d days\n", totalProcessed)
	fmt.Printf("  Skipped: %d days\n", totalSkipped)
	fmt.Printf("  Failed: %d days\n", totalFailed)
	if forceRecompute {
		fmt.Printf("  Deleted (no new data): %d days\n", totalDeleted)
	}
	fmt.Printf("  Prometheus has data: %d days\n", len(datesWithData))
	fmt.Printf("  Prometheus no data: %d days\n", len(datesWithoutData))

	if dryRun {
		fmt.Println()
		fmt.Println("⚠️  DRY RUN: No changes were made")
	} else if len(datesWithoutData) > 0 && forceRecompute && !skipIfNoData {
		fmt.Println()
		fmt.Println("⚠️  Warning: Some dates had no data in Prometheus, old data was deleted")
		fmt.Printf("   Dates without data: %s\n", strings.Join(datesWithoutData[:min(10, len(datesWithoutData))], ", "))
		if len(datesWithoutData) > 10 {
			fmt.Printf("   ... and %d more dates\n", len(datesWithoutData)-10)
		}
	}

	logger.Info("Data repair completed",
		"total_processed", totalProcessed,
		"total_skipped", totalSkipped,
		"total_failed", totalFailed,
		"total_deleted", totalDeleted)
}

// loadQueryConfigFromDB loads query configuration from database
func loadQueryConfigFromDB(db *database.DB, queryID string) (*models.QueryConfig, error) {
	query := `
		SELECT query_id, name, description, query, schedule, timeout,
		       enabled, retry_count, retry_interval,
		       time_range_type, time_range_time
		FROM query_configs
		WHERE query_id = ? AND enabled = 1
	`

	var config models.QueryConfig
	var retryInterval string
	var timeRangeType, timeRangeTime sql.NullString

	err := db.GetConn().QueryRow(query, queryID).Scan(
		&config.ID,
		&config.Name,
		&config.Description,
		&config.Query,
		&config.Schedule,
		&config.Timeout,
		&config.Enabled,
		&config.RetryCount,
		&retryInterval,
		&timeRangeType,
		&timeRangeTime,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("query config not found: %s", queryID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query config: %w", err)
	}

	config.RetryInterval = retryInterval

	if timeRangeType.Valid && timeRangeType.String != "" {
		config.TimeRange = &models.TimeRangeConfig{
			Type: timeRangeType.String,
		}
		if timeRangeTime.Valid {
			config.TimeRange.Time = timeRangeTime.String
		}
	}

	return &config, nil
}

// convertSampleToRecord converts a VectorSample to MetricRecord
func convertSampleToRecord(sample *models.VectorSample, queryID string, queryTime time.Time, timeRange *models.TimeRangeConfig, timeResolver timeparser.TimeResolver, currentDate time.Time) (*models.MetricRecord, error) {
	// Extract metric name
	metricName := sample.Metric["__name__"]
	if metricName == "" {
		metricName = queryID
	}

	// Parse timestamp and value
	if len(sample.Value) != 2 {
		return nil, fmt.Errorf("invalid sample value format")
	}

	timestamp, ok := sample.Value[0].(float64)
	if !ok {
		return nil, fmt.Errorf("invalid timestamp format")
	}

	valueStr, ok := sample.Value[1].(string)
	if !ok {
		return nil, fmt.Errorf("invalid value format")
	}

	// Convert string value to float64
	value, err := strconv.ParseFloat(valueStr, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse value: %w", err)
	}

	// Clean labels (remove internal labels)
	labels := make(map[string]interface{})
	for k, v := range sample.Metric {
		if k != "__name__" {
			labels[k] = v
		}
	}

	// Determine result type
	resultType := "instant"
	if timeRange != nil && timeRange.Type == "range" {
		resultType = "range"
	}

	// Calculate collected_at based on query configuration
	dataPointTime := time.Unix(int64(timestamp), 0)
	collectedAt := calculateCollectedAt(dataPointTime, timeRange, timeResolver, currentDate)

	return &models.MetricRecord{
		QueryID:     queryID,
		MetricName:  metricName,
		Labels:      labels,
		Value:       value,
		Timestamp:   dataPointTime,
		ResultType:  resultType,
		CollectedAt: collectedAt,
	}, nil
}

// calculateCollectedAt calculates the collected_at timestamp based on query configuration
// For historical data repair, currentDate should be the target date being processed,
// not the current system time. This ensures "yesterday_end" resolves correctly.
func calculateCollectedAt(dataPointTime time.Time, timeRange *models.TimeRangeConfig, timeResolver timeparser.TimeResolver, currentDate time.Time) time.Time {
	if timeRange == nil {
		// No time range config, use data point's day start
		dataDate := time.Date(dataPointTime.Year(), dataPointTime.Month(), dataPointTime.Day(), 0, 0, 0, 0, dataPointTime.Location())
		return dataDate
	}

	// Update time resolver to use currentDate + 1 day as "now" for historical data repair
	// This ensures that "yesterday_end" resolves to currentDate, not the actual yesterday
	if relativeParser, ok := timeResolver.(*timeparser.RelativeTimeParser); ok {
		relativeParser.UpdateNow(currentDate.Add(24 * time.Hour))
	}

	if timeRange.Type == "instant" {
		// For instant queries, check if querying yesterday's data
		if timeRange.Time == "yesterday" || timeRange.Time == "yesterday_end" {
			queryTime, err := timeResolver.ResolveTime(timeRange.Time)
			if err == nil {
				// Use yesterday's start time (which should be currentDate for historical repair)
				yesterdayStart := time.Date(queryTime.Year(), queryTime.Month(), queryTime.Day(), 0, 0, 0, 0, queryTime.Location())
				return yesterdayStart
			}
		}
		// For other instant queries, use the data point's day start
		dataDate := time.Date(dataPointTime.Year(), dataPointTime.Month(), dataPointTime.Day(), 0, 0, 0, 0, dataPointTime.Location())
		return dataDate
	}

	// Default: use data point's day start
	dataDate := time.Date(dataPointTime.Year(), dataPointTime.Month(), dataPointTime.Day(), 0, 0, 0, 0, dataPointTime.Location())
	return dataDate
}

// checkExistingData checks if data already exists for a specific date
func checkExistingData(db *database.DB, queryID string, date time.Time) (int64, error) {
	startOfDay := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, date.Location())
	endOfDay := startOfDay.Add(24 * time.Hour)

	query := `
		SELECT COUNT(*) 
		FROM metrics_data 
		WHERE query_id = ? 
		AND collected_at >= ? 
		AND collected_at < ?
	`

	var count int64
	err := db.GetConn().QueryRow(query, queryID, startOfDay, endOfDay).Scan(&count)
	if err != nil {
		return 0, err
	}

	return count, nil
}

// isRetentionError checks if error is due to data retention
// Note: This function uses string matching against error messages, which is fragile.
// The Prometheus client library doesn't provide typed errors for retention issues.
// Expected error patterns: "out of bounds", "too old", "retention".
// If Prometheus client library changes error messages in future versions, this logic may need updates.
func isRetentionError(err error) bool {
	errMsg := err.Error()
	return strings.Contains(errMsg, "out of bounds") ||
		strings.Contains(errMsg, "too old") ||
		strings.Contains(errMsg, "retention")
}

// loadEnvFile loads environment variables from a .env file
func loadEnvFile(filename string) {
	file, err := os.Open(filename)
	if err != nil {
		// .env file doesn't exist, that's okay
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Parse KEY=VALUE format
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		// Remove quotes if present
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}

		// Set environment variable if not already set
		if os.Getenv(key) == "" {
			os.Setenv(key, value)
		}
	}
}

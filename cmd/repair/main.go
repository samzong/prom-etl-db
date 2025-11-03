package main

import (
	"bufio"
	"context"
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
)

func main() {
	// Load .env file if it exists
	loadEnvFile(".env")

	var startDate, endDate time.Time
	var err error

	now := time.Now()
	// Default: query last 90 days from yesterday
	yesterday := now.AddDate(0, 0, -1)
	defaultDays := 90

	if len(os.Args) >= 3 {
		// Manual date range specified
		startDateStr := os.Args[1]
		endDateStr := os.Args[2]

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
	} else if len(os.Args) == 2 {
		// Days specified
		days, err := strconv.Atoi(os.Args[1])
		if err != nil {
			log.Fatalf("Failed to parse days: %v. Usage: repair [days] or repair <start_date> <end_date>", err)
		}
		endDate = yesterday
		startDate = yesterday.AddDate(0, 0, -days+1)
		defaultDays = days
	} else {
		// Default: last 90 days
		endDate = yesterday
		startDate = yesterday.AddDate(0, 0, -defaultDays+1)
	}

	fmt.Printf("Data Repair Tool\n")
	fmt.Printf("================\n")
	fmt.Printf("Start Date: %s (last %d days from yesterday)\n", startDate.Format("2006-01-02"), defaultDays)
	fmt.Printf("End Date: %s (yesterday)\n", endDate.Format("2006-01-02"))
	fmt.Printf("Query ID: gpu_utilization_daily\n")
	fmt.Printf("\nUsage:\n")
	fmt.Printf("  repair                    # Query last 90 days (default)\n")
	fmt.Printf("  repair 30                 # Query last 30 days\n")
	fmt.Printf("  repair 2025-07-24 2025-11-03  # Query specific date range\n")
	fmt.Println()

	// Load configuration
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Check if Prometheus URL is using default value
	if cfg.Prometheus.URL == "http://localhost:9090" {
		fmt.Println("\n⚠️  Warning: Prometheus URL is using default value (localhost:9090)")
		fmt.Println("Please set PROMETHEUS_URL environment variable or create .env file")
		fmt.Println("\nExample:")
		fmt.Println("  export PROMETHEUS_URL=http://10.20.100.200:30588/select/0/prometheus")
		fmt.Println("  ./build/repair")
		fmt.Println("\nOr create .env file based on env.example and run:")
		fmt.Println("  source .env  # or export $(cat .env | grep -v '^#' | xargs)")
		fmt.Println("  ./build/repair")
		fmt.Println()
		os.Exit(1)
	}

	// Create logger
	logger := logger.NewLogger(cfg.App.LogLevel)
	logger.Info("Starting data repair",
		"start_date", startDate.Format("2006-01-02"),
		"end_date", endDate.Format("2006-01-02"))

	// Create database connection
	dsn := config.GetMySQLDSN(&cfg.MySQL)
	db, err := database.NewDB(dsn)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

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

	// Create query configuration
	queryConfig := &models.QueryConfig{
		ID:          "gpu_utilization_daily",
		Name:        "GPU每日利用率统计",
		Query:       "sum(sum_over_time(max without(exported_namespace, exported_pod, modelName, prometheus, cluster, insight, mode) (kpanda_gpu_pod_utilization != bool 999999)[24h:1m])) by (cluster_name, node, UUID) * 60 / 3600",
		TimeRange:   nil, // Will be set per day
	}

	// Process each day
	currentDate := startDate
	totalProcessed := 0
	totalFailed := 0

	for !currentDate.After(endDate) {
		// Calculate yesterday_end for this date
		yesterdayEnd := time.Date(
			currentDate.Year(),
			currentDate.Month(),
			currentDate.Day(),
			23, 59, 59, 0,
			currentDate.Location(),
		)

		// Set time range for this query
		queryConfig.TimeRange = &models.TimeRangeConfig{
			Type: "instant",
			Time: "", // Will be set to specific time
		}

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
			totalFailed++
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
				record, err := convertSampleToRecord(&sample, queryConfig.ID, yesterdayEnd)
				if err != nil {
					logger.Warn("Failed to convert sample to record, skipping",
						"error", err)
					continue
				}
				metricRecords = append(metricRecords, record)
			}
		}

		// Check if data already exists for this date
		existingCount, err := checkExistingData(db, queryConfig.ID, currentDate)
		if err != nil {
			logger.Warn("Failed to check existing data",
				"date", currentDate.Format("2006-01-02"),
				"error", err)
		}

		if existingCount > 0 {
			logger.Info("Data already exists, skipping",
				"date", currentDate.Format("2006-01-02"),
				"existing_count", existingCount,
				"new_count", len(metricRecords))
		} else {
			// Store metric records
			if len(metricRecords) > 0 {
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
			} else {
				logger.Warn("No data found for date",
					"date", currentDate.Format("2006-01-02"))
			}
		}

		cancel()
		currentDate = currentDate.AddDate(0, 0, 1)

		// Small delay to avoid overwhelming Prometheus
		time.Sleep(100 * time.Millisecond)
	}

	logger.Info("Data repair completed",
		"total_processed", totalProcessed,
		"total_failed", totalFailed)
	fmt.Printf("\nRepair Summary:\n")
	fmt.Printf("  Processed: %d days\n", totalProcessed)
	fmt.Printf("  Failed: %d days\n", totalFailed)
}

// convertSampleToRecord converts a VectorSample to MetricRecord
func convertSampleToRecord(sample *models.VectorSample, queryID string, queryTime time.Time) (*models.MetricRecord, error) {
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

	return &models.MetricRecord{
		QueryID:     queryID,
		MetricName:  metricName,
		Labels:      labels,
		Value:       value,
		Timestamp:   time.Unix(int64(timestamp), 0),
		ResultType:  "instant",
		CollectedAt: time.Unix(int64(timestamp), 0), // Use Prometheus timestamp as collected_at for proper date grouping
	}, nil
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


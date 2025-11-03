package executor

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/samzong/prom-etl-db/internal/database"
	"github.com/samzong/prom-etl-db/internal/logger"
	"github.com/samzong/prom-etl-db/internal/models"
	"github.com/samzong/prom-etl-db/internal/prometheus"
	"github.com/samzong/prom-etl-db/internal/timeparser"
)

// Executor handles query execution and data storage
type Executor struct {
	promClient  *prometheus.Client
	db          *database.DB
	logger      *slog.Logger
	timeResolver timeparser.TimeResolver
}

// NewExecutor creates a new query executor
func NewExecutor(promClient *prometheus.Client, db *database.DB, baseLogger *slog.Logger) *Executor {
	return &Executor{
		promClient:   promClient,
		db:           db,
		logger:       logger.WithComponent(baseLogger, "executor"),
		timeResolver: timeparser.NewRelativeTimeParser(time.Now()),
	}
}

// ExecuteQuery executes a single query and stores the results
func (e *Executor) ExecuteQuery(ctx context.Context, queryConfig *models.QueryConfig) error {
	startTime := time.Now()
	queryLogger := logger.WithQueryID(e.logger, queryConfig.ID)

	// Create query execution record
	execution := &models.QueryExecution{
		QueryID:   queryConfig.ID,
		QueryName: queryConfig.Name,
		Status:    "running",
		StartTime: startTime,
		CreatedAt: startTime,
	}

	queryLogger.Info("Starting query execution",
		"query", queryConfig.Query,
		"name", queryConfig.Name,
	)

	// Execute Prometheus query based on time range configuration
	var response *models.PrometheusResponse
	var err error

	if queryConfig.TimeRange != nil {
		// Use time range configuration
		response, err = e.promClient.QueryWithTimeRange(ctx, queryConfig.Query, queryConfig.TimeRange)
		queryLogger.Info("Executing query with time range",
			"type", queryConfig.TimeRange.Type,
			"time", queryConfig.TimeRange.Time,
			"start", queryConfig.TimeRange.Start,
			"end", queryConfig.TimeRange.End,
		)
	} else {
		// Use default instant query
		response, err = e.promClient.QueryInstant(ctx, queryConfig.Query)
		queryLogger.Info("Executing instant query with current time")
	}

	if err != nil {
		// Record failure
		execution.Status = "failed"
		endTime := time.Now()
		execution.EndTime = &endTime
		duration := endTime.Sub(startTime).Milliseconds()
		execution.DurationMs = &duration
		errorMsg := err.Error()
		execution.ErrorMessage = &errorMsg

		// Log error
		logger.WithError(queryLogger, err).Error("Query execution failed")

		// Store execution record
		if dbErr := e.db.InsertQueryExecution(execution); dbErr != nil {
			logger.WithError(queryLogger, dbErr).Error("Failed to store execution record")
		}

		return fmt.Errorf("failed to execute query: %w", err)
	}

	// Parse result based on result type
	var metricRecords []*models.MetricRecord

	switch response.Data.ResultType {
	case "vector":
		// Parse vector result (instant queries)
		vectorResult, err := response.ParseVectorResult()
		if err != nil {
			// Record failure
			execution.Status = "failed"
			endTime := time.Now()
			execution.EndTime = &endTime
			duration := endTime.Sub(startTime).Milliseconds()
			execution.DurationMs = &duration
			errorMsg := err.Error()
			execution.ErrorMessage = &errorMsg

			logger.WithError(queryLogger, err).Error("Failed to parse vector result")

			// Store execution record
			if dbErr := e.db.InsertQueryExecution(execution); dbErr != nil {
				logger.WithError(queryLogger, dbErr).Error("Failed to store execution record")
			}

			return fmt.Errorf("failed to parse vector result: %w", err)
		}

		// Convert vector samples to metric records
		for _, sample := range vectorResult {
			record, err := e.convertSampleToRecord(&sample, queryConfig.ID, queryConfig.TimeRange)
			if err != nil {
				logger.WithError(queryLogger, err).Warn("Failed to convert sample to record, skipping")
				continue
			}
			metricRecords = append(metricRecords, record)
		}

	case "matrix":
		// Parse matrix result (range queries)
		matrixResult, err := response.ParseMatrixResult()
		if err != nil {
			// Record failure
			execution.Status = "failed"
			endTime := time.Now()
			execution.EndTime = &endTime
			duration := endTime.Sub(startTime).Milliseconds()
			execution.DurationMs = &duration
			errorMsg := err.Error()
			execution.ErrorMessage = &errorMsg

			logger.WithError(queryLogger, err).Error("Failed to parse matrix result")

			// Store execution record
			if dbErr := e.db.InsertQueryExecution(execution); dbErr != nil {
				logger.WithError(queryLogger, dbErr).Error("Failed to store execution record")
			}

			return fmt.Errorf("failed to parse matrix result: %w", err)
		}

		// Convert matrix samples to metric records
		for _, matrixSample := range matrixResult {
			records, err := e.convertMatrixSampleToRecords(&matrixSample, queryConfig.ID, queryConfig.TimeRange)
			if err != nil {
				logger.WithError(queryLogger, err).Warn("Failed to convert matrix sample to records, skipping")
				continue
			}
			metricRecords = append(metricRecords, records...)
		}

	default:
		// Record failure
		execution.Status = "failed"
		endTime := time.Now()
		execution.EndTime = &endTime
		duration := endTime.Sub(startTime).Milliseconds()
		execution.DurationMs = &duration
		errorMsg := fmt.Sprintf("unsupported result type: %s", response.Data.ResultType)
		execution.ErrorMessage = &errorMsg

		queryLogger.Error("Unsupported result type", "error", fmt.Errorf(errorMsg))

		// Store execution record
		if dbErr := e.db.InsertQueryExecution(execution); dbErr != nil {
			logger.WithError(queryLogger, dbErr).Error("Failed to store execution record")
		}

		return fmt.Errorf("unsupported result type: %s", response.Data.ResultType)
	}

	// Store metric records
	if len(metricRecords) > 0 {
		if err := e.db.InsertMetricRecords(metricRecords); err != nil {
			// Record failure
			execution.Status = "failed"
			endTime := time.Now()
			execution.EndTime = &endTime
			duration := endTime.Sub(startTime).Milliseconds()
			execution.DurationMs = &duration
			errorMsg := err.Error()
			execution.ErrorMessage = &errorMsg

			logger.WithError(queryLogger, err).Error("Failed to store metric records")

			// Store execution record
			if dbErr := e.db.InsertQueryExecution(execution); dbErr != nil {
				logger.WithError(queryLogger, dbErr).Error("Failed to store execution record")
			}

			return fmt.Errorf("failed to store metric records: %w", err)
		}
	}

	// Record success
	execution.Status = "success"
	endTime := time.Now()
	execution.EndTime = &endTime
	duration := endTime.Sub(startTime).Milliseconds()
	execution.DurationMs = &duration
	execution.RecordsCount = len(metricRecords)

	// Store execution record
	if err := e.db.InsertQueryExecution(execution); err != nil {
		logger.WithError(queryLogger, err).Error("Failed to store execution record")
	}

	// Log success
	logger.WithDuration(
		logger.WithCount(queryLogger, len(metricRecords)),
		duration,
	).Info("Query execution completed successfully")

	return nil
}

// convertSampleToRecord converts a VectorSample to MetricRecord
func (e *Executor) convertSampleToRecord(sample *models.VectorSample, queryID string, timeRange *models.TimeRangeConfig) (*models.MetricRecord, error) {
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
	collectedAt := e.calculateCollectedAt(time.Unix(int64(timestamp), 0), timeRange)

	return &models.MetricRecord{
		QueryID:     queryID,
		MetricName:  metricName,
		Labels:      labels,
		Value:       value,
		Timestamp:   time.Unix(int64(timestamp), 0),
		ResultType:  resultType,
		CollectedAt: collectedAt,
	}, nil
}

// convertMatrixSampleToRecords converts a MatrixSample to multiple MetricRecords
func (e *Executor) convertMatrixSampleToRecords(matrixSample *models.MatrixSample, queryID string, timeRange *models.TimeRangeConfig) ([]*models.MetricRecord, error) {
	// Extract metric name
	metricName := matrixSample.Metric["__name__"]
	if metricName == "" {
		metricName = queryID
	}

	// Clean labels (remove internal labels)
	labels := make(map[string]interface{})
	for k, v := range matrixSample.Metric {
		if k != "__name__" {
			labels[k] = v
		}
	}

	var records []*models.MetricRecord

	// Process each value in the matrix
	for _, valueArray := range matrixSample.Values {
		// Each valueArray should contain [timestamp, value]
		if len(valueArray) != 2 {
			e.logger.Warn("Invalid matrix value format, skipping",
				"query_id", queryID,
				"value_length", len(valueArray),
			)
			continue
		}

		timestamp, ok := valueArray[0].(float64)
		if !ok {
			e.logger.Warn("Invalid timestamp format in matrix, skipping",
				"query_id", queryID,
				"timestamp_type", fmt.Sprintf("%T", valueArray[0]),
			)
			continue
		}

		valueStr, ok := valueArray[1].(string)
		if !ok {
			e.logger.Warn("Invalid value format in matrix, skipping",
				"query_id", queryID,
				"value_type", fmt.Sprintf("%T", valueArray[1]),
			)
			continue
		}

		// Convert string value to float64
		value, err := strconv.ParseFloat(valueStr, 64)
		if err != nil {
			e.logger.Warn("Failed to parse value in matrix, skipping",
				"query_id", queryID,
				"value", valueStr,
				"error", err,
			)
			continue
		}

		// Calculate collected_at based on query configuration
		dataPointTime := time.Unix(int64(timestamp), 0)
		collectedAt := e.calculateCollectedAt(dataPointTime, timeRange)

		// Create metric record
		record := &models.MetricRecord{
			QueryID:     queryID,
			MetricName:  metricName,
			Labels:      labels,
			Value:       value,
			Timestamp:   dataPointTime,
			ResultType:  "range",
			CollectedAt: collectedAt,
		}

		records = append(records, record)
	}

	return records, nil
}

// ExecuteQueryWithRetry executes a query with retry logic
func (e *Executor) ExecuteQueryWithRetry(ctx context.Context, queryConfig *models.QueryConfig) error {
	var lastErr error

	for attempt := 0; attempt <= queryConfig.RetryCount; attempt++ {
		if attempt > 0 {
			// Parse retry interval
			retryInterval, err := time.ParseDuration(queryConfig.RetryInterval)
			if err != nil {
				retryInterval = 5 * time.Second
			}

			e.logger.Info("Retrying query execution",
				"query_id", queryConfig.ID,
				"attempt", attempt,
				"retry_interval", retryInterval,
			)

			// Wait before retry
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(retryInterval):
			}
		}

		// Execute query
		if err := e.ExecuteQuery(ctx, queryConfig); err != nil {
			lastErr = err
			continue
		}

		// Success
		return nil
	}

	return fmt.Errorf("query failed after %d attempts: %w", queryConfig.RetryCount+1, lastErr)
}

// calculateCollectedAt calculates the collected_at timestamp based on query configuration
// For range queries, if the query time range is within the same day, use that day's start time
// For instant queries with "yesterday" or "yesterday_end", use yesterday's start time
// Otherwise, use the data point's timestamp
func (e *Executor) calculateCollectedAt(dataPointTime time.Time, timeRange *models.TimeRangeConfig) time.Time {
	if timeRange == nil {
		// No time range config, use data point timestamp
		return dataPointTime
	}

	// Update time resolver to use current time
	if relativeParser, ok := e.timeResolver.(*timeparser.RelativeTimeParser); ok {
		relativeParser.UpdateNow(time.Now())
	}

	if timeRange.Type == "range" {
		// For range queries, check if start and end are on the same day
		if timeRange.Start != "" && timeRange.End != "" {
			start, end, err := e.timeResolver.ResolveRangeTime(timeRange.Start, timeRange.End)
			if err == nil {
				// Check if start and end are on the same day
				startDate := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, start.Location())
				endDate := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, end.Location())
				
				if startDate.Equal(endDate) {
					// Same day: use the start of that day for all data points
					return startDate
				} else {
					// Different days: use the start of the day for each data point
					dataDate := time.Date(dataPointTime.Year(), dataPointTime.Month(), dataPointTime.Day(), 0, 0, 0, 0, dataPointTime.Location())
					return dataDate
				}
			}
		}
		// If we can't resolve the time range, fall back to using data point's day start
		dataDate := time.Date(dataPointTime.Year(), dataPointTime.Month(), dataPointTime.Day(), 0, 0, 0, 0, dataPointTime.Location())
		return dataDate
	}

	if timeRange.Type == "instant" {
		// For instant queries, check if querying yesterday's data
		if timeRange.Time == "yesterday" || timeRange.Time == "yesterday_end" {
			queryTime, err := e.timeResolver.ResolveTime(timeRange.Time)
			if err == nil {
				// Use yesterday's start time
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

// TestConnections tests both Prometheus and MySQL connections
func (e *Executor) TestConnections(ctx context.Context) error {
	// Test Prometheus connection
	if err := e.promClient.TestConnection(ctx); err != nil {
		return fmt.Errorf("prometheus connection test failed: %w", err)
	}

	// Test MySQL connection
	if err := e.db.TestConnection(); err != nil {
		return fmt.Errorf("mysql connection test failed: %w", err)
	}

	e.logger.Info("All connections tested successfully")
	return nil
}

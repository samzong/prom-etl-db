package prometheus

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	"github.com/samzong/prom-etl-db/internal/models"
	"github.com/samzong/prom-etl-db/internal/timeparser"
)

// Client represents a Prometheus client using official library
type Client struct {
	client       v1.API
	timeResolver timeparser.TimeResolver
	logger       *slog.Logger
}

// NewClient creates a new Prometheus client using official library
func NewClient(baseURL, timeout string) (*Client, error) {
	timeoutDuration, err := time.ParseDuration(timeout)
	if err != nil {
		return nil, fmt.Errorf("invalid timeout format: %w", err)
	}
	return NewClientWithLogger(baseURL, timeoutDuration, nil)
}

// NewClientWithLogger creates a new Prometheus client with custom logger
func NewClientWithLogger(baseURL string, timeout time.Duration, baseLogger *slog.Logger) (*Client, error) {
	// Create Prometheus API client
	client, err := api.NewClient(api.Config{
		Address: baseURL,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create prometheus client: %w", err)
	}

	// Use provided logger or create a default one
	var clientLogger *slog.Logger
	if baseLogger != nil {
		clientLogger = baseLogger.With("component", "prometheus-client")
	} else {
		clientLogger = slog.Default().With("component", "prometheus-client")
	}

	return &Client{
		client:       v1.NewAPI(client),
		timeResolver: timeparser.NewRelativeTimeParser(time.Now()),
		logger:       clientLogger,
	}, nil
}

// QueryInstant executes an instant query
func (c *Client) QueryInstant(ctx context.Context, query string) (*models.PrometheusResponse, error) {
	return c.QueryInstantWithTime(ctx, query, time.Now())
}

// QueryInstantWithTime executes an instant query at a specific time
func (c *Client) QueryInstantWithTime(ctx context.Context, query string, queryTime time.Time) (*models.PrometheusResponse, error) {
	c.logger.Info("Executing instant query",
		"query", query,
		"time", queryTime.Format(time.RFC3339),
		"time_unix", queryTime.Unix(),
	)

	result, warnings, err := c.client.Query(ctx, query, queryTime)
	if err != nil {
		c.logger.Error("Instant query failed",
			"query", query,
			"error", err,
		)
		return nil, fmt.Errorf("instant query failed: %w", err)
	}

	if len(warnings) > 0 {
		c.logger.Warn("Query returned warnings",
			"query", query,
			"warnings", warnings,
		)
	}

	response := c.convertToPrometheusResponse(result)
	c.logger.Info("Instant query completed successfully",
		"query", query,
		"result_type", response.Data.ResultType,
	)

	return response, nil
}

// QueryInstantWithConfig executes an instant query with time configuration
func (c *Client) QueryInstantWithConfig(ctx context.Context, query string, timeConfig *models.TimeRangeConfig) (*models.PrometheusResponse, error) {
	queryTime := time.Now()

	if timeConfig != nil && timeConfig.Time != "" {
		var err error
		queryTime, err = c.timeResolver.ResolveTime(timeConfig.Time)
		if err != nil {
			c.logger.Error("Failed to resolve query time",
				"time_expr", timeConfig.Time,
				"error", err,
			)
			return nil, fmt.Errorf("failed to resolve query time: %w", err)
		}
		c.logger.Info("Resolved query time",
			"time_expr", timeConfig.Time,
			"resolved_time", queryTime.Format(time.RFC3339),
		)
	}

	return c.QueryInstantWithTime(ctx, query, queryTime)
}

// QueryRange executes a range query
func (c *Client) QueryRange(ctx context.Context, query string, start, end time.Time, step time.Duration) (*models.PrometheusResponse, error) {
	c.logger.Info("Executing range query",
		"query", query,
		"start", start.Format(time.RFC3339),
		"end", end.Format(time.RFC3339),
		"step", step.String(),
		"duration", end.Sub(start).String(),
	)

	r := v1.Range{
		Start: start,
		End:   end,
		Step:  step,
	}

	result, warnings, err := c.client.QueryRange(ctx, query, r)
	if err != nil {
		c.logger.Error("Range query failed",
			"query", query,
			"error", err,
		)
		return nil, fmt.Errorf("range query failed: %w", err)
	}

	if len(warnings) > 0 {
		c.logger.Warn("Query returned warnings",
			"query", query,
			"warnings", warnings,
		)
	}

	response := c.convertToPrometheusResponse(result)
	c.logger.Info("Range query completed successfully",
		"query", query,
		"result_type", response.Data.ResultType,
	)

	return response, nil
}

// QueryRangeWithConfig executes a range query with time configuration
func (c *Client) QueryRangeWithConfig(ctx context.Context, query string, timeConfig *models.TimeRangeConfig) (*models.PrometheusResponse, error) {
	if timeConfig == nil {
		return nil, fmt.Errorf("time configuration is required for range query")
	}

	start, end, err := c.timeResolver.ResolveRangeTime(timeConfig.Start, timeConfig.End)
	if err != nil {
		c.logger.Error("Failed to resolve time range",
			"start_expr", timeConfig.Start,
			"end_expr", timeConfig.End,
			"error", err,
		)
		return nil, fmt.Errorf("failed to resolve time range: %w", err)
	}

	step, err := time.ParseDuration(timeConfig.Step)
	if err != nil {
		c.logger.Error("Failed to parse step duration",
			"step", timeConfig.Step,
			"error", err,
		)
		return nil, fmt.Errorf("failed to parse step duration: %w", err)
	}

	c.logger.Info("Resolved time range configuration",
		"start_expr", timeConfig.Start,
		"end_expr", timeConfig.End,
		"step_expr", timeConfig.Step,
		"resolved_start", start.Format(time.RFC3339),
		"resolved_end", end.Format(time.RFC3339),
		"resolved_step", step.String(),
	)

	return c.QueryRange(ctx, query, start, end, step)
}

// QueryWithTimeRange executes a query with time range configuration (unified interface)
func (c *Client) QueryWithTimeRange(ctx context.Context, query string, timeRange *models.TimeRangeConfig) (*models.PrometheusResponse, error) {
	if timeRange == nil {
		return c.QueryInstant(ctx, query)
	}

	c.logger.Info("Processing time range configuration",
		"type", timeRange.Type,
		"time", timeRange.Time,
		"start", timeRange.Start,
		"end", timeRange.End,
		"step", timeRange.Step,
	)

	switch timeRange.Type {
	case "instant":
		return c.QueryInstantWithConfig(ctx, query, timeRange)
	case "range":
		return c.QueryRangeWithConfig(ctx, query, timeRange)
	default:
		c.logger.Warn("Unknown time range type, defaulting to instant query",
			"type", timeRange.Type,
		)
		return c.QueryInstant(ctx, query)
	}
}

// convertToPrometheusResponse converts Prometheus API result to our response format
func (c *Client) convertToPrometheusResponse(value model.Value) *models.PrometheusResponse {
	response := &models.PrometheusResponse{
		Status: "success",
		Data: models.ResultData{
			ResultType: value.Type().String(),
		},
	}

	switch v := value.(type) {
	case model.Vector:
		response.Data.Result = c.convertVector(v)
	case model.Matrix:
		response.Data.Result = c.convertMatrix(v)
	case *model.Scalar:
		response.Data.Result = c.convertScalar(v)
	case *model.String:
		response.Data.Result = c.convertString(v)
	default:
		c.logger.Warn("Unknown result type", "type", value.Type())
		response.Data.Result = []interface{}{}
	}

	return response
}

// convertVector converts model.Vector to our format
func (c *Client) convertVector(vector model.Vector) []interface{} {
	result := make([]interface{}, len(vector))
	for i, sample := range vector {
		result[i] = map[string]interface{}{
			"metric": sample.Metric,
			"value":  []interface{}{float64(sample.Timestamp.Unix()), sample.Value.String()},
		}
	}
	return result
}

// convertMatrix converts model.Matrix to our format
func (c *Client) convertMatrix(matrix model.Matrix) []interface{} {
	result := make([]interface{}, len(matrix))
	for i, sampleStream := range matrix {
		values := make([]interface{}, len(sampleStream.Values))
		for j, pair := range sampleStream.Values {
			values[j] = []interface{}{float64(pair.Timestamp.Unix()), pair.Value.String()}
		}
		result[i] = map[string]interface{}{
			"metric": sampleStream.Metric,
			"values": values,
		}
	}
	return result
}

// convertScalar converts model.Scalar to our format
func (c *Client) convertScalar(scalar *model.Scalar) []interface{} {
	return []interface{}{
		[]interface{}{float64(scalar.Timestamp.Unix()), scalar.Value.String()},
	}
}

// convertString converts model.String to our format
func (c *Client) convertString(str *model.String) []interface{} {
	return []interface{}{
		[]interface{}{float64(str.Timestamp.Unix()), string(str.Value)},
	}
}

// TestConnection tests the connection to Prometheus
func (c *Client) TestConnection(ctx context.Context) error {
	_, err := c.QueryInstant(ctx, "up")
	if err != nil {
		return fmt.Errorf("connection test failed: %w", err)
	}
	return nil
}

// GetMetrics returns available metrics
func (c *Client) GetMetrics(ctx context.Context) ([]string, error) {
	labelValues, warnings, err := c.client.LabelValues(ctx, "__name__", nil, time.Now().Add(-time.Hour), time.Now())
	if err != nil {
		return nil, fmt.Errorf("failed to get metrics: %w", err)
	}

	if len(warnings) > 0 {
		c.logger.Warn("GetMetrics returned warnings", "warnings", warnings)
	}

	metrics := make([]string, len(labelValues))
	for i, value := range labelValues {
		metrics[i] = string(value)
	}

	return metrics, nil
}

// Close closes the client
func (c *Client) Close() error {
	// Prometheus client doesn't need explicit closing
	return nil
}

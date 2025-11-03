# Prometheus to MySQL ETL

A Go-based ETL tool that collects Prometheus metrics and stores them in MySQL database with scheduled execution using cron expressions.

## Features

- Scheduled metric collection using cron expressions
- Multiple PromQL query support with instant and range queries
- Unified storage in MySQL with JSON labels
- Database-driven query configuration
- Retry mechanism with configurable intervals
- Relative time parsing for flexible time ranges
- Transaction-based batch inserts

## Quick Start

### Prerequisites

- Go 1.21+
- MySQL 8.0+
- Docker & Docker Compose (optional)

### Configuration

```bash
make setup
```

Configure environment variables in `.env`:

```bash
# Prometheus Configuration
PROMETHEUS_URL=http://localhost:9090
PROMETHEUS_TIMEOUT=30s

# MySQL Configuration
MYSQL_HOST=localhost
MYSQL_PORT=3306
MYSQL_DATABASE=prometheus_data
MYSQL_USERNAME=root
MYSQL_PASSWORD=password
MYSQL_CHARSET=utf8mb4

# Application Configuration
LOG_LEVEL=info
HTTP_PORT=8080
WORKER_POOL_SIZE=10
```

### Database Setup

```bash
make init-db
```

### Running

```bash
# Docker Compose
make docker-up

# Local Development
make build && make run
# or
make debug
```

## Configuration

### Environment Variables

| Variable             | Description           | Default                 |
| -------------------- | --------------------- | ----------------------- |
| `PROMETHEUS_URL`     | Prometheus server URL | `http://localhost:9090` |
| `PROMETHEUS_TIMEOUT` | Query timeout         | `30s`                   |
| `MYSQL_HOST`         | MySQL host            | `localhost`             |
| `MYSQL_PORT`         | MySQL port            | `3306`                  |
| `MYSQL_DATABASE`     | Database name         | `prometheus_data`       |
| `MYSQL_USERNAME`     | Database username     | `root`                  |
| `MYSQL_PASSWORD`     | Database password     | `password`              |
| `MYSQL_CHARSET`      | MySQL charset         | `utf8mb4`               |
| `LOG_LEVEL`          | Log level             | `info`                  |
| `HTTP_PORT`          | HTTP server port      | `8080`                  |
| `WORKER_POOL_SIZE`   | Worker pool size      | `10`                    |

### Query Configuration

Queries are stored in `query_configs` table:

- `query_id`: Unique identifier
- `query`: PromQL expression
- `schedule`: Cron expression (with seconds)
- `time_range_type`: `instant` or `range`
- `enabled`: Boolean flag

## Database Schema

**metrics_data**: Stores metric values with JSON labels

- `query_id`, `metric_name`, `labels`, `value`, `timestamp`, `result_type`

**query_executions**: Execution history and performance

- `query_id`, `status`, `start_time`, `end_time`, `duration_ms`, `records_count`

**query_configs**: Query configurations

- `query_id`, `query`, `schedule`, `time_range_type`, `enabled`

## Project Structure

```
prom-etl-db/
├── cmd/server/main.go
├── internal/
│   ├── config/      # Configuration
│   ├── database/     # MySQL operations
│   ├── executor/     # Query execution
│   ├── logger/       # Logging
│   ├── models/       # Data models
│   ├── prometheus/   # Prometheus client
│   └── timeparser/   # Time parsing
├── scripts/migrate.sql
└── Makefile
```

## Time Range Support

**Instant**: `time_range_type: instant`, `time_range_time: "now-1h"`

**Range**: `time_range_type: range`, `time_range_start: "now-1d/d"`, `time_range_end: "now/d"`, `time_range_step: "1h"`

## Make Commands

```bash
# Development
make setup          # Setup environment
make build          # Build binary
make debug          # Run in debug mode
make run            # Run application

# Docker
make docker-build   # Build image
make docker-up      # Start services
make docker-down    # Stop services

# Database
make init-db        # Initialize database
make db-migrate     # Run migrations

# Help
make help           # Show all commands
```

## Dependencies

- `github.com/go-sql-driver/mysql` - MySQL driver
- `github.com/robfig/cron/v3` - Cron scheduler
- `github.com/prometheus/client_golang` - Official Prometheus client
- `github.com/prometheus/common` - Prometheus common libraries
- `github.com/jinzhu/now` - Time parsing utilities

## License

[MIT](LICENSE)

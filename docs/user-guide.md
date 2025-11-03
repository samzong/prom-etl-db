# Prometheus ETL 系统使用指南

## 目录

1. [系统概述](#系统概述)
2. [查询设计与配置](#查询设计与配置)
3. [查询参数设置](#查询参数设置)
4. [数据查询](#数据查询)
5. [执行记录查询](#执行记录查询)
6. [最佳实践](#最佳实践)
7. [故障排除](#故障排除)

## 系统概述

Prometheus ETL 系统是一个基于 Go 的数据采集工具，用于从 Prometheus 收集指标数据并存储到 MySQL 数据库中。系统支持：

- 基于 Cron 表达式的定时任务调度
- 即时查询（instant）和范围查询（range）
- 灵活的时间范围配置
- 重试机制和错误处理
- 事务性批量数据插入

## 查询设计与配置

### 1. 查询配置表结构

查询配置存储在 `query_configs` 表中，包含以下字段：

```sql
CREATE TABLE query_configs (
    query_id varchar(100) PRIMARY KEY,
    name varchar(255) NOT NULL,
    description text,
    query text NOT NULL,
    schedule varchar(100) NOT NULL,
    timeout varchar(20) DEFAULT '30s',
    enabled boolean DEFAULT true,
    retry_count int DEFAULT 3,
    retry_interval varchar(20) DEFAULT '10s',
    time_range_type enum('instant','range') DEFAULT 'instant',
    time_range_time varchar(100) NULL,
    time_range_start varchar(100) NULL,
    time_range_end varchar(100) NULL,
    time_range_step varchar(100) NULL,
    created_at timestamp DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamp DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
);
```

### 2. 插入查询配置

#### 即时查询（Instant Query）示例

```sql
INSERT INTO query_configs (
    query_id, name, description, query, schedule, timeout, enabled,
    retry_count, retry_interval, time_range_type, time_range_time,
    time_range_start, time_range_end, time_range_step
) VALUES (
    'gpu_utilization_daily',
    'GPU 每日利用率统计',
    '计算过去24小时的GPU利用率，每天凌晨1点执行',
    'sum(sum_over_time(max without(exported_namespace, exported_pod, modelName, prometheus, cluster, insight, mode) (kpanda_gpu_pod_utilization != bool 999999)[24h:1m])) by (cluster_name, node, UUID) * 60 / 3600',
    '0 0 1 * * *',
    '60s',
    true,
    3,
    '10s',
    'instant',
    'yesterday_end',
    null,
    null,
    null
);
```

#### 范围查询（Range Query）示例

```sql
INSERT INTO query_configs (
    query_id, name, description, query, schedule, timeout, enabled,
    retry_count, retry_interval, time_range_type, time_range_time,
    time_range_start, time_range_end, time_range_step
) VALUES (
    'cpu_usage_hourly',
    'CPU 使用率小时统计',
    '每小时收集CPU使用率数据',
    'avg(cpu_usage_percent) by (instance)',
    '0 0 * * * *',
    '30s',
    true,
    2,
    '5s',
    'range',
    null,
    'yesterday',  -- start_time
    'yesterday_end',  -- end_time
    '1m'
);
```

## 查询参数设置

### 1. 基础参数

| 参数             | 类型    | 必填 | 说明                    | 示例                           |
| ---------------- | ------- | ---- | ----------------------- | ------------------------------ |
| `query_id`       | string  | 是   | 查询唯一标识符          | `gpu_utilization_daily`        |
| `name`           | string  | 是   | 查询名称                | `GPU 每日利用率统计`           |
| `description`    | text    | 否   | 查询描述                | `计算过去24小时的GPU利用率`    |
| `query`          | text    | 是   | PromQL 查询语句         | `sum(cpu_usage) by (instance)` |
| `schedule`       | string  | 是   | Cron 表达式（支持秒级） | `0 0 1 * * *`                  |
| `timeout`        | string  | 否   | 查询超时时间            | `30s`, `1m`, `2m30s`           |
| `enabled`        | boolean | 否   | 是否启用查询            | `true`, `false`                |
| `retry_count`    | int     | 否   | 失败重试次数            | `3`                            |
| `retry_interval` | string  | 否   | 重试间隔                | `10s`, `30s`                   |

### 2. 时间范围参数

#### 即时查询（instant）

用于在特定时间点执行查询，返回单个时间点的数据。

| 参数               | 必填 | 说明               | 示例                               |
| ------------------ | ---- | ------------------ | ---------------------------------- |
| `time_range_type`  | 是   | 设置为 `'instant'` | `'instant'`                        |
| `time_range_time`  | 否   | 查询时间点         | `'now'`, `'yesterday'`, `'now-1h'` |
| `time_range_start` | 否   | 设置为 `null`      | `null`                             |
| `time_range_end`   | 否   | 设置为 `null`      | `null`                             |
| `time_range_step`  | 否   | 设置为 `null`      | `null`                             |

**支持的时间表达式：**

#### 固定时间点表达式

- `'now'` - 当前时间
- `'today'` - 今天 00:00:00
- `'today_end'` - 今天 23:59:59
- `'yesterday'` - 昨天 00:00:00
- `'yesterday_end'` - 昨天 23:59:59

#### 周期时间表达式

- `'last_week'` - 上周开始时间（周一 00:00:00）
- `'last_week_end'` - 上周结束时间（周日 23:59:59）
- `'last_month'` - 上个月开始时间（1 号 00:00:00）
- `'last_month_end'` - 上个月结束时间（最后一天 23:59:59）
- `'last_quarter'` - 上个季度开始时间
- `'last_year'` - 去年开始时间（1 月 1 日 00:00:00）

#### 相对时间偏移表达式

- `'now-1h'` - 1 小时前
- `'now-1d'` - 1 天前
- `'now-24h'` - 24 小时前
- `'now-1w'` - 1 周前
- `'now-30m'` - 30 分钟前
- `'+1h'` - 1 小时后（相对于当前时间）
- `'+30m'` - 30 分钟后

**时间偏移支持的单位：**

- `s` - 秒
- `m` - 分钟
- `h` - 小时
- `d` - 天（24 小时）
- `w` - 周（7 天）

**使用示例：**

```sql
-- 查询昨天全天的数据（从昨天00:00到23:59）
time_range_type = 'range'
time_range_start = 'yesterday'
time_range_end = 'yesterday_end'
time_range_step = '1h'

-- 查询上个月的数据
time_range_type = 'range'
time_range_start = 'last_month'
time_range_end = 'last_month_end'
time_range_step = '1d'

-- 查询最近2小时30分钟的数据
time_range_type = 'range'
time_range_start = 'now-2h30m'
time_range_end = 'now'
time_range_step = '5m'
```

#### 范围查询（range）

用于在时间范围内执行查询，返回时间序列数据。

| 参数               | 必填 | 说明             | 示例                   |
| ------------------ | ---- | ---------------- | ---------------------- |
| `time_range_type`  | 是   | 设置为 `'range'` | `'range'`              |
| `time_range_time`  | 否   | 设置为 `null`    | `null`                 |
| `time_range_start` | 是   | 开始时间         | `'now-1h'`, `'today'`  |
| `time_range_end`   | 是   | 结束时间         | `'now'`, `'now-30m'`   |
| `time_range_step`  | 是   | 采样间隔         | `'1m'`, `'5m'`, `'1h'` |

### 3. Cron 表达式格式

系统使用 6 位 Cron 表达式（包含秒）：

```
秒 分 时 日 月 星期
```

**常用示例：**

- `0 0 1 * * *` - 每天凌晨 1 点
- `0 */5 * * * *` - 每 5 分钟
- `0 0 */2 * * *` - 每 2 小时
- `0 30 9 * * 1-5` - 工作日上午 9:30
- `0 0 0 1 * *` - 每月 1 号午夜

## 数据查询

### 1. 查询最新指标数据

```sql
-- 查询指定查询ID的最新100条数据
SELECT
    id, query_id, metric_name, labels, value, timestamp, result_type, collected_at
FROM metrics_data
WHERE query_id = 'gpu_utilization_daily'
ORDER BY timestamp DESC
LIMIT 100;
```

### 2. 按时间范围查询数据

```sql
-- 查询最近24小时的数据
SELECT
    metric_name, labels, value, timestamp
FROM metrics_data
WHERE query_id = 'cpu_usage_hourly'
    AND timestamp >= NOW() - INTERVAL 24 HOUR
ORDER BY timestamp DESC;
```

### 3. 聚合查询示例

```sql
-- 按小时统计平均值
SELECT
    DATE_FORMAT(timestamp, '%Y-%m-%d %H:00:00') as hour,
    AVG(value) as avg_value,
    COUNT(*) as count
FROM metrics_data
WHERE query_id = 'cpu_usage_hourly'
    AND timestamp >= NOW() - INTERVAL 7 DAY
GROUP BY DATE_FORMAT(timestamp, '%Y-%m-%d %H:00:00')
ORDER BY hour DESC;
```

### 4. JSON 标签查询

```sql
-- 查询特定标签的数据
SELECT
    metric_name, value, timestamp,
    JSON_EXTRACT(labels, '$.instance') as instance,
    JSON_EXTRACT(labels, '$.cluster_name') as cluster
FROM metrics_data
WHERE query_id = 'gpu_utilization_daily'
    AND JSON_EXTRACT(labels, '$.cluster_name') = 'production'
ORDER BY timestamp DESC;
```

### 5. 统计查询

```sql
-- 查询数据统计信息
SELECT
    query_id,
    COUNT(*) as total_records,
    MIN(timestamp) as earliest_data,
    MAX(timestamp) as latest_data,
    AVG(value) as avg_value
FROM metrics_data
GROUP BY query_id;
```

## 执行记录查询

### 1. 查询执行历史

```sql
-- 查询指定查询的执行历史
SELECT
    id, query_id, query_name, status, start_time, end_time,
    duration_ms, records_count, error_message, created_at
FROM query_executions
WHERE query_id = 'gpu_utilization_daily'
ORDER BY start_time DESC
LIMIT 50;
```

### 2. 查询执行状态统计

```sql
-- 统计各状态的执行次数
SELECT
    query_id,
    status,
    COUNT(*) as count,
    AVG(duration_ms) as avg_duration_ms
FROM query_executions
WHERE start_time >= NOW() - INTERVAL 7 DAY
GROUP BY query_id, status
ORDER BY query_id, status;
```

### 3. 查询失败记录

```sql
-- 查询失败的执行记录
SELECT
    query_id, query_name, start_time, error_message
FROM query_executions
WHERE status = 'failed'
    AND start_time >= NOW() - INTERVAL 24 HOUR
ORDER BY start_time DESC;
```

### 4. 性能分析查询

```sql
-- 查询执行性能统计
SELECT
    query_id,
    COUNT(*) as total_executions,
    COUNT(CASE WHEN status = 'success' THEN 1 END) as successful,
    COUNT(CASE WHEN status = 'failed' THEN 1 END) as failed,
    ROUND(COUNT(CASE WHEN status = 'success' THEN 1 END) * 100.0 / COUNT(*), 2) as success_rate,
    AVG(duration_ms) as avg_duration,
    MAX(duration_ms) as max_duration,
    AVG(records_count) as avg_records
FROM query_executions
WHERE start_time >= NOW() - INTERVAL 30 DAY
GROUP BY query_id
ORDER BY success_rate DESC;
```

## 最佳实践

### 1. 查询设计建议

- **查询 ID 命名**：使用描述性的名称，如 `gpu_utilization_daily`
- **PromQL 优化**：避免过于复杂的查询，考虑性能影响
- **时间范围**：根据数据保留策略合理设置时间范围
- **采样间隔**：范围查询的步长应该合理，避免数据过密或过疏

### 2. 调度策略

- **错峰执行**：避免多个重型查询同时执行
- **合理频率**：根据数据变化频率设置合适的调度间隔
- **超时设置**：根据查询复杂度设置合理的超时时间

### 3. 监控和维护

- **定期检查执行状态**：监控失败率和执行时间
- **数据清理**：定期清理过期的指标数据和执行记录
- **性能优化**：根据执行统计优化查询和调度

### 4. 时间配置最佳实践

#### 日报场景

```sql
-- 每天凌晨1点统计昨天全天数据
time_range_type = 'instant'
time_range_time = 'yesterday_end'
schedule = '0 0 1 * * *'
```

#### 周报场景

```sql
-- 每周一上午8点统计上周数据
time_range_type = 'range'
time_range_start = 'last_week'
time_range_end = 'last_week_end'
time_range_step = '1h'
schedule = '0 0 8 * * 1'
```

#### 月报场景

```sql
-- 每月1号上午9点统计上个月数据
time_range_type = 'range'
time_range_start = 'last_month'
time_range_end = 'last_month_end'
time_range_step = '1d'
schedule = '0 0 9 1 * *'
```

#### 实时监控场景

```sql
-- 每5分钟收集最近1小时数据
time_range_type = 'range'
time_range_start = 'now-1h'
time_range_end = 'now'
time_range_step = '1m'
schedule = '0 */5 * * * *'
```

#### 高频监控场景

```sql
-- 每分钟收集最近5分钟数据
time_range_type = 'range'
time_range_start = 'now-5m'
time_range_end = 'now'
time_range_step = '30s'
schedule = '0 * * * * *'
```

#### 历史数据分析场景

```sql
-- 每天凌晨2点分析过去7天的趋势
time_range_type = 'range'
time_range_start = 'now-7d'
time_range_end = 'yesterday_end'
time_range_step = '1h'
schedule = '0 0 2 * * *'
```

## 故障排除

### 1. 常见错误

#### 列数不匹配错误

```
ERROR 1136 (21S01): Column count doesn't match value count at row 1
```

**解决方案**：检查 INSERT 语句中列数和值数是否匹配。

#### 时间解析错误

```
Failed to resolve query time
```

**解决方案**：检查时间表达式格式是否正确，参考支持的时间表达式列表。

#### 查询超时

```
Query execution timeout
```

**解决方案**：增加 `timeout` 值或优化 PromQL 查询。

### 2. 调试方法

1. **检查查询配置**：

```sql
SELECT * FROM query_configs WHERE query_id = 'your_query_id';
```

2. **查看最近执行记录**：

```sql
SELECT * FROM query_executions
WHERE query_id = 'your_query_id'
ORDER BY start_time DESC LIMIT 10;
```

3. **检查系统日志**：查看应用程序日志了解详细错误信息。

### 3. 性能优化

- **索引优化**：确保 `metrics_data` 表有适当的索引
- **批量插入**：系统自动使用事务批量插入数据
- **数据分区**：对于大量数据，考虑按时间分区
- **定期清理**：删除过期数据释放存储空间

---

## 附录

### A. 支持的时间单位和表达式

#### 时间偏移单位

- `s` - 秒
- `m` - 分钟
- `h` - 小时
- `d` - 天（24 小时）
- `w` - 周（7 天）

#### 预定义时间表达式

- **当前时间**: `now`
- **今天**: `today` (00:00:00), `today_end` (23:59:59)
- **昨天**: `yesterday` (00:00:00), `yesterday_end` (23:59:59)
- **上周**: `last_week` (周一 00:00:00), `last_week_end` (周日 23:59:59)
- **上月**: `last_month` (1 号 00:00:00), `last_month_end` (最后一天 23:59:59)
- **上季度**: `last_quarter`
- **去年**: `last_year` (1 月 1 日 00:00:00)

#### 相对时间偏移格式

- **向前偏移**: `-1h`, `-30m`, `-1d`, `-1w`
- **向后偏移**: `+1h`, `+30m`, `+1d`, `+1w`
- **组合偏移**: `-2h30m`, `-1d12h`, `-1w3d`

### B. 查询状态说明

- `running` - 执行中
- `success` - 执行成功
- `failed` - 执行失败
- `timeout` - 执行超时

### C. 结果类型说明

- `instant` - 即时查询结果
- `range` - 范围查询结果
- `scalar` - 标量结果

---

_本文档基于 Prometheus ETL 系统版本编写，如有疑问请参考源代码或联系开发团队。_

# 数据修复工具使用说明

## 概述

由于时间解析bug导致历史数据缺失，本工具用于从 Prometheus 重新查询并修复历史数据。

## 工具说明

### repair - 数据修复工具

从 Prometheus 查询历史数据并保存到 MySQL 数据库。

**使用方法：**
```bash
# 编译工具
make build-repair

# 默认查询最近90天的数据（从昨天往前推）
./build/repair

# 指定查询最近N天的数据（例如最近30天）
./build/repair 30

# 指定具体的日期范围
./build/repair 2025-08-05 2025-11-03
```

**功能：**
- 默认查询最近90天的数据（符合 Prometheus 数据保留期）
- 自动检查数据是否已存在，避免重复插入
- 支持增量修复（只修复缺失的数据）
- 详细的日志输出

**注意事项：**
- 需要确保 `.env` 文件中配置了正确的 Prometheus 和 MySQL 连接信息
- 查询会以每天为单位进行，避免对 Prometheus 造成过大压力
- 如果某天的数据已存在，会跳过该天的修复
- 默认从昨天往前推90天，确保查询的数据在 Prometheus 保留期内

## 完整修复流程

### 步骤 1: 修复代码中的时间解析bug

代码已修复，确保使用最新的代码版本。

### 步骤 2: 修复历史数据

```bash
# 1. 配置环境变量（如果还没有）
cp env.example .env
# 编辑 .env 文件，设置正确的 Prometheus 和 MySQL 连接信息

# 2. 编译修复工具
make build-repair

# 3. 运行修复工具（工具会自动加载 .env 文件）
./build/repair

# 或者指定天数（例如最近30天）
./build/repair 30

# 或者指定具体的日期范围
./build/repair 2025-08-05 2025-11-03
```

### 步骤 3: 验证数据

```sql
-- 检查数据量
SELECT DATE(collected_at) as date, COUNT(*) as count
FROM metrics_data
WHERE query_id = 'gpu_utilization_daily'
  AND DATE(collected_at) >= DATE_SUB(CURDATE(), INTERVAL 90 DAY)
GROUP BY DATE(collected_at)
ORDER BY date DESC;

-- 检查最新数据
SELECT MAX(collected_at) as latest_date
FROM metrics_data
WHERE query_id = 'gpu_utilization_daily';
```

### 步骤 4: 重启服务

```bash
# 重启服务以使用修复后的代码
# 确保新的代码版本已部署，时间解析将正常工作
```

## 注意事项

1. **数据重复检查**：修复工具会自动检查数据是否已存在，避免重复插入
2. **性能考虑**：修复工具会在每次查询后等待100ms，避免对 Prometheus 造成过大压力
3. **日期格式**：所有日期格式必须为 `YYYY-MM-DD`
4. **时区**：确保数据库和应用程序使用相同的时区设置
5. **备份**：在生产环境操作前，建议先备份数据库

## 故障排查

### 修复工具无法连接 Prometheus

检查 `.env` 文件中的 `PROMETHEUS_URL` 配置是否正确。

### 修复工具无法连接 MySQL

检查 `.env` 文件中的 MySQL 配置是否正确，确保数据库服务正在运行。

### 查询返回空数据

- 检查查询的日期是否在 Prometheus 的数据保留期内（通常为90天）
- 检查 Prometheus 中是否有对应时间的数据
- 检查查询语句是否正确

## 后续维护

修复完成后，确保：
1. 代码已更新（时间解析bug已修复）
2. 服务已重启，使用新代码
3. 定时任务正常运行，每天自动查询昨天的数据


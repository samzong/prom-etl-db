# 数据修复快速指南

## 问题说明

由于时间解析bug，系统一直查询固定的 `2025-07-24` 的数据，导致 10/24 之后没有新数据（因为 Prometheus 的数据保留期约为90天）。

## 解决方案

已修复代码中的时间解析bug，并提供了数据修复工具。

## 快速开始

### 1. 配置环境变量

```bash
# 方式1: 创建 .env 文件（推荐）
cp env.example .env
# 编辑 .env 文件，设置正确的 Prometheus 和 MySQL 连接信息

# 方式2: 直接设置环境变量
export PROMETHEUS_URL=http://10.20.100.200:30588/select/0/prometheus
export MYSQL_HOST=localhost
export MYSQL_PORT=3306
export MYSQL_DATABASE=prometheus_data
export MYSQL_USERNAME=root
export MYSQL_PASSWORD=your_password
```

### 2. 编译工具

```bash
make build-all
# 或者只编译修复工具
make build-repair
```

### 3. 修复历史数据（推荐：自动查询最近90天）

**注意：工具会自动加载 `.env` 文件，无需手动设置环境变量**

```bash
# 直接运行即可，工具会自动加载 .env 文件
./build/repair

# 或者指定天数（例如最近30天）
./build/repair 30

# 或者指定具体的日期范围
./build/repair 2025-08-05 2025-11-03
```

### 4. 重启服务

重启服务以使用修复后的代码，之后数据将正常生成。

## 详细说明

查看 `docs/data-repair-guide.md` 获取完整的使用说明。


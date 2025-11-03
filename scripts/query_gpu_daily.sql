-- Query GPU usage by cluster, node, and day
-- Extract cluster_name and node from labels JSON, group by date
SELECT cluster,
    DATE(collected_at) AS "Date",
    node,
    gpu_count AS "GPU Count",
    ROUND(SUM(total_value), 3) AS "Total Usage"
FROM (
        SELECT SUBSTRING_INDEX(
                JSON_UNQUOTE(JSON_EXTRACT(labels, '$.cluster_name')),
                '-',
                2
            ) as cluster,
            JSON_UNQUOTE(JSON_EXTRACT(labels, '$.node')) AS node,
            collected_at,
            COUNT(*) AS gpu_count,
            SUM(value) AS total_value
        FROM metrics_data
        WHERE query_id = 'gpu_utilization_daily'
        GROUP BY node,
            collected_at
    ) as aaa
where 1 = 1
    and cluster = { { cluster_info.clusters } }
GROUP BY node,
    DATE(collected_at)
ORDER BY "Date" DESC,
    node;
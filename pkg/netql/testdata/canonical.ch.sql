SELECT
  pop_id AS pop,
  quantile(0.95)(latency_ms) AS latency_p95
FROM canary_results
WHERE tenant_id = $1
  AND observed_at >= now() - INTERVAL 24 HOUR
  AND target = $2
GROUP BY pop
ORDER BY pop ASC
LIMIT 10000
SELECT
  pop_id AS pop,
  target AS target,
  quantile(0.95)(latency_ms) AS latency_p95
FROM canary_results
WHERE tenant_id = $1
  AND observed_at >= now() - INTERVAL 1 HOUR
GROUP BY pop, target
ORDER BY latency_p95 DESC
LIMIT 10
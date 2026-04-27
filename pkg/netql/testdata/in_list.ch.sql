SELECT
  pop_id AS pop,
  countIf(error_kind = '') / count(*) AS success_rate
FROM canary_results
WHERE tenant_id = $1
  AND observed_at >= now() - INTERVAL 6 HOUR
  AND pop_id IN ($2, $3, $4)
GROUP BY pop
ORDER BY pop ASC
LIMIT 10000
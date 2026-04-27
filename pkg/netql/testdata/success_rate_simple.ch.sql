SELECT
  countIf(error_kind = '') / count(*) AS success_rate
FROM canary_results
WHERE tenant_id = $1
  AND observed_at >= now() - INTERVAL 1 HOUR
  AND pop_id = $2
LIMIT 10000
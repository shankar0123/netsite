SELECT
  count(*) AS count
FROM canary_results
WHERE tenant_id = $1
  AND observed_at >= now() - INTERVAL 12 HOUR
  AND ((pop_id = $2 OR pop_id = $3) AND NOT (error_kind = $4))
LIMIT 10000
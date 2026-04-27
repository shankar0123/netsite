SELECT
  error_kind AS error_kind,
  count(*) AS count
FROM canary_results
WHERE tenant_id = $1
  AND observed_at >= now() - INTERVAL 7 DAY
  AND kind = $2
GROUP BY error_kind
ORDER BY error_kind ASC
LIMIT 10000
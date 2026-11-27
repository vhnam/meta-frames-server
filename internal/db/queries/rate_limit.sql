-- name: HitRateLimit :one
INSERT INTO auth_rate_limit (bucket, key, window_started_at, hits)
VALUES (@bucket, @key, now(), 1)
ON CONFLICT (bucket, key) DO UPDATE SET
  window_started_at = CASE WHEN auth_rate_limit.window_started_at <= now() - make_interval(secs => @window_seconds::float8)
    THEN now() ELSE auth_rate_limit.window_started_at END,
  hits = CASE WHEN auth_rate_limit.window_started_at <= now() - make_interval(secs => @window_seconds::float8)
    THEN 1 ELSE auth_rate_limit.hits + 1 END
RETURNING hits, EXTRACT(EPOCH FROM (window_started_at + make_interval(secs => @window_seconds::float8) - now()))::float8 AS seconds_left;

-- name: DeleteExpiredRateLimits :exec
-- Every window is 15 minutes, so a start more than 24 hours 15 minutes ago (elapsed time, not calendar days) means the window ended over 1 day ago.
DELETE FROM auth_rate_limit WHERE window_started_at < now() - interval '24 hours 15 minutes';

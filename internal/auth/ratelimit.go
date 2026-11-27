package auth

import (
	"context"
	"crypto/sha256"
	"math"
	"net"
	"net/http"
	"strings"
	"time"

	"meta-frames-server/internal/db/gen"
)

const (
	rateLimitBucketVerify         = "verify"
	rateLimitBucketResend         = "resend"
	rateLimitBucketVerifyPassword = "verify_password"

	verifyLimit         = 30
	resendLimit         = 30
	verifyPasswordLimit = 10
	rateLimitWindow     = 15 * time.Minute
)

// sourceIP returns the client address. With hops 0 it is the host part of RemoteAddr. With hops N
// it is the Nth entry from the right of X-Forwarded-For; the client-controlled left side is never read.
// A short or empty header falls back to RemoteAddr.
func sourceIP(request *http.Request, hops int) string {
	remote := request.RemoteAddr
	if host, _, err := net.SplitHostPort(remote); err == nil {
		remote = host
	}
	if hops <= 0 {
		return remote
	}
	var entries []string
	for _, line := range request.Header.Values("X-Forwarded-For") {
		for _, entry := range strings.Split(line, ",") {
			entries = append(entries, strings.TrimSpace(entry))
		}
	}
	if len(entries) < hops || entries[len(entries)-hops] == "" {
		return remote
	}
	return entries[len(entries)-hops]
}

// ipKey is the counter key for a source address: the SHA-256 of its text, so no raw address is stored.
func ipKey(ip string) []byte {
	digest := sha256.Sum256([]byte(ip))
	return digest[:]
}

// hit counts one request in the bucket and reports whether it is within the limit. The window,
// the count and the retry time all come from the database clock.
func hit(ctx context.Context, queries gen.Querier, bucket string, key []byte, limit int, window time.Duration) (allowed bool, retryAfter time.Duration, err error) {
	if err := queries.DeleteExpiredRateLimits(ctx); err != nil {
		return false, 0, err
	}
	row, err := queries.HitRateLimit(ctx, gen.HitRateLimitParams{Bucket: bucket, Key: key, WindowSeconds: window.Seconds()})
	if err != nil {
		return false, 0, err
	}
	if int(row.Hits) <= limit {
		return true, 0, nil
	}
	return false, time.Duration(max(1, math.Ceil(row.SecondsLeft))) * time.Second, nil
}

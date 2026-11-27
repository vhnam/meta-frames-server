package auth

import (
	"context"
	"encoding/hex"
	"net/http"
	"testing"
	"time"

	"meta-frames-server/internal/db/gen"
)

// fakeLimits counts hits in one window. It stands in for the database: testutil.Memory
// cannot be imported here, because testutil imports this package.
type fakeLimits struct {
	gen.Querier
	hits    int32
	seconds float64
	lastKey []byte
}

func (fake *fakeLimits) DeleteExpiredRateLimits(context.Context) error { return nil }

func (fake *fakeLimits) HitRateLimit(_ context.Context, params gen.HitRateLimitParams) (gen.HitRateLimitRow, error) {
	fake.hits++
	fake.lastKey = params.Key
	return gen.HitRateLimitRow{Hits: fake.hits, SecondsLeft: fake.seconds}, nil
}

func requestFrom(remote string, forwarded ...string) *http.Request {
	request := &http.Request{RemoteAddr: remote, Header: http.Header{}}
	for _, line := range forwarded {
		request.Header.Add("X-Forwarded-For", line)
	}
	return request
}

func TestSourceIP(test *testing.T) {
	for _, tc := range []struct {
		name    string
		request *http.Request
		hops    int
		want    string
	}{
		{"hops 0 ignores the header", requestFrom("192.0.2.1:4000", "198.51.100.9"), 0, "192.0.2.1"},
		{"hops 1 takes the last entry", requestFrom("192.0.2.1:4000", "198.51.100.9, 203.0.113.5"), 1, "203.0.113.5"},
		{"hops 2 takes the second from the right", requestFrom("192.0.2.1:4000", "198.51.100.9, 203.0.113.5, 10.0.0.1"), 2, "203.0.113.5"},
		{"short header falls back", requestFrom("192.0.2.1:4000", "203.0.113.5"), 2, "192.0.2.1"},
		{"empty entry falls back", requestFrom("192.0.2.1:4000", "203.0.113.5,"), 1, "192.0.2.1"},
		{"spoofed left entry is ignored", requestFrom("192.0.2.1:4000", "6.6.6.6, 203.0.113.5"), 1, "203.0.113.5"},
		{"ipv6 remote", requestFrom("[2001:db8::1]:4000"), 0, "2001:db8::1"},
		{"two header lines", requestFrom("192.0.2.1:4000", "6.6.6.6, 198.51.100.9", "203.0.113.5"), 2, "198.51.100.9"},
	} {
		test.Run(tc.name, func(test *testing.T) {
			if got := sourceIP(tc.request, tc.hops); got != tc.want {
				test.Fatalf("sourceIP = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHitRefusesPastTheLimit(test *testing.T) {
	queries := &fakeLimits{seconds: 0.2}
	key := ipKey("192.0.2.1")
	for count := range 3 {
		allowed, _, err := hit(context.Background(), queries, "verify", key, 3, time.Minute)
		if err != nil || !allowed {
			test.Fatalf("hit %d: allowed = %v, err = %v, want allowed", count+1, allowed, err)
		}
	}
	allowed, retryAfter, err := hit(context.Background(), queries, "verify", key, 3, time.Minute)
	if err != nil || allowed || retryAfter < time.Second {
		test.Fatalf("hit 4: allowed = %v, retryAfter = %v, err = %v, want refused with retry >= 1s", allowed, retryAfter, err)
	}
}

func TestSourceIPDigestReachesTheLimiter(test *testing.T) {
	request := requestFrom("192.0.2.1:4000", "198.51.100.9, 203.0.113.5")
	queries := &fakeLimits{}
	if _, _, err := hit(test.Context(), queries, rateLimitBucketVerify, ipKey(sourceIP(request, 1)), verifyLimit, rateLimitWindow); err != nil {
		test.Fatal(err)
	}
	const expected = "440a628a0c975ea32d4db42ca94acebc975ab378b3ee2a692ccf2ecae6038bbd"
	if got := hex.EncodeToString(queries.lastKey); got != expected {
		test.Fatalf("limiter key = %s, want SHA-256 of the selected address", got)
	}
}

func TestHitLimitsOfTheVerificationBuckets(test *testing.T) {
	for _, tc := range []struct {
		bucket string
		limit  int
	}{
		{rateLimitBucketVerify, verifyLimit},
		{rateLimitBucketResend, resendLimit},
		{rateLimitBucketVerifyPassword, verifyPasswordLimit},
	} {
		test.Run(tc.bucket, func(test *testing.T) {
			queries := &fakeLimits{seconds: 1}
			for range tc.limit {
				if allowed, _, err := hit(context.Background(), queries, tc.bucket, ipKey("192.0.2.1"), tc.limit, rateLimitWindow); err != nil || !allowed {
					test.Fatalf("hit within the limit: allowed = %v, err = %v", allowed, err)
				}
			}
			if allowed, _, err := hit(context.Background(), queries, tc.bucket, ipKey("192.0.2.1"), tc.limit, rateLimitWindow); err != nil || allowed {
				test.Fatalf("hit over the limit: allowed = %v, err = %v, want refused", allowed, err)
			}
		})
	}
}

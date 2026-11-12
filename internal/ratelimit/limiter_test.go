//go:build test

package ratelimit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"windshift/internal/contextkeys"
	"windshift/internal/models"
	"windshift/internal/utils"
)

func requestWithUser(id int, remote string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/items", nil)
	r.RemoteAddr = remote
	return r.WithContext(context.WithValue(r.Context(), contextkeys.User, &models.User{ID: id}))
}

func requestWithToken(id int, remote string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/rest/api/v1/items", nil)
	r.RemoteAddr = remote
	return r.WithContext(context.WithValue(r.Context(), contextkeys.APIToken, &models.APIToken{ID: id}))
}

func TestLimiterAllowsBurstThenDenies(t *testing.T) {
	limiter := New(0, 2, ByUser())
	defer limiter.Stop()

	key := UserKey(1)
	if !limiter.Allow(key) || !limiter.Allow(key) {
		t.Fatal("burst of 2 should both be allowed")
	}
	if limiter.Allow(key) {
		t.Fatal("third request should be denied once the burst is exhausted")
	}
}

func TestLimiterSeparatesKeys(t *testing.T) {
	limiter := New(0, 1, ByUser())
	defer limiter.Stop()

	if !limiter.Allow(UserKey(1)) {
		t.Fatal("first user should be allowed")
	}
	if limiter.Allow(UserKey(1)) {
		t.Fatal("same user should be denied")
	}
	if !limiter.Allow(UserKey(2)) {
		t.Fatal("a different user has an independent budget")
	}
}

func TestLimiterCheckReportsRemaining(t *testing.T) {
	limiter := New(0, 3, ByUser())
	defer limiter.Stop()

	result := limiter.Check(UserKey(7))
	if !result.Allowed || result.Limit != 3 || result.Remaining != 2 {
		t.Fatalf("first check = %+v, want allowed with 2 remaining", result)
	}
	result = limiter.Check(UserKey(7))
	if result.Remaining != 1 {
		t.Fatalf("second check remaining = %d, want 1", result.Remaining)
	}
}

func TestLimiterMiddlewareWritesUnified429(t *testing.T) {
	limiter := New(0, 1, ByIP(nil))
	defer limiter.Stop()

	handler := limiter.Limit(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	newRequest := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/items", nil)
		r.RemoteAddr = "198.51.100.7:4000"
		return r
	}

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, newRequest())
	if first.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", first.Code)
	}
	if got := first.Header().Get("X-RateLimit-Limit"); got != "1" {
		t.Fatalf("X-RateLimit-Limit = %q, want 1", got)
	}

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, newRequest())
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d, want 429", second.Code)
	}
	if second.Header().Get("Retry-After") == "" {
		t.Fatal("429 missing Retry-After")
	}
	if got := second.Header().Get("X-RateLimit-Remaining"); got != "0" {
		t.Fatalf("X-RateLimit-Remaining = %q, want 0", got)
	}
	var body struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode 429 body: %v (%s)", err, second.Body)
	}
	if body.Code != "RATE_LIMITED" || body.Error == "" {
		t.Fatalf("429 body = %+v, want a RATE_LIMITED error", body)
	}
}

func TestLimiterByUserSharesBudgetAcrossIPs(t *testing.T) {
	limiter := New(0, 1, ByUser())
	defer limiter.Stop()

	if !limiter.AllowRequest(requestWithUser(1, "1.1.1.1:1000")) {
		t.Fatal("first request for user 1 should be allowed")
	}
	if limiter.AllowRequest(requestWithUser(1, "2.2.2.2:2000")) {
		t.Fatal("user 1's budget should be shared across client IPs")
	}
	if !limiter.AllowRequest(requestWithUser(2, "1.1.1.1:1000")) {
		t.Fatal("user 2 has an independent budget")
	}
}

func TestLimiterByUserOrIPFallsBackToAnonymousIP(t *testing.T) {
	limiter := New(0, 1, ByUserOrIP(nil))
	defer limiter.Stop()

	anon := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/public/board", nil)
		r.RemoteAddr = "203.0.113.9:3000"
		return r
	}
	if !limiter.AllowRequest(anon()) {
		t.Fatal("first anonymous request should be allowed")
	}
	if limiter.AllowRequest(anon()) {
		t.Fatal("second anonymous request from the same IP should be denied")
	}

	if !limiter.AllowRequest(requestWithUser(3, "203.0.113.9:3000")) {
		t.Fatal("authenticated request should key by user, not the shared IP")
	}
}

func TestLimiterByUserDeclinesAnonymousRequests(t *testing.T) {
	limiter := New(0, 1, ByUser())
	defer limiter.Stop()

	anonymous := httptest.NewRequest(http.MethodGet, "/api/items", nil)
	anonymous.RemoteAddr = "198.51.100.1:1000"
	if !limiter.AllowRequest(anonymous) {
		t.Fatal("anonymous request on a user-keyed limiter should be unlimited")
	}
	if _, ok := limiter.Decide(anonymous); ok {
		t.Fatal("Decide should report the key function declined the request")
	}
}

func TestLimiterByTokenKeysByToken(t *testing.T) {
	limiter := New(0, 1, ByToken())
	defer limiter.Stop()

	if !limiter.AllowRequest(requestWithToken(1, "1.1.1.1:1")) {
		t.Fatal("first request for token 1 should be allowed")
	}
	if limiter.AllowRequest(requestWithToken(1, "2.2.2.2:2")) {
		t.Fatal("token 1's budget should be shared across client IPs")
	}
	if !limiter.AllowRequest(requestWithToken(2, "1.1.1.1:1")) {
		t.Fatal("token 2 has an independent budget")
	}
}

// loginRequest builds a login request whose socket peer is a private (trusted)
// proxy and whose leading X-Forwarded-For entry is attacker-chosen. The trusted
// proxy appends the real client (203.0.113.7) on the right.
func loginRequest(spoofedXFF string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	r.RemoteAddr = "10.0.0.2:54321"
	r.Header.Set("X-Forwarded-For", spoofedXFF+", 203.0.113.7")
	return r
}

func TestLimiterByIPIgnoresSpoofedForwardedForBehindTrustedProxy(t *testing.T) {
	limiter := New(1, 1, ByIP(utils.NewIPExtractor(true, nil)))
	defer limiter.Stop()

	if !limiter.AllowRequest(loginRequest("10.9.0.1")) {
		t.Fatal("first request for the real client should be allowed")
	}
	if limiter.AllowRequest(loginRequest("10.9.0.2")) {
		t.Fatal("rotating X-Forwarded-For bypassed the per-IP rate limit")
	}
}

func TestLimiterByIPIgnoresForwardedForWhenProxyDisabled(t *testing.T) {
	limiter := New(0, 1, ByIP(utils.NewIPExtractor(false, nil)))
	defer limiter.Stop()

	request := func(xff string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/items", nil)
		r.RemoteAddr = "198.51.100.4:1000"
		r.Header.Set("X-Forwarded-For", xff)
		return r
	}
	if !limiter.AllowRequest(request("1.1.1.1")) {
		t.Fatal("first request should be allowed")
	}
	if limiter.AllowRequest(request("2.2.2.2")) {
		t.Fatal("spoofed X-Forwarded-For changed the key while proxy mode is off")
	}
}

func TestLimiterSweepEvictsIdleBuckets(t *testing.T) {
	limiter := New(100, 100, ByUser())
	defer limiter.Stop()

	limiter.Allow(UserKey(1))
	limiter.Allow(UserKey(2))
	if got := limiter.totalBuckets(); got != 2 {
		t.Fatalf("bucket count = %d, want 2", got)
	}

	limiter.sweep(time.Now().Add(2 * limiter.idleTTL))

	if got := limiter.totalBuckets(); got != 0 {
		t.Fatalf("after sweep, bucket count = %d, want 0", got)
	}
}

func TestLimiterMaxEntriesBoundsBuckets(t *testing.T) {
	limiter := New(100, 100, ByUser(), WithShards(1), WithMaxEntries(4))
	defer limiter.Stop()

	for i := 0; i < 100; i++ {
		limiter.Allow(UserKey(i))
	}
	if got := limiter.totalBuckets(); got > 4 {
		t.Fatalf("bucket count = %d, want at most 4", got)
	}
}

func TestLimiterStopIsIdempotent(t *testing.T) {
	limiter := New(10, 10, ByUser())
	limiter.Stop()
	limiter.Stop()
}

func TestLimiterAllowPathDoesNotAllocate(t *testing.T) {
	limiter := New(1e9, 1e9, ByUser())
	defer limiter.Stop()

	key := UserKey(1)
	limiter.Allow(key)
	if allocs := testing.AllocsPerRun(1000, func() { limiter.Allow(key) }); allocs != 0 {
		t.Fatalf("Allow allocated %v times per call, want 0", allocs)
	}
}

func (l *Limiter) totalBuckets() int {
	total := 0
	for i := range l.shards {
		l.shards[i].mu.RLock()
		total += len(l.shards[i].buckets)
		l.shards[i].mu.RUnlock()
	}
	return total
}

func BenchmarkLimiterAllowParallel(b *testing.B) {
	limiter := New(1e9, 1e9, ByUser())
	defer limiter.Stop()

	keys := make([]Key, 1024)
	for i := range keys {
		keys[i] = UserKey(i)
	}

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			limiter.Allow(keys[i&1023])
			i++
		}
	})
}

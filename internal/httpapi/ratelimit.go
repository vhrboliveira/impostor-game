package httpapi

import (
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Budget is a token bucket per client: Burst requests at once, then one every Every.
type Budget struct {
	Every time.Duration
	Burst int
}

// Limits are the per-client budgets of the room endpoints.
type Limits struct {
	// Create bounds POST /api/room.
	Create Budget
	// Requests bounds every request to join, the photo upload and delete, and the WebSocket
	// handshake. Players of one room usually share one Wi-Fi address, so it fits a full room.
	Requests Budget
	// Misses bounds the failed lookups (404 or 401) of join and the photo endpoints: guessing room
	// codes is almost only misses, while a group joining a real room has none.
	Misses Budget
}

// DefaultLimits are the budgets used in production.
var DefaultLimits = Limits{
	Create:   Budget{Every: 30 * time.Second, Burst: 5}, // 5 rooms at once, then 2 a minute
	Requests: Budget{Every: time.Second, Burst: 60},
	Misses:   Budget{Every: 6 * time.Second, Burst: 20}, // about 10 a minute
}

const (
	// maxClients bounds the buckets kept per limiter. New clients beyond it share one overflow
	// bucket, so a distributed sweep cannot grow memory without limit.
	maxClients = 100_000
	// idleBucket is how long a bucket is kept without requests (longer than a full refill).
	idleBucket = 10 * time.Minute
	// retryAfter is the Retry-After of a 429 response, in seconds.
	retryAfter = 10
)

// clientLimiter keeps one token bucket per client address.
type clientLimiter struct {
	budget Budget

	mu        sync.Mutex
	clients   map[string]*bucket
	overflow  *bucket
	lastSweep time.Time
}

type bucket struct {
	lim  *rate.Limiter
	seen time.Time
}

func newClientLimiter(b Budget) *clientLimiter {
	return &clientLimiter{
		budget:   b,
		clients:  make(map[string]*bucket),
		overflow: &bucket{lim: rate.NewLimiter(rate.Every(b.Every), b.Burst)},
	}
}

// bucketOf returns the client's bucket, creating it if needed.
func (l *clientLimiter) bucketOf(key string, now time.Time) *rate.Limiter {
	l.mu.Lock()
	if now.Sub(l.lastSweep) > time.Minute {
		for k, b := range l.clients {
			if now.Sub(b.seen) > idleBucket {
				delete(l.clients, k)
			}
		}
		l.lastSweep = now
	}
	b, ok := l.clients[key]
	if !ok {
		if len(l.clients) >= maxClients {
			b = l.overflow
		} else {
			b = &bucket{lim: rate.NewLimiter(rate.Every(l.budget.Every), l.budget.Burst)}
			l.clients[key] = b
		}
	}
	b.seen = now
	l.mu.Unlock()
	return b.lim
}

// allow spends one token of the client's bucket and reports false when it is empty.
func (l *clientLimiter) allow(key string) bool {
	now := time.Now()
	return l.bucketOf(key, now).AllowN(now, 1)
}

// spent reports whether the client's bucket is empty, without spending a token.
func (l *clientLimiter) spent(key string) bool {
	now := time.Now()
	return l.bucketOf(key, now).TokensAt(now) < 1
}

// charge spends one token of the client's bucket, even if that empties it.
func (l *clientLimiter) charge(key string) {
	now := time.Now()
	l.bucketOf(key, now).AllowN(now, 1)
}

// clientAddress tells who sent a request: the address the connection comes from, or, when that is
// one of the trusted proxies, the last address of X-Forwarded-For that is not a trusted proxy
// (proxies append what they saw, so earlier entries are whatever the client wrote). IPv6 clients
// are grouped by their /64, which a single subscriber usually gets whole.
type clientAddress struct {
	trusted []netip.Prefix
}

func (c clientAddress) of(r *http.Request) string {
	addr := remoteIP(r.RemoteAddr)
	if addr.IsValid() && c.isTrusted(addr) {
		hops := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
		for _, hop := range slices.Backward(hops) {
			hop, err := netip.ParseAddr(strings.TrimSpace(hop))
			if err != nil {
				break
			}
			addr = hop.Unmap()
			if !c.isTrusted(addr) {
				break
			}
		}
	}
	if !addr.IsValid() {
		return r.RemoteAddr
	}
	if addr.Is6() {
		prefix, _ := addr.Prefix(64)
		return prefix.String()
	}
	return addr.String()
}

func (c clientAddress) isTrusted(addr netip.Addr) bool {
	for _, p := range c.trusted {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

func remoteIP(remoteAddr string) netip.Addr {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	return addr.Unmap()
}

func tooManyRequests(w http.ResponseWriter) {
	w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
	writeError(w, http.StatusTooManyRequests, "rate_limited")
}

// limitRequests spends a token of the client's budget on every request.
func limitRequests(l *clientLimiter, addr clientAddress, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.allow(addr.of(r)) {
			tooManyRequests(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// limitMisses spends a token only on a failed room or token lookup (404 or 401): a client whose
// budget is spent gets 429 before the lookup runs. Concurrent requests can overshoot it by at most
// what is in flight, which limitRequests bounds.
func limitMisses(l *clientLimiter, addr clientAddress, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := addr.of(r)
		if l.spent(key) {
			tooManyRequests(w)
			return
		}
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if rec.status == http.StatusNotFound || rec.status == http.StatusUnauthorized {
			l.charge(key)
		}
	})
}

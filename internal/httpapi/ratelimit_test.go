package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vhrboliveira/impostor-game/internal/room"
)

// requestFrom sends a request as if it came from remoteIP, optionally through a proxy header.
func requestFrom(t *testing.T, handler http.Handler, method, target, body, remoteIP, forwardedFor string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = remoteIP + ":40000"
	if forwardedFor != "" {
		req.Header.Set("X-Forwarded-For", forwardedFor)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func join(t *testing.T, handler http.Handler, remoteIP, forwardedFor string) int {
	t.Helper()
	return requestFrom(t, handler, http.MethodPost, "/api/room/ZZZZZ/join", `{"playerName":"Probe"}`, remoteIP, forwardedFor).Code
}

// Guessing room codes means failing lookups: each client has a small budget of them, then gets
// 429. Another client is not affected.
func TestRateLimit_FailedLookupsFromOneClient(t *testing.T) {
	h := NewRouter(Deps{Logger: discardLogger(), DB: fakePinger{}, Rooms: &fakeRooms{err: room.ErrRoomNotFound}})

	for i := range DefaultLimits.Misses.Burst {
		require.Equal(t, http.StatusNotFound, join(t, h, "198.51.100.7", ""), "miss %d", i+1)
	}
	rec := requestFrom(t, h, http.MethodPost, "/api/room/ZZZZZ/join", `{"playerName":"Probe"}`, "198.51.100.7", "")
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.Equal(t, "rate_limited", decodeBody(t, rec)["code"])
	assert.NotEmpty(t, rec.Header().Get("Retry-After"))

	assert.Equal(t, http.StatusNotFound, join(t, h, "198.51.100.8", ""), "another client keeps its budget")
}

// A whole group joining one real room from the same Wi-Fi (one public address) is not slowed down:
// only failed lookups spend the miss budget.
func TestRateLimit_AGroupOnOneAddress(t *testing.T) {
	h := NewRouter(Deps{Logger: discardLogger(), DB: fakePinger{}, Rooms: &fakeRooms{session: testSession}})

	for i := range 40 {
		require.Equal(t, http.StatusCreated, join(t, h, "198.51.100.7", ""), "join %d", i+1)
	}
}

func TestRateLimit_CreatingRooms(t *testing.T) {
	h := NewRouter(Deps{Logger: discardLogger(), DB: fakePinger{}, Rooms: &fakeRooms{session: testSession}})
	create := func() int {
		return requestFrom(t, h, http.MethodPost, "/api/room", `{"playerName":"Ana","totalRounds":5}`, "198.51.100.7", "").Code
	}

	for i := range DefaultLimits.Create.Burst {
		require.Equal(t, http.StatusCreated, create(), "room %d", i+1)
	}
	assert.Equal(t, http.StatusTooManyRequests, create())
}

// X-Forwarded-For names the client only when the request comes from a trusted proxy; otherwise a
// client could reset its budget by sending a different header each time.
func TestRateLimit_ClientAddress(t *testing.T) {
	exhaust := func(t *testing.T, h http.Handler, remoteIP string, forwardedFor func(i int) string) {
		t.Helper()
		for i := range DefaultLimits.Misses.Burst {
			require.Equal(t, http.StatusNotFound, join(t, h, remoteIP, forwardedFor(i)))
		}
	}

	t.Run("spoofed header is ignored", func(t *testing.T) {
		h := NewRouter(Deps{Logger: discardLogger(), DB: fakePinger{}, Rooms: &fakeRooms{err: room.ErrRoomNotFound}})
		exhaust(t, h, "198.51.100.7", func(i int) string { return "203.0.113." + string(rune('a'+i)) })
		assert.Equal(t, http.StatusTooManyRequests, join(t, h, "198.51.100.7", "203.0.113.200"))
	})

	t.Run("trusted proxy", func(t *testing.T) {
		h := NewRouter(Deps{
			Logger: discardLogger(), DB: fakePinger{}, Rooms: &fakeRooms{err: room.ErrRoomNotFound},
			TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
		})
		exhaust(t, h, "10.1.1.1", func(int) string { return "203.0.113.5" })
		assert.Equal(t, http.StatusTooManyRequests, join(t, h, "10.1.1.1", "203.0.113.5"))
		assert.Equal(t, http.StatusNotFound, join(t, h, "10.1.1.1", "203.0.113.6"), "another client behind the proxy")
		// The proxy appends the address it saw: earlier entries are client-supplied.
		assert.Equal(t, http.StatusTooManyRequests, join(t, h, "10.1.1.1", "1.2.3.4, 203.0.113.5"))
	})

	t.Run("IPv6 clients are grouped by /64", func(t *testing.T) {
		h := NewRouter(Deps{Logger: discardLogger(), DB: fakePinger{}, Rooms: &fakeRooms{err: room.ErrRoomNotFound}})
		for i := range DefaultLimits.Misses.Burst {
			require.Equal(t, http.StatusNotFound, join(t, h, fmt.Sprintf("[2001:db8:1:2::%x]", i+1), ""))
		}
		assert.Equal(t, http.StatusTooManyRequests, join(t, h, "[2001:db8:1:2::ff]", ""))
	})
}

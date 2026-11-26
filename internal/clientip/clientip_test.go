package clientip_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/lutfailham96/prom-tenant-gateway/internal/clientip"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// app.Test serves requests from this peer address.
const peer = "0.0.0.0"

// resolve returns the client IP resolved for a request from the app.Test
// peer carrying the given X-Forwarded-For header lines.
func resolve(t *testing.T, trusted []string, xff ...string) string {
	t.Helper()
	r, err := clientip.New(trusted)
	require.NoError(t, err)

	app := fiber.New()
	app.Get("/", func(c *fiber.Ctx) error { return c.SendString(r.IP(c)) })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, v := range xff {
		req.Header.Add("X-Forwarded-For", v)
	}
	resp, err := app.Test(req, -1)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(body)
}

func TestNew_InvalidEntries(t *testing.T) {
	for _, entry := range []string{"not-an-ip", "10.0.0.0/33", "1.2.3", "::ffff:10.0.0.0/8"} {
		_, err := clientip.New([]string{entry})
		assert.Error(t, err, entry)
	}
}

func TestNew_HasTrustedProxies(t *testing.T) {
	r, err := clientip.New(nil)
	require.NoError(t, err)
	assert.False(t, r.HasTrustedProxies())

	r, err = clientip.New([]string{"10.0.0.0/8", " 192.0.2.1 ", "2001:db8::/32", "::ffff:10.0.0.0/104"})
	require.NoError(t, err)
	assert.True(t, r.HasTrustedProxies())
}

func TestIP(t *testing.T) {
	tests := []struct {
		name    string
		trusted []string
		xff     []string
		want    string
	}{
		{"no trusted proxies: XFF ignored", nil, []string{"9.9.9.1"}, peer},
		{"untrusted peer: XFF ignored", []string{"10.0.0.0/8"}, []string{"9.9.9.1"}, peer},
		{"trusted peer without XFF", []string{peer}, nil, peer},
		{"trusted peer, single client", []string{peer}, []string{"9.9.9.1"}, "9.9.9.1"},
		{"forged left-most entry ignored", []string{peer}, []string{"1.1.1.1, 9.9.9.1"}, "9.9.9.1"},
		{"trusted hops skipped from the right", []string{peer, "10.0.0.0/8"}, []string{"1.1.1.1, 9.9.9.1, 10.1.2.3"}, "9.9.9.1"},
		{"several header lines are one list", []string{peer}, []string{"1.1.1.1", "9.9.9.1"}, "9.9.9.1"},
		{"invalid right-most entry falls back to peer", []string{peer}, []string{"9.9.9.1, not-an-ip"}, peer},
		{"invalid entry stops at last trusted hop", []string{peer, "10.0.0.0/8"}, []string{"garbage, 10.1.2.3"}, "10.1.2.3"},
		{"all entries trusted: left-most trusted hop", []string{peer, "10.0.0.0/8"}, []string{"10.0.0.1, 10.0.0.2"}, "10.0.0.1"},
		{"empty entries skipped", []string{peer}, []string{"9.9.9.1, , "}, "9.9.9.1"},
		{"IPv4-mapped IPv6 unmapped", []string{peer}, []string{"::ffff:9.9.9.1"}, "9.9.9.1"},
		{"IPv6 client", []string{peer}, []string{"2001:db8::1"}, "2001:db8::1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, resolve(t, tt.trusted, tt.xff...))
		})
	}
}

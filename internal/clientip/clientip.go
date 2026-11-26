// Package clientip resolves the IP of the client behind trusted reverse
// proxies, for rate limiting and request logs.
package clientip

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// Resolver determines a request's client IP from the TCP peer and, when the
// peer is a trusted proxy, from X-Forwarded-For.
type Resolver struct {
	trusted []netip.Prefix
}

// New returns a Resolver trusting the given proxy IPs and CIDRs. Invalid
// entries are an error, so a typo cannot silently disable proxy handling.
func New(trustedProxies []string) (*Resolver, error) {
	r := &Resolver{}
	for _, entry := range trustedProxies {
		entry = strings.TrimSpace(entry)
		if strings.Contains(entry, "/") {
			prefix, err := netip.ParsePrefix(entry)
			if err != nil {
				return nil, fmt.Errorf("invalid trusted proxy CIDR %q: %w", entry, err)
			}
			if prefix.Addr().Is4In6() {
				// ::ffff:a.b.c.d/n: keep only the IPv4 part so it matches
				// unmapped peer addresses.
				bits := prefix.Bits() - 96
				if bits < 0 {
					return nil, fmt.Errorf("invalid trusted proxy CIDR %q: IPv4-mapped prefix shorter than /96", entry)
				}
				prefix = netip.PrefixFrom(prefix.Addr().Unmap(), bits)
			}
			r.trusted = append(r.trusted, prefix.Masked())
			continue
		}
		addr, err := netip.ParseAddr(entry)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy IP %q: %w", entry, err)
		}
		addr = addr.Unmap()
		r.trusted = append(r.trusted, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return r, nil
}

// HasTrustedProxies reports whether any trusted proxy is configured. Without
// one, X-Forwarded-For is always ignored.
func (r *Resolver) HasTrustedProxies() bool {
	return len(r.trusted) > 0
}

func (r *Resolver) isTrusted(addr netip.Addr) bool {
	for _, p := range r.trusted {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// IP returns the client IP of the request.
//
// If the TCP peer is not a trusted proxy, its address is the client IP and
// X-Forwarded-For is ignored. Otherwise X-Forwarded-For (all header lines, in
// order) is walked from right to left, skipping trusted proxies, and the
// first untrusted address is the client IP. Walking from the right means only
// entries written by trusted proxies are used: a client cannot pick its
// rate-limit key by prepending forged entries, whether the proxy overwrites
// or appends to the header. An unparsable entry stops the walk and the last
// trusted hop is returned.
//
// The returned string never aliases request buffers, so it is safe to keep
// (e.g. as a map key) after the request.
func (r *Resolver) IP(c *fiber.Ctx) string {
	rawPeer := c.Context().RemoteIP()
	peer, ok := netip.AddrFromSlice(rawPeer)
	if !ok {
		return rawPeer.String()
	}
	peer = peer.Unmap()
	if len(r.trusted) == 0 || !r.isTrusted(peer) {
		return peer.String()
	}

	client := peer
	lines := c.Request().Header.PeekAll(fiber.HeaderXForwardedFor)
walk:
	for i := len(lines) - 1; i >= 0; i-- {
		rest := string(lines[i])
		for rest != "" {
			var entry string
			if j := strings.LastIndexByte(rest, ','); j >= 0 {
				entry, rest = rest[j+1:], rest[:j]
			} else {
				entry, rest = rest, ""
			}
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			addr, err := netip.ParseAddr(entry)
			if err != nil {
				break walk
			}
			client = addr.Unmap()
			if !r.isTrusted(client) {
				break walk
			}
		}
	}
	return client.String()
}

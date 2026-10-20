package handlers

import (
	"net"
	"net/http"
	"net/netip"
	"strings"

	"app/internal/config"
)

// clientIPResolver extracts the IP address of the respondent behind a
// request. X-Forwarded-For is trusted only when the direct peer
// (RemoteAddr) matches one of trustedProxies (config.SecurityConfig); from
// any other peer the header is attacker-controlled and trivial to forge, so
// it is ignored and the direct peer is used instead. The zero value trusts
// no proxy, which is the safe default.
type clientIPResolver struct {
	trustedProxies []netip.Prefix
}

// newClientIPResolver builds a clientIPResolver from cfg. A malformed
// trusted_proxies entry is reported by config.Config.validate at startup;
// here it is treated as "trust nothing", the fail-safe behavior.
func newClientIPResolver(cfg config.SecurityConfig) clientIPResolver {
	prefixes, _ := cfg.TrustedProxyPrefixes()
	return clientIPResolver{trustedProxies: prefixes}
}

// peer returns the direct TCP peer of r, or "" when RemoteAddr does not
// parse as host:port (shouldn't happen outside tests).
func (res clientIPResolver) peer(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || host == "" {
		return ""
	}
	return host
}

// resolve returns the respondent's IP address: the first X-Forwarded-For
// entry when the peer is a trusted proxy, the peer itself otherwise.
func (res clientIPResolver) resolve(r *http.Request) string {
	host := res.peer(r)
	if host == "" {
		return ""
	}
	addr, err := netip.ParseAddr(host)
	if err != nil || !res.trusted(addr) {
		return host
	}
	fwd := r.Header.Get("X-Forwarded-For")
	if fwd == "" {
		return host
	}
	if first := strings.TrimSpace(strings.SplitN(fwd, ",", 2)[0]); first != "" {
		return first
	}
	return host
}

// resolvePtr is resolve as a *string, nil when empty, for storage as
// submission metadata.
func (res clientIPResolver) resolvePtr(r *http.Request) *string {
	if ip := res.resolve(r); ip != "" {
		return &ip
	}
	return nil
}

// limitKey is resolve with a non-empty fallback (the raw RemoteAddr), so
// malformed peers still get their own rate-limit bucket instead of sharing
// one keyed by the empty string.
func (res clientIPResolver) limitKey(r *http.Request) string {
	if ip := res.resolve(r); ip != "" {
		return ip
	}
	return r.RemoteAddr
}

func (res clientIPResolver) trusted(addr netip.Addr) bool {
	for _, p := range res.trustedProxies {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

package center

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// clientIP returns the source IP used for login throttling.
//
// By default it trusts only the raw connection address (RemoteAddr), because
// X-Forwarded-For is attacker-controlled and would otherwise let a guesser
// dodge the per-IP lockout by spoofing the header. When the connection comes
// from a configured trusted proxy, it walks X-Forwarded-For right-to-left and
// returns the first address that is not itself a trusted proxy — the real client
// as seen by the outermost trusted hop.
func (s *Server) clientIP(r *http.Request) string {
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		host = h
	}
	remote, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	if len(s.cfg.TrustedProxies) == 0 || !ipInAny(remote, s.cfg.TrustedProxies) {
		return remote.String()
	}

	parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(parts) - 1; i >= 0; i-- {
		ip, err := netip.ParseAddr(strings.TrimSpace(parts[i]))
		if err != nil {
			continue
		}
		if !ipInAny(ip, s.cfg.TrustedProxies) {
			return ip.String()
		}
	}
	return remote.String()
}

func ipInAny(ip netip.Addr, prefixes []netip.Prefix) bool {
	for _, p := range prefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

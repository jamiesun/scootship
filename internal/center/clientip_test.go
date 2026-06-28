package center

import (
	"net/http"
	"net/netip"
	"testing"

	"github.com/jamiesun/scootship/internal/config"
)

func mustPrefixes(t *testing.T, cidrs ...string) []netip.Prefix {
	t.Helper()
	var out []netip.Prefix
	for _, c := range cidrs {
		p, err := netip.ParsePrefix(c)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

func TestClientIP(t *testing.T) {
	cases := []struct {
		name    string
		proxies []netip.Prefix
		remote  string
		xff     string
		want    string
	}{
		{
			name:   "no proxy config uses remote addr",
			remote: "203.0.113.7:5000",
			xff:    "1.1.1.1",
			want:   "203.0.113.7",
		},
		{
			name:    "untrusted remote ignores spoofed xff",
			proxies: mustPrefixes(t, "10.0.0.0/8"),
			remote:  "203.0.113.7:5000",
			xff:     "1.1.1.1",
			want:    "203.0.113.7",
		},
		{
			name:    "trusted proxy yields real client from xff",
			proxies: mustPrefixes(t, "10.0.0.0/8"),
			remote:  "10.1.2.3:5000",
			xff:     "203.0.113.9, 10.9.9.9",
			want:    "203.0.113.9",
		},
		{
			name:    "skips chained trusted proxies right to left",
			proxies: mustPrefixes(t, "10.0.0.0/8"),
			remote:  "10.1.2.3:5000",
			xff:     "198.51.100.4, 10.8.8.8, 10.9.9.9",
			want:    "198.51.100.4",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{cfg: config.Config{TrustedProxies: tc.proxies}}
			r := &http.Request{RemoteAddr: tc.remote, Header: http.Header{}}
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}
			if got := s.clientIP(r); got != tc.want {
				t.Fatalf("clientIP=%q want %q", got, tc.want)
			}
		})
	}
}

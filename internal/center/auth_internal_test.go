package center

import "testing"

func TestSafeNext(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: "/"},
		{name: "relative", in: "nodes/n-1", want: "/"},
		{name: "scheme relative", in: "//evil.example", want: "/"},
		{name: "absolute URL", in: "https://evil.example", want: "/"},
		{name: "backslash", in: `/\evil`, want: "/"},
		{name: "control char", in: "/nodes/\x01", want: "/"},
		{name: "local path", in: "/nodes/n-1", want: "/nodes/n-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := safeNext(tt.in); got != tt.want {
				t.Fatalf("safeNext(%q)=%q want %q", tt.in, got, tt.want)
			}
		})
	}
}

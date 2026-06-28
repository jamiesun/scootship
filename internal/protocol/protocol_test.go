package protocol

import (
	"encoding/json"
	"testing"
)

func TestEnvelopeValidate(t *testing.T) {
	cases := []struct {
		name string
		env  Envelope
		ok   bool
	}{
		{"good status", Envelope{V: 1, Type: TypeStatus, NodeID: "n-1"}, true},
		{"good audit", Envelope{V: 1, Type: TypeAuditBatch, NodeID: "n-1"}, true},
		{"bad version", Envelope{V: 2, Type: TypeStatus, NodeID: "n-1"}, false},
		{"missing node", Envelope{V: 1, Type: TypeStatus}, false},
		{"unknown type", Envelope{V: 1, Type: "shell", NodeID: "n-1"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.env.Validate()
			if tc.ok && err != nil {
				t.Fatalf("expected valid, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("expected invalid, got nil")
			}
		})
	}
}

func TestCursorAfter(t *testing.T) {
	base := Cursor{FileGen: 3, ByteTo: 40960}
	cases := []struct {
		name string
		c    Cursor
		want bool
	}{
		{"same range is duplicate", Cursor{FileGen: 3, ByteTo: 40960}, false},
		{"older byte is duplicate", Cursor{FileGen: 3, ByteTo: 1024}, false},
		{"newer byte advances", Cursor{FileGen: 3, ByteTo: 61440}, true},
		{"rotation advances even with smaller byte", Cursor{FileGen: 4, ByteTo: 1024}, true},
		{"older generation is duplicate", Cursor{FileGen: 2, ByteTo: 999999}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.c.After(base); got != tc.want {
				t.Fatalf("After=%v want %v", got, tc.want)
			}
		})
	}
}

func TestEnvelopeIgnoresUnknownFields(t *testing.T) {
	raw := `{"v":1,"type":"status","node_id":"n-1","sent_ts":1,"body":{},"future_field":42}`
	var env Envelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := env.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

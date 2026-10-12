package tlv

import (
	"testing"

	enc "github.com/named-data/ndnd/std/encoding"
)

func TestIsWkspKeyCertName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"/alice/wksp/bob/KEY/k1/anchor/v=1", true},
		{"/alice/wksp/32=owner/KEY/k1/anchor/v=1", true},
		{"/alice/KEY/k1/identity/v=1", false},
		{"/alice/wksp/KEY", false},
		{"/alice/not-wksp/bob/KEY/k1/anchor/v=1", false},
		{"/alice/wksp/bob/not-KEY/k1/anchor/v=1", false},
		{"/alice/wksp/bob/KEY/k1/identity/v=1", false},
		{"/alice/wksp/bob/KEY/k1/anchor/not-version", false},
		{"/wksp/bob/KEY/k1/anchor/v=1", false},
		{"/alice/wksp/KEY/k1/anchor/v=1", false},
	}
	for _, test := range tests {
		name, err := enc.NameFromStr(test.name)
		if err != nil {
			t.Fatalf("parse %q: %v", test.name, err)
		}
		if got := IsWkspKeyCertName(name); got != test.want {
			t.Errorf("IsWkspKeyCertName(%q) = %v, want %v", test.name, got, test.want)
		}
	}
}

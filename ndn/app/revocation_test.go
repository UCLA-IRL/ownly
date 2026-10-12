package app

import (
	"testing"

	enc "github.com/named-data/ndnd/std/encoding"
)

func TestBytesEq(t *testing.T) {
	if !bytesEq([]byte{1, 2, 3}, []byte{1, 2, 3}) {
		t.Fatal("equal byte slices did not match")
	}
	if bytesEq([]byte{1, 2, 3}, []byte{1, 2, 4}) {
		t.Fatal("different byte slices matched")
	}
	if bytesEq([]byte{1}, []byte{1, 2}) {
		t.Fatal("different-length byte slices matched")
	}
}

func TestCertRevokedPayload(t *testing.T) {
	name, err := enc.NameFromStr("/alice/wksp/bob/KEY/k1/anchor/v=1")
	if err != nil {
		t.Fatal(err)
	}
	record := &RevocationRecord{
		Reason:         9,
		InvalidityTime: 42,
		CertHash:       []byte{0x01, 0xab},
		CertName:       name,
	}
	payload := certRevokedPayload(name, record)
	if payload["cert_name"] != name.String() {
		t.Fatalf("cert_name = %v, want %s", payload["cert_name"], name)
	}
	if payload["reason"] != 9 || payload["invalidity_time"] != 42 {
		t.Fatalf("unexpected payload: %#v", payload)
	}
	if payload["cert_hash"] != "01ab" {
		t.Fatalf("cert_hash = %v, want 01ab", payload["cert_hash"])
	}
}

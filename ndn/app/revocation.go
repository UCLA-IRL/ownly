// Revocation values exposed through the JS API. Durable state is stored by
// ndnd's TrustConfig in the keychain store.

package app

import enc "github.com/named-data/ndnd/std/encoding"

type RevocationRecord struct {
	Reason         uint8
	InvalidityTime uint64
	CertHash       []byte
	CertName       enc.Name
}

func bytesEq(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type revocationListEntry struct {
	Name enc.Name
	Rec  *RevocationRecord
}

// certRevokedPayload builds the JS-callback payload for a revocation.
func certRevokedPayload(certName enc.Name, rec *RevocationRecord) map[string]any {
	certNameStr := ""
	if len(certName) > 0 {
		certNameStr = certName.String()
	}
	return map[string]any{
		"reason":          int(rec.Reason),
		"invalidity_time": int(rec.InvalidityTime),
		"cert_hash":       encHex(rec.CertHash),
		"cert_name":       certNameStr,
	}
}

// encHex hex-encodes a byte slice for JSON transport. Using hex
// instead of the old base32 (the cert hash is opaque to the UI).
func encHex(b []byte) string {
	const hex = "0123456789abcdef"
	if len(b) == 0 {
		return ""
	}
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[i*2] = hex[c>>4]
		out[i*2+1] = hex[c&0x0F]
	}
	return string(out)
}

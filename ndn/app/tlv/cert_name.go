package tlv

import (
	enc "github.com/named-data/ndnd/std/encoding"
	"github.com/named-data/ndnd/std/security"
)

// IsWkspKeyCertName distinguishes workspace-scoped certificates from
// personal identity certificates. Revocation never targets an idKey.
func IsWkspKeyCertName(name enc.Name) bool {
	if len(name) < 7 || !name.At(-4).IsGeneric("KEY") ||
		!name.At(-2).IsGeneric("anchor") || !name.At(-1).IsVersion() {
		return false
	}
	identity, err := security.GetIdentityFromCertName(name)
	if err != nil {
		return false
	}
	for i, comp := range identity {
		if comp.IsGeneric("wksp") {
			return i > 0 && i < len(identity)-1
		}
	}
	return false
}

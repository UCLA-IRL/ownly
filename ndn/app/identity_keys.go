//go:build js && wasm

package app

import (
	"crypto/elliptic"
	"encoding/json"
	"fmt"
	"syscall/js"
	"time"

	enc "github.com/named-data/ndnd/std/encoding"
	"github.com/named-data/ndnd/std/log"
	"github.com/named-data/ndnd/std/ndn"
	spec "github.com/named-data/ndnd/std/ndn/spec_2022"
	"github.com/named-data/ndnd/std/security"
	sig "github.com/named-data/ndnd/std/security/signer"
	jsutil "github.com/named-data/ndnd/std/utils/js"
)

var identityIssuer = enc.NewGenericComponent("identity")
var ephemeralIssuer = enc.NewGenericComponent("ephemeral")

const peerIndexKey = "/local/peer-identities"                      // value: peerPublishIndex
const fastJoinIndexKey = "/local/fast-join-ephemeral-certificates" // value: peerPublishIndex
const localIdentityPublishIndexKey = "/local/published-local-identity-certificates"

type peerPublishIndex map[string]map[string]bool // cert -> group -> published

type identityEntry struct {
	Identity   string
	KeyName    string
	CertName   string
	HasPrivate bool
	Source     string // "local" or "peer"
}

func (e identityEntry) toJs() map[string]any {
	return map[string]any{
		"identity":   e.Identity,
		"keyName":    e.KeyName,
		"certName":   e.CertName,
		"hasPrivate": e.HasPrivate,
		"source":     e.Source,
	}
}

func (a *App) identityName() (enc.Name, error) {
	signer, _ := a.GetTestbedKey()
	if signer == nil {
		return nil, fmt.Errorf("no testbed key, cannot derive identity key name")
	}
	return signer.KeyName().Prefix(-2), nil
}

func (a *App) getIdentitySigner() (ndn.Signer, error) {
	entries, err := a.localIdentityEntries()
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no identity key found")
	}

	primary := selectPrimaryIdentityEntry(entries)
	keyName, err := enc.NameFromStr(primary.KeyName)
	if err != nil {
		return nil, err
	}
	idName, err := security.GetIdentityFromKeyName(keyName)
	if err != nil {
		return nil, err
	}

	id := a.keychain.IdentityByName(idName)
	if id == nil {
		return nil, fmt.Errorf("identity key not found")
	}
	for _, key := range id.Keys() {
		if key.KeyName().Equal(keyName) {
			return key.Signer(), nil
		}
	}

	return nil, fmt.Errorf("identity key not found")
}

// getIdentitySignerForWorkspace resolves the identity signer for `wkspName`,
// preferring a fast-join ephemeral cert if the local keychain has one (per
// the `#wksp_detect_key: ...32=fast... <= #ephemeral_cert` rule). The bool
// return indicates whether the chosen signer came from the fast-join path,
// so callers do not need to re-derive this from the signer's cert shape.
func (a *App) getIdentitySignerForWorkspace(wkspName enc.Name) (ndn.Signer, bool, error) {
	for _, id := range a.keychain.Identities() {
		idName := id.Name()
		if len(idName) == 0 {
			continue
		}
		detect := wkspName.
			Append(enc.NewKeywordComponent("KD")).
			Append(enc.NewKeywordComponent("fast")).
			Append(idName...)
		if signer := a.trust.Suggest(detect); signer != nil {
			return signer, true, nil
		}
	}
	signer, err := a.getIdentitySigner()
	return signer, false, err
}

func (a *App) identityCertNameForSigner(signer ndn.Signer) (enc.Name, error) {
	if signer == nil {
		return nil, fmt.Errorf("no identity signer")
	}
	// Fast path: ContextSigner with a bound cert locator (e.g. from
	// trust.Suggest or sig.WithKeyLocator) already names its cert.
	if name := signer.KeyLocator(); len(name) > 0 && !name.Equal(signer.KeyName()) {
		return name, nil
	}
	// Fallback: signer is a raw key signer with no cert locator bound
	// (e.g. from getIdentitySigner()); find any cert in the keychain for
	// this key.
	idName, err := security.GetIdentityFromKeyName(signer.KeyName())
	if err != nil {
		return nil, err
	}
	id := a.keychain.IdentityByName(idName)
	if id == nil {
		return nil, fmt.Errorf("identity key not found")
	}
	for _, key := range id.Keys() {
		if !key.KeyName().Equal(signer.KeyName()) {
			continue
		}
		// Find the latest valid self-signed cert for this key. Picking the
		// first cert in insertion order can mis-identify an older cert
		// (e.g. after a key rotation), which then fails signature checks
		// elsewhere because the wire shipped by the producer was the
		// newest cert.
		var bestCert enc.Name
		var bestVersion uint64
		for _, cert := range key.UniqueCerts() {
			wire, _ := a.store.Get(cert.Prefix(-1), true)
			if wire == nil {
				continue
			}
			certData, _, err := spec.Spec{}.ReadData(enc.NewWireView(enc.Wire{wire}))
			if err != nil {
				continue
			}
			if security.CertIsExpired(certData) {
				continue
			}
			// Cert name is /<key>/<issuer-id>/<version>; the version is
			// the last component.
			version := certData.Name().At(-1).NumberVal()
			if bestCert == nil || version > bestVersion {
				bestCert = cert.Prefix(-1)
				bestVersion = version
			}
		}
		if bestCert != nil {
			return bestCert, nil
		}
	}
	return nil, fmt.Errorf("identity certificate not found for %s", signer.KeyName())
}

func identityCertIsSelfSigned(certData ndn.Data, sigCov enc.Wire) bool {
	if certData == nil {
		return false
	}
	valid, err := sig.ValidateData(certData, sigCov, certData)
	return err == nil && valid
}

func (a *App) makeIdentityCert(signer ndn.Signer) (enc.Wire, ndn.Data, error) {
	certName := signer.KeyName().Append(identityIssuer)
	ctxSigner := sig.WithKeyLocator(signer, certName)
	wire, err := security.SelfSign(security.SignCertArgs{
		Signer:    ctxSigner,
		IssuerId:  identityIssuer,
		NotBefore: time.Now().Add(-time.Hour),
		NotAfter:  time.Now().AddDate(10, 0, 0), // 10 year is enough?
	})
	if err != nil {
		return nil, nil, err
	}

	certData, _, err := spec.Spec{}.ReadData(enc.NewWireView(wire))
	return wire, certData, err
}

// localIdCertWire returns the wire and parsed cert name for the local user's
// self-signed /identity/<ver> IDCERT, or an error if none exists. Used by
// participant-side code that needs to ship the invitee's IDCERT (not the
// fast-join ephemeral cert) to the owner via the BootJoin payload and SVS
// boot group.
func (a *App) localIdCertWire() (enc.Wire, enc.Name, error) {
	entries, err := a.localIdentityEntries()
	if err != nil {
		return nil, nil, err
	}
	if len(entries) == 0 {
		return nil, nil, fmt.Errorf("no local identity certificate found")
	}
	// Prefer the entry whose signer has a private key, but localIdentityEntries
	// already filters to HasPrivate=true, so any entry works. Pick the latest
	// by cert name version for stability.
	var bestName enc.Name
	var bestVersion uint64
	for _, e := range entries {
		name, err := enc.NameFromStr(e.CertName)
		if err != nil {
			continue
		}
		v := name.At(-1).NumberVal()
		if bestName == nil || v > bestVersion {
			bestName = name
			bestVersion = v
		}
	}
	if bestName == nil {
		return nil, nil, fmt.Errorf("no usable local identity certificate name")
	}
	wire, _, err := a.certWireByName(bestName)
	if err != nil {
		return nil, nil, err
	}
	return wire, bestName, nil
}

func (a *App) makeOwnerSignedIdentityCert(subjectSigner ndn.Signer, ownerSigner ndn.Signer, ownerCertName enc.Name) (enc.Wire, ndn.Data, error) {
	if subjectSigner == nil {
		return nil, nil, fmt.Errorf("no subject signer")
	}
	if ownerSigner == nil || len(ownerCertName) == 0 {
		return nil, nil, fmt.Errorf("no owner signer")
	}

	subjectSecret, err := sig.MarshalSecretToData(subjectSigner)
	if err != nil {
		return nil, nil, err
	}
	ownerCtxSigner := sig.WithKeyLocator(ownerSigner, ownerCertName)
	wire, err := security.SignCert(security.SignCertArgs{
		Data:      subjectSecret,
		Signer:    ownerCtxSigner,
		IssuerId:  ephemeralIssuer,
		NotBefore: time.Now().Add(-time.Hour),
		NotAfter:  time.Now().AddDate(0, 0, 14),
	})
	if err != nil {
		return nil, nil, err
	}

	certData, _, err := spec.Spec{}.ReadData(enc.NewWireView(wire))
	return wire, certData, err
}

func (a *App) generateIdentityKey() (identityEntry, error) {
	idName, err := a.identityName()
	if err != nil {
		return identityEntry{}, err
	}

	keyName := security.MakeKeyName(idName)
	signer, err := sig.KeygenEcc(keyName, elliptic.P256())
	if err != nil {
		return identityEntry{}, err
	}

	if err = a.keychain.InsertKey(signer); err != nil {
		return identityEntry{}, err
	}

	certWire, certData, err := a.makeIdentityCert(signer)
	if err != nil {
		return identityEntry{}, err
	}
	if err = a.keychain.InsertCert(certWire.Join()); err != nil {
		return identityEntry{}, err
	}

	entry := identityEntry{
		Identity:   idName.String(),
		KeyName:    signer.KeyName().String(),
		CertName:   certData.Name().String(),
		HasPrivate: true,
		Source:     "local",
	}
	a.publishPendingBootPeers()
	return entry, nil
}

func (a *App) importIdentityKey(secret []byte) (identityEntry, error) {
	signers, _, err := security.DecodeFile(secret)
	if err != nil {
		return identityEntry{}, err
	}
	if len(signers) == 0 {
		return identityEntry{}, fmt.Errorf("No signing key found")
	}

	idName, err := a.identityName()
	if err != nil {
		return identityEntry{}, err
	}

	for _, signer := range signers {
		if signer == nil {
			continue
		}

		signerId, err := security.GetIdentityFromKeyName(signer.KeyName())
		if err != nil {
			continue
		}
		if !signerId.Equal(idName) {
			return identityEntry{}, fmt.Errorf("Identity key must use %s", idName)
		}
		if ok, err := a.localIdentityHasKeyName(signer.KeyName()); err != nil {
			return identityEntry{}, err
		} else if ok {
			return identityEntry{}, fmt.Errorf("Identity key already exists: %s", signer.KeyName())
		}
		if ok, err := a.peerCertHasKeyName(signer.KeyName()); err != nil {
			return identityEntry{}, err
		} else if ok {
			return identityEntry{}, fmt.Errorf("Key name already exists as peer cert: %s", signer.KeyName())
		}

		if err = a.keychain.InsertKey(signer); err != nil {
			return identityEntry{}, err
		}
		certWire, certData, err := a.makeIdentityCert(signer)
		if err != nil {
			return identityEntry{}, err
		}
		if err = a.keychain.InsertCert(certWire.Join()); err != nil {
			return identityEntry{}, err
		}

		entry := identityEntry{
			Identity:   idName.String(),
			KeyName:    signer.KeyName().String(),
			CertName:   certData.Name().String(),
			HasPrivate: true,
			Source:     "local",
		}
		a.publishPendingBootPeers()
		return entry, nil
	}

	return identityEntry{}, fmt.Errorf("No usable identity key found")
}

func (a *App) importFastJoinIdentity(secret []byte, certWire []byte, ownerCertWire []byte) (identityEntry, error) {
	ownerCertData, _, err := a.decodeAndValidateCert(ownerCertWire, enc.Component{}, true)
	if err != nil {
		return identityEntry{}, err
	}
	if err := a.insertCertIfAbsent(ownerCertData.Name(), ownerCertWire); err != nil {
		return identityEntry{}, err
	}
	a.trust.PromoteAnchor(ownerCertData, enc.Wire{ownerCertWire})
	// Also record the owner cert in the peer index so the owner surfaces in
	// the Authenticated Peers UI. The empty group key is the conventional
	// "this peer cert is anchored globally, not tied to a specific group"
	// marker that forgetCertsFromIndex already handles.
	peerIndex := a.loadPublishIndex(peerIndexKey)
	peerIndex.ensureGroup(ownerCertData.Name().String(), "", true)
	if err := a.persistPublishIndex(peerIndexKey, peerIndex); err != nil {
		log.Warn(a, "Failed to persist peer index for fast-join owner cert", "err", err)
	}

	signers, _, err := security.DecodeFile(secret)
	if err != nil {
		return identityEntry{}, err
	}
	if len(signers) == 0 {
		return identityEntry{}, fmt.Errorf("No signing key found")
	}
	if len(certWire) == 0 {
		return identityEntry{}, fmt.Errorf("No identity certificate found")
	}

	certData, sigCov, err := a.decodeAndValidateCert(certWire, ephemeralIssuer, false)
	if err != nil {
		return identityEntry{}, err
	}

	certKeyName, err := security.GetKeyNameFromCertName(certData.Name())
	if err != nil {
		return identityEntry{}, err
	}
	certIdentity, err := security.GetIdentityFromKeyName(certKeyName)
	if err != nil {
		return identityEntry{}, err
	}

	// The owner may stamp a versioned or versionless KeyLocator in the
	// ephemeral cert signature. Accept either form against the owner
	// cert's parsed name.
	keyLocator := certData.Signature().KeyName()
	if keyLocator == nil {
		return identityEntry{}, fmt.Errorf("Fast join certificate %s has no KeyLocator", certData.Name())
	}
	ownerName := ownerCertData.Name()
	if !keyLocator.Equal(ownerName) && !keyLocator.Equal(ownerName.Prefix(-1)) {
		return identityEntry{}, fmt.Errorf("Fast join certificate %s KeyLocator %s does not match owner certificate name %s", certData.Name(), keyLocator, ownerName)
	}
	valid, err := sig.ValidateData(certData, sigCov, ownerCertData)
	if err != nil || !valid {
		return identityEntry{}, fmt.Errorf("Fast join certificate %s signature does not verify under owner certificate %s: %v", certData.Name(), ownerCertData.Name(), err)
	}

	var signer ndn.Signer
	for _, candidate := range signers {
		if candidate == nil || !candidate.KeyName().Equal(certKeyName) {
			continue
		}
		signer = candidate
		break
	}
	if signer == nil {
		return identityEntry{}, fmt.Errorf("Fast join private key does not match %s", certKeyName)
	}

	id := a.keychain.IdentityByName(certIdentity)
	keyExists := false
	if id != nil {
		for _, key := range id.Keys() {
			if key.KeyName().Equal(certKeyName) && key.Signer() != nil {
				keyExists = true
				break
			}
		}
	}
	if !keyExists {
		if err = a.keychain.InsertKey(signer); err != nil {
			return identityEntry{}, err
		}
	}

	if err := a.insertCertIfAbsent(certData.Name(), certWire); err != nil {
		return identityEntry{}, err
	}

	return identityEntry{
		Identity:   certIdentity.String(),
		KeyName:    certKeyName.String(),
		CertName:   certData.Name().String(),
		HasPrivate: true,
		Source:     "local",
	}, nil
}

// decodeAndValidateCert parses a Data packet, verifies ContentType=Key and
// non-expiry, optionally enforces an expected issuer-id at At(-2), and
// optionally verifies the certificate is self-signed. When `expectedIssuer`
// is the zero value, the issuer check is skipped. When `selfSigned` is
// true, the signature is verified against the cert itself.
func (a *App) decodeAndValidateCert(wire []byte, expectedIssuer enc.Component, selfSigned bool) (ndn.Data, enc.Wire, error) {
	certData, sigCov, err := spec.Spec{}.ReadData(enc.NewWireView(enc.Wire{wire}))
	if err != nil {
		return nil, nil, err
	}
	if ctype, ok := certData.ContentType().Get(); !ok || ctype != ndn.ContentTypeKey {
		return nil, nil, fmt.Errorf("Invalid certificate content type for %s", certData.Name())
	}
	if security.CertIsExpired(certData) {
		return nil, nil, fmt.Errorf("Certificate is expired: %s", certData.Name())
	}
	if expectedIssuer.Typ != 0 || len(expectedIssuer.Val) > 0 {
		if issuer := certData.Name().At(-2); !issuer.Equal(expectedIssuer) {
			return nil, nil, fmt.Errorf("Certificate %s has issuer %s, expected %s", certData.Name(), issuer, expectedIssuer)
		}
	}
	if selfSigned {
		valid, err := sig.ValidateData(certData, sigCov, certData)
		if err != nil || !valid {
			return nil, nil, fmt.Errorf("Certificate %s is not self-signed", certData.Name())
		}
	}
	return certData, sigCov, nil
}

// insertCertIfAbsent inserts `wire` into the keychain only if a cert with the
// same name is not already present in the local store.
func (a *App) insertCertIfAbsent(name enc.Name, wire []byte) error {
	existing, _ := a.store.Get(name, false)
	if existing != nil {
		return nil
	}
	return a.keychain.InsertCert(wire)
}

func (a *App) localIdentityEntries() ([]identityEntry, error) {
	idName, err := a.identityName()
	if err != nil {
		return nil, err
	}

	id := a.keychain.IdentityByName(idName)
	if id == nil {
		return []identityEntry{}, nil
	}

	entries := make([]identityEntry, 0)
	for _, key := range id.Keys() {
		for _, cert := range key.UniqueCerts() {
			wire, _ := a.store.Get(cert.Prefix(-1), true)
			if wire == nil {
				continue
			}
			certData, sigCov, err := spec.Spec{}.ReadData(enc.NewWireView(enc.Wire{wire}))
			if err != nil {
				continue
			}
			if issuer := certData.Name().At(-2); !issuer.Equal(identityIssuer) {
				continue
			}
			if security.CertIsExpired(certData) || !identityCertIsSelfSigned(certData, sigCov) {
				continue
			}

			entries = append(entries, identityEntry{
				Identity:   idName.String(),
				KeyName:    key.KeyName().String(),
				CertName:   certData.Name().String(),
				HasPrivate: true,
				Source:     "local",
			})
		}
	}

	return entries, nil
}

func entriesToJs(entries []identityEntry) []any {
	out := make([]any, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.toJs())
	}
	return out
}

func (a *App) identityOverview() (map[string]any, error) {
	idName, err := a.identityName()
	if err != nil {
		return nil, err
	}

	local, err := a.localIdentityEntries()
	if err != nil {
		return nil, err
	}
	peers, err := a.peerIdentityEntries()
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"identity": idName.String(),
		"local":    entriesToJs(local),
		"peers":    entriesToJs(peers),
	}, nil
}

func (a *App) loadPublishIndex(key string) peerPublishIndex {
	index := make(peerPublishIndex)

	var wire []byte
	if !a.bootStateLoad.IsUndefined() && !a.bootStateLoad.IsNull() {
		if result, err := jsutil.Await(a.bootStateLoad.Invoke(js.ValueOf(key))); err == nil && result.Truthy() && !result.IsUndefined() && !result.IsNull() {
			wire = jsutil.JsArrayToSlice(result)
		}
	}
	if len(wire) == 0 {
		return index
	}

	if err := json.Unmarshal(wire, &index); err != nil {
		log.Warn(a, "Failed to decode certificate publish index", "key", key, "err", err)
	}
	return index
}

func (a *App) persistPublishIndex(key string, index peerPublishIndex) error {
	wire, err := json.Marshal(index)
	if err != nil {
		return err
	}
	if a.bootStatePersist.IsUndefined() || a.bootStatePersist.IsNull() {
		return nil
	}
	jsVal := jsutil.SliceToJsArray(wire)
	_, err = jsutil.Await(a.bootStatePersist.Invoke(js.ValueOf(key), jsVal))
	return err
}

func (a *App) loadPeerIndex() peerPublishIndex {
	return a.loadPublishIndex(peerIndexKey)
}

func (a *App) persistPeerIndex(index peerPublishIndex) error {
	return a.persistPublishIndex(peerIndexKey, index)
}

func (a *App) loadFastJoinIndex() peerPublishIndex {
	return a.loadPublishIndex(fastJoinIndexKey)
}

func (a *App) persistFastJoinIndex(index peerPublishIndex) error {
	return a.persistPublishIndex(fastJoinIndexKey, index)
}

func (a *App) peerIdentityEntries() ([]identityEntry, error) {
	index := a.loadPeerIndex()
	entries := make([]identityEntry, 0, len(index))
	updated := false

	for name := range index {
		certName, err := enc.NameFromStr(name)
		if err != nil {
			delete(index, name)
			updated = true
			continue
		}

		wire, _ := a.store.Get(certName, false)
		if wire == nil {
			delete(index, name)
			updated = true
			continue
		}

		certData, _, err := spec.Spec{}.ReadData(enc.NewWireView(enc.Wire{wire}))
		if err != nil {
			delete(index, name)
			updated = true
			continue
		}

		keyName, err := security.GetKeyNameFromCertName(certData.Name())
		if err != nil {
			delete(index, name)
			updated = true
			continue
		}
		identity, _ := security.GetIdentityFromKeyName(keyName)

		entries = append(entries, identityEntry{
			Identity:   identity.String(),
			KeyName:    keyName.String(),
			CertName:   certData.Name().String(),
			HasPrivate: false,
			Source:     "peer",
		})
	}

	if updated {
		_ = a.persistPeerIndex(index)
	}

	return entries, nil
}

func (a *App) localTrustAnchorCertNames() []string {
	out := make([]string, 0)
	seen := make(map[string]struct{})
	for _, id := range a.keychain.Identities() {
		for _, key := range id.Keys() {
			for _, cert := range key.UniqueCerts() {
				wire, _ := a.store.Get(cert.Prefix(-1), true)
				if wire == nil {
					continue
				}
				certData, sigCov, err := spec.Spec{}.ReadData(enc.NewWireView(enc.Wire{wire}))
				if err != nil {
					continue
				}
				if ctype, ok := certData.ContentType().Get(); !ok || ctype != ndn.ContentTypeKey {
					continue
				}
				if security.CertIsExpired(certData) {
					continue
				}
				valid, err := sig.ValidateData(certData, sigCov, certData)
				if err != nil || !valid {
					continue
				}
				name := certData.Name().String()
				if _, ok := seen[name]; ok {
					continue
				}
				seen[name] = struct{}{}
				out = append(out, name)
			}
		}
	}
	return out
}

func (a *App) peerTrustAnchorCertNames() []string {
	out := make([]string, 0)
	seen := make(map[string]struct{})
	for name := range a.loadPeerIndex() {
		certName, err := enc.NameFromStr(name)
		if err != nil {
			continue
		}
		wire, _ := a.store.Get(certName, false)
		if wire == nil && len(certName) > 0 {
			wire, _ = a.store.Get(certName.Prefix(-1), true)
		}
		if wire == nil {
			continue
		}
		certData, sigCov, err := spec.Spec{}.ReadData(enc.NewWireView(enc.Wire{wire}))
		if err != nil {
			continue
		}
		if ctype, ok := certData.ContentType().Get(); !ok || ctype != ndn.ContentTypeKey {
			continue
		}
		if security.CertIsExpired(certData) {
			continue
		}
		valid, err := sig.ValidateData(certData, sigCov, certData)
		if err != nil || !valid {
			continue
		}
		name := certData.Name().String()
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

func (a *App) promoteIdentityAnchors() error {
	promote := func(certNameStr string) {
		certName, err := enc.NameFromStr(certNameStr)
		if err != nil {
			log.Warn(a, "Failed to parse trust anchor name", "name", certNameStr, "err", err)
			return
		}
		wire, _ := a.store.Get(certName, false)
		if wire == nil {
			wire, _ = a.store.Get(certName.Prefix(-1), true)
		}
		if wire == nil {
			log.Warn(a, "Trust anchor certificate missing from store", "name", certNameStr)
			return
		}
		certData, _, err := spec.Spec{}.ReadData(enc.NewWireView(enc.Wire{wire}))
		if err != nil {
			log.Warn(a, "Failed to parse trust anchor certificate", "name", certNameStr, "err", err)
			return
		}
		a.trust.PromoteAnchor(certData, enc.Wire{wire})
	}

	for _, certName := range a.localTrustAnchorCertNames() {
		promote(certName)
	}
	for _, certName := range a.peerTrustAnchorCertNames() {
		promote(certName)
	}

	return nil
}

func (a *App) localIdentityHasKeyName(keyName enc.Name) (bool, error) {
	idName, err := a.identityName()
	if err != nil {
		return false, err
	}

	id := a.keychain.IdentityByName(idName)
	if id == nil {
		return false, nil
	}

	for _, key := range id.Keys() {
		if key.KeyName().Equal(keyName) {
			return true, nil
		}
	}

	return false, nil
}

func (a *App) peerCertHasKeyName(keyName enc.Name) (bool, error) {
	entries, err := a.peerIdentityEntries()
	if err != nil {
		return false, err
	}

	target := keyName.String()
	for _, entry := range entries {
		if entry.KeyName == target {
			return true, nil
		}
	}

	return false, nil
}

type peerCertImportOpts struct {
	Published bool
	Group     enc.Name
}

func (a *App) rememberFastJoinCert(certWire []byte, opts peerCertImportOpts) (enc.Name, error) {
	certData, _, err := spec.Spec{}.ReadData(enc.NewWireView(enc.Wire{certWire}))
	if err != nil {
		return nil, err
	}
	if ctype, ok := certData.ContentType().Get(); !ok || ctype != ndn.ContentTypeKey {
		return nil, fmt.Errorf("Invalid certificate content type for %s", certData.Name())
	}
	if security.CertIsExpired(certData) {
		return nil, fmt.Errorf("Certificate is expired: %s", certData.Name())
	}
	if len(certData.Name()) < 2 || !certData.Name().At(-2).Equal(ephemeralIssuer) {
		return nil, fmt.Errorf("Fast join certificate must be an ephemeral certificate: %s", certData.Name())
	}

	if existing, _ := a.store.Get(certData.Name(), false); existing == nil {
		if err := a.keychain.InsertCert(certWire); err != nil {
			return nil, err
		}
	}

	index := a.loadFastJoinIndex()
	groupStr := ""
	if len(opts.Group) > 0 {
		groupStr = opts.Group.String()
	}
	index.ensureGroup(certData.Name().String(), groupStr, opts.Published)
	if err := a.persistFastJoinIndex(index); err != nil {
		return nil, err
	}
	return certData.Name(), nil
}

// importBootJoinIdentityCert dispatches an incoming piggybacked cert from a
// BootJoin payload to the right ingestion path based on its issuer-id:
//
//   - ephemeral (`/ephemeral/<ver>`): the fast-join path — stored in the
//     fast-join index via rememberFastJoinCert.
//   - identity (`/identity/<ver>`): the participant's self-signed IDCERT —
//     ingested into the regular peer index via importPeerCerts so the owner
//     sees it in the Authenticated Peers UI.
//
// Any other issuer-id is rejected.
func (a *App) importBootJoinIdentityCert(certWire []byte, group enc.Name) (enc.Name, error) {
	certData, _, err := spec.Spec{}.ReadData(enc.NewWireView(enc.Wire{certWire}))
	if err != nil {
		return nil, err
	}
	if len(certData.Name()) < 2 {
		return nil, fmt.Errorf("Boot join identity cert has no issuer-id: %s", certData.Name())
	}
	issuer := certData.Name().At(-2)
	switch {
	case issuer.Equal(ephemeralIssuer):
		return a.rememberFastJoinCert(certWire, peerCertImportOpts{
			Published: true,
			Group:     group,
		})
	case issuer.Equal(identityIssuer):
		entries, err := a.importPeerCerts([][]byte{certWire}, peerCertImportOpts{
			Published: true,
			Group:     group,
		})
		if err != nil {
			return nil, err
		}
		if len(entries) == 0 {
			// importPeerCerts may skip if it already exists locally; that's fine.
			return certData.Name(), nil
		}
		return enc.Name{}, nil
	default:
		return nil, fmt.Errorf("Boot join identity cert has unexpected issuer-id %s: %s", issuer, certData.Name())
	}
}

func (a *App) importPeerCerts(blobs [][]byte, opts peerCertImportOpts) ([]identityEntry, error) {
	index := a.loadPeerIndex()
	groupStr := ""
	if len(opts.Group) > 0 {
		groupStr = opts.Group.String()
	}
	existingCerts := make(map[string]bool)
	existingKeys := make(map[string]bool)
	entries, err := a.peerIdentityEntries()
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		existingKeys[entry.KeyName] = true
		existingCerts[entry.CertName] = true
	}

	wires := make([][]byte, 0)
	for _, blob := range blobs {
		_, certs, err := security.DecodeFile(blob)
		if err != nil {
			return nil, err
		}
		wires = append(wires, certs...)
	}

	imported := make([]identityEntry, 0)
	for _, certWire := range wires {
		certData, sigCov, err := spec.Spec{}.ReadData(enc.NewWireView(enc.Wire{certWire}))
		if err != nil {
			return nil, err
		}
		if ctype, ok := certData.ContentType().Get(); !ok || ctype != ndn.ContentTypeKey {
			return nil, fmt.Errorf("Invalid certificate content type for %s", certData.Name())
		}
		if security.CertIsExpired(certData) {
			return nil, fmt.Errorf("Certificate is expired: %s", certData.Name())
		}

		valid, err := sig.ValidateData(certData, sigCov, certData)
		if err != nil || !valid {
			return nil, fmt.Errorf("Certificate %s is not self-signed", certData.Name())
		}

		keyName, err := security.GetKeyNameFromCertName(certData.Name())
		if err != nil {
			return nil, err
		}
		nameStr := certData.Name().String()
		keyStr := keyName.String()

		if ok, err := a.localIdentityHasKeyName(keyName); err != nil {
			return nil, err
		} else if ok {
			// Key name already exists as identity key
			continue
		}

		if existingCerts[nameStr] || existingKeys[keyStr] {
			// Just in case the cert not marked as published
			index.ensureGroup(nameStr, groupStr, opts.Published)
			a.trust.PromoteAnchor(certData, nil)
			continue
		}

		if err = a.keychain.InsertCert(certWire); err != nil {
			continue
		}
		a.trust.PromoteAnchor(certData, nil)

		existingCerts[nameStr] = true
		existingKeys[keyStr] = true
		index.ensureGroup(nameStr, groupStr, opts.Published)

		identity, _ := security.GetIdentityFromKeyName(keyName)
		imported = append(imported, identityEntry{
			Identity:   identity.String(),
			KeyName:    keyStr,
			CertName:   nameStr,
			HasPrivate: false,
			Source:     "peer",
		})
	}
	if err := a.persistPeerIndex(index); err != nil {
		return nil, err
	}

	a.publishPendingBootPeers()
	return imported, nil
}

func (a *App) exportIdentitySecret(keyName enc.Name) ([]byte, error) {
	idName, err := a.identityName()
	if err != nil {
		return nil, err
	}
	if !idName.IsPrefix(keyName) {
		return nil, fmt.Errorf("key not part of identity")
	}

	id := a.keychain.IdentityByName(idName)
	if id == nil {
		return nil, fmt.Errorf("identity key not found")
	}
	for _, key := range id.Keys() {
		if !key.KeyName().Equal(keyName) {
			continue
		}
		// Only allow exporting managed identity keys.
		hasIdentityCert := false
		for _, cert := range key.UniqueCerts() {
			wire, _ := a.store.Get(cert.Prefix(-1), true)
			if wire == nil {
				continue
			}
			certData, _, err := spec.Spec{}.ReadData(enc.NewWireView(enc.Wire{wire}))
			if err == nil && certData.Name().At(-2).Equal(identityIssuer) {
				hasIdentityCert = true
				break
			}
		}
		if !hasIdentityCert {
			continue
		}
		secret, err := sig.MarshalSecret(key.Signer())
		if err != nil {
			return nil, err
		}
		return secret.Join(), nil
	}

	return nil, fmt.Errorf("Identity key not found")
}

func (a *App) exportPeerCerts(names []enc.Name) ([][]byte, error) {
	index := a.loadPeerIndex()
	out := make([][]byte, 0, len(names))
	for _, name := range names {
		if _, ok := index[name.String()]; !ok {
			return nil, fmt.Errorf("Peer key %s not found", name)
		}
		wire, _ := a.store.Get(name, false)
		if wire == nil {
			return nil, fmt.Errorf("Peer key %s missing", name)
		}
		out = append(out, wire)
	}
	return out, nil
}

func (a *App) deleteIdentityEntry(certName enc.Name) error {
	wire, _ := a.store.Get(certName, false)
	if wire == nil {
		return fmt.Errorf("Certificate not found")
	}
	certData, _, err := spec.Spec{}.ReadData(enc.NewWireView(enc.Wire{wire}))
	if err != nil {
		return err
	}

	keyName, err := security.GetKeyNameFromCertName(certData.Name())
	if err != nil {
		return err
	}

	nameStr := certData.Name().String()
	index := a.loadPeerIndex()
	_, managedPeer := index[nameStr]
	issuerIsIdentity := certData.Name().At(-2).Equal(identityIssuer)
	if issuerIsIdentity {
		hasKey, err := a.localIdentityHasKeyName(keyName)
		if err != nil {
			return err
		}
		if hasKey {
			if err := a.keychain.DeleteKey(keyName); err != nil {
				return err
			}
		} else if err := a.keychain.DeleteCert(certData.Name()); err != nil {
			return err
		}
	} else if err := a.keychain.DeleteCert(certData.Name()); err != nil {
		return err
	}

	if managedPeer {
		delete(index, nameStr)
		if err := a.persistPeerIndex(index); err != nil {
			return err
		}
	}

	return nil
}

func selectPrimaryIdentityEntry(entries []identityEntry) identityEntry {
	best := entries[0]
	bestVer := certVersion(best.CertName)
	for i := 1; i < len(entries); i++ {
		entry := entries[i]
		ver := certVersion(entry.CertName)
		if ver > bestVer || (ver == bestVer && entry.CertName > best.CertName) {
			best = entry
			bestVer = ver
		}
	}
	return best
}

func (a *App) exportIdentityCertByName(certName enc.Name) ([]byte, error) {
	wire, _ := a.store.Get(certName, false)
	if wire == nil && len(certName) > 0 {
		// Fallback for version-less names: return the latest cert under the prefix.
		wire, _ = a.store.Get(certName.Prefix(-1), true)
	}
	if wire == nil {
		return nil, fmt.Errorf("identity certificate not found in store")
	}
	return wire, nil
}

func (a *App) exportIdentityCert() ([]byte, error) {
	entries, err := a.localIdentityEntries()
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no identity certificate found")
	}

	best := selectPrimaryIdentityEntry(entries)

	name, err := enc.NameFromStr(best.CertName)
	if err != nil {
		return nil, err
	}
	wire, _ := a.store.Get(name, false)
	if wire == nil {
		return nil, fmt.Errorf("identity certificate not found in store")
	}
	return wire, nil
}

func certVersion(nameStr string) uint64 {
	name, err := enc.NameFromStr(nameStr)
	if err != nil || len(name) == 0 {
		return 0
	}
	return name.At(-1).NumberVal()
}

func (idx peerPublishIndex) ensureGroup(cert, group string, published bool) {
	if cert == "" {
		return
	}
	groups, ok := idx[cert]
	if !ok {
		groups = make(map[string]bool)
		idx[cert] = groups
	}
	if _, exists := groups[group]; !exists {
		groups[group] = published
	} else if published {
		groups[group] = true
	}
}

func (idx peerPublishIndex) publishedInGroup(cert, group string) bool {
	if groups, ok := idx[cert]; ok {
		if v, exists := groups[group]; exists {
			return v
		}
	}
	return false
}

func (idx peerPublishIndex) certNamesForGroup(group string) []enc.Name {
	out := make([]enc.Name, 0, len(idx))
	for cert, groups := range idx {
		if _, ok := groups[group]; !ok {
			if _, ok := groups[""]; !ok {
				continue
			}
		}
		certName, err := enc.NameFromStr(cert)
		if err != nil {
			continue
		}
		out = append(out, certName)
	}
	return out
}

func (idx peerPublishIndex) knownLatestCertPrefixForGroup(prefix enc.Name, group string) bool {
	_, ok := idx.latestCertForPrefixInGroup(prefix, group)
	return ok
}

func (idx peerPublishIndex) latestCertForPrefixInGroup(prefix enc.Name, group string) (enc.Name, bool) {
	if len(prefix) == 0 {
		return nil, false
	}

	prefixKeyName := prefix
	if keyName, err := security.GetKeyNameFromCertName(prefix); err == nil && keyName != nil {
		prefixKeyName = keyName
	} else if len(prefix) >= 3 && prefix.At(-2).String() == "KEY" {
		// Already a key name (3 components: /id/KEY/<kid>).
	} else {
		return nil, false
	}
	prefixIdentity, err := security.GetIdentityFromKeyName(prefixKeyName)
	if err != nil || prefixIdentity == nil {
		return nil, false
	}

	var latestCert enc.Name
	var latestKey enc.Name
	var latestVersion uint64
	for cert, groups := range idx {
		if _, ok := groups[group]; !ok {
			if _, ok := groups[""]; !ok {
				continue
			}
		}

		certName, err := enc.NameFromStr(cert)
		if err != nil {
			continue
		}
		keyName, err := security.GetKeyNameFromCertName(certName)
		if err != nil || keyName == nil {
			continue
		}
		identity, err := security.GetIdentityFromKeyName(keyName)
		if err != nil || identity == nil || !identity.Equal(prefixIdentity) {
			continue
		}

		ver := certVersion(cert)
		if latestCert == nil || ver > latestVersion || (ver == latestVersion && cert > latestCert.String()) {
			latestCert = certName
			latestKey = keyName
			latestVersion = ver
		}
	}

	if latestCert == nil {
		return nil, false
	}
	if prefix.Equal(latestKey) || prefix.IsPrefix(latestCert) || latestCert.IsPrefix(prefix) {
		return latestCert, true
	}
	return nil, false
}

// forgetCertsFromIndex removes entries from `index` whose cert belongs to
// `identity` and whose group set contains `group` (or ""). Matching groups
// are deleted; if a cert's group set becomes empty, the cert is removed from
// the index and the keychain. `keepCert` is excluded from deletion. The
// caller is responsible for persisting the resulting index. `deleteWarnMsg`
// is the log string used when keychain.DeleteCert fails.
func (a *App) forgetCertsFromIndex(
	index peerPublishIndex,
	identity, group, keepCert enc.Name,
	deleteWarnMsg string,
) (peerPublishIndex, bool) {
	groupStr := group.String()
	keepCertStr := ""
	if len(keepCert) > 0 {
		keepCertStr = keepCert.String()
	}
	changed := false

	for cert, groups := range index {
		if cert == keepCertStr {
			continue
		}
		if _, ok := groups[groupStr]; !ok {
			if _, ok := groups[""]; !ok {
				continue
			}
		}

		certName, err := enc.NameFromStr(cert)
		if err != nil {
			delete(index, cert)
			changed = true
			continue
		}
		keyName, err := security.GetKeyNameFromCertName(certName)
		if err != nil || keyName == nil {
			delete(index, cert)
			changed = true
			continue
		}
		certIdentity, err := security.GetIdentityFromKeyName(keyName)
		if err != nil || certIdentity == nil || !certIdentity.Equal(identity) {
			continue
		}

		delete(groups, groupStr)
		delete(groups, "")
		if len(groups) == 0 {
			delete(index, cert)
			if err := a.keychain.DeleteCert(certName); err != nil {
				log.Warn(a, deleteWarnMsg, "name", certName, "err", err)
			}
		}
		changed = true
	}
	return index, changed
}

func (a *App) forgetFastJoinCertForGroup(identity enc.Name, group enc.Name, keepCert enc.Name) error {
	if len(identity) == 0 {
		return nil
	}
	index := a.loadFastJoinIndex()
	index, changed := a.forgetCertsFromIndex(index, identity, group, keepCert,
		"Failed to delete stale fast-join certificate")
	if !changed {
		return nil
	}
	return a.persistFastJoinIndex(index)
}

func (a *App) forgetPeerIdentityForGroup(identity enc.Name, group enc.Name, keepCert enc.Name) error {
	if len(identity) == 0 {
		return nil
	}
	index := a.loadPeerIndex()
	index, changed := a.forgetCertsFromIndex(index, identity, group, keepCert,
		"Failed to delete stale peer identity cert")
	if !changed {
		return nil
	}
	return a.persistPeerIndex(index)
}

// ensurePeerGroup initializes publish tracking entries for the given group.
func (a *App) ensurePeerGroup(group enc.Name) error {
	index := a.loadPeerIndex()
	groupStr := group.String()

	peers, err := a.peerIdentityEntries()
	if err != nil {
		return err
	}
	for _, peer := range peers {
		index.ensureGroup(peer.CertName, groupStr, index.publishedInGroup(peer.CertName, groupStr))
	}
	return a.persistPeerIndex(index)
}

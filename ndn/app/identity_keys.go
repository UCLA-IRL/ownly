//go:build js && wasm

package app

import (
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"syscall/js"
	"time"

	enc "github.com/named-data/ndnd/std/encoding"
	"github.com/named-data/ndnd/std/log"
	"github.com/named-data/ndnd/std/ndn"
	spec "github.com/named-data/ndnd/std/ndn/spec_2022"
	"github.com/named-data/ndnd/std/security"
	"github.com/named-data/ndnd/std/security/keychain"
	sig "github.com/named-data/ndnd/std/security/signer"
	jsutil "github.com/named-data/ndnd/std/utils/js"
)

var identityIssuer = enc.NewGenericComponent("identity")

const peerIndexKey = "/local/peer-identities" // value: map[certName]published
const peerOptInKey = "peer-opt-in"

type identityEntry struct {
	Identity   string
	KeyName    string
	CertName   string
	HasPrivate bool
	Source     string // "local" or "peer"
	Published  bool   // whether the peer cert was published to boot sync
}

func (e identityEntry) toJs() map[string]any {
	return map[string]any{
		"identity":   e.Identity,
		"keyName":    e.KeyName,
		"certName":   e.CertName,
		"hasPrivate": e.HasPrivate,
		"source":     e.Source,
		"published":  e.Published,
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
	a.publishPendingBootPeers(a.bootOwnerSession)
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
		a.publishPendingBootPeers(a.bootOwnerSession)
		return entry, nil
	}

	return identityEntry{}, fmt.Errorf("No usable identity key found")
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

	publishIndex := a.loadPeerIndex()
	entries := make([]identityEntry, 0)
	for _, key := range id.Keys() {
		for _, cert := range key.UniqueCerts() {
			wire, _ := a.store.Get(cert.Prefix(-1), true)
			if wire == nil {
				continue
			}
			certData, _, err := spec.Spec{}.ReadData(enc.NewWireView(enc.Wire{wire}))
			if err != nil {
				continue
			}
			if issuer := certData.Name().At(-2); !issuer.Equal(identityIssuer) {
				continue
			}

			entries = append(entries, identityEntry{
				Identity:   idName.String(),
				KeyName:    key.KeyName().String(),
				CertName:   certData.Name().String(),
				HasPrivate: true,
				Source:     "local",
				Published:  publishIndex[certData.Name().String()],
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

func (a *App) loadPeerIndex() map[string]bool {
	index := make(map[string]bool)

	var wire []byte
	if !a.bootStateLoad.IsUndefined() && !a.bootStateLoad.IsNull() {
		if result, err := jsutil.Await(a.bootStateLoad.Invoke(js.ValueOf(peerIndexKey))); err == nil && result.Truthy() && !result.IsUndefined() && !result.IsNull() {
			wire = jsutil.JsArrayToSlice(result)
		}
	}
	if len(wire) == 0 {
		return index
	}

	if err := json.Unmarshal(wire, &index); err != nil {
		log.Warn(a, "Failed to decode peer index", "err", err)
	}
	return index
}

func (a *App) persistPeerIndex(index map[string]bool) error {
	wire, err := json.Marshal(index)
	if err != nil {
		return err
	}
	if a.bootStatePersist.IsUndefined() || a.bootStatePersist.IsNull() {
		return nil
	}
	jsVal := jsutil.SliceToJsArray(wire)
	_, err = jsutil.Await(a.bootStatePersist.Invoke(js.ValueOf(peerIndexKey), jsVal))
	return err
}

func (a *App) bootPeerOptIn() bool {
	var wire []byte
	if !a.bootStateLoad.IsUndefined() && !a.bootStateLoad.IsNull() {
		if result, err := jsutil.Await(a.bootStateLoad.Invoke(js.ValueOf(peerOptInKey))); err == nil && result.Truthy() && !result.IsUndefined() && !result.IsNull() {
			wire = jsutil.JsArrayToSlice(result)
		}
	}
	if len(wire) == 0 {
		return false
	}
	return wire[0] == 1
}

func (a *App) setBootPeerOptIn(optIn bool) error {
	val := byte(0)
	if optIn {
		val = 1
	}
	if a.bootStatePersist.IsUndefined() || a.bootStatePersist.IsNull() {
		return nil
	}
	jsVal := jsutil.SliceToJsArray([]byte{val})
	_, err := jsutil.Await(a.bootStatePersist.Invoke(js.ValueOf(peerOptInKey), jsVal))
	return err
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
			Published:  index[name],
		})
	}

	if updated {
		_ = a.persistPeerIndex(index)
	}

	return entries, nil
}

func (a *App) promoteIdentityAnchors() error {

	local, err := a.localIdentityEntries()
	if err != nil {
		return err
	}
	peers, err := a.peerIdentityEntries()
	if err != nil {
		return err
	}

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

	for _, entry := range local {
		log.Warn(a, "promoting", "name", entry.CertName)
		promote(entry.CertName)
	}
	for _, entry := range peers {
		log.Warn(a, "promoting", "name", entry.CertName)
		promote(entry.CertName)
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

func (a *App) keychainRemoveNamesForKeyName(keyName enc.Name) ([]string, error) {
	api := js.Global().Get("_ndnd_keychain_js")
	if !api.Truthy() {
		return nil, fmt.Errorf("keychain store unavailable")
	}

	list, err := jsutil.Await(api.Call("list"))
	if err != nil {
		return nil, err
	}

	names := make(map[string]bool)
	for i := 0; i < list.Length(); i++ {
		blob := jsutil.JsArrayToSlice(list.Index(i))
		signers, certs, err := security.DecodeFile(blob)
		if err != nil {
			continue
		}

		for _, signer := range signers {
			if signer != nil && signer.KeyName().Equal(keyName) {
				names[hashName(blob, keychain.EXT_KEY)] = true
			}
		}
		for _, certWire := range certs {
			certData, _, err := spec.Spec{}.ReadData(enc.NewWireView(enc.Wire{certWire}))
			if err != nil {
				continue
			}
			certKeyName, err := security.GetKeyNameFromCertName(certData.Name())
			if err != nil {
				continue
			}
			if certKeyName.Equal(keyName) {
				names[hashName(blob, keychain.EXT_CERT)] = true
			}
		}
	}

	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	return out, nil
}

func (a *App) importPeerCerts(blobs [][]byte) ([]identityEntry, error) {
	index := a.loadPeerIndex()
	existingCerts := make(map[string]bool)
	existingKeys := make(map[string]bool)
	entries, err := a.peerIdentityEntries()
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		existingKeys[entry.KeyName] = true
		existingCerts[entry.CertName] = true
		if entry.Published {
			index[entry.CertName] = true
		}
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

		if existingCerts[nameStr] || index[nameStr] || existingKeys[keyStr] {
			continue
		}

		if err = a.keychain.InsertCert(certWire); err != nil {
			continue
		}

		existingCerts[nameStr] = true
		existingKeys[keyStr] = true
		index[nameStr] = false

		identity, _ := security.GetIdentityFromKeyName(keyName)
		imported = append(imported, identityEntry{
			Identity:   identity.String(),
			KeyName:    keyStr,
			CertName:   nameStr,
			HasPrivate: false,
			Source:     "peer",
			Published:  false,
		})
	}
	if err := a.persistPeerIndex(index); err != nil {
		return nil, err
	}

	a.publishPendingBootPeers(a.bootOwnerSession)
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

	// Delete certificate from store and keychain blobs.
	removeNames := []string{hashName(wire, keychain.EXT_CERT)}
	if err := a.store.Remove(certName); err != nil {
		return err
	}

	index := a.loadPeerIndex()
	_, managedPeer := index[certName.String()]
	issuerIsIdentity := certData.Name().At(-2).Equal(identityIssuer)
	localHasKey := false
	if issuerIsIdentity {
		hasKey, err := a.localIdentityHasKeyName(keyName)
		if err != nil {
			return err
		}
		localHasKey = hasKey
	}

	if issuerIsIdentity && localHasKey {
		// Managed local identity key: remove the signing key as well.
		idName, err := a.identityName()
		if err != nil {
			return err
		}
		id := a.keychain.IdentityByName(idName)
		if id != nil {
			for _, key := range id.Keys() {
				if key.KeyName().Equal(keyName) {
					secret, err := sig.MarshalSecret(key.Signer())
					if err == nil {
						removeNames = append(removeNames, hashName(secret.Join(), keychain.EXT_KEY))
					}
					break
				}
			}
		}
		extraNames, err := a.keychainRemoveNamesForKeyName(keyName)
		if err != nil {
			return err
		}
		removeNames = append(removeNames, extraNames...)
		if managedPeer {
			delete(index, certName.String())
			if err := a.persistPeerIndex(index); err != nil {
				return err
			}
		}
	} else if managedPeer {
		// Remove peer entry from index.
		delete(index, certName.String())
		if err := a.persistPeerIndex(index); err != nil {
			return err
		}
	} else {
		// Certificate exists but is not tracked; still remove the cert blob.
	}

	api := js.Global().Get("_ndnd_keychain_js")
	if !api.Truthy() {
		return fmt.Errorf("Keychain store unavailable")
	}
	jsNames := js.Global().Get("Array").New(len(removeNames))
	for i, n := range removeNames {
		jsNames.SetIndex(i, js.ValueOf(n))
	}
	if _, err := jsutil.Await(api.Call("remove", jsNames)); err != nil {
		return err
	}

	// Reload keychain to reflect changes
	kc, err := keychain.NewKeyChainJS(js.Global().Get("_ndnd_keychain_js"), a.store)
	if err != nil {
		return err
	}
	a.keychain = kc

	if err != nil {
		return err
	}
	return nil
}

func hashName(wire []byte, ext string) string {
	sum := sha256.Sum256(wire)
	return hex.EncodeToString(sum[:]) + ext
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

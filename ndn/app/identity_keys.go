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

const peerIndexKey = "/local/peer-identities" // value: peerPublishIndex

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

func (a *App) loadPeerIndex() peerPublishIndex {
	index := make(peerPublishIndex)

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

func (a *App) persistPeerIndex(index peerPublishIndex) error {
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

type peerCertImportOpts struct {
	Published bool
	Group     enc.Name
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

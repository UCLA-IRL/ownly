//go:build js && wasm

// Revocation App methods: receive validation, JS callback, publish path,
// and the Phase 5 SecurityConfig re-shoot stub.

package app

import (
	"crypto/sha256"
	"fmt"
	"time"

	enc "github.com/named-data/ndnd/std/encoding"
	"github.com/named-data/ndnd/std/log"
	"github.com/named-data/ndnd/std/ndn"
	spec "github.com/named-data/ndnd/std/ndn/spec_2022"
	"github.com/named-data/ndnd/std/security"
	revocationtlv "github.com/named-data/ndnd/std/security/revocation_tlv"
	ndn_sync "github.com/named-data/ndnd/std/sync"
	"github.com/named-data/ndnd/std/types/optional"
	"github.com/pulsejet/ownly/ndn/app/tlv"
)

const reasonKeyCompromise uint8 = 1

func (a *App) workspaceOwnerSigner(wkspName enc.Name) (ndn.Signer, error) {
	if a == nil || a.trust == nil {
		return nil, fmt.Errorf("trust configuration is not initialized")
	}
	signer := a.trust.Suggest(wkspName.Append(enc.NewKeywordComponent("KD")))
	if signer == nil {
		return nil, fmt.Errorf("workspace owner key is unavailable")
	}
	identity, err := security.GetIdentityFromKeyName(signer.KeyName())
	if err != nil {
		return nil, fmt.Errorf("resolve revocation signer identity: %w", err)
	}
	ownerIdentity := wkspName.Append(enc.NewKeywordComponent("owner"))
	if !identity.Equal(ownerIdentity) {
		return nil, fmt.Errorf("revocation signer is not the workspace owner: %s", identity)
	}
	return signer, nil
}

// emitCertRevoked invokes the JS on_cert_revoked callback if set.
func (a *App) emitCertRevoked(certName enc.Name, rec *RevocationRecord) {
	if a == nil || rec == nil {
		return
	}
	if a.certRevokedCb.IsUndefined() || a.certRevokedCb.IsNull() {
		return
	}
	a.certRevokedCb.Invoke(certName.String(), certRevokedPayload(certName, rec))
}

// publishRevocationToAlo is shared by the workspace and boot SVS APIs.
//
// When reason is keyCompromise (1) and invalidityTime is 0, the cert
// is parsed to use its NotBefore as the effective invalidity time
// (RFC 5280 §5.3.2).
func publishRevocationToAlo(
	alo *ndn_sync.SvsALO,
	certName enc.Name,
	certBytes []byte,
	signer ndn.Signer,
	reason uint8,
	invalidityTime uint64,
) (string, enc.Wire, error) {
	if len(certBytes) == 0 {
		return "", nil, fmt.Errorf("cert wire bytes are empty")
	}
	if len(certName) == 0 {
		return "", nil, fmt.Errorf("cert name is empty")
	}
	if !tlv.IsWkspKeyCertName(certName) {
		return "", nil, fmt.Errorf("can only revoke wkspKey certs (path must contain /wksp/ and /KEY/), got %s", certName)
	}
	certData, _, err := spec.Spec{}.ReadData(enc.NewWireView(enc.Wire{certBytes}))
	if err != nil {
		return "", nil, fmt.Errorf("parse certificate: %w", err)
	}

	notBefore := optional.None[time.Time]()
	effective := invalidityTime
	if reason == reasonKeyCompromise && effective == 0 {
		if certData.Signature() != nil {
			if nb, _ := certData.Signature().Validity(); nb.IsSet() {
				effective = uint64(nb.Unwrap().UnixMilli())
			}
		}
	}
	if effective != 0 {
		notBefore = optional.Some(time.UnixMilli(int64(effective)))
	}
	recordWire, err := security.RevokeCert(security.RevokeCertArgs{
		Cert:      certData,
		Signer:    signer,
		Reason:    security.RevocationReason(reason),
		NotBefore: notBefore,
	})
	if err != nil {
		return "", nil, fmt.Errorf("build revocation record: %w", err)
	}
	_, state, err := alo.Publish(recordWire)
	if err != nil {
		return "", nil, fmt.Errorf("publish revocation: %w", err)
	}
	recordData, _, err := spec.Spec{}.ReadData(enc.NewWireView(recordWire))
	if err != nil {
		return certName.String(), state, nil
	}
	return recordData.Name().String(), state, nil
}

// reshootSecurityConfig is the post-revoke repo sync stub. The owner
// re-shoots SecurityConfig after every revoke so repos joined later
// see the up-to-date revocation set. The field shape is TBD, so
// this logs and returns; v2 wires the actual publishSecurityConfig.
func (a *App) reshootSecurityConfig() {
	if a == nil {
		return
	}
	log.Info(a, "Reshoot SecurityConfig (stub)")
}

// handleRevocationPub validates and stores an embedded revocation record.
// It returns true when the publication used a revocation-record name, even
// when validation fails, so malformed records are never parsed as app data.
func (a *App) handleRevocationPub(client ndn.Client, wkspName enc.Name, pub ndn_sync.SvsPub) bool {
	data, sigCovered, err := spec.Spec{}.ReadData(enc.NewWireView(pub.Content))
	if err != nil || !containsRevocationMarker(data.Name()) {
		return false
	}
	certName, err := security.CertNameFromRevocationRecordName(data.Name())
	if err != nil {
		log.Warn(nil, "Rejecting revocation with invalid record name", "name", data.Name(), "err", err)
		return true
	}
	if !tlv.IsWkspKeyCertName(certName) || !wkspName.IsPrefix(certName) {
		log.Warn(nil, "Rejecting revocation for non-workspace certificate", "name", certName)
		return true
	}
	ownerPublisher, _ := enc.NameFromStr("32=owner")
	if !pub.Publisher.Equal(ownerPublisher) {
		log.Warn(nil, "Rejecting revocation from non-owner publisher", "publisher", pub.Publisher)
		return true
	}
	if err := validateOwnerRevocation(client, wkspName, data, sigCovered); err != nil {
		log.Warn(nil, "Rejecting unauthorized revocation", "name", data.Name(), "err", err)
		return true
	}
	rev, err := revocationtlv.ParseRevocationRecord(enc.NewWireView(data.Content()), false)
	if err != nil {
		log.Warn(nil, "Failed to decode revocation", "err", err)
		return true
	}
	if len(rev.PublicKeyHash) != sha256.Size {
		log.Warn(nil, "Rejecting revocation with invalid public-key hash", "size", len(rev.PublicKeyHash))
		return true
	}
	if certWire, readErr := a.resolveCertWire(certName); readErr == nil {
		cert, _, parseErr := spec.Spec{}.ReadData(enc.NewWireView(enc.Wire{certWire}))
		if parseErr != nil {
			log.Warn(nil, "Rejecting revocation because the target certificate cannot be parsed", "name", certName, "err", parseErr)
			return true
		}
		hash := sha256.Sum256(cert.Content().Join())
		if !bytesEq(hash[:], rev.PublicKeyHash) {
			log.Warn(nil, "Rejecting revocation with mismatched public-key hash", "name", certName)
			return true
		}
	}
	if err := a.trust.InsertRevoke(pub.Content); err != nil {
		log.Warn(nil, "Failed to install revocation", "err", err)
		return true
	}
	invalidityTime := uint64(0)
	if nb, ok := rev.NotBefore.Get(); ok {
		invalidityTime = nb
	}
	rec := &RevocationRecord{
		Reason:         uint8(rev.Reason),
		InvalidityTime: invalidityTime,
		CertHash:       append([]byte(nil), rev.PublicKeyHash...),
		CertName:       certName,
	}
	a.emitCertRevoked(certName, rec)
	log.Info(nil, "Installed revocation", "record", data.Name(), "certificate", certName)
	return true
}

func validateOwnerRevocation(client ndn.Client, wkspName enc.Name, data ndn.Data, sigCovered enc.Wire) error {
	if client == nil || data.Signature() == nil {
		return fmt.Errorf("missing validation context or signature")
	}
	ownerPrefix := wkspName.Append(enc.NewKeywordComponent("owner"))
	if keyName := data.Signature().KeyName(); len(keyName) == 0 || !ownerPrefix.IsPrefix(keyName) {
		return fmt.Errorf("signer is not the workspace owner: %s", keyName)
	}

	result := make(chan error, 1)
	client.ValidateExt(ndn.ValidateExtArgs{
		Data:       data,
		SigCovered: sigCovered,
		OverrideName: wkspName.
			Append(enc.NewKeywordComponent("boot")).
			Append(enc.NewKeywordComponent("owner")).
			Append(enc.NewGenericComponent("revocation")).
			Append(enc.NewVersionComponent(uint64(time.Now().UnixMilli()))),
		Callback: func(valid bool, err error) {
			if !valid && err == nil {
				err = fmt.Errorf("signature validation failed")
			}
			result <- err
		},
	})
	return <-result
}

func containsRevocationMarker(name enc.Name) bool {
	for _, comp := range name {
		if comp.Typ == enc.TypeGenericNameComponent && string(comp.Val) == "REVOKE" {
			return true
		}
	}
	return false
}

func (a *App) knownRevocations() []revocationListEntry {
	if a == nil || a.keychain == nil || a.trust == nil {
		return nil
	}
	out := make([]revocationListEntry, 0)
	for _, identity := range a.keychain.Identities() {
		for _, key := range identity.Keys() {
			for _, certName := range key.UniqueCerts() {
				if !tlv.IsWkspKeyCertName(certName) {
					continue
				}
				wire, err := a.resolveCertWire(certName)
				if err != nil {
					continue
				}
				cert, _, err := spec.Spec{}.ReadData(enc.NewWireView(enc.Wire{wire}))
				if err != nil {
					continue
				}
				record, err := a.trust.CheckRevoke(cert)
				if err != nil || record == nil {
					continue
				}
				invalidityTime := uint64(0)
				if nb, ok := record.NotBefore.Get(); ok {
					invalidityTime = nb
				}
				out = append(out, revocationListEntry{
					Name: certName,
					Rec: &RevocationRecord{
						Reason:         uint8(record.Reason),
						InvalidityTime: invalidityTime,
						CertHash:       append([]byte(nil), record.PublicKeyHash...),
						CertName:       certName,
					},
				})
			}
		}
	}
	return out
}

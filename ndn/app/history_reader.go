//go:build js && wasm

package app

import (
	"crypto/sha256"
	"fmt"

	enc "github.com/named-data/ndnd/std/encoding"
	"github.com/named-data/ndnd/std/log"
	"github.com/named-data/ndnd/std/merklelog"
	"github.com/named-data/ndnd/std/ndn"
	merkledef "github.com/named-data/ndnd/std/ndn/merklelog"
	"github.com/named-data/ndnd/std/object"
	"github.com/named-data/ndnd/std/security"
)

// The endpoint is routing configuration. Response authorization and append
// signing are defined by schema.trust.
var historyLogName, _ = enc.NameFromStr("/ndn/edu/ucla/ndnd/merkle")

// historyLogScopeID binds durable rows to the endpoint and LVS signer policy.
// A future signer or endpoint change must use a new ID, never retarget rows.
const historyLogScopeID = "ucla-ndnd-merkle/site-issued-v1"

// historyReader returns a client for retained workspace Data.
func (a *App) historyReader(client ndn.Client, requester enc.Name, allowMissingHistory bool) (ndn.Client, error) {
	if len(historyLogName) == 0 {
		return client, nil
	}

	proofClient, err := a.historyLogClient(requester)
	if err != nil {
		return nil, err
	}
	policy := merklelog.NewCertExpiredPolicy(proofClient)
	if allowMissingHistory {
		policy = allowMissingHistoryPolicy(proofClient, policy)
	}
	return withHistoryExpiry(client, policy), nil
}

// Magic only waives expiry after an authenticated NotFound response.
// Included packets still use the SDK's ingestion-time policy.
func allowMissingHistoryPolicy(proofClient *merklelog.Client, historyPolicy ndn.CertExpiredCallback) ndn.CertExpiredCallback {
	return func(args ndn.CertExpiredCallbackArgs, complete func(error)) {
		if args.Data == nil || args.RawData.Length() == 0 {
			historyPolicy(args, complete)
			return
		}
		hash := sha256.Sum256(args.RawData.Join())
		proofClient.Check(hash[:], func(response *merkledef.CheckResponse, err error) {
			if err != nil {
				complete(fmt.Errorf("Merkle log check failed: %w", err))
				return
			}
			if response.Result.Status == merkledef.CheckStatusNotFound {
				security.IgnoreExpiredCert(args, complete)
				return
			}
			// A second check keeps proof and ingestion-time handling in the SDK.
			historyPolicy(args, complete)
		})
	}
}

// historyLogClient constructs a client whose requests and responses use ordinary
// current-validity trust. Historical expiry is only applied by historyReader.
func (a *App) historyLogClient(requester enc.Name) (*merklelog.Client, error) {
	// The log client uses the application's unmodified SchemaBytes trust setup.
	// It deliberately has no history policy: proof responses require a currently
	// valid signer and cannot be justified by another proof.
	trust, err := getTrustConfig(a.keychain)
	if err != nil {
		return nil, err
	}
	return merklelog.NewClient(
		object.NewClient(a.engine, a.store, trust), historyLogName, requester,
	)
}

// historyExpiryClient adds the history policy only to object consumption.
// Direct validation, live SVS, and command traffic retain current-validity checks.
type historyExpiryClient struct {
	ndn.Client
	onCertExpired ndn.CertExpiredCallback
}

func withHistoryExpiry(client ndn.Client, onCertExpired ndn.CertExpiredCallback) ndn.Client {
	return &historyExpiryClient{Client: client, onCertExpired: onCertExpired}
}

func (c *historyExpiryClient) Consume(name enc.Name, callback func(ndn.ConsumeState)) {
	c.ConsumeExt(ndn.ConsumeExtArgs{Name: name, Callback: callback})
}

func (c *historyExpiryClient) ConsumeExt(args ndn.ConsumeExtArgs) {
	args.OnCertExpired = c.onCertExpired
	onComplete := args.Callback
	args.Callback = func(status ndn.ConsumeState) {
		if err := status.Error(); err != nil {
			log.Warn(nil, "History object fetch failed", "name", status.Name(), "err", err)
		}
		onComplete(status)
	}
	c.Client.ConsumeExt(args)
}

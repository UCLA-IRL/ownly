//go:build js && wasm

package app

import (
	enc "github.com/named-data/ndnd/std/encoding"
	"github.com/named-data/ndnd/std/merklelog"
	"github.com/named-data/ndnd/std/ndn"
	"github.com/named-data/ndnd/std/object"
)

// The endpoint is routing configuration. Response authorization and append
// signing are defined by schema.trust.
var historyLogName, _ = enc.NameFromStr("/ndnd/merkle")

// historyLogScopeID binds durable rows to the endpoint and LVS operator policy.
// A future operator or endpoint change must use a new ID, never retarget rows.
const historyLogScopeID = "ndnd-merkle/testbed-site-v1"

// historyReader returns a client for retained workspace Data.
func (a *App) historyReader(client ndn.Client, requester enc.Name) (ndn.Client, error) {
	if len(historyLogName) == 0 {
		return client, nil
	}

	proofClient, err := a.historyLogClient(requester)
	if err != nil {
		return nil, err
	}
	return withHistoryExpiry(client, merklelog.NewCertExpiredPolicy(proofClient)), nil
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
	c.Client.ConsumeExt(args)
}

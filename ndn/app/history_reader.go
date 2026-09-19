//go:build js && wasm

package app

import (
	enc "github.com/named-data/ndnd/std/encoding"
	"github.com/named-data/ndnd/std/merklelog"
	"github.com/named-data/ndnd/std/ndn"
	"github.com/named-data/ndnd/std/object"
)

// Set for the deployed history service. Response authorization is defined by
// schema.trust; this only supplies the protocol endpoint. It remains unset until
// deployment configuration is available.
var historyLogName enc.Name

// historyReader returns a client for retained workspace Data. It has no effect
// until a history-log endpoint is configured.
func (a *App) historyReader(client ndn.Client, requester enc.Name) (ndn.Client, error) {
	if len(historyLogName) == 0 {
		return client, nil
	}

	// The proof client uses the application's unmodified SchemaBytes trust setup.
	// It deliberately has no history policy: proof responses require a currently
	// valid signer and cannot be justified by another proof.
	trust, err := getTrustConfig(a.keychain)
	if err != nil {
		return nil, err
	}
	proofClient, err := merklelog.NewClient(
		object.NewClient(a.engine, a.store, trust), historyLogName, requester,
	)
	if err != nil {
		return nil, err
	}

	return withHistoryExpiry(client, merklelog.NewCertExpiredPolicy(proofClient)), nil
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

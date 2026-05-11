//go:build js && wasm

package app

import (
	"fmt"
	"time"

	"syscall/js"

	enc "github.com/named-data/ndnd/std/encoding"
	"github.com/named-data/ndnd/std/log"
	"github.com/named-data/ndnd/std/ndn"
	spec "github.com/named-data/ndnd/std/ndn/spec_2022"
	"github.com/named-data/ndnd/std/security"
	sig "github.com/named-data/ndnd/std/security/signer"
	ndn_sync "github.com/named-data/ndnd/std/sync"
	"github.com/named-data/ndnd/std/types/optional"
	jsutil "github.com/named-data/ndnd/std/utils/js"
	"github.com/pulsejet/ownly/ndn/app/tlv"
)

type bootSyncSession struct {
	group enc.Name
	alo   *ndn_sync.SvsALO
}

func (a *App) NewBootSyncAlo(client ndn.Client, nodeName, group enc.Name, initialState enc.Wire) (*ndn_sync.SvsALO, []enc.Name, error) {
	alo, err := ndn_sync.NewSvsALO(ndn_sync.SvsAloOpts{
		Name:         nodeName,
		InitialState: initialState,
		Svs: ndn_sync.SvSyncOpts{
			Client:         client,
			GroupPrefix:    group,
			IgnoreValidity: optional.Some(false),
		},
		// Data in boot sync is small; no snapshot needed.
		Snapshot:        nil,
		MulticastPrefix: multicastPrefix,
	})
	if err != nil {
		return nil, nil, err
	}
	routes := []enc.Name{alo.SyncPrefix(), alo.DataPrefix()}
	return alo, routes, nil
}

func (a *App) startBootSyncAlo(client ndn.Client, alo *ndn_sync.SvsALO, routes []enc.Name) error {
	for _, route := range routes {
		client.AnnouncePrefix(ndn.Announcement{
			Name:   route,
			Expose: true,
		})
	}

	a.ExecWithConnectivity(func() {
		a.NotifyRepoJoin(client, alo.GroupPrefix(), alo.DataPrefix(), false)
	})

	return alo.Start()
}

func (a *App) publishPendingBootPeers() {
	if a.bootSyncSession == nil || a.bootSyncSession.alo == nil {
		return
	}

	// Only owners should publish peer identities to the boot group.
	wkspName := a.bootSyncSession.group.Prefix(-1)
	isOwner, err := a.IsWorkspaceOwner(wkspName.String())
	if err != nil || !isOwner {
		return
	}

	peerIndex := a.loadPeerIndex()
	peers, err := a.peerIdentityEntries()
	if err != nil {
		log.Warn(a, "Failed to list peer entries for boot publish", "err", err)
		return
	}
	groupStr := a.bootSyncSession.group.String()

	for _, entry := range peers {
		certName, err := enc.NameFromStr(entry.CertName)
		if err != nil {
			continue
		}
		nameStr := certName.String()
		if peerIndex.publishedInGroup(nameStr, groupStr) {
			continue
		}

		wire, _ := a.store.Get(certName, false)
		if wire == nil && len(certName) > 0 {
			wire, _ = a.store.Get(certName.Prefix(-1), true)
		}
		if wire == nil {
			continue
		}

		if _, state, err := a.bootSyncSession.alo.Publish(enc.Wire{wire}); err != nil {
			log.Error(a, "Failed to publish identity cert to boot group", "err", err, "name", certName)
			continue
		} else {
			a.PersistBootState(state)
		}

		peerIndex.ensureGroup(nameStr, groupStr, true)
		log.Info(a, "Published identity/peer cert to boot sync", "name", certName, "group", a.bootSyncSession.group)
	}

	if err := a.persistPeerIndex(peerIndex); err != nil {
		log.Warn(a, "Failed to persist peer index", "err", err, "group", a.bootSyncSession.group)
	}
}

func (a *App) handleBootIdentityCert(data ndn.Data, dataWire enc.Wire) {
	if data == nil || len(data.Name()) < 2 {
		return
	}

	_, err := a.importPeerCerts([][]byte{dataWire.Join()}, peerCertImportOpts{
		Published: true,
		Group:     a.bootSyncSession.group,
	})
	if err != nil {
		log.Warn(a, "Failed to import boot peer cert", "err", err, "name", data.Name())
		return
	}
	log.Info(a, "Accepted boot peer identity cert", "name", data.Name())
}

func (a *App) participantSub(client ndn.Client) error {
	if a.bootSyncSession == nil || a.bootSyncSession.alo == nil {
		return fmt.Errorf("Boot sync need to start first")
	}

	ownerName, _ := enc.NameFromStr("32=owner")
	a.bootSyncSession.alo.SubscribePublisher(ownerName, func(pub ndn_sync.SvsPub) {
		// Parsing
		data, _, err := spec.Spec{}.ReadData(enc.NewWireView(pub.Content))
		if err != nil {
			return
		}

		// If not a cert, ignore (could be repo BlobFetch commands)
		ct, _ := data.ContentType().Get()
		if ct != ndn.ContentTypeKey {
			return
		}
		a.PersistBootState(pub.State)

		// Ignore expired certs
		if security.CertIsExpired(data) {
			log.Info(a, "Ignoring expired cert from boot sync", "name", data.Name())
			return
		}

		// Identity cert
		if len(data.Name()) > 1 && data.Name().At(-2).Equal(identityIssuer) {
			// Always accept owner-published peer identities from boot sync.
			a.handleBootIdentityCert(data, pub.Content)
			return
		}

		// We push every final cert we receive into the keychain, including those belong to others.
		log.Info(a, "Received final cert", "name", data.Name())
		if err := a.keychain.InsertCert(pub.Content.Join()); err != nil {
			log.Error(a, "Failed to insert cert", "err", err)
			return
		}
		if err := client.Store().Put(data.Name(), pub.Content.Join()); err != nil {
			log.Warn(a, "Failed to store final cert in local store", "err", err, "name", data.Name())
		}
		log.Info(a, "Inserted and stored final cert", "name", data.Name())
	})
	return nil
}

func (a *App) StartBootSyncParticipant(client ndn.Client, wkspName, userName enc.Name, preCert enc.Wire, appPayload []byte) error {
	// shortcut to check if we already started
	group := wkspName.Append(enc.NewKeywordComponent("boot"))
	key := "boot-pub:" + group.String()
	if a.bootSyncs[key] {
		return nil
	}
	a.bootSyncs[key] = true
	fail := func(err error) error {
		delete(a.bootSyncs, key)
		return err
	}
	initialState := a.LoadBootState(group)

	alo, routes, err := a.NewBootSyncAlo(client, userName, group, initialState)
	if err != nil {
		log.Error(a, "Failed to create boot cert publisher ALO", "err", err, "group", group)
		return fail(err)
	}
	if err := a.startBootSyncAlo(client, alo, routes); err != nil {
		log.Error(a, "Failed to start boot sync ALO", "err", err)
		return fail(err)
	}
	a.bootSyncSession = &bootSyncSession{
		group: group,
		alo:   alo,
	}

	if err := a.ensurePeerGroup(wkspName); err != nil {
		log.Warn(a, "Failed to update peer publish index for group", "group", wkspName, "err", err)
	}
	// Subscribe to updates
	err = a.participantSub(client)
	if err != nil {
		return fail(err)
	}

	// Rejoining the same workspace can reuse an existing final workspace cert.
	// In that case there is no local precert to publish again.
	if len(preCert) == 0 {
		log.Info(a, "Skipping boot precert publish; existing workspace cert already present", "group", group, "user", userName)
		return nil
	}
	// Publish or detect pending precert
	var preCertName enc.Name
	preData, _, err := spec.Spec{}.ReadData(enc.NewWireView(preCert))
	if err != nil {
		return fail(err)
	}
	preCertName = preData.Name().ToFullName(preCert)

	bootPayload := (&tlv.Message{
		BootJoin: &tlv.BootJoin{
			PreCertFullName: preCertName.Bytes(),
			AppPayload:      appPayload,
		},
	}).Encode()
	if _, state, err := alo.Publish(bootPayload); err != nil {
		log.Error(a, "Failed to publish precert", "err", err)
		return fail(err)
	} else {
		a.PersistBootState(state)
		log.Info(a, "Published precert for signing", "name", preCertName, "app_payload_len", len(appPayload))
	}
	return nil
}

func (a *App) ownerSub(client ndn.Client, wkspName enc.Name, rootSigner ndn.Signer) error {
	if a.bootSyncSession == nil || a.bootSyncSession.alo == nil {
		return fmt.Errorf("Boot sync need to start first")
	}
	oneWeekAgo := time.Now().Add(-7 * 24 * time.Hour)
	ownerPrefix := wkspName.Append(enc.NewKeywordComponent("owner"))

	// Owner should subscribe to everything and get three types of data
	// 1. participant join payload carrying user precert full name (+ optional app payload),
	// 2. user final cert, 3. repo command to fetch invitation
	a.bootSyncSession.alo.SubscribePublisher(enc.Name{}, func(pub ndn_sync.SvsPub) {
		content := pub.Content
		// Case 1: content is a final cert (encapsulated Data).
		contentData, _, err := spec.Spec{}.ReadData(enc.NewWireView(content))
		if err == nil {
			if ct, ok := contentData.ContentType().Get(); ok && ct == ndn.ContentTypeKey {
				if len(contentData.Name()) > 1 && contentData.Name().At(-2).Equal(identityIssuer) {
					a.PersistBootState(pub.State)
					a.handleBootIdentityCert(contentData, pub.Content)
					return
				}

				name := contentData.Name()
				if kl := contentData.Signature().KeyName(); kl != nil && ownerPrefix.IsPrefix(kl) {
					// Keep track of user certs issued by peer owners
					if err := client.Store().Put(name, content.Join()); err != nil {
						log.Warn(a, "Failed to store final cert in store", "err", err, "name", name)
					}
					a.PersistBootState(pub.State)
					log.Info(a, "Stored final cert from boot sync", "name", name)
					return
				}
			}
		}

		// Case 2: participant boot-join payload (strict TLV)
		msg, err := tlv.ParseMessage(enc.NewWireView(content), true)
		if err == nil && msg != nil && msg.BootJoin != nil {
			preCertName, err := enc.NameFromBytes(msg.BootJoin.PreCertFullName)
			if err != nil || preCertName == nil {
				log.Warn(a, "Ignoring boot join payload with invalid precert name", "err", err)
				return
			}
			keyName, err := security.GetKeyNameFromCertName(preCertName)
			if err != nil || keyName == nil {
				log.Warn(a, "Ignoring boot join payload with invalid precert key name", "err", err, "name", preCertName)
				return
			}
			appPayload := msg.BootJoin.AppPayload
			a.PersistBootState(pub.State)

			// Skip if we already have a final cert for this precert.
			finalCertPrefix := keyName.Append(enc.NewGenericComponent("anchor"))
			if finalCert, _ := client.LatestLocal(finalCertPrefix); finalCert != nil {
				log.Info(a, "Already have a final cert for precert key, skipping")
				return
			}

			// In case we fetch too history precert
			if comp := preCertName.At(-1); comp.IsVersion() {
				ver := comp.NumberVal()
				t := time.UnixMicro(int64(ver))
				if t.Before(oneWeekAgo) {
					log.Info(a, "Ignoring stale precert", "name", preCertName, "ts", t)
					return
				}
			}

			if len(appPayload) > 0 {
				log.Info(a, "Received boot join payload", "name", preCertName, "app_payload_len", len(appPayload))
			}
			if !a.bootJoinPayloadCb.IsUndefined() && !a.bootJoinPayloadCb.IsNull() {
				if _, cbErr := jsutil.Await(a.bootJoinPayloadCb.Invoke(
					js.ValueOf(wkspName.String()),
					js.ValueOf(preCertName.String()),
					js.ValueOf(keyName.String()),
					jsutil.SliceToJsArray(appPayload),
				)); cbErr != nil {
					log.Warn(a, "Boot join payload callback failed", "err", cbErr, "name", preCertName)
				}
			}

			// In most cases the fetching logic here is redundant since precert is in cache
			respCh := make(chan ndn.ExpressCallbackArgs, 1)
			client.ExpressR(ndn.ExpressRArgs{
				Name: preCertName,
				Config: &ndn.InterestConfig{
					CanBePrefix:    false,
					MustBeFresh:    true,
					Lifetime:       optional.Some(time.Second),
					ForwardingHint: []enc.Name{pub.DataName},
				},
				Retries: 3,
				Callback: func(cb ndn.ExpressCallbackArgs) {
					if cb.Result != ndn.InterestResultData || cb.Data == nil {
						respCh <- cb
						return
					}
					client.ValidateExt(ndn.ValidateExtArgs{
						Data:              cb.Data,
						SigCovered:        cb.SigCovered,
						UseDataNameFwHint: optional.Some(true),
						IgnoreValidity:    optional.Some(false),
						Callback: func(valid bool, err error) {
							if !valid {
								respCh <- ndn.ExpressCallbackArgs{
									Result: ndn.InterestResultError,
									Error:  err,
								}
								return
							}
							respCh <- cb
						},
					})
				},
			})
			args := <-respCh
			if args.Result != ndn.InterestResultData || args.RawData == nil || args.Data == nil {
				log.Error(a, "Failed to fetch precert", "name", preCertName, "result", args.Result, "err", args.Error)
				return
			}
			preWire := args.RawData

			userCert, err := a.SignFinalCert(preWire, rootSigner)
			userCertData, _, err := spec.Spec{}.ReadData(enc.NewWireView(userCert))
			if err != nil {
				log.Error(a, "Failed to sign final cert for", "err", err, "name", preCertName)
				return
			}

			if err := a.keychain.InsertCert(userCert.Join()); err != nil {
				log.Warn(a, "Failed to store final cert locally", "err", err, "name", userCertData.Name())
			}

			// Keep track of user certs issued by this owner
			if err := client.Store().Put(userCertData.Name(), userCert.Join()); err != nil {
				log.Warn(a, "Failed to store final cert in local store", "err", err, "name", userCertData.Name())
			}

			_, state, err := a.bootSyncSession.alo.Publish(userCert)
			if err != nil {
				log.Error(a, "Failed to publish final cert", "err", err, "name", userCertData.Name())
				return
			} else {
				a.PersistBootState(state)
				log.Info(a, "Published final cert", "name", "name", userCertData.Name())
			}
		}
		// Case 3: Repo blob fetch command
	})
	return nil
}

// StartBootSyncOwner listens on the boot sync group for precerts, re-signs them with the
// workspace anchor/trust anchor, and republishes the final certs back into the group.
func (a *App) StartBootSyncOwner(client ndn.Client, wkspName enc.Name, rootSigner ndn.Signer) error {
	if rootSigner == nil {
		return nil
	}
	// shortcut to check if we already started
	group := wkspName.Append(enc.NewKeywordComponent("boot"))
	key := "boot-owner:" + group.String()
	if a.bootSyncs[key] {
		return nil
	}
	a.bootSyncs[key] = true

	ownerName, _ := enc.NameFromStr("32=owner")
	initialState := a.LoadBootState(group)

	alo, routes, err := a.NewBootSyncAlo(client, ownerName, group, initialState)
	if err != nil {
		log.Error(a, "Failed to create boot sync ALO", "err", err, "group", group)
		return err
	}
	a.bootSyncSession = &bootSyncSession{
		group: group,
		alo:   alo,
	}
	if err := a.ensurePeerGroup(wkspName); err != nil {
		log.Warn(a, "Failed to update peer publish index for group", "group", wkspName, "err", err)
	}
	// Subscribe to all updates
	if err := a.ownerSub(client, wkspName, rootSigner); err != nil {
		return err
	}
	if err := a.startBootSyncAlo(client, alo, routes); err != nil {
		log.Error(a, "Failed to start boot sync ALO", "err", err, "group", group)
		return err
	}
	a.publishPendingBootPeers()
	return nil
}

func (a *App) SignFinalCert(appCert enc.Wire, rootSigner ndn.Signer) (enc.Wire, error) {
	certData, _, err := spec.Spec{}.ReadData(enc.NewWireView(appCert))
	if err != nil {
		return nil, err
	}
	rootCertName := rootSigner.KeyName().Append(enc.NewGenericComponent("self"))
	rootCtxSigner := sig.WithKeyLocator(rootSigner, rootCertName)

	return security.SignCert(security.SignCertArgs{
		Data:      certData,
		Signer:    rootCtxSigner,
		IssuerId:  enc.NewGenericComponent("anchor"),
		NotBefore: time.Now().Add(-time.Hour),
		NotAfter:  time.Now().AddDate(0, 0, 180), // for now
	})
}

func (a *App) LoadBootState(group enc.Name) enc.Wire {
	if a.bootStateLoad.IsUndefined() || a.bootStateLoad.IsNull() {
		return nil
	}
	result, err := jsutil.Await(a.bootStateLoad.Invoke(js.ValueOf(group.String())))
	if err != nil || result.IsUndefined() || result.IsNull() {
		return nil
	}
	return enc.Wire{jsutil.JsArrayToSlice(result)}
}

func (a *App) PersistBootState(state enc.Wire) {
	if state == nil || len(state.Join()) == 0 {
		return
	}
	if a.bootStatePersist.IsUndefined() || a.bootStatePersist.IsNull() {
		return
	}
	jsState := jsutil.SliceToJsArray(state.Join())
	if _, err := jsutil.Await(a.bootStatePersist.Invoke(js.ValueOf(a.bootSyncSession.group.String()), jsState)); err != nil {
		log.Warn(a, "Failed to persist boot state", "group", a.bootSyncSession.group, "err", err)
	}
}

//go:build js && wasm

package app

import (
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
)

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

func (a *App) StartBootSyncAlo(client ndn.Client, alo *ndn_sync.SvsALO, routes []enc.Name) error {
	for _, route := range routes {
		client.AnnouncePrefix(ndn.Announcement{
			Name:   route,
			Expose: true,
		})
	}

	a.ExecWithConnectivity(func() {
		a.NotifyRepoJoin(client, alo.GroupPrefix(), alo.DataPrefix(), false)
	})

	// Wait for 1 second to mask the prefix registration race
	time.Sleep(time.Second)
	return alo.Start()
}

// Question: should one quit boot sync group? owners should definitely stay but not sure about users
func (a *App) StopBootSyncAlo(client ndn.Client, alo *ndn_sync.SvsALO, routes []enc.Name) {
	if alo != nil {
		if err := alo.Stop(); err != nil {
			log.Warn(a, "Failed to stop boot ALO", "err", err, "group", alo.GroupPrefix())
		}
	}
	for _, route := range routes {
		client.WithdrawPrefix(route, nil)
	}
}

// StartBootSyncParticipant publishes a precert to the shared boot sync group and waits for
// an owner-signed cert to arrive. Once received, it stores the cert and stops syncing.
func (a *App) StartBootSyncParticipant(client ndn.Client, wkspName, userName enc.Name, preCert enc.Wire) *ndn_sync.SvsALO {
	var preCertName enc.Name
	if preCert == nil || len(preCert.Join()) == 0 {
		return nil
	}
	group := wkspName.Append(enc.NewKeywordComponent("boot"))
	key := "boot-pub:" + group.String()
	if a.bootSyncs[key] {
		return nil
	}
	a.bootSyncs[key] = true
	initialState := a.GetOwnerInitialState(group)

	// Get precert fullname
	preData, _, err := spec.Spec{}.ReadData(enc.NewWireView(preCert))
	if err == nil {
		preCertName = preData.Name().ToFullName(preCert)
	}

	alo, routes, err := a.NewBootSyncAlo(client, userName, group, initialState)
	if err != nil {
		log.Error(a, "Failed to create boot cert publisher ALO", "err", err, "group", group)
		return nil
	}
	ownerName, _ := enc.NameFromStr("32=owner")
	alo.SubscribePublisher(ownerName, func(pub ndn_sync.SvsPub) {
		// Parsing
		data, _, err := spec.Spec{}.ReadData(enc.NewWireView(pub.Content))
		if err != nil {
			return
		}

		// If not a cert, ignore (could be repo BlobFetch commands)
		ct, _ := data.ContentType().Get()
		if ct == ndn.ContentTypeKey {
			a.PersistBootState(group, pub.State)

			// Ignore expired certs
			if security.CertIsExpired(data) {
				log.Info(a, "Ignoring expired cert from boot sync", "name", data.Name())
				return
			}

			// We push every cert we receive into the keychain, including those belong to others.
			log.Info(a, "Received final cert", "name", data.Name())
			if err := a.keychain.InsertCert(pub.Content.Join()); err != nil {
				log.Error(a, "Failed to insert cert", "err", err)
				return
			}
			if err := client.Store().Put(data.Name(), pub.Content.Join()); err != nil {
				log.Warn(a, "Failed to store final cert in local store", "err", err, "name", data.Name())
			}
			log.Info(a, "Inserted and stored final cert", "name", data.Name())

			// // quit boot group
			// a.StopBootSyncAlo(client, alo, routes)
		} else {
			// should be repo command, skip
		}
	})

	if err := a.StartBootSyncAlo(client, alo, routes); err != nil {
		log.Error(a, "Failed to start boot sync ALO", "err", err)
		return nil
	}

	if _, state, err := alo.Publish(enc.Wire{preCertName.Bytes()}); err != nil {
		log.Error(a, "Failed to publish precert", "err", err)
	} else {
		a.PersistBootState(group, state)
		log.Info(a, "Published precert for signing", "name", preCertName)
	}
	return alo
}

// StartBootSyncOwner listens on the boot sync group for precerts, re-signs them with the
// workspace anchor/trust anchor, and republishes the final certs back into the group.
func (a *App) StartBootSyncOwner(client ndn.Client, wkspName enc.Name, rootSigner ndn.Signer) *ndn_sync.SvsALO {
	if rootSigner == nil {
		return nil
	}
	group := wkspName.Append(enc.NewKeywordComponent("boot"))
	key := "boot-owner:" + group.String()
	if a.bootSyncs[key] {
		return nil
	}
	a.bootSyncs[key] = true

	ownerName, _ := enc.NameFromStr("32=owner")
	initialState := a.GetOwnerInitialState(group)
	oneWeekAgo := time.Now().Add(-7 * 24 * time.Hour)
	ownerPrefix := wkspName.Append(enc.NewKeywordComponent("owner"))

	alo, routes, err := a.NewBootSyncAlo(client, ownerName, group, initialState)
	if err != nil {
		log.Error(a, "Failed to create boot sync ALO", "err", err, "group", group)
		return nil
	}

	// Owner should subscribe to everything and get three types of data
	// 1. the full name of user precert, 2. user final cert, 3. repo command to fetch invitation
	alo.SubscribePublisher(enc.Name{}, func(pub ndn_sync.SvsPub) {
		content := pub.Content

		// Case 1: content is a final cert (encapsulated Data).
		contentData, _, err := spec.Spec{}.ReadData(enc.NewWireView(content))
		if err == nil {
			if ct, ok := contentData.ContentType().Get(); ok && ct == ndn.ContentTypeKey {
				name := contentData.Name()
				if kl := contentData.Signature().KeyName(); kl != nil && ownerPrefix.IsPrefix(kl) {
					// Keep track of user certs issued by peer owners
					if err := client.Store().Put(name, content.Join()); err != nil {
						log.Warn(a, "Failed to store final cert in store", "err", err, "name", name)
					}
					a.PersistBootState(group, pub.State)
					log.Info(a, "Stored final cert from boot sync", "name", name)
					return
				}
			}
		}

		// Case 2: precert fullname
		preCertName, err := enc.NameFromBytes(content.Join())
		if err == nil && preCertName != nil {
			a.PersistBootState(group, pub.State)

			// Skip if we already have a final cert for this precert.
			if keyName, err := security.GetKeyNameFromCertName(preCertName); err == nil {
				finalCertPrefix := keyName.Append(enc.NewGenericComponent("anchor"))
				if finalCert, _ := client.LatestLocal(finalCertPrefix); finalCert != nil {
					log.Info(a, "Already have a final cert for precert key, skipping")
					return
				}
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

			_, state, err := alo.Publish(userCert)
			if err != nil {
				log.Error(a, "Failed to publish final cert", "err", err, "name", userCertData.Name())
				return
			} else {
				a.PersistBootState(group, state)
				log.Info(a, "Published final cert", "name", "name", userCertData.Name())
			}
		}
		// Case 3: Repo blob fetch command
	})

	if err := a.StartBootSyncAlo(client, alo, routes); err != nil {
		log.Error(a, "Failed to start boot sync ALO", "err", err, "group", group)
		return nil
	}
	return alo
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
		NotAfter:  time.Now().AddDate(10, 0, 0), // for now
	})
}

func (a *App) GetOwnerInitialState(group enc.Name) enc.Wire {
	if a.bootStateLoad.IsUndefined() || a.bootStateLoad.IsNull() {
		return nil
	}
	result, err := jsutil.Await(a.bootStateLoad.Invoke(js.ValueOf(group.String())))
	if err != nil || result.IsUndefined() || result.IsNull() {
		return nil
	}
	return enc.Wire{jsutil.JsArrayToSlice(result)}
}

func (a *App) PersistBootState(group enc.Name, state enc.Wire) {
	if state == nil || len(state.Join()) == 0 {
		return
	}
	if a.bootStatePersist.IsUndefined() || a.bootStatePersist.IsNull() {
		return
	}
	jsState := jsutil.SliceToJsArray(state.Join())
	if _, err := jsutil.Await(a.bootStatePersist.Invoke(js.ValueOf(group.String()), jsState)); err != nil {
		log.Warn(a, "Failed to persist boot state", "group", group, "err", err)
	}
}

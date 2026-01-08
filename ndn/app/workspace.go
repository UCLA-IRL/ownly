//go:build js && wasm

package app

import (
	"crypto/aes"
	"crypto/ecdh"
	"crypto/elliptic"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"fmt"
	math_rand "math/rand/v2"
	"syscall/js"
	"time"

	spec_repo "github.com/named-data/ndnd/repo/tlv"
	enc "github.com/named-data/ndnd/std/encoding"
	"github.com/named-data/ndnd/std/log"
	"github.com/named-data/ndnd/std/ndn"
	spec "github.com/named-data/ndnd/std/ndn/spec_2022"
	"github.com/named-data/ndnd/std/ndn/svs_ps"
	"github.com/named-data/ndnd/std/object"
	"github.com/named-data/ndnd/std/security"
	sig "github.com/named-data/ndnd/std/security/signer"
	"github.com/named-data/ndnd/std/security/trust_schema"
	ndn_sync "github.com/named-data/ndnd/std/sync"
	"github.com/named-data/ndnd/std/types/optional"
	jsutil "github.com/named-data/ndnd/std/utils/js"
	"github.com/pulsejet/ownly/ndn/app/tlv"
)

// TODO: find optimal value
const SnapshotThreshold = 100

// TODO: change this
var repoName, _ = enc.NameFromStr("/ndnd/ucla/repo3")

// TODO: this is testbed configuration
var multicastPrefix, _ = enc.NameFromStr("/ndn/multicast")

//go:embed schema.tlv
var SchemaBytes []byte

// JoinWorkspace joins the workspace with the given name.
// If the workspace does not exist, it will be created if create is true.
func (a *App) JoinWorkspace(wkspStr_ string, create bool) (wkspStr string, err error) {
	wkspName, err := enc.NameFromStr(wkspStr_)
	if err != nil {
		return
	}
	wkspStr = wkspName.String()

	// Connect to network
	if err = a.WaitForConnectivity(time.Second * 5); err != nil {
		return
	}

	// TODO: fetch workspace "metadata" and check for existence
	// If not existing, check the create flag and proceed

	// Get a valid identity key to sign the certificate
	idSigner, _ := a.GetTestbedKey()
	if idSigner == nil {
		err = fmt.Errorf("no identity key found")
		return
	}
	idName := idSigner.KeyName().Prefix(-2) // pop KeyId and KEY

	// Check if the workspace is outside our namespace
	// In that case we need to attach the invitation cross schema to certificate
	var invitation enc.Wire = nil
	if !idName.IsPrefix(wkspName) {
		// Check if we are allowed to create the workspace
		if create {
			err = fmt.Errorf("cannot create workspace outside your namespace: %s", idName)
			return
		}

		// Other namespace - check for invitation
		inviteName := wkspName.
			Append(enc.NewKeywordComponent("boot")).
			Append(enc.NewKeywordComponent("INVITE")).
			Append(idName...)

		// accessRequestPrefix, _ := enc.NameFromStr("/ndn/multicast" + wkspStr) // Uncomment if you want to use multicast
		accessRequestPrefix, _ := enc.NameFromStr(wkspStr)

		// Name to request access from workspace initiator
		accessRequestName := accessRequestPrefix.
			Append(enc.NewKeywordComponent("boot")).
			Append(enc.NewKeywordComponent("INVITE")).
			Append(idName...)

		// Fetch the invitation from the repo
		log.Info(a, "Fetching workspace invite from repo", "name", inviteName)
		ch := make(chan ndn.ExpressCallbackArgs)
		object.ExpressR(a.engine, ndn.ExpressRArgs{
			Name: inviteName,
			Config: &ndn.InterestConfig{
				MustBeFresh:    true,
				CanBePrefix:    true,
				ForwardingHint: []enc.Name{repoName},
			},
			Retries:  1,
			Callback: func(args ndn.ExpressCallbackArgs) { ch <- args },
		})
		args := <-ch
		if args.Result != ndn.InterestResultData {
			// If the invite is not found, request access from the workspace initiator
			log.Info(a, "Fetching workspace invite from initiator", "name", inviteName)
			ch2 := make(chan ndn.ExpressCallbackArgs)
			object.ExpressR(a.engine, ndn.ExpressRArgs{
				Name: accessRequestName,
				Config: &ndn.InterestConfig{
					MustBeFresh: true,
					CanBePrefix: true,
				},
				Retries:  20,
				Callback: func(args ndn.ExpressCallbackArgs) { ch2 <- args },
			})
			args = <-ch2

			if args.Result != ndn.InterestResultData {
				// Failed if both attempts do not return data
				err = fmt.Errorf("failed to get invitation, make sure %s is invited to %s (%s)",
					idName, wkspName, args.Result)
				return
			}
		}

		// TODO: validate the invitation itself
		invitation = args.RawData
		invitationData, _, _ := spec.Spec{}.ReadData(enc.NewWireView(invitation))
		a.store.Put(invitationData.Name(), invitation.Join())

		log.Info(a, "Got workspace invitation", "name", wkspStr, "invite", args.Data.Name())
	} else {
		log.Info(a, "Joining workspace in own namespace", "name", wkspStr)
	}
	return
}

// IsWorkspaceOwner returns true if the current identity has owner permissions.
func (a *App) IsWorkspaceOwner(wkspStr string) (bool, error) {
	wkspName, err := enc.NameFromStr(wkspStr)
	if err != nil {
		return false, err
	}

	idKey, _ := a.GetTestbedKey()
	if idKey == nil {
		return false, fmt.Errorf("no testbed key")
	}

	// Currently this only checks if the workspace is in the identity namespace, but in the
	// future it should check for actual delegation (valid signer)
	// We don't support any owner-level delegation yet.
	idName := idKey.KeyName().Prefix(-2)
	return idName.IsPrefix(wkspName), nil
}

// onAccessRequest handles incoming access requests if the user is owner of the workspace.
func (a *App) onAccessRequest(args ndn.InterestHandlerArgs) {
	interest := args.Interest

	// Reply with stored invitation. Interest may come from repo or new comer
	inviteBytes, _ := a.store.Get(interest.Name(), true)
	if inviteBytes != nil {
		if err := args.Reply(enc.Wire{inviteBytes}); err != nil {
			log.Warn(a, "Failed to reply with stored invitation", "name", interest.Name(), "err", err)
		}
		return
	}

	// Get list of access requests, add the new one if not a duplicate
	access_requests := js.Global().Get("_access_requests")
	name := interest.Name()
	requester := ""
	wksp := ""
	for c := 0; c < name.EncodingLength(); c++ {
		if name[c].Equal(enc.NewKeywordComponent("INVITE")) {
			requester = name[c+1:].String()
			wksp = name[:c-1].String()
			break
		}
	}

	r := 0
	duplicate := false

	for r < access_requests.Length() {
		if access_requests.Index(r).Index(0).Equal(js.ValueOf(wksp)) &&
			access_requests.Index(r).Index(1).Equal(js.ValueOf(requester)) {

			duplicate = true
		}
		r++
	}

	log.Info(nil, "Received access request", "requester", requester, "duplicate", duplicate)

	if !duplicate {
		wksp_data := append(make([]interface{}, 0), wksp, requester, false) // Creates an array of the wksp name and requester to pass to JS
		access_requests.Call("push", js.ValueOf(wksp_data))

		js.Global().Set("_access_requests", access_requests)

		log.Info(nil, "Access requests:", "requests", access_requests)
	}

}

// GetWorkspace returns a JS object representing the workspace with the given name.
func (a *App) GetWorkspace(groupStr string, ignoreValidity bool) (api js.Value, err error) {
	// Create trust configuration
	trust, err := getTrustConfig(a.keychain)
	if err != nil {
		return
	}

	// Get identity key to use (same as testbed key)
	idSigner, _ := a.GetTestbedKey()
	if idSigner == nil {
		err = fmt.Errorf("no valid testbed key found")
		return
	}
	// Use testbed key to sign NFD management commands
	a.SetCmdKey(idSigner)
	idName := idSigner.KeyName().Prefix(-2) // pop KeyId and KEY

	// Announce testbed user key prefix
	client := object.NewClient(a.engine, a.store, a.trust)
	client.AnnouncePrefix(ndn.Announcement{
		Name:    idSigner.KeyName(),
		Expose:  true,
		OnError: nil, // TODO
	})
	log.Info(nil, "Announcing prefix", "name", "prefix", idSigner.KeyName())
	clientStarted := false
	if err := client.Start(); err != nil {
		return js.Value{}, err
	}
	clientStarted = true

	// The store must have invitation, otherwise we are in trouble
	wkspName, _ := enc.NameFromStr(groupStr)
	inviteName := wkspName.
		Append(enc.NewKeywordComponent("boot")).
		Append(enc.NewKeywordComponent("INVITE")).
		Append(idName...)
	invitation, err := client.Store().Get(inviteName, true)
	if err != nil && invitation == nil {
		err = fmt.Errorf("No invitation found")
		return
	}
	detectUser := wkspName.Append(enc.NewKeywordComponent("KD"))
	detectRoot := wkspName.Append(enc.NewKeywordComponent("RD"))
	userSigner := trust.Suggest(detectUser)
	rootSigner := trust.Suggest(detectRoot)
	var bootAlo *ndn_sync.SvsALO

	isOwner, _ := a.IsWorkspaceOwner(wkspName.String())
	if isOwner {
		if rootSigner == nil || userSigner == nil {
			rootSigner, userSigner, err = a.SetupOwner(wkspName, idSigner)
			if err != nil {
				err = fmt.Errorf("Failed to setup workspace anchor and owner")
				return
			}
		}
		bootAlo = a.StartBootSyncOwner(client, wkspName, rootSigner)
	} else if userSigner == nil {
		var preCertWire enc.Wire
		var recentPreCert bool

		detect := wkspName.Append(enc.NewKeywordComponent("PD"))
		preUserSigner := trust.Suggest(detect)
		if preUserSigner != nil {
			preCertBytes, _ := a.store.Get(preUserSigner.KeyName(), true)
			if preCertBytes != nil {
				candidate := enc.Wire{preCertBytes}
				preCertData, _, perr := spec.Spec{}.ReadData(enc.NewWireView(candidate))
				if perr == nil {
					notBefore, _ := preCertData.Signature().Validity()
					if nb, ok := notBefore.Get(); ok && time.Since(nb) <= 48*time.Hour {
						log.Info(a, "Reusing existing precert", "name", preCertData.Name())
						recentPreCert = true
						preCertWire = candidate
					} else {
						recentPreCert = false
					}
				} else {
					recentPreCert = false
				}
			}
		}

		if !recentPreCert && preCertWire == nil {
			preCertWire, userSigner, err = a.SignPreCert(wkspName, idName, idSigner, enc.Wire{invitation})
			if err != nil {
				err = fmt.Errorf("Failed to sign precert")
				return
			}
		}
		// User don't need join boot group once bootstrapped
		bootAlo = a.StartBootSyncParticipant(client, wkspName, idName, preCertWire)
	}

	// Reset encryption keys
	a.psk = nil
	a.dsk = nil
	a.aes = nil

	// If owner, watch for access request interests
	nodeName := idName
	if isOwner {
		nodeName, _ = enc.NameFromStr("32=owner")
		// prefix, _ := enc.NameFromStr("/ndn/multicast" + groupStr) // Uncomment if you want to use multicast
		prefix, _ := enc.NameFromStr(groupStr)
		accessRequestPrefix := prefix.
			Append(enc.NewKeywordComponent("boot")).
			Append(enc.NewKeywordComponent("INVITE"))
		a.engine.AttachHandler(accessRequestPrefix, a.onAccessRequest)
		client.AnnouncePrefix(ndn.Announcement{
			Name:    accessRequestPrefix,
			Expose:  true,
			OnError: nil, // TODO
		})
		log.Info(nil, "Watching for access requests")
		// Need to join boot process anyway
		detect := wkspName.Append(enc.NewKeywordComponent("RD"))
		rootSigner := trust.Suggest(detect)
		if rootSigner == nil {
			log.Error(a, "Missing wksp root key")
			return
		}
		a.StartBootSyncOwner(client, wkspName, rootSigner)
	}

	// After bootstrapping
	var workspaceJs map[string]any
	workspaceJs = map[string]any{
		// name: string;
		"name": js.ValueOf(nodeName.String()), // wrong

		// group: string;
		"group": js.ValueOf(wkspName.String()),

		// set_encrypt_keys(psk: Uint8Array, dsk: Uint8Array): Promise<void>;
		"set_encrypt_keys": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			a.psk = jsutil.JsArrayToSlice(p[0])
			a.dsk = jsutil.JsArrayToSlice(p[1])
			if len(a.psk) == 0 || len(a.dsk) == 0 {
				return nil, fmt.Errorf("invalid keys")
			}

			symKey, err := hkdfSha256(append(a.psk, a.dsk...))
			if err != nil {
				return nil, err
			}
			a.aes, err = aes.NewCipher(symKey)
			if err != nil {
				return nil, err
			}
			a.ivb = idSigner.KeyName().Hash()
			// a.ivb = userSigner.KeyName().Hash()

			return nil, nil
		}),

		// start(): Promise<void>;
		"start": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			if !clientStarted {
				if err := client.Start(); err != nil {
					return nil, err
				}
				clientStarted = true
			}

			return nil, nil
		}),

		// wait_user_key(): Promise<void>;
		"wait_user_key": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			return nil, a.WaitUserKey(wkspName.String())
		}),

		// stop(): Promise<void>;
		"stop": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			if clientStarted {
				if err := client.Stop(); err != nil {
					return nil, err
				}
				clientStarted = false
			}
			if bootAlo != nil {
				bootAlo.Stop()
			}
			jsutil.ReleaseMap(workspaceJs)
			return nil, nil
		}),

		// produce(name: string, data: Uint8Array): Promise<void>;
		"produce": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			name, err := enc.NameFromStr(p[0].String())
			if err != nil {
				return nil, err
			}

			_, err = client.Produce(ndn.ProduceArgs{
				Name:    name,
				Content: enc.Wire{jsutil.JsArrayToSlice(p[1])},
			})

			return nil, err
		}),

		// consume(name: string): Promise<{ data: Uint8Array; name: string; }>;
		"consume": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			name, err := enc.NameFromStr(p[0].String())
			if err != nil {
				return nil, err
			}

			// Fetch the content from the network
			ch := make(chan ndn.ConsumeState)
			client.ConsumeExt(ndn.ConsumeExtArgs{
				Name:           name,
				TryStore:       true,
				IgnoreValidity: optional.Some(ignoreValidity),
				Callback:       func(state ndn.ConsumeState) { ch <- state },
			})
			state := <-ch
			if err := state.Error(); err != nil {
				return nil, err
			}

			return js.ValueOf(map[string]any{
				"data": jsutil.SliceToJsArray(state.Content().Join()),
				"name": js.ValueOf(state.Name().String()),
			}), nil
		}),

		// svs_alo(group: string, state: Uint8Array | undefined, persist_state: (state: Uint8Array) => Promise<void>): Promise<SvsAloApi>;
		"svs_alo": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			svsAloGroup, err := enc.NameFromStr(p[0].String())
			if err != nil {
				return nil, err
			}

			// Parse initial state
			var stateWire enc.Wire = nil
			if !p[1].IsUndefined() {
				stateWire = enc.Wire{jsutil.JsArrayToSlice(p[1])}
			}

			// Create new SVS ALO instance
			svsAlo, err := ndn_sync.NewSvsALO(ndn_sync.SvsAloOpts{
				Name:         nodeName,
				InitialState: stateWire,

				Svs: ndn_sync.SvSyncOpts{
					Client:         client,
					GroupPrefix:    svsAloGroup,
					IgnoreValidity: optional.Some(ignoreValidity),
				},

				Snapshot: &ndn_sync.SnapshotNodeHistory{
					Client:         client,
					Threshold:      SnapshotThreshold,
					Compress:       a.CompressSnapshotYjs,
					IgnoreValidity: optional.Some(ignoreValidity),
				},

				MulticastPrefix: multicastPrefix,
			})
			if err != nil {
				return nil, err
			}

			// Create JS API for SVS ALO
			return a.SvsAloJs(client, svsAlo, p[2])
		}),

		// sign_and_pub_invitation(invitee: string): Promise<Uint8Array>;
		"sign_and_pub_invitation": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			invitee, err := enc.NameFromStr(p[0].String())
			if err != nil {
				return nil, err
			}

			// Make the invitation name under boot sync group:
			// /<wksp>/32=boot/32=INVITE/<invitee>/v=<time>
			inviteName := wkspName.
				Append(enc.NewKeywordComponent("boot")).
				Append(enc.NewKeywordComponent("INVITE")).
				Append(invitee...).
				WithVersion(enc.VersionUnixMicro)

			// Make sure we can make this invitation
			signer := client.SuggestSigner(inviteName)
			if signer == nil {
				return nil, fmt.Errorf("No valid signing key for invitation")
			}

			wire, err := trust_schema.SignCrossSchema(trust_schema.SignCrossSchemaArgs{
				Name:   inviteName,
				Signer: signer,
				Content: trust_schema.CrossSchemaContent{
					SimpleSchemaRules: []*trust_schema.SimpleSchemaRule{{
						// Authorize invitee's identity key to sign data
						NamePrefix: wkspName.Append(invitee...),
						KeyLocator: &spec.KeyLocator{
							Name: invitee.Append(enc.NewGenericComponent("KEY")),
						},
					}},
				},
				NotBefore: time.Now().Add(-time.Hour),
				NotAfter:  time.Now().AddDate(50, 0, 0), // for now
				Store:     client.Store(),               // auto-store
			})
			if err != nil {
				return nil, err
			}
			// Publish invitation to boot group with encapsulated Data so repo can store it immediately
			if bootAlo != nil {
				cmd := spec_repo.RepoCmd{
					BlobFetch: &spec_repo.BlobFetch{
						Data: [][]byte{wire.Join()},
					},
				}
				_, bootAloState, err := bootAlo.Publish(cmd.Encode())
				if err != nil {
					return nil, err
				}
				a.PersistBootState(bootAlo.GroupPrefix(), bootAloState)
			} else {
				return nil, fmt.Errorf("Failed to publish in boot alo")
			}
			return jsutil.SliceToJsArray(wire.Join()), nil
		}),

		// wait_for_dsk(key: Uint8Array): Promise<Uint8Array>;
		"wait_for_dsk": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			dsk, err := a.fetchDsk(client, wkspName, jsutil.JsArrayToSlice(p[0]))
			if err != nil {
				return nil, err
			}
			return jsutil.SliceToJsArray(dsk), nil
		}),
	}

	return js.ValueOf(workspaceJs), nil
}

func (a *App) SignPreCert(wkspName enc.Name, idName enc.Name, idSigner ndn.Signer, invitation enc.Wire) (enc.Wire, ndn.Signer, error) {
	// Generate key and certificate for this workspace
	userName := wkspName.Append(idName...)
	userKeyName := security.MakeKeyName(userName)
	userSigner, err := sig.KeygenEcc(userKeyName, elliptic.P256())
	if err != nil {
		return nil, nil, err
	}

	// Get key secret to sign certificate
	userSecret, err := sig.MarshalSecretToData(userSigner)
	if err != nil {
		return nil, userSigner, err
	}

	// Create certificate for this workspace
	// TODO: limit validity to same as invite validity
	preCertWire, err := security.SignCert(security.SignCertArgs{
		Data:        userSecret,
		SignerName:  idSigner.KeyName().Append(enc.NewGenericComponent("NDNCERT")), // this is hack
		Signer:      idSigner,
		IssuerId:    enc.NewGenericComponent("pre"),
		NotBefore:   time.Now().Add(-time.Hour),
		NotAfter:    time.Now().AddDate(0, 0, 14), // for two weeks
		CrossSchema: invitation,
	})
	if err != nil {
		return nil, userSigner, err
	}

	// Insert key and certificate into keychain
	if err = a.keychain.InsertKey(userSigner); err != nil {
		return preCertWire, userSigner, err
	}
	if err = a.keychain.InsertCert(preCertWire.Join()); err != nil {
		return preCertWire, userSigner, err
	}
	return preCertWire, userSigner, nil
}

func (a *App) SetupOwner(wkspName enc.Name, idSigner ndn.Signer) (ndn.Signer, ndn.Signer, error) {
	// Generate a trust anchor key
	rootKeyName := security.MakeKeyName(wkspName)
	rootSigner, err := sig.KeygenEcc(rootKeyName, elliptic.P256())
	if err = a.keychain.InsertKey(rootSigner); err != nil {
		return rootSigner, nil, err
	}
	rootSecret, err := sig.MarshalSecretToData(rootSigner)
	if err != nil {
		return rootSigner, nil, err
	}

	// Generate a pre trust anchor
	preAnchorWire, err := security.SignCert(security.SignCertArgs{
		Data:      rootSecret,
		Signer:    idSigner,
		IssuerId:  enc.NewGenericComponent("pre"),
		NotBefore: time.Now().Add(-time.Hour),
		NotAfter:  time.Now().AddDate(0, 0, 90),
	})
	if err = a.keychain.InsertCert(preAnchorWire.Join()); err != nil {
		return rootSigner, nil, err
	}
	preAnchor, _, _ := spec.Spec{}.ReadData(enc.NewWireView(preAnchorWire))

	// Generate a trust anchor
	anchorWire, err := security.SelfSign(security.SignCertArgs{
		Signer:     rootSigner,
		SignerName: rootSigner.KeyName().Append(enc.NewGenericComponent("self")),
		NotBefore:  time.Now().Add(-time.Hour),
		NotAfter:   time.Now().AddDate(10, 0, 0), // for now
	})
	if err = a.keychain.InsertCert(anchorWire.Join()); err != nil {
		return rootSigner, nil, err
	}

	// Generate a cert list: we cannot client produce API since it always assumes an object
	listContent, _ := security.EncodeCertList([]enc.Name{preAnchor.Name()})
	listPrefix, _ := security.CertListPrefix(rootSigner.KeyName())
	listName := listPrefix.Append(enc.NewVersionComponent(uint64(time.Now().UnixMicro())))
	listWireEnc, _ := spec.Spec{}.MakeData(listName, &ndn.DataConfig{
		Freshness: optional.Some(time.Hour),
	}, listContent, rootSigner)
	// Network should fetch from local store
	a.store.Put(listName, listWireEnc.Wire.Join())

	// Generate owner
	ownerName := wkspName.Append(enc.NewKeywordComponent("owner"))
	ownerKeyName := security.MakeKeyName(ownerName)
	ownerSigner, err := sig.KeygenEcc(ownerKeyName, elliptic.P256())
	if err != nil {
		return rootSigner, ownerSigner, err
	}
	ownerSecret, err := sig.MarshalSecretToData(ownerSigner)
	if err != nil {
		return rootSigner, ownerSigner, err
	}
	ownerCertWire, err := security.SignCert(security.SignCertArgs{
		Data:       ownerSecret,
		Signer:     rootSigner,
		SignerName: rootSigner.KeyName().Append(enc.NewGenericComponent("self")),
		IssuerId:   enc.NewGenericComponent("anchor"),
		NotBefore:  time.Now().Add(-time.Hour),
		NotAfter:   time.Now().AddDate(10, 0, 0), // for now
	})
	if err != nil {
		return rootSigner, ownerSigner, err
	}
	// Insert key and certificate into keychain
	if err = a.keychain.InsertKey(ownerSigner); err != nil {
		return rootSigner, ownerSigner, err
	}
	if err = a.keychain.InsertCert(ownerCertWire.Join()); err != nil {
		return rootSigner, ownerSigner, err
	}
	return rootSigner, ownerSigner, nil
}

func (a *App) SvsAloJs(
	client ndn.Client,
	alo *ndn_sync.SvsALO,
	persistState js.Value,
) (api js.Value, err error) {
	// List of SVS routes to announce
	routes := []enc.Name{
		alo.SyncPrefix(),
		alo.DataPrefix(),
	}

	// Wrap the SVS ALO instance in a JS API
	var svsAloJs map[string]any
	svsAloJs = map[string]any{
		"sync_prefix": js.ValueOf(alo.SyncPrefix().String()),
		"data_prefix": js.ValueOf(alo.DataPrefix().String()),

		// start(): Promise<void>;
		"start": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			// Announce prefixes to the network
			for _, route := range routes {
				client.AnnouncePrefix(ndn.Announcement{
					Name:    route,
					Expose:  true,
					OnError: nil, // TODO
				})
				log.Info(nil, "Announcing prefix", "name", "prefix", route)
			}

			// Notify repo to start
			a.ExecWithConnectivity(func() {
				a.NotifyRepoJoin(client, alo.GroupPrefix(), alo.DataPrefix(), true)
			})

			if err := alo.Start(); err != nil {
				return nil, err
			}

			return nil, nil
		}),

		// stop(): Promise<void>;
		"stop": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			if err := alo.Stop(); err != nil {
				return nil, err
			}

			for _, route := range routes {
				client.WithdrawPrefix(route, nil)
			}

			jsutil.ReleaseMap(svsAloJs)
			return nil, nil
		}),

		// set_on_error(): void;
		"set_on_error": js.FuncOf(func(this js.Value, p []js.Value) any {
			alo.SetOnError(func(err error) {
				p[0].Invoke(js.ValueOf(err.Error()))
			})
			return nil
		}),

		// names(): Promise<string[]>;
		"names": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			names := alo.SVS().GetNames()

			arr := js.Global().Get("Array").New()
			for _, name := range names {
				arr.Call("push", js.ValueOf(name.String()))
			}

			return arr, nil
		}),

		// pub_yjs_delta(binary: Uint8Array): Promise<void>;
		"pub_yjs_delta": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			pub := &tlv.Message{
				YjsDelta: &tlv.YjsDelta{
					UUID:   p[0].String(),
					Binary: jsutil.JsArrayToSlice(p[1]),
				},
			}

			// Encrypt the publication
			epub, err := a.encryptPub(pub, alo.SeqNo())
			if err != nil {
				return nil, err
			}

			name, state, err := alo.Publish(epub.Encode())
			if err != nil {
				return nil, err
			}

			// Persist state
			jsutil.Await(persistState.Invoke(jsutil.SliceToJsArray(state.Join())))

			return js.ValueOf(name.String()), nil
		}),

		// pub_blob_fetch(name: string, encapsulate: Uint8Array | undefined): Promise<string>;
		"pub_blob_fetch": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			// This message is special, in the sense that it is purely intended for repo.
			// So subscribers will never see this message.
			cmd := spec_repo.RepoCmd{
				BlobFetch: &spec_repo.BlobFetch{},
			}
			if !p[1].IsUndefined() { // encapsulate
				// For now this only supports a single encapsulated Data
				cmd.BlobFetch.Data = [][]byte{
					jsutil.JsArrayToSlice(p[1]),
				}
			} else { // pointer only
				blobName, err := enc.NameFromStr(p[0].String())
				if err != nil {
					return nil, err
				}
				cmd.BlobFetch.Name = &spec.NameContainer{Name: blobName}
			}

			blobName, state, err := alo.Publish(cmd.Encode())
			if err != nil {
				return nil, err
			}

			// Persist state
			jsutil.Await(persistState.Invoke(jsutil.SliceToJsArray(state.Join())))

			return js.ValueOf(blobName.String()), nil
		}),

		// pub_dsk_request(): Promise<Uint8Array>;
		"pub_dsk_request": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			sk, err := ecdh.X25519().GenerateKey(rand.Reader)
			if err != nil {
				return nil, err
			}
			pub := &tlv.Message{
				DSKRequest: &tlv.DSKRequest{
					X25519Pub: sk.PublicKey().Bytes(),
					Expiry:    uint64(time.Now().Add(24 * time.Hour).Unix()),
				},
			}
			_, state, err := alo.Publish(pub.Encode())
			if err != nil {
				return nil, err
			}

			// Persist state
			jsutil.Await(persistState.Invoke(jsutil.SliceToJsArray(state.Join())))

			return jsutil.SliceToJsArray(sk.Bytes()), nil
		}),

		// pub_dsk_ack(key: Uint8Array): Promise<void>;
		"pub_dsk_ack": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			sk, err := ecdh.X25519().NewPrivateKey(jsutil.JsArrayToSlice(p[0]))
			if err != nil {
				return nil, err
			}
			pub := &tlv.Message{
				DSKACK: &tlv.DSKACK{
					X25519Peer: sk.PublicKey().Bytes(),
				},
			}
			_, state, err := alo.Publish(pub.Encode())
			if err != nil {
				return nil, err
			}

			// Persist state
			jsutil.Await(persistState.Invoke(jsutil.SliceToJsArray(state.Join())))

			return nil, nil
		}),

		// subscribe(name: string, { on_yjs_delta }): Promise<void>;
		"subscribe": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			// Send a list of publications to the JS callback
			sendPub := func(pubs []ndn_sync.SvsPub) {
				yjsDeltas := js.Global().Get("Array").New()

				for _, pub := range pubs {
					pmsg, err := tlv.ParseMessage(enc.NewWireView(pub.Content), true)
					if err != nil {
						log.Error(nil, "Failed to parse publication", "err", err)
						continue
					}

					pmsg, err = a.decryptPub(pmsg)
					if err != nil {
						log.Error(nil, "Failed to decrypt publication", "err", err)
						continue
					}

					// All possible message type conversions listed here
					switch {
					case pmsg.YjsDelta != nil:
						yjsDeltas.Call("push", js.ValueOf(map[string]any{
							"uuid":   pmsg.YjsDelta.UUID,
							"binary": jsutil.SliceToJsArray(pmsg.YjsDelta.Binary),
						}))

					case pmsg.DSKRequest != nil:
						if pmsg.DSKRequest.Expiry < uint64(time.Now().Unix()) {
							continue
						}

						pub := pmsg.DSKRequest.X25519Pub
						if pub == nil {
							log.Warn(nil, "DSK request missing X25519 public key")
							continue
						}

						// Randomness for some crude suppression
						suppress := time.Duration(1+math_rand.IntN(3)) * time.Second

						pubHex := hex.EncodeToString(pub)
						a.dskReqs[pubHex] = time.AfterFunc(suppress, func() {
							delete(a.dskReqs, pubHex)

							group := alo.GroupPrefix()
							dskRes := a.processDskRequest(client, group, pub)
							if dskRes == nil {
								return
							}
							_, state, err := alo.Publish(dskRes)
							if err != nil {
								log.Error(nil, "Failed to publish DSK response", "err", err)
							}
							jsutil.Await(persistState.Invoke(jsutil.SliceToJsArray(state.Join())))
						})

					case pmsg.DSKACK != nil:
						if pmsg.DSKACK.X25519Peer == nil {
							log.Warn(nil, "DSK ACK missing X25519 public key")
							continue
						}

						// Remove the request that matches this ACK
						peerHex := hex.EncodeToString(pmsg.DSKACK.X25519Peer)
						if timer, ok := a.dskReqs[peerHex]; ok {
							timer.Stop()
							delete(a.dskReqs, peerHex)
						}

					default:
						// This will be logged even for BlobFetch commands, which is fine
						// (can be fixed but avoid the extra parse that is unused)
						// log.Warn(a, "Ignoring unknown message", "publisher", pub.Publisher)
					}
				}

				if yjsDeltas.Get("length").Int() > 0 {
					jsutil.Await(p[0].Get("on_yjs_delta").Invoke(yjsDeltas))
				}
			}

			// Subscribe to the SVS instance
			alo.SubscribePublisher(enc.Name{}, func(pub ndn_sync.SvsPub) {
				if !pub.IsSnapshot {
					sendPub([]ndn_sync.SvsPub{pub})
				} else {
					snapshot, err := svs_ps.ParseHistorySnap(enc.NewWireView(pub.Content), true)
					if err != nil {
						panic(err) // we encode this, so this never happens
					}

					pubs := make([]ndn_sync.SvsPub, 0, len(snapshot.Entries))
					for _, entry := range snapshot.Entries {
						pubs = append(pubs, ndn_sync.SvsPub{
							Publisher: pub.Publisher,
							Content:   entry.Content,
							BootTime:  pub.BootTime,
							SeqNum:    entry.SeqNo,
						})
					}
					sendPub(pubs)
				}

				// Persist state
				jsutil.Await(persistState.Invoke(jsutil.SliceToJsArray(pub.State.Join())))

				return
			})
			return nil, nil
		}),

		// awareness(uuid: string): Promise<AwarenessApi>;
		"awareness": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			// One awareness instance per document
			suffix := enc.Name{
				enc.NewKeywordComponent("aware"),
				enc.NewGenericComponent(p[0].String()),
			}

			// Create new Awareness instance
			return a.AwarenessJs(&Awareness{
				Group:  alo.SyncPrefix().Append(suffix...),
				Name:   alo.DataPrefix().Append(suffix...),
				Client: client,
			}), nil
		}),
	}
	return js.ValueOf(svsAloJs), nil
}

func (a *App) AwarenessJs(awareness *Awareness) (api js.Value) {
	// Create JS API for Awareness
	var awarenessJs map[string]any
	awarenessJs = map[string]any{
		// start(): Promise<void>;
		"start": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			err := awareness.Start()
			return nil, err
		}),

		// stop(): Promise<void>;
		"stop": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			if err := awareness.Stop(); err != nil {
				return nil, err
			}
			jsutil.ReleaseMap(awarenessJs)
			return nil, nil
		}),

		// publish(data: Uint8Array): Promise<void>;
		"publish": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			return nil, awareness.Publish(enc.Wire{jsutil.JsArrayToSlice(p[0])})
		}),

		// subscribe(cb: (pub: Uint8Array) => void): Promise<void>;
		"subscribe": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			awareness.OnData = func(wire enc.Wire) {
				p[0].Invoke(jsutil.SliceToJsArray(wire.Join()))
			}
			return nil, nil
		}),
	}
	return js.ValueOf(awarenessJs)
}

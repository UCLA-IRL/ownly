//go:build js && wasm

package app

import (
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
func (a *App) JoinWorkspace(wkspStr_ string, create bool, payload []byte) (wkspStr string, err error) {
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

	// Get a valid identity key to sign the certificate. For new workspaces
	// (`create=true`) the signer is always a regular self-signed identity
	// cert. For joining an existing workspace, fast-join participants get
	// their owner-signed ephemeral cert back from the local keychain; legacy
	// participants fall back to a regular self-signed identity cert.
	var idSigner ndn.Signer
	var fastJoin bool
	if create {
		idSigner, err = a.getIdentitySigner()
	} else {
		idSigner, fastJoin, err = a.getIdentitySignerForWorkspace(wkspName)
	}
	if err != nil {
		return
	}

	// Check if the workspace is outside our namespace. Legacy joins fetch and
	// cache the cross-schema invitation; fast joins already imported their
	// owner-signed ephemeral cert locally.
	idName, _ := security.GetIdentityFromKeyName(idSigner.KeyName())
	if !idName.IsPrefix(wkspName) {
		// Check if we are allowed to create the workspace
		if create {
			err = fmt.Errorf("cannot create workspace outside your namespace: %s", idName)
			return
		}

		if fastJoin {
			log.Info(a, "Using local fast-join authority", "name", wkspStr, "identity", idName)
		} else {
			// Other namespace - check for invitation
			inviteName := wkspName.
				Append(enc.NewKeywordComponent("boot")).
				Append(enc.NewKeywordComponent("INVITE")).
				Append(idName...)

			accessRequestPrefix, _ := enc.NameFromStr(wkspStr)

			// Name to request access from workspace initiator
			accessRequestName := accessRequestPrefix.
				Append(enc.NewKeywordComponent("boot")).
				Append(enc.NewKeywordComponent("INVITE")).
				Append(idName...)

			if invitationBytes, _ := a.store.Get(inviteName, true); invitationBytes != nil {
				log.Info(a, "Using local workspace invitation", "name", wkspStr, "invite", inviteName)
			} else {
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
				invitation := args.RawData
				invitationData, _, _ := spec.Spec{}.ReadData(enc.NewWireView(invitation))
				a.store.Put(invitationData.Name(), invitation.Join())

				log.Info(a, "Got workspace invitation", "name", wkspStr, "invite", args.Data.Name())
			}
		}
	} else {
		log.Info(a, "Joining workspace in own namespace", "name", wkspStr)
	}

	// Stash optional app payload so GetWorkspace can publish it in boot sync.
	if len(payload) == 0 {
		delete(a.joinPayloads, wkspStr)
	} else {
		a.joinPayloads[wkspStr] = payload
	}
	return
}

// IsWorkspaceOwner returns true if the current identity has owner permissions.
func (a *App) IsWorkspaceOwner(wkspStr string) (bool, error) {
	wkspName, err := enc.NameFromStr(wkspStr)
	if err != nil {
		return false, err
	}

	idSigner, err := a.getIdentitySigner()
	if err != nil {
		return false, err
	}

	// Currently this only checks if the workspace is in the identity namespace, but in the
	// future it should check for actual delegation (valid signer)
	// We don't support any owner-level delegation yet.
	idName, _ := security.GetIdentityFromKeyName(idSigner.KeyName())
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
	if err = a.promoteIdentityAnchors(); err != nil {
		return
	}

	wkspName, _ := enc.NameFromStr(groupStr)

	// Get identity key to use. The fast-join bool is captured for symmetry
	// with JoinWorkspace; GetWorkspace's participant path detects fast-join
	// via the absence of a cross-schema invitation wire (see below).
	identitySigner, _, err := a.getIdentitySignerForWorkspace(wkspName)
	if err != nil {
		return
	}
	idName, _ := security.GetIdentityFromKeyName(identitySigner.KeyName())
	// Get testbed key to sign NFD management commands
	testbedSigner, _ := a.GetTestbedKey()
	if testbedSigner == nil {
		err = fmt.Errorf("No valid testbed key found")
		return
	}
	a.SetCmdKey(testbedSigner)

	// Announce testbed key prefix for mgmt/repo operations
	client := object.NewClient(a.engine, a.store, a.trust)

	detectUser := wkspName.Append(enc.NewKeywordComponent("KD"))
	detectRoot := wkspName.Append(enc.NewKeywordComponent("RD"))
	userSigner := a.trust.Suggest(detectUser)
	rootSigner := a.trust.Suggest(detectRoot)
	var nodeName enc.Name
	var preCertWire enc.Wire
	var identityCertWire enc.Wire
	var joinPayload []byte
	var bootSyncFunc func() error

	isOwner, _ := a.IsWorkspaceOwner(wkspName.String())
	if isOwner {
		nodeName, _ = enc.NameFromStr("32=owner")
		if rootSigner == nil || userSigner == nil {
			rootSigner, userSigner, err = a.setupOwner(wkspName, identitySigner)
			if err != nil {
				err = fmt.Errorf("Failed to setup workspace anchor and owner: %w", err)
				return
			}
		}
		bootSyncFunc = func() error {
			return a.StartBootSyncOwner(client, wkspName, rootSigner)
		}
	} else {
		nodeName = idName
		// Check local invitation first. The cross-schema invitation wire is
		// only present for legacy invites (published via sign_and_pub_invitation);
		// fast-join participants don't have one and proceed directly to
		// signPreCert with no CrossSchema attached (the regular schema rule
		// #user_precert <= #userid_cert | #ephemeral_cert validates the precert
		// via the trust-schema path).
		inviteName := wkspName.
			Append(enc.NewKeywordComponent("boot")).
			Append(enc.NewKeywordComponent("INVITE")).
			Append(idName...)
		invitation, _ := client.Store().Get(inviteName, true)
		// invitation may be nil for fast-join participants.
		// Prepare wksp user key
		if userSigner == nil {
			detect := wkspName.Append(enc.NewKeywordComponent("PD"))
			preUserSigner := a.trust.Suggest(detect)
			if preUserSigner != nil {
				preCertBytes, _ := a.store.Get(preUserSigner.KeyLocator(), true)
				preCertWire = enc.Wire{preCertBytes}

			} else {
				preCertWire, userSigner, err = a.signPreCert(wkspName, identitySigner, enc.Wire{invitation})
				if err != nil {
					err = fmt.Errorf("Failed to sign precert")
					return
				}
			}
		}
		// User always willing to help
		// Resolve the participant's IDCERT (self-signed /identity/<ver>)
		// separately from the fast-join signer. The fast-join signer (used
		// for signPreCert above) chains to the owner via the ephemeral cert,
		// but for the BootJoin payload we want the participant's own
		// /identity/<ver> cert so the owner can ingest it as a peer in the
		// Authenticated Peers UI.
		if wire, _, certErr := a.localIdCertWire(); certErr == nil {
			identityCertWire = wire
		} else {
			log.Warn(a, "Participant has no local IDCERT for boot join; falling back to fast-join cert", "err", certErr)
			certName := identitySigner.KeyLocator()
			if len(certName) == 0 || certName.Equal(identitySigner.KeyName()) {
				certName, err = a.identityCertNameForSigner(identitySigner)
			}
			if err == nil {
				if wire, _, certErr := a.certWireByName(certName); certErr == nil {
					identityCertWire = wire
				} else {
					log.Warn(a, "Failed to load participant identity cert for boot join", "name", certName, "err", certErr)
				}
			} else {
				log.Warn(a, "Failed to find participant identity cert for boot join", "err", err)
				err = nil
			}
		}
		joinPayload = a.joinPayloads[wkspName.String()]
		bootSyncFunc = func() error {
			return a.StartBootSyncParticipant(client, wkspName, idName, preCertWire, identityCertWire, joinPayload)
		}
	}

	// Announce testbed key prefix and wksp key prefix
	a.announceKeyPrefix(announceKeyPrefixArgs{
		Client:    client,
		Workspace: wkspName,
		Testbed:   testbedSigner,
		Root:      rootSigner,
		User:      userSigner,
		IsOwner:   isOwner,
	})
	// Reorder function calls to ensure key prefixes get registered first
	time.Sleep(100 * time.Millisecond)
	if err = bootSyncFunc(); err != nil {
		err = fmt.Errorf("Failed to start boot sync: %w", err)
		return
	}

	// Reset encryption keys
	a.psk = nil
	a.dsk = nil
	a.aes = nil

	// After bootstrapping
	// Watch for directed request interests used by SOS and MLS reset.
	var refreshReqPrefix enc.Name
	var mlsRstReqPrefix enc.Name
	exportWorkspaceCert := func() ([]byte, error) {
		signer := a.trust.Suggest(wkspName.Append(enc.NewKeywordComponent("KD")))
		if signer == nil {
			return nil, fmt.Errorf("workspace certificate not ready")
		}

		certName := signer.KeyLocator()
		if certName == nil {
			return nil, fmt.Errorf("workspace signer missing key locator")
		}

		wire, _ := a.store.Get(certName, false)
		if wire == nil && len(certName) > 0 {
			wire, _ = a.store.Get(certName.Prefix(-1), true)
		}
		if wire == nil {
			return nil, fmt.Errorf("workspace certificate not found in store")
		}

		return wire, nil
	}

	var workspaceJs map[string]any
	publishInvitation := func(invitee enc.Name) (enc.Wire, error) {
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
		preCertNameRule := wkspName.
			Append(invitee...).
			Append(enc.NewGenericComponent("KEY")).
			Append(enc.NewGenericComponent("_")).
			Append(enc.NewGenericComponent("pre"))

		wire, err := trust_schema.SignCrossSchema(trust_schema.SignCrossSchemaArgs{
			Name:   inviteName,
			Signer: signer,
			Content: trust_schema.CrossSchemaContent{
				SimpleSchemaRules: []*trust_schema.SimpleSchemaRule{{
					NamePrefix: preCertNameRule,
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
		if a.bootSyncSession != nil && a.bootSyncSession.alo != nil {
			cmd := spec_repo.RepoCmd{
				BlobFetch: &spec_repo.BlobFetch{
					Data: [][]byte{wire.Join()},
				},
			}
			_, bootState, err := a.bootSyncSession.alo.Publish(cmd.Encode())
			if err != nil {
				return nil, err
			}
			a.PersistBootState(bootState)
		} else {
			return nil, fmt.Errorf("Boot Sync hasn't started yet")
		}
		return wire, nil
	}

	workspaceJs = map[string]any{
		// name: string;
		// Expose the real identity name to JS. The boot/SVS node label may be
		// "32=owner", which is not a signer identity and must not be used for
		// trust-schema-bound request naming such as SOS refresh requests.
		"name": js.ValueOf(idName.String()),

		// group: string;
		"group": js.ValueOf(wkspName.String()),

		// export_workspace_cert(): Promise<Uint8Array>;
		"export_workspace_cert": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			wire, err := exportWorkspaceCert()
			if err != nil {
				return nil, err
			}
			return jsutil.SliceToJsArray(wire), nil
		}),

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
			// set Session ID to a static number for legacy path
			a.sessionId = LegacySessionID
			if err := a.installEncryptKey(a.sessionId, symKey); err != nil {
				return nil, err
			}
			a.ivb = identitySigner.KeyName().Hash()

			return nil, nil
		}),

		// set_encrypt_key(sessionId: string, key: Uint8Array): Promise<void>;
		"set_encrypt_key": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			sessionID := p[0].String()
			key := jsutil.JsArrayToSlice(p[1])
			if sessionID == "" || len(key) == 0 {
				return nil, fmt.Errorf("invalid key")
			}

			if err := a.installEncryptKey(sessionID, key); err != nil {
				return nil, err
			}
			a.ivb = identitySigner.KeyName().Hash()
			a.sessionId = sessionID
			return nil, nil
		}),

		// start(): Promise<void>;
		"start": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			if err := client.Start(); err != nil {
				return nil, err
			}
			return nil, nil
		}),

		// wait_user_key(): Promise<void>;
		"wait_user_key": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			return nil, a.WaitUserKey(wkspName.String())
		}),

		// stop(): Promise<void>;
		"stop": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			if len(refreshReqPrefix) > 0 {
				_ = a.engine.DetachHandler(refreshReqPrefix)
				client.WithdrawPrefix(refreshReqPrefix, nil)
			}
			if len(mlsRstReqPrefix) > 0 {
				_ = a.engine.DetachHandler(mlsRstReqPrefix)
				client.WithdrawPrefix(mlsRstReqPrefix, nil)
			}

			if err := client.Stop(); err != nil {
				return nil, err
			}
			if a.bootSyncSession != nil && a.bootSyncSession.alo != nil {
				_ = a.bootSyncSession.alo.Stop()
				routes := []enc.Name{
					a.bootSyncSession.alo.SyncPrefix(),
					a.bootSyncSession.alo.DataPrefix(),
				}
				for _, route := range routes {
					client.WithdrawPrefix(route, nil)
				}
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

		// set_on_refresh_req(responder: string, cb: (requestId: string, requester: string) => Promise<void>): Promise<void>;
		"set_on_refresh_req": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			responderName, err := enc.NameFromStr(p[0].String())
			if err != nil {
				return nil, err
			}
			cb := p[1]
			if cb.Type() != js.TypeFunction {
				return nil, fmt.Errorf("refresh request callback must be a function")
			}

			if len(refreshReqPrefix) > 0 {
				_ = a.engine.DetachHandler(refreshReqPrefix)
				client.WithdrawPrefix(refreshReqPrefix, nil)
			}

			nextRefreshReqPrefix := wkspName.
				Append(enc.NewGenericComponent("root")).
				Append(enc.NewKeywordComponent("REFRESH_REQ")).
				Append(responderName...)

			refreshReqPrefix = nextRefreshReqPrefix

			if err := a.engine.AttachHandler(refreshReqPrefix, func(args ndn.InterestHandlerArgs) {
				name := args.Interest.Name()

				if len(name) < len(refreshReqPrefix)+2 {
					log.Warn(nil, "Invalid refresh request name", "name", name)
					return
				}

				requestId := name[len(refreshReqPrefix)].String()
				requester := name[len(refreshReqPrefix)+1:].String()

				go func() {
					replyStatus := func(status string) {
						signer := client.SuggestSigner(name)
						if signer == nil {
							log.Warn(nil, "No signer for refresh response", "name", name, "status", status)
							return
						}

						data, err := spec.Spec{}.MakeData(
							name,
							&ndn.DataConfig{
								Freshness: optional.Some(time.Second),
							},
							enc.Wire{[]byte(status)},
							signer,
						)
						if err != nil {
							log.Warn(nil, "Failed to make refresh response", "err", err, "status", status)
							return
						}

						if err := args.Reply(data.Wire); err != nil {
							log.Warn(nil, "Failed to reply to refresh request", "err", err, "status", status)
						}
					}

					_, err := jsutil.Await(cb.Invoke(
						js.ValueOf(requestId),
						js.ValueOf(requester),
					))
					if err != nil {
						log.Warn(nil, "Refresh request callback failed", "err", err)
						replyStatus("fail")
						return
					}

					replyStatus("ok")
				}()
			}); err != nil {
				return nil, err
			}

			client.AnnouncePrefix(ndn.Announcement{
				Name:    refreshReqPrefix,
				Expose:  true,
				OnError: nil,
			})

			return nil, nil
		}),

		// set_on_mls_rst_req(responder: string, cb: (requestId: string, requester: string) => Promise<void>): Promise<void>;
		"set_on_mls_rst_req": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			responderName, err := enc.NameFromStr(p[0].String())
			if err != nil {
				return nil, err
			}
			cb := p[1]
			if cb.Type() != js.TypeFunction {
				return nil, fmt.Errorf("MLS reset request callback must be a function")
			}

			if len(mlsRstReqPrefix) > 0 {
				_ = a.engine.DetachHandler(mlsRstReqPrefix)
				client.WithdrawPrefix(mlsRstReqPrefix, nil)
			}

			nextMlsRstReqPrefix := wkspName.
				Append(enc.NewGenericComponent("root")).
				Append(enc.NewKeywordComponent("MLS_RST_REQ")).
				Append(responderName...)

			mlsRstReqPrefix = nextMlsRstReqPrefix

			if err := a.engine.AttachHandler(mlsRstReqPrefix, func(args ndn.InterestHandlerArgs) {
				name := args.Interest.Name()

				if len(name) < len(mlsRstReqPrefix)+2 {
					log.Warn(nil, "Invalid MLS reset request name", "name", name)
					return
				}

				requestId := name[len(mlsRstReqPrefix)].String()
				requester := name[len(mlsRstReqPrefix)+1:].String()

				go func() {
					replyStatus := func(status string) {
						signer := client.SuggestSigner(name)
						if signer == nil {
							log.Warn(nil, "No signer for MLS reset response", "name", name, "status", status)
							return
						}

						data, err := spec.Spec{}.MakeData(
							name,
							&ndn.DataConfig{
								Freshness: optional.Some(time.Second),
							},
							enc.Wire{[]byte(status)},
							signer,
						)
						if err != nil {
							log.Warn(nil, "Failed to make MLS reset response", "err", err, "status", status)
							return
						}

						if err := args.Reply(data.Wire); err != nil {
							log.Warn(nil, "Failed to reply to MLS reset request", "err", err, "status", status)
						}
					}

					_, err := jsutil.Await(cb.Invoke(
						js.ValueOf(requestId),
						js.ValueOf(requester),
					))
					if err != nil {
						log.Warn(nil, "MLS reset request callback failed", "err", err)
						replyStatus("fail")
						return
					}

					replyStatus("ok")
				}()
			}); err != nil {
				return nil, err
			}

			client.AnnouncePrefix(ndn.Announcement{
				Name:    mlsRstReqPrefix,
				Expose:  true,
				OnError: nil,
			})

			return nil, nil
		}),

		// send_refresh_req(name: string): Promise<"ok" | "fail">;
		"send_refresh_req": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			name, err := enc.NameFromStr(p[0].String())
			if err != nil {
				return nil, err
			}

			type refreshResult struct {
				status string
				err    error
			}
			ch := make(chan refreshResult, 1)
			client.ExpressR(ndn.ExpressRArgs{
				Name: name,
				Config: &ndn.InterestConfig{
					MustBeFresh: true,
					Lifetime:    optional.Some(5 * time.Second),
				},
				Retries: 2,
				Callback: func(args ndn.ExpressCallbackArgs) {
					if args.Result == ndn.InterestResultError {
						ch <- refreshResult{err: fmt.Errorf("refresh request failed: %w", args.Error)}
						return
					}
					if args.Result != ndn.InterestResultData {
						ch <- refreshResult{err: fmt.Errorf("refresh request failed with result: %s", args.Result)}
						return
					}

					client.ValidateExt(ndn.ValidateExtArgs{
						Data:       args.Data,
						SigCovered: args.SigCovered,
						Callback: func(valid bool, err error) {
							if !valid {
								if err != nil {
									ch <- refreshResult{err: fmt.Errorf("invalid refresh response: %w", err)}
								} else {
									ch <- refreshResult{err: fmt.Errorf("invalid refresh response")}
								}
								return
							}

							status := string(args.Data.Content().Join())
							if status != "ok" && status != "fail" {
								ch <- refreshResult{err: fmt.Errorf("invalid refresh response status: %q", status)}
								return
							}

							ch <- refreshResult{status: status}
						},
					})
				},
			})

			result := <-ch
			if result.err != nil {
				return nil, result.err
			}
			return js.ValueOf(result.status), nil
		}),

		// send_mls_rst_req(name: string): Promise<"ok" | "fail">;
		"send_mls_rst_req": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			name, err := enc.NameFromStr(p[0].String())
			if err != nil {
				return nil, err
			}

			type mlsResetResult struct {
				status string
				err    error
			}
			ch := make(chan mlsResetResult, 1)
			client.ExpressR(ndn.ExpressRArgs{
				Name: name,
				Config: &ndn.InterestConfig{
					MustBeFresh: true,
					Lifetime:    optional.Some(5 * time.Second),
				},
				Retries: 2,
				Callback: func(args ndn.ExpressCallbackArgs) {
					if args.Result == ndn.InterestResultError {
						ch <- mlsResetResult{err: fmt.Errorf("MLS reset request failed: %w", args.Error)}
						return
					}
					if args.Result != ndn.InterestResultData {
						ch <- mlsResetResult{err: fmt.Errorf("MLS reset request failed with result: %s", args.Result)}
						return
					}

					client.ValidateExt(ndn.ValidateExtArgs{
						Data:       args.Data,
						SigCovered: args.SigCovered,
						Callback: func(valid bool, err error) {
							if !valid {
								if err != nil {
									ch <- mlsResetResult{err: fmt.Errorf("invalid MLS reset response: %w", err)}
								} else {
									ch <- mlsResetResult{err: fmt.Errorf("invalid MLS reset response")}
								}
								return
							}

							status := string(args.Data.Content().Join())
							if status != "ok" && status != "fail" {
								ch <- mlsResetResult{err: fmt.Errorf("invalid MLS reset response status: %q", status)}
								return
							}

							ch <- mlsResetResult{status: status}
						},
					})
				},
			})

			result := <-ch
			if result.err != nil {
				return nil, result.err
			}
			return js.ValueOf(result.status), nil
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

			wire, err := publishInvitation(invitee)
			if err != nil {
				return nil, err
			}
			return jsutil.SliceToJsArray(wire.Join()), nil
		}),

		// forget_peer_identity(invitee: string): Promise<void>;
		"forget_peer_identity": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			invitee, err := enc.NameFromStr(p[0].String())
			if err != nil {
				return nil, err
			}
			if !isOwner {
				return nil, fmt.Errorf("Only owner can forget peer identities")
			}
			bootGroup := wkspName.Append(enc.NewKeywordComponent("boot"))
			return nil, a.forgetPeerIdentityForGroup(invitee, bootGroup, nil)
		}),

		// make_fast_join_invitation(invitee: string): Promise<FastJoinInvitation>;
		"make_fast_join_invitation": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			invitee, err := enc.NameFromStr(p[0].String())
			if err != nil {
				return nil, err
			}
			if !isOwner {
				return nil, fmt.Errorf("Only owner can create fast join invitations")
			}

			fast, err := a.makeFastJoinIdentity(wkspName, invitee, identitySigner)
			if err != nil {
				return nil, err
			}
			bootGroup := wkspName.Append(enc.NewKeywordComponent("boot"))
			a.publishPendingBootFastJoinCerts()
			a.ExecWithConnectivity(func() {
				var dataPrefix enc.Name
				if a.bootSyncSession != nil && a.bootSyncSession.alo != nil && a.bootSyncSession.group.Equal(bootGroup) {
					dataPrefix = a.bootSyncSession.alo.DataPrefix()
				}
				a.NotifyRepoJoin(client, bootGroup, dataPrefix, false)
			})
			// Fast-join bundles do NOT carry a cross-schema invitation wire.
			// The ephemeral cert chains to the owner's #ownerid_cert (a trust
			// anchor on the invitee side via PromoteAnchor), so the regular
			// schema rule #user_precert <= #userid_cert | #ephemeral_cert
			// validates the precert without needing a fallback cross-schema.
			// Legacy invites still go through sign_and_pub_invitation +
			// signPreCert with the cross-schema attached, for backward
			// compatibility with the pre-fast-join flow.
			return fast.toJs(), nil
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

type announceKeyPrefixArgs struct {
	Client    ndn.Client
	Workspace enc.Name
	Testbed   ndn.Signer
	Root      ndn.Signer
	User      ndn.Signer
	IsOwner   bool
}

func (a *App) announceKeyPrefix(args announceKeyPrefixArgs) {
	if args.User == nil || args.Testbed == nil {
		return
	}

	routes := []enc.Name{args.Testbed.KeyName(), args.User.KeyName()}
	if args.IsOwner {
		accessRequestPrefix := args.Workspace.
			Append(enc.NewKeywordComponent("boot")).
			Append(enc.NewKeywordComponent("INVITE"))
		routes = append(routes, accessRequestPrefix)
		a.engine.AttachHandler(accessRequestPrefix, a.onAccessRequest)
		if args.Root != nil {
			routes = append(routes, args.Root.KeyName())
		}
	}

	for _, route := range routes {
		args.Client.AnnouncePrefix(ndn.Announcement{
			Name:   route,
			Expose: true,
			OnError: func(err error) {
				log.Warn(a, "Failed to announce prefix", "prefix", route, "err", err)
			},
		})
	}
}

func (a *App) signPreCert(wkspName enc.Name, identitySigner ndn.Signer, invitation enc.Wire) (enc.Wire, ndn.Signer, error) {
	// Generate key and certificate for this workspace
	idName, err := security.GetIdentityFromKeyName(identitySigner.KeyName())
	if err != nil {
		return nil, nil, err
	}
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
	signerName := identitySigner.KeyName()
	identityCertName := identitySigner.KeyLocator()
	if len(identityCertName) == 0 || identityCertName.Equal(signerName) {
		identityCertName, err = a.identityCertNameForSigner(identitySigner)
		if err != nil {
			return nil, userSigner, err
		}
	}
	identityCtxSigner := sig.WithKeyLocator(identitySigner, identityCertName)
	preCertWire, err := security.SignCert(security.SignCertArgs{
		Data:        userSecret,
		Signer:      identityCtxSigner,
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

type fastJoinIdentity struct {
	OwnerCert       []byte
	EphemeralSecret []byte
	EphemeralCert   []byte
	// InviteeIdentity is the NDN name the inviter designated for this
	// fast-join invitation. Carried alongside the cert so the invitee can
	// display it without parsing the cert wire themselves.
	InviteeIdentity string
}

func (f fastJoinIdentity) toJs() map[string]any {
	return map[string]any{
		"owner_cert":       jsutil.SliceToJsArray(f.OwnerCert),
		"ephemeral_secret": jsutil.SliceToJsArray(f.EphemeralSecret),
		"ephemeral_cert":   jsutil.SliceToJsArray(f.EphemeralCert),
		"invitee_identity": f.InviteeIdentity,
	}
}

func (a *App) certWireByName(name enc.Name) (enc.Wire, ndn.Data, error) {
	if len(name) == 0 {
		return nil, nil, fmt.Errorf("empty certificate name")
	}
	wireBytes, _ := a.store.Get(name, false)
	if wireBytes == nil {
		wireBytes, _ = a.store.Get(name.Prefix(-1), true)
	}
	if wireBytes == nil {
		return nil, nil, fmt.Errorf("certificate not found: %s", name)
	}
	data, _, err := spec.Spec{}.ReadData(enc.NewWireView(enc.Wire{wireBytes}))
	if err != nil {
		return nil, nil, err
	}
	return enc.Wire{wireBytes}, data, nil
}

func (a *App) makeFastJoinIdentity(wkspName, invitee enc.Name, ownerIdentitySigner ndn.Signer) (fastJoinIdentity, error) {
	if ownerIdentitySigner == nil {
		return fastJoinIdentity{}, fmt.Errorf("No owner identity signer")
	}

	keyName := security.MakeKeyName(invitee)
	ephemeralSigner, err := sig.KeygenEcc(keyName, elliptic.P256())
	if err != nil {
		return fastJoinIdentity{}, err
	}

	ownerCertName, err := a.identityCertNameForSigner(ownerIdentitySigner)
	if err != nil {
		return fastJoinIdentity{}, err
	}
	ownerCertWire, _, err := a.certWireByName(ownerCertName)
	if err != nil {
		return fastJoinIdentity{}, err
	}

	certWire, certData, err := a.makeOwnerSignedIdentityCert(ephemeralSigner, ownerIdentitySigner, ownerCertName)
	if err != nil {
		return fastJoinIdentity{}, err
	}

	secretWire, err := sig.MarshalSecret(ephemeralSigner)
	if err != nil {
		return fastJoinIdentity{}, err
	}

	bootGroup := wkspName.Append(enc.NewKeywordComponent("boot"))
	if err := a.forgetFastJoinCertForGroup(invitee, bootGroup, certData.Name()); err != nil {
		return fastJoinIdentity{}, err
	}

	_, err = a.rememberFastJoinCert(certWire.Join(), peerCertImportOpts{
		Published: false,
		Group:     bootGroup,
	})
	if err != nil {
		return fastJoinIdentity{}, err
	}

	return fastJoinIdentity{
		OwnerCert:       ownerCertWire.Join(),
		EphemeralSecret: secretWire.Join(),
		EphemeralCert:   certWire.Join(),
		InviteeIdentity: invitee.String(),
	}, nil
}

func (a *App) setupOwner(wkspName enc.Name, identitySigner ndn.Signer) (ndn.Signer, ndn.Signer, error) {
	if identitySigner == nil {
		return nil, nil, fmt.Errorf("No identity signer")
	}
	// Prepare identitySigner
	identityKeylocator := identitySigner.KeyName().Append(enc.NewGenericComponent("identity"))
	identityCtxSigner := sig.WithKeyLocator(identitySigner, identityKeylocator)

	// Generate a trust anchor key
	rootKeyName := security.MakeKeyName(wkspName)
	rootSigner, err := sig.KeygenEcc(rootKeyName, elliptic.P256())
	rootKeylocator := rootSigner.KeyName().Append(enc.NewGenericComponent("self"))
	rootCtxSigner := sig.WithKeyLocator(rootSigner, rootKeylocator)
	rootSecret, err := sig.MarshalSecretToData(rootSigner)

	// Generate a pre trust anchor
	preAnchorWire, err := security.SignCert(security.SignCertArgs{
		Data:      rootSecret,
		Signer:    identityCtxSigner,
		IssuerId:  enc.NewGenericComponent("pre"),
		NotBefore: time.Now().Add(-time.Hour),
		NotAfter:  time.Now().AddDate(10, 0, 0),
	})
	if err != nil {
		return rootSigner, nil, fmt.Errorf("Sign pre-anchor cert: %w", err)
	}

	// Generate a trust anchor
	anchorWire, err := security.SelfSign(security.SignCertArgs{
		Signer:    rootCtxSigner,
		NotBefore: time.Now().Add(-time.Hour),
		NotAfter:  time.Now().AddDate(10, 0, 0), // for now
	})
	if err != nil {
		return rootSigner, nil, fmt.Errorf("Self-sign anchor cert: %w", err)
	}
	a.keychain.InsertKey(rootSigner)
	a.keychain.InsertCert(preAnchorWire.Join())
	a.keychain.InsertCert(anchorWire.Join())

	// Generate a cert list: we cannot client produce API since it always assumes an object
	preAnchor, _, _ := spec.Spec{}.ReadData(enc.NewWireView(preAnchorWire))
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
	ownerSigner, _ := sig.KeygenEcc(ownerKeyName, elliptic.P256())
	ownerSecret, _ := sig.MarshalSecretToData(ownerSigner)
	ownerCertWire, err := security.SignCert(security.SignCertArgs{
		Data:      ownerSecret,
		Signer:    rootCtxSigner,
		IssuerId:  enc.NewGenericComponent("anchor"),
		NotBefore: time.Now().Add(-time.Hour),
		NotAfter:  time.Now().AddDate(10, 0, 0), // for now
	})
	if err != nil {
		return rootSigner, ownerSigner, fmt.Errorf("Sign owner cert: %w", err)
	}
	// Insert key and certificate into keychain
	a.keychain.InsertKey(ownerSigner)
	a.keychain.InsertCert(ownerCertWire.Join())
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

		// pub_refresh_ping(requestId: string, requester: string, sentAt: string): Promise<string>;
		"pub_refresh_ping": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			requestId := p[0].String()
			requester := p[1].String()
			sentAt := p[2].String()

			if requestId == "" || requester == "" {
				return nil, fmt.Errorf("invalid request parameters")
			}

			pub := &tlv.Message{
				RefreshPing: &tlv.RefreshPing{
					RequestId: requestId,
					Requester: requester,
					SentAt:    sentAt,
				},
			}

			name, state, err := alo.Publish(pub.Encode())
			if err != nil {
				return nil, err
			}

			jsutil.Await(persistState.Invoke(jsutil.SliceToJsArray(state.Join())))
			return js.ValueOf(name.String()), nil
		}),

		// pub_refresh_pong(requestId: string, requester: string, responder: string, freshness: number, sentAt: string): Promise<string>;
		"pub_refresh_pong": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			requestId := p[0].String()
			requester := p[1].String()
			responder := p[2].String()
			freshness := uint64(p[3].Int())
			sentAt := p[4].String()

			if requestId == "" || requester == "" || responder == "" {
				return nil, fmt.Errorf("invalid request parameters")
			}

			pub := &tlv.Message{
				RefreshPong: &tlv.RefreshPong{
					RequestId: requestId,
					Requester: requester,
					Responder: responder,
					Freshness: freshness,
					SentAt:    sentAt,
				},
			}

			name, state, err := alo.Publish(pub.Encode())
			if err != nil {
				return nil, err
			}

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

		// pub_mls_kp_ref(invitee: string, blobName: string): Promise<string>;
		"pub_mls_kp_ref": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			invitee := p[0].String()
			blobName := p[1].String()
			sessionId := p[2].String()
			log.Info(nil, "MLS KP publish request", "invitee", invitee, "blob", blobName)
			if invitee == "" || blobName == "" {
				return nil, fmt.Errorf("invalid invitee or blob name")
			}

			pub := &tlv.Message{
				MlsKeyPackage: &tlv.MlsBlobRef{
					Invitee:   invitee,
					BlobName:  blobName,
					SessionId: sessionId,
				},
			}
			name, state, err := alo.Publish(pub.Encode())
			if err != nil {
				return nil, err
			}
			log.Info(nil, "MLS KP published", "name", name, "boot", alo.BootTime(), "seq", alo.SeqNo())
			// Persist state
			jsutil.Await(persistState.Invoke(jsutil.SliceToJsArray(state.Join())))

			return js.ValueOf(name.String()), nil
		}),

		// pub_mls_welcome_ref(invitee: string, blobName: string): Promise<string>;
		"pub_mls_welcome_ref": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			invitee := p[0].String()
			blobName := p[1].String()
			sessionId := p[2].String()
			if invitee == "" || blobName == "" || sessionId == "" {
				return nil, fmt.Errorf("invalid invitee, blob name, or session ID")
			}

			pub := &tlv.Message{
				MlsWelcome: &tlv.MlsBlobRef{
					Invitee:   invitee,
					BlobName:  blobName,
					SessionId: sessionId,
				},
			}
			name, state, err := alo.Publish(pub.Encode())
			if err != nil {
				return nil, err
			}

			// Persist state
			jsutil.Await(persistState.Invoke(jsutil.SliceToJsArray(state.Join())))

			return js.ValueOf(name.String()), nil
		}),

		// pub_mls_commit_ref(invitee: string, blobName: string): Promise<string>;
		"pub_mls_commit_ref": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			invitee := p[0].String()
			blobName := p[1].String()
			sessionId := p[2].String()
			if invitee == "" || blobName == "" || sessionId == "" {
				return nil, fmt.Errorf("invalid invitee, blob name, or session ID")
			}

			pub := &tlv.Message{
				MlsCommit: &tlv.MlsBlobRef{
					Invitee:   invitee,
					BlobName:  blobName,
					SessionId: sessionId,
				},
			}
			name, state, err := alo.Publish(pub.Encode())
			if err != nil {
				return nil, err
			}

			// Persist state
			jsutil.Await(persistState.Invoke(jsutil.SliceToJsArray(state.Join())))

			return js.ValueOf(name.String()), nil
		}),

		// subscribe({
		//   on_yjs_delta,
		//   on_mls_kp_ref,
		//   on_mls_welcome_ref,
		//   on_mls_commit_ref,
		//   on_refresh_ping,
		//   on_refresh_pong,
		// }): Promise<void>;
		"subscribe": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			// Send a list of publications to the JS callback
			sendPub := func(pubs []ndn_sync.SvsPub) {
				yjsDeltas := js.Global().Get("Array").New()
				mlsKpRefs := js.Global().Get("Array").New()
				mlsWelcomeRefs := js.Global().Get("Array").New()
				mlsCommitRefs := js.Global().Get("Array").New()
				refreshPings := js.Global().Get("Array").New()
				refreshPongs := js.Global().Get("Array").New()

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

					// Handle incoming MLS KeyPackage reference
					case pmsg.MlsKeyPackage != nil:
						log.Info(nil, "Decoded MLS KP ref", "invitee", pmsg.MlsKeyPackage.Invitee, "blob", pmsg.MlsKeyPackage.BlobName)
						mlsKpRefs.Call("push", js.ValueOf(map[string]any{
							"invitee":    pmsg.MlsKeyPackage.Invitee,
							"blob_name":  pmsg.MlsKeyPackage.BlobName,
							"session_id": pmsg.MlsKeyPackage.SessionId,
							"publisher":  pub.Publisher.String(),
							"boot_time":  pub.BootTime,
							"seq_num":    pub.SeqNum,
						}))

					// Handle incoming MLS Welcome reference
					case pmsg.MlsWelcome != nil:
						mlsWelcomeRefs.Call("push", js.ValueOf(map[string]any{
							"invitee":    pmsg.MlsWelcome.Invitee,
							"blob_name":  pmsg.MlsWelcome.BlobName,
							"session_id": pmsg.MlsWelcome.SessionId,
							"publisher":  pub.Publisher.String(),
							"boot_time":  pub.BootTime,
							"seq_num":    pub.SeqNum,
						}))

					// Handle incoming MLS Commit reference
					case pmsg.MlsCommit != nil:
						mlsCommitRefs.Call("push", js.ValueOf(map[string]any{
							"invitee":    pmsg.MlsCommit.Invitee,
							"blob_name":  pmsg.MlsCommit.BlobName,
							"session_id": pmsg.MlsCommit.SessionId,
							"publisher":  pub.Publisher.String(),
							"boot_time":  pub.BootTime,
							"seq_num":    pub.SeqNum,
						}))

					case pmsg.RefreshPing != nil:
						refreshPings.Call("push", js.ValueOf(map[string]any{
							"request_id": pmsg.RefreshPing.RequestId,
							"requester":  pmsg.RefreshPing.Requester,
							"sent_at":    pmsg.RefreshPing.SentAt,
							"publisher":  pub.Publisher.String(),
							"boot_time":  pub.BootTime,
							"seq_num":    pub.SeqNum,
						}))

					case pmsg.RefreshPong != nil:
						refreshPongs.Call("push", js.ValueOf(map[string]any{
							"request_id": pmsg.RefreshPong.RequestId,
							"requester":  pmsg.RefreshPong.Requester,
							"responder":  pmsg.RefreshPong.Responder,
							"freshness":  pmsg.RefreshPong.Freshness,
							"sent_at":    pmsg.RefreshPong.SentAt,
							"publisher":  pub.Publisher.String(),
							"boot_time":  pub.BootTime,
							"seq_num":    pub.SeqNum,
						}))

					default:
						// This will be logged even for BlobFetch commands, which is fine
						// (can be fixed but avoid the extra parse that is unused)
						// log.Warn(a, "Ignoring unknown message", "publisher", pub.Publisher)
					}
				}

				invokeBatch := func(name string, arr js.Value) {
					if arr.Get("length").Int() == 0 {
						return
					}
					cb := p[0].Get(name)
					if cb.Type() != js.TypeFunction {
						return
					}
					jsutil.Await(cb.Invoke(arr))
				}

				invokeBatch("on_yjs_delta", yjsDeltas)
				invokeBatch("on_mls_kp_ref", mlsKpRefs)
				invokeBatch("on_mls_welcome_ref", mlsWelcomeRefs)
				invokeBatch("on_mls_commit_ref", mlsCommitRefs)
				invokeBatch("on_refresh_ping", refreshPings)
				invokeBatch("on_refresh_pong", refreshPongs)
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

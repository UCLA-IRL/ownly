//go:build js && wasm

package app

import (
	"crypto/cipher"
	"fmt"
	"syscall/js"
	"time"

	enc "github.com/named-data/ndnd/std/encoding"
	"github.com/named-data/ndnd/std/log"
	"github.com/named-data/ndnd/std/ndn"
	"github.com/named-data/ndnd/std/object/storage"
	"github.com/named-data/ndnd/std/security"
	"github.com/named-data/ndnd/std/security/keychain"
	"github.com/named-data/ndnd/std/security/trust_schema"
	jsutil "github.com/named-data/ndnd/std/utils/js"
)

type App struct {
	face     ndn.Face
	engine   ndn.Engine
	store    ndn.Store
	keychain ndn.KeyChain

	// Trust config for testbed certs only
	// In practice all trust configs are currently the same, but
	// each workspace could theoretically have a different trust config.
	trust *security.TrustConfig

	// Encryption keys
	psk []byte
	dsk []byte
	aes cipher.Block
	ivb uint64

	// Pending DSK requests -> cancel function
	dskReqs map[string]*time.Timer

	// Tracks boot sync groups we have already joined to avoid duplicates
	bootSyncs map[string]bool

	// Active boot owner session (owners open one workspace at a time)
	bootSyncSession *bootSyncSession

	// Optional app-defined payload to publish with participant boot sync join.
	joinPayloads map[string][]byte

	// JS callbacks to load/persist boot SVS state
	bootStateLoad    js.Value
	bootStatePersist js.Value

	// JS callback for owner-side participant boot join payloads.
	bootJoinPayloadCb js.Value
}

var _ndnd_store_js = js.Global().Get("_ndnd_store_js")
var _ndnd_keychain_js = js.Global().Get("_ndnd_keychain_js")

// function(connected: boolean, router: string): void
var _ndnd_conn_change_js = js.Global().Get("_ndnd_conn_change_js")

func NewApp() *App {
	// Setup JS shim store
	store := storage.NewJsStore(_ndnd_store_js)

	// Setup JS shim keychain
	kc, err := keychain.NewKeyChainJS(_ndnd_keychain_js, store)
	if err != nil {
		panic(err)
	}

	a := &App{
		store:        store,
		keychain:     kc,
		dskReqs:      make(map[string]*time.Timer),
		bootSyncs:    make(map[string]bool),
		joinPayloads: make(map[string][]byte),
	}
	a.initialize()
	return a
}

func NewNodeApp() *App {
	// NodeApp currently only supports consumer mode.
	// If we want producer mode, we need a real store implementation.
	// FS already works but badger may be too slow.
	store := storage.NewMemoryStore()

	// Setup directory keychain
	// TODO: make this path configurable, maybe env variable
	kc, err := keychain.NewKeyChainDir("./keychain", store)
	if err != nil {
		panic(err)
	}

	a := &App{
		store:        store,
		keychain:     kc,
		dskReqs:      make(map[string]*time.Timer),
		bootSyncs:    make(map[string]bool),
		joinPayloads: make(map[string][]byte),
	}

	a.initialize()
	return a
}

// Common initialization for both Node and WASM apps
func (a *App) initialize() {
	var err error

	// Insert trust anchor
	if err = a.keychain.InsertCert(testbedRootCert); err != nil {
		panic(err)
	}

	// trust config
	a.trust, err = getTrustConfig(a.keychain)
	if err != nil {
		panic(err)
	}
}

func (a *App) String() string {
	return "app"
}

func (a *App) JsApi() js.Value {
	api := map[string]any{
		// has_testbed_key(): Promise<boolean>;
		"has_testbed_key": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			key, _ := a.GetTestbedKey()
			return key != nil, nil
		}),

		// is_testbed_cert_expiring_soon(): Promise<boolean>;
		"is_testbed_cert_expiring_soon": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			// Check if certificate expires within one week
			_, notAfter := a.GetTestbedKey()
			return notAfter.Before(time.Now().Add(7 * 24 * time.Hour)), nil
		}),

		// get_testbed_key(): Promise<string>;
		"get_testbed_key": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			key, _ := a.GetTestbedKey()
			if key == nil {
				return nil, fmt.Errorf("no testbed key")
			}
			return js.ValueOf(key.KeyName().String()), nil
		}),

		// connect_testbed(): Promise<void>;
		"connect_testbed": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			return nil, a.ConnectTestbed()
		}),

		// ndncert_email(email: string, code: (status: string) => Promise<string>): Promise<void>;
		"ndncert_email": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			return nil, a.NdncertEmail(p[0].String(), func(status string) string {
				code, err := jsutil.Await(p[1].Invoke(status))
				if err != nil {
					return ""
				}
				return code.String()
			})
		}),

		// ndncert_dns(domain: string, confirm: (recordName: string, recordValue: string, status: string) => Promise<string>): Promise<void>;
		"ndncert_dns": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			return nil, a.NdncertDns(p[0].String(), func(recordName, expectedValue, status string) string {
				confirmation, err := jsutil.Await(p[1].Invoke(recordName, expectedValue, status))
				if err != nil {
					return ""
				}
				return confirmation.String()
			})
		}),

		// join_workspace(wksp: string, create: boolean, payload: Uint8Array | null): Promise<string>;
		"join_workspace": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			if len(p) < 3 || p[2].IsUndefined() {
				return nil, fmt.Errorf("payload argument required: pass Uint8Array or null")
			}
			payload := []byte(nil)
			if arg := p[2]; !arg.IsNull() {
				payload = jsutil.JsArrayToSlice(arg)
			}
			return a.JoinWorkspace(p[0].String(), p[1].Bool(), payload)
		}),

		// is_workspace_owner(wksp: string): Promise<boolean>;
		"is_workspace_owner": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			return a.IsWorkspaceOwner(p[0].String())
		}),

		// get_workspace(name: string, ignore: boolean): Promise<WorkspaceAPI>;
		"get_workspace": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			return a.GetWorkspace(p[0].String(), p[1].Bool())
		}),

		// wait_user_key(wksp: string): Promise<void>;
		"wait_user_key": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			return nil, a.WaitUserKey(p[0].String())
		}),

		// load_boot_state(load: () => Promise<Uint8Array|undefined>, persist: (state: Uint8Array) => Promise<void>): Promise<void>;
		"load_boot_state": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			// p[0]: load callback, p[1]: persist callback
			a.bootStateLoad = p[0]
			a.bootStatePersist = p[1]
			return nil, nil
		}),

		// on_boot_join_payload(cb: (workspace: string, preCertFullName: string, preCertKeyName: string, payload: Uint8Array) => Promise<void>): Promise<void>;
		"on_boot_join_payload": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			if len(p) == 0 || p[0].IsUndefined() || p[0].IsNull() {
				a.bootJoinPayloadCb = js.Undefined()
				return nil, nil
			}
			a.bootJoinPayloadCb = p[0]
			return nil, nil
		}),

		// list_identity_keys(): Promise<{identity: string; local: any[]; peers: any[]}>;
		"list_identity_keys": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			return a.identityOverview()
		}),

		// generate_identity_key(): Promise<any>;
		"generate_identity_key": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			entry, err := a.generateIdentityKey()
			if err != nil {
				return nil, err
			}
			return entry.toJs(), nil
		}),

		// import_identity_key(secret: Uint8Array): Promise<any>;
		"import_identity_key": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			entry, err := a.importIdentityKey(jsutil.JsArrayToSlice(p[0]))
			if err != nil {
				return nil, err
			}
			return entry.toJs(), nil
		}),

		// import_peer_certs(blobs: Uint8Array[]): Promise<any[]>;
		"import_peer_certs": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			blobArr := p[0]
			blobs := make([][]byte, blobArr.Length())
			for i := range blobs {
				blobs[i] = jsutil.JsArrayToSlice(blobArr.Index(i))
			}

			group := enc.Name(nil)
			if a.bootSyncSession != nil {
				group = a.bootSyncSession.group
			}
			entries, err := a.importPeerCerts(blobs, peerCertImportOpts{
				Published: false,
				Group:     group,
			})
			if err != nil {
				return nil, err
			}
			return entriesToJs(entries), nil
		}),

		// delete_identity_entry(certName: string): Promise<void>;
		"delete_identity_entry": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			name, err := enc.NameFromStr(p[0].String())
			if err != nil {
				return nil, err
			}
			return nil, a.deleteIdentityEntry(name)
		}),

		// export_identity_secret(keyName: string): Promise<Uint8Array>;
		"export_identity_secret": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			keyName, err := enc.NameFromStr(p[0].String())
			if err != nil {
				return nil, err
			}
			secret, err := a.exportIdentitySecret(keyName)
			if err != nil {
				return nil, err
			}
			return jsutil.SliceToJsArray(secret), nil
		}),

		// export_peer_certs(names: string[]): Promise<Uint8Array[]>;
		"export_peer_certs": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			raw := p[0]
			names := make([]enc.Name, 0, raw.Length())
			for i := 0; i < raw.Length(); i++ {
				name, err := enc.NameFromStr(raw.Index(i).String())
				if err != nil {
					return nil, err
				}
				names = append(names, name)
			}

			certs, err := a.exportPeerCerts(names)
			if err != nil {
				return nil, err
			}

			out := js.Global().Get("Array").New(len(certs))
			for i, wire := range certs {
				out.SetIndex(i, jsutil.SliceToJsArray(wire))
			}
			return out, nil
		}),

		// export_identity_cert(): Promise<Uint8Array>;
		"export_identity_cert": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			wire, err := a.exportIdentityCert()
			if err != nil {
				return nil, err
			}
			return jsutil.SliceToJsArray(wire), nil
		}),

		// export_identity_cert_by_name(certName: string): Promise<Uint8Array>;
		"export_identity_cert_by_name": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			certName, err := enc.NameFromStr(p[0].String())
			if err != nil {
				return nil, err
			}
			wire, err := a.exportIdentityCertByName(certName)
			if err != nil {
				return nil, err
			}
			return jsutil.SliceToJsArray(wire), nil
		}),
	}

	return js.ValueOf(api)
}

func getTrustConfig(keychain ndn.KeyChain) (trust *security.TrustConfig, err error) {
	schema, err := trust_schema.NewLvsSchema(SchemaBytes)
	if err != nil {
		return
	}

	trust, err = security.NewTrustConfig(keychain, schema, []enc.Name{testbedRootName})
	if err != nil {
		return
	}
	trust.UseDataNameFwHint = true

	return
}

// WaitUserKey blocks until trust schema suggests a valid user key for the workspace.
func (a *App) WaitUserKey(groupStr string) error {
	group, err := enc.NameFromStr(groupStr)
	if err != nil {
		return err
	}

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err == nil && a.trust.Suggest(group.Append(enc.NewKeywordComponent("KD"))) != nil {
			return nil
		}
		<-ticker.C
	}
}

func (a *App) GatherAnchors() [][]byte {
	anchors := make([][]byte, 0)
	seen := make(map[string]struct{})

	add := func(nameStr string) {
		name, err := enc.NameFromStr(nameStr)
		if err != nil {
			log.Warn(a, "Failed to parse cert name for repo anchor", "name", nameStr, "err", err)
			return
		}
		key := name.String()
		if _, ok := seen[key]; ok {
			return
		}

		wire, _ := a.store.Get(name, false)
		if wire == nil && len(name) > 0 {
			wire, _ = a.store.Get(name.Prefix(-1), true)
		}
		if wire == nil {
			log.Warn(a, "Missing cert wire for repo anchor", "name", name)
			return
		}

		anchors = append(anchors, wire)
		seen[key] = struct{}{}
	}

	if local, err := a.localIdentityEntries(); err == nil {
		for _, entry := range local {
			add(entry.CertName)
		}
	} else {
		log.Warn(a, "Failed to list local identity certs for repo anchor set", "err", err)
	}

	if peers, err := a.peerIdentityEntries(); err == nil {
		for _, entry := range peers {
			add(entry.CertName)
		}
	} else {
		log.Warn(a, "Failed to list peer identity certs for repo anchor set", "err", err)
	}

	return anchors
}

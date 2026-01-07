//go:build js && wasm

package app

import (
	"crypto/cipher"
	"fmt"
	"syscall/js"
	"time"

	enc "github.com/named-data/ndnd/std/encoding"
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

	// Known owner trust anchor certificate names
	ownerAnchors map[string]bool

	// JS callbacks to load/persist owner cert SVS state
	ownerStateLoad    js.Value
	ownerStatePersist js.Value
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
		ownerAnchors: make(map[string]bool),
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
		ownerAnchors: make(map[string]bool),
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

	// Testbed trust config
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

		// get_identity_name(): Promise<string>;
		"get_identity_name": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			key, _ := a.GetTestbedKey()
			if key == nil {
				return nil, fmt.Errorf("no testbed key")
			}
			return js.ValueOf(key.KeyName().Prefix(-2).String()), nil
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

		// join_workspace(wksp: string, create: boolean): Promise<string>;
		"join_workspace": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			return a.JoinWorkspace(p[0].String(), p[1].Bool())
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

		// set_owner_state_callbacks(load: () => Promise<Uint8Array|undefined>, persist: (state: Uint8Array) => Promise<void>): Promise<void>;
		"set_owner_state_callbacks": jsutil.AsyncFunc(func(this js.Value, p []js.Value) (any, error) {
			// p[0]: load callback, p[1]: persist callback
			a.ownerStateLoad = p[0]
			a.ownerStatePersist = p[1]
			return nil, nil
		}),
	}

	return js.ValueOf(api)
}

// GetTestbedKey returns an instance of the trust configuration
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
	trust, err := getTrustConfig(a.keychain)
	if err != nil {
		return err
	}
	group, err := enc.NameFromStr(groupStr)
	if err != nil {
		return err
	}
	detect := group.Append(enc.NewKeywordComponent("KD"))

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if trust.Suggest(detect) != nil {
			return nil
		}
		<-ticker.C
	}
}

package historylog

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	enc "github.com/named-data/ndnd/std/encoding"
	"github.com/named-data/ndnd/std/ndn"
	spec "github.com/named-data/ndnd/std/ndn/spec_2022"
	"github.com/named-data/ndnd/std/object"
)

const PendingPageSize = 64

// Scope binds pending work to its original submitting identity and trusted log.
// logID must identify both the log prefix and its trust configuration. A change
// in either creates a different scope; pending work must never be retargeted.
type Scope struct{ key string }

func NewScope(identity enc.Name, logID string) (Scope, error) {
	if len(identity) == 0 || logID == "" {
		return Scope{}, fmt.Errorf("history scope requires identity and log ID")
	}
	key, err := json.Marshal([2]string{hex.EncodeToString(identity.BytesInner()), logID})
	return Scope{key: string(key)}, err
}

func (s Scope) Key() string { return s.key }

// Packet owns the exact signed wire. Construct it with PrepareData; parsing is
// only for indexing and never changes the bytes used for the digest.
type Packet struct {
	name enc.Name
	wire []byte
	hash Hash
}

func PrepareData(wire enc.Wire) (Packet, error) {
	return prepareOwnedData(bytes.Clone(wire.Join()))
}

func prepareOwnedData(wire []byte) (Packet, error) {
	data, _, err := spec.Spec{}.ReadData(enc.NewWireView(enc.Wire{wire}))
	if err != nil {
		return Packet{}, fmt.Errorf("history packet: %w", err)
	}
	return Packet{name: data.Name(), wire: wire, hash: sha256.Sum256(wire)}, nil
}

// Pending identifies one durable queue generation. An old acknowledgement must
// not dequeue a newer row for the same hash.
type Pending struct {
	ID   uint64
	Hash Hash
}

// Outbox commits packets and pending hashes atomically. Ack durably dequeues
// authenticated acknowledgements, retaining the signed packets themselves.
// Dispatch never mutates pending rows. Callers must await Ack before completing
// a batch and retain work on any error. Pending reads at most PendingPageSize.
type Outbox interface {
	CommitProduced(Scope, []Packet) error
	Pending(scope Scope, afterRowID uint64, limit int) ([]Pending, error)
	Ack(Scope, []Pending) error
}

// Capture is immutable and may be shared by producers for the same scope.
type Capture struct {
	outbox Outbox
	scope  Scope
}

func NewCapture(outbox Outbox, scope Scope) (*Capture, error) {
	if outbox == nil || scope.key == "" {
		return nil, fmt.Errorf("history capture requires durable storage and scope")
	}
	return &Capture{outbox: outbox, scope: scope}, nil
}

// PersistProducedData commits exact public Data before callers install it in a
// keychain, export it, or publish it. Never pass private-key/secret packets here.
func (c *Capture) PersistProducedData(wires ...enc.Wire) error {
	packets := make([]Packet, 0, len(wires))
	for _, wire := range wires {
		packet, err := PrepareData(wire)
		if err != nil {
			return err
		}
		packets = append(packets, packet)
	}
	return c.outbox.CommitProduced(c.scope, packets)
}

type producerClient struct {
	ndn.Client
	capture *Capture
}

// WrapClient captures only Produce. Fetches, cache writes, and other client
// operations keep their existing behavior. A nil capture leaves logging disabled.
// Do not wrap the client used for log append/check traffic.
func WrapClient(client ndn.Client, capture *Capture) ndn.Client {
	if capture == nil {
		return client
	}
	return &producerClient{Client: client, capture: capture}
}

// PersistProducedData stores directly signed public Data in the client's scope.
// With capture disabled it preserves ordinary packet storage behavior.
func PersistProducedData(client ndn.Client, wire enc.Wire) error {
	if producer, ok := client.(*producerClient); ok {
		return producer.capture.PersistProducedData(wire)
	}
	packet, err := PrepareData(wire)
	if err != nil {
		return err
	}
	return client.Store().Put(packet.name, packet.wire)
}

func (c *producerClient) Produce(args ndn.ProduceArgs) (enc.Name, error) {
	if len(args.Name) == 0 || !args.Name.At(-1).IsVersion() {
		return nil, fmt.Errorf("object version not set: %s", args.Name)
	}
	return c.capture.Produce(args, c.SuggestSigner(args.Name.Prefix(-1)))
}

// Produce reuses upstream segmentation/signing with private staging, since the
// pinned upstream producer ignores deferred Commit errors. Nothing enters the
// shared packet store/cache until production and the durable commit succeed.
func (c *Capture) Produce(args ndn.ProduceArgs, signer ndn.Signer) (enc.Name, error) {
	if signer == nil {
		return nil, fmt.Errorf("no valid signer found for %s", args.Name)
	}
	if len(args.Name) == 0 {
		return nil, fmt.Errorf("object name is empty")
	}
	staged := &stagingStore{}
	name, err := object.Produce(args, staged, signer)
	if err != nil {
		return nil, err
	}
	if err = c.outbox.CommitProduced(c.scope, staged.packets); err != nil {
		return nil, fmt.Errorf("persist produced history: %w", err)
	}
	return name, nil
}

// stagingStore is private to one object. Upstream uses only Begin/Put/Commit.
// Its deferred Commit must be harmless even after a signing error.
type stagingStore struct{ packets []Packet }

func (s *stagingStore) Put(name enc.Name, wire []byte) error {
	// Upstream Produce supplies the name and exact wire it just signed.
	s.packets = append(s.packets, Packet{name: name, wire: wire, hash: sha256.Sum256(wire)})
	return nil
}

func (s *stagingStore) Begin() (ndn.Store, error) { return s, nil }
func (s *stagingStore) Commit() error             { return nil }
func (s *stagingStore) Rollback() error           { s.packets = nil; return nil }
func (s *stagingStore) Get(enc.Name, bool) ([]byte, error) {
	return nil, fmt.Errorf("producer staging does not support reads")
}
func (s *stagingStore) Remove(enc.Name) error {
	return fmt.Errorf("producer staging does not support removal")
}
func (s *stagingStore) RemovePrefix(enc.Name) error {
	return fmt.Errorf("producer staging does not support removal")
}
func (s *stagingStore) RemoveFlatRange(enc.Name, enc.Component, enc.Component) error {
	return fmt.Errorf("producer staging does not support removal")
}

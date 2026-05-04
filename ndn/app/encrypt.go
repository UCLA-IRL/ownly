//go:build js && wasm

package app

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"time"

	spec_repo "github.com/named-data/ndnd/repo/tlv"
	enc "github.com/named-data/ndnd/std/encoding"
	"github.com/named-data/ndnd/std/log"
	"github.com/named-data/ndnd/std/ndn"
	spec "github.com/named-data/ndnd/std/ndn/spec_2022"
	"github.com/named-data/ndnd/std/types/optional"
	"github.com/pulsejet/ownly/ndn/app/tlv"
)

const (
	maxSessionKeys  = 5
	LegacySessionID = "legacy"
)

func (a *App) installEncryptKey(sessionID string, key []byte) error {
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}

	a.aes = block
	a.sessionId = sessionID

	ring := []SessionCipher{{SessionId: sessionID, AES: block}}
	for _, e := range a.keyRing {
		if e.SessionId == sessionID {
			continue
		}
		ring = append(ring, e)
		if len(ring) == maxSessionKeys {
			break
		}
	}
	a.keyRing = ring
	return nil
}

func (a *App) cipherForSession(sessionID string) cipher.Block {
	if a.sessionId == sessionID && a.aes != nil {
		return a.aes
	}
	for _, e := range a.keyRing {
		if e.SessionId == sessionID {
			return e.AES
		}
	}
	return nil
}

func (a *App) processDskRequest(client ndn.Client, group enc.Name, pub []byte) enc.Wire {
	if len(a.dsk) != 32 || len(pub) > 64 {
		// We are not capable of answering DSK requests
		return nil
	}

	sk, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		log.Error(a, "Failed to generate DSK response key", "err", err)
		return nil
	}
	sym, err := x25519HkdfSha256(pub, sk.Bytes())
	if err != nil {
		log.Error(a, "Failed to compute DSK response sym key", "err", err)
		return nil
	}

	cipher, err := aes.NewCipher(sym)
	if err != nil {
		log.Error(a, "Failed to create AES cipher", "err", err)
		return nil
	}

	// Encrypt the DSK, should be multiple of block size
	ciphertext := make([]byte, len(a.dsk))
	for i := 0; i < len(a.dsk); i += cipher.BlockSize() {
		cipher.Encrypt(ciphertext[i:], a.dsk[i:])
	}

	dskRes := &tlv.DSKResponse{
		X25519Peer: sk.PublicKey().Bytes(),
		Ciphertext: ciphertext,
	}

	// Create Data packet under the group
	name := group.
		Append(enc.NewKeywordComponent("DSK")).
		Append(enc.NewGenericBytesComponent(pub)).
		WithVersion(enc.VersionUnixMicro)

	signer := client.SuggestSigner(name)
	if signer == nil {
		log.Error(a, "Failed to suggest signer for DSK response", "name", name)
		return nil
	}

	data, err := spec.Spec{}.MakeData(name, &ndn.DataConfig{
		Freshness: optional.Some(60 * time.Second),
	}, dskRes.Encode(), signer)
	if err != nil {
		log.Error(a, "Failed to create DSK response", "err", err)
		return nil
	}
	log.Info(a, "Created DSK response", "name", name)

	repoCmd := &spec_repo.RepoCmd{
		BlobFetch: &spec_repo.BlobFetch{
			Data: [][]byte{data.Wire.Join()},
		},
	}
	return repoCmd.Encode()
}

func (a *App) fetchDsk(client ndn.Client, wksp enc.Name, priv []byte) ([]byte, error) {
	sk, err := ecdh.X25519().NewPrivateKey(priv)
	if err != nil {
		return nil, err
	}

	name := wksp.
		Append(enc.NewGenericComponent("root")).
		Append(enc.NewKeywordComponent("DSK")).
		Append(enc.NewGenericBytesComponent(sk.PublicKey().Bytes()))
	log.Info(a, "Expressing DSK request", "name", name)

	ch := make(chan ndn.ExpressCallbackArgs, 1)
	client.ExpressR(ndn.ExpressRArgs{
		Name: name,
		Config: &ndn.InterestConfig{
			CanBePrefix: true,
			MustBeFresh: true,
			Lifetime:    optional.Some(2 * time.Second),
		},
		Retries:  3,
		Callback: func(args ndn.ExpressCallbackArgs) { ch <- args },
	})
	args := <-ch

	if args.Error != nil {
		return nil, args.Error
	}
	if args.Result != ndn.InterestResultData {
		return nil, fmt.Errorf("%s", args.Result)
	}

	dskRes, err := tlv.ParseDSKResponse(enc.NewWireView(args.Data.Content()), false)
	if err != nil {
		return nil, fmt.Errorf("failed to parse DSK response: %w", err)
	}

	sym, err := x25519HkdfSha256(dskRes.X25519Peer, priv)
	if err != nil {
		return nil, fmt.Errorf("failed to compute DSK response sym key: %w", err)
	}

	cipher, err := aes.NewCipher(sym)
	if err != nil {
		return nil, fmt.Errorf("failed to create AES cipher: %w", err)
	}

	// Decrypt the DSK, should be multiple of block size
	dsk := make([]byte, len(dskRes.Ciphertext))
	for i := 0; i < len(dskRes.Ciphertext); i += cipher.BlockSize() {
		cipher.Decrypt(dsk[i:], dskRes.Ciphertext[i:])
	}

	return dsk, nil
}

func (a *App) encryptPub(pub *tlv.Message, seq uint64) (*tlv.Message, error) {
	if a.aes == nil {
		return nil, fmt.Errorf("AES key not set")
	}

	iv := make([]byte, 12) // 96-bit IV
	binary.BigEndian.PutUint32(iv[0:], uint32(a.ivb))
	binary.BigEndian.PutUint64(iv[4:], a.ivb+seq)
	ciphertext, err := aeadSeal(a.aes, iv, pub.Encode().Join())
	if err != nil {
		return nil, err
	}

	return &tlv.Message{
		AeadBlock: &tlv.AeadBlock{
			SessionID: a.sessionId,
			IV:         iv,
			Ciphertext: ciphertext,
		},
	}, nil
}

func (a *App) decryptPub(pub *tlv.Message) (*tlv.Message, error) {
	if pub.AeadBlock == nil {
		return pub, nil
	}

	aes := a.cipherForSession(pub.AeadBlock.SessionID)
	if aes == nil {
		return nil, fmt.Errorf("no cipher for session ID %s", pub.AeadBlock.SessionID)
	}

	iv := pub.AeadBlock.IV
	ct := pub.AeadBlock.Ciphertext
	plaintext, err := aeadOpen(aes, iv, ct)
	if err != nil {
		return nil, err
	}

	return tlv.ParseMessage(enc.NewBufferView(plaintext), true)
}

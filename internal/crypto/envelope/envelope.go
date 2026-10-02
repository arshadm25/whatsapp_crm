// Package envelope encrypts secrets (BISU tokens, two-step PINs, TOTP seeds, webhook secrets)
// with a fresh AES-256-GCM data key per record. The data key is itself sealed by a master key
// that lives outside the database, so a database dump alone never reveals a secret.
package envelope

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

// Sealed is what gets stored: the ciphertext, the wrapped data key, and which master key wrapped it.
type Sealed struct {
	Ciphertext       []byte
	DataKeyEncrypted []byte
	MasterKeyVersion int
}

type Keyring struct {
	keys    map[int][]byte
	current int
}

// NewKeyring takes master keys by version; current is used for new seals.
func NewKeyring(keys map[int][]byte, current int) (*Keyring, error) {
	if _, ok := keys[current]; !ok {
		return nil, errors.New("envelope: current master key version not present")
	}
	for v, k := range keys {
		if len(k) != 32 {
			return nil, fmt.Errorf("envelope: master key %d must be 32 bytes", v)
		}
	}
	return &Keyring{keys: keys, current: current}, nil
}

// Seal encrypts plaintext under a new data key. aad binds the ciphertext to its owner (for example
// the tenant and record ID), so a ciphertext copied to another row will not decrypt.
func (k *Keyring) Seal(plaintext, aad []byte) (Sealed, error) {
	dataKey := make([]byte, 32)
	if _, err := rand.Read(dataKey); err != nil {
		return Sealed{}, err
	}
	ct, err := gcmSeal(dataKey, plaintext, aad)
	if err != nil {
		return Sealed{}, err
	}
	wrapped, err := gcmSeal(k.keys[k.current], dataKey, nil)
	if err != nil {
		return Sealed{}, err
	}
	clear(dataKey)
	return Sealed{Ciphertext: ct, DataKeyEncrypted: wrapped, MasterKeyVersion: k.current}, nil
}

// Open reverses Seal.
func (k *Keyring) Open(s Sealed, aad []byte) ([]byte, error) {
	master, ok := k.keys[s.MasterKeyVersion]
	if !ok {
		return nil, fmt.Errorf("envelope: master key version %d not loaded", s.MasterKeyVersion)
	}
	dataKey, err := gcmOpen(master, s.DataKeyEncrypted, nil)
	if err != nil {
		return nil, fmt.Errorf("envelope: unwrap data key: %w", err)
	}
	defer clear(dataKey)
	pt, err := gcmOpen(dataKey, s.Ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("envelope: decrypt: %w", err)
	}
	return pt, nil
}

// SealCompact packs the wrapped data key, version and ciphertext into one blob, for columns
// that store a single bytea (two_step_pin_enc, totp_secret_enc, secret_ciphertext).
func (k *Keyring) SealCompact(plaintext, aad []byte) ([]byte, error) {
	s, err := k.Seal(plaintext, aad)
	if err != nil {
		return nil, err
	}
	if len(s.DataKeyEncrypted) > 255 || s.MasterKeyVersion > 0xffff {
		return nil, errors.New("envelope: compact header overflow")
	}
	out := make([]byte, 0, 3+len(s.DataKeyEncrypted)+len(s.Ciphertext))
	out = append(out, byte(s.MasterKeyVersion>>8), byte(s.MasterKeyVersion), byte(len(s.DataKeyEncrypted)))
	out = append(out, s.DataKeyEncrypted...)
	return append(out, s.Ciphertext...), nil
}

// OpenCompact reverses SealCompact.
func (k *Keyring) OpenCompact(blob, aad []byte) ([]byte, error) {
	if len(blob) < 3 {
		return nil, errors.New("envelope: blob too short")
	}
	ver := int(blob[0])<<8 | int(blob[1])
	n := int(blob[2])
	if len(blob) < 3+n {
		return nil, errors.New("envelope: blob truncated")
	}
	return k.Open(Sealed{DataKeyEncrypted: blob[3 : 3+n], Ciphertext: blob[3+n:], MasterKeyVersion: ver}, aad)
}

func gcmSeal(key, plaintext, aad []byte) ([]byte, error) {
	aead, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, plaintext, aad), nil
}

func gcmOpen(key, sealed, aad []byte) ([]byte, error) {
	aead, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(sealed) < aead.NonceSize() {
		return nil, errors.New("ciphertext too short")
	}
	return aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], aad)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

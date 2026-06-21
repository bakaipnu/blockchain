package wallet

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"math/big"
)

type Wallet struct {
	PrivateKey *ecdsa.PrivateKey
	PublicKey  *ecdsa.PublicKey
}

func NewWallet() (*Wallet, error) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate private key: %w", err)
	}

	return &Wallet{
		PrivateKey: privateKey,
		PublicKey:  &privateKey.PublicKey,
	}, nil
}

func (w *Wallet) Address() []byte {
	publicKeyBytes := PublicKeyToBytes(w.PublicKey)
	hash := sha256.Sum256(publicKeyBytes)
	return hash[:]
}

func PublicKeyToBytes(publicKey *ecdsa.PublicKey) []byte {
	return elliptic.Marshal(publicKey.Curve, publicKey.X, publicKey.Y)
}

func BytesToPublicKey(data []byte) *ecdsa.PublicKey {
	curve := elliptic.P256()
	x, y := elliptic.Unmarshal(curve, data)
	if x == nil {
		return nil
	}

	return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}
}

type Signature struct {
	R *big.Int
	S *big.Int
}

func (w *Wallet) Sign(data []byte) (*Signature, error) {
	hash := sha256.Sum256(data)

	r, s, err := ecdsa.Sign(rand.Reader, w.PrivateKey, hash[:])
	if err != nil {
		return nil, fmt.Errorf("failed to sign: %w", err)
	}

	return &Signature{R: r, S: s}, nil
}

func Verify(publicKey *ecdsa.PublicKey, data []byte, signature *Signature) bool {
	hash := sha256.Sum256(data)
	return ecdsa.Verify(publicKey, hash[:], signature.R, signature.S)
}

func privateKeyToPEM(privateKey *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal private key: %w", err)
	}

	block := &pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: der,
	}

	return pem.EncodeToMemory(block), nil
}

func pemToPrivateKey(data []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	return x509.ParseECPrivateKey(block.Bytes)
}

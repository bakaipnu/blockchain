package transaction

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/bakaipnu/blockchain/internal/wallet"
)

type Transaction struct {
	ID        []byte
	Sender    []byte
	Receiver  []byte
	Amount    int64
	Timestamp int64
	Signature *wallet.Signature
}

func NewTransaction(sender, receiver *wallet.Wallet, amount int64) (*Transaction, error) {
	if amount <= 0 {
		return nil, errors.New("amount must be greater than zero")
	}

	tx := &Transaction{
		Sender:    wallet.PublicKeyToBytes(sender.PublicKey),
		Receiver:  wallet.PublicKeyToBytes(receiver.PublicKey),
		Amount:    amount,
		Timestamp: time.Now().Unix(),
	}

	tx.ID = tx.hash()

	return tx, nil
}

func (tx *Transaction) Sign(senderWallet *wallet.Wallet) error {
	senderPublicKeyBytes := wallet.PublicKeyToBytes(senderWallet.PublicKey)
	if !bytes.Equal(tx.Sender, senderPublicKeyBytes) {
		return errors.New("only sender can sign transaction")
	}

	signature, err := senderWallet.Sign(tx.dataToSign())
	if err != nil {
		return fmt.Errorf("failed to sign transaction: %w", err)
	}

	tx.Signature = signature

	return nil
}

func (tx *Transaction) Verify() bool {
	if tx.Signature == nil {
		return false
	}

	publicKey := wallet.BytesToPublicKey(tx.Sender)
	if publicKey == nil {
		return false
	}

	return wallet.Verify(publicKey, tx.dataToSign(), tx.Signature)
}

func (tx *Transaction) IsCoinbase() bool {
	return len(tx.Sender) == 0
}

func NewCoinbaseTX(to *wallet.Wallet, reward int64) *Transaction {
	tx := &Transaction{
		Sender:    []byte{},
		Receiver:  wallet.PublicKeyToBytes(to.PublicKey),
		Amount:    reward,
		Timestamp: time.Now().Unix(),
	}

	tx.ID = tx.hash()

	return tx
}

func (tx *Transaction) hash() []byte {
	h := sha256.Sum256(tx.dataToSign())
	return h[:]
}

func (tx *Transaction) dataToSign() []byte {
	amountBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(amountBytes, uint64(tx.Amount))

	tsBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(tsBytes, uint64(tx.Timestamp))

	return bytes.Join(
		[][]byte{tx.Sender, tx.Receiver, amountBytes, tsBytes},
		[]byte{},
	)
}

func NewFixedCoinbase() *Transaction {
	tx := &Transaction{
		Sender:    []byte{},
		Receiver:  []byte("genesis"),
		Amount:    50,
		Timestamp: 0,
	}
	tx.ID = tx.hash()
	return tx
}

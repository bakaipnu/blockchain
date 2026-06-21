package blockchain

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"time"

	"github.com/bakaipnu/blockchain/internal/transaction"
)

type Block struct {
	Timestamp    int64
	Transactions []*transaction.Transaction
	PreviousHash []byte
	Hash         []byte
}

func NewBlock(transactions []*transaction.Transaction, previousHash []byte) *Block {
	b := &Block{
		Timestamp:    time.Now().Unix(),
		Transactions: transactions,
		PreviousHash: previousHash,
	}

	b.Hash = b.CalculateHash()

	return b
}

func NewGenesisBlock() *Block {
	tx := &transaction.Transaction{
		ID:        []byte("genesis-tx"),
		Sender:    []byte{},
		Receiver:  []byte("genesis"),
		Amount:    0,
		Timestamp: 0,
	}

	b := &Block{
		Timestamp:    0,
		Transactions: []*transaction.Transaction{tx},
		PreviousHash: []byte{},
	}
	b.Hash = b.CalculateHash()
	return b
}

func (b *Block) CalculateHash() []byte {
	timestamp := make([]byte, 8)
	binary.BigEndian.PutUint64(timestamp, uint64(b.Timestamp))

	data := bytes.Join([][]byte{
		b.PreviousHash,
		timestamp,
		b.merkleRoot(),
	}, []byte{})

	h := sha256.Sum256(data)

	return h[:]
}

func (b *Block) merkleRoot() []byte {
	if len(b.Transactions) == 0 {
		empty := sha256.Sum256([]byte{})
		return empty[:]
	}

	var ids [][]byte
	for _, tx := range b.Transactions {
		ids = append(ids, tx.ID)
	}

	hash := sha256.Sum256(bytes.Join(ids, []byte{}))
	return hash[:]
}

func (b *Block) HasValidTransactions() bool {
	for _, tx := range b.Transactions {
		if tx.IsCoinbase() {
			continue
		}

		if !tx.Verify() {
			return false
		}
	}

	return true
}

func (b *Block) IsGenesisBlock() bool {
	return len(b.PreviousHash) == 0
}

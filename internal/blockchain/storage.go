package blockchain

import (
	"encoding/json"
	"fmt"
	"math/big"
	"time"

	"github.com/bakaipnu/blockchain/internal/transaction"
	"github.com/bakaipnu/blockchain/internal/wallet"
	bolt "go.etcd.io/bbolt"
)

var (
	bucketBlocks = []byte("blocks")
	bucketMeta   = []byte("meta")
	keyLastHash  = []byte("last_hash")
)

type Storage struct {
	db *bolt.DB
}

func OpenStorage(path string) (*Storage, error) {
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("open db %s: %w", path, err)
	}

	err = db.Update(func(tx *bolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists(bucketBlocks); err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists(bucketMeta); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("init buckets: %w", err)
	}

	return &Storage{db: db}, nil
}

func (s *Storage) Close() error {
	return s.db.Close()
}

func (s *Storage) SaveBlock(block *Block) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketBlocks)

		data, err := serializeBlock(block)
		if err != nil {
			return fmt.Errorf("serialize block: %w", err)
		}

		if err := b.Put(block.Hash, data); err != nil {
			return fmt.Errorf("put block: %w", err)
		}

		meta := tx.Bucket(bucketMeta)
		return meta.Put(keyLastHash, block.Hash)
	})
}

func (s *Storage) LoadAllBlocks() ([]*Block, error) {
	var lastHash []byte

	err := s.db.View(func(tx *bolt.Tx) error {
		meta := tx.Bucket(bucketMeta)
		v := meta.Get(keyLastHash)
		if v != nil {
			lastHash = make([]byte, len(v))
			copy(lastHash, v)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	if lastHash == nil {
		return nil, nil
	}

	var chain []*Block

	err = s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketBlocks)
		current := lastHash

		for {
			data := b.Get(current)
			if data == nil {
				break
			}

			block, err := deserializeBlock(data)
			if err != nil {
				return fmt.Errorf("deserialize block %x: %w", current, err)
			}

			chain = append(chain, block)

			if len(block.PreviousHash) == 0 {
				break
			}
			current = block.PreviousHash
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}

	return chain, nil
}

func NewWithStorage(dbPath string) (*Blockchain, *Storage, error) {
	storage, err := OpenStorage(dbPath)
	if err != nil {
		return nil, nil, err
	}

	blocks, err := storage.LoadAllBlocks()
	if err != nil {
		storage.Close()
		return nil, nil, fmt.Errorf("load blocks: %w", err)
	}

	var bc *Blockchain

	if len(blocks) == 0 {
		genesis := NewGenesisBlock()
		if err := storage.SaveBlock(genesis); err != nil {
			storage.Close()
			return nil, nil, fmt.Errorf("save genesis: %w", err)
		}
		bc = &Blockchain{blocks: []*Block{genesis}}
	} else {
		bc = &Blockchain{blocks: blocks}
	}

	return bc, storage, nil
}

type blockJSON struct {
	Timestamp    int64     `json:"timestamp"`
	PreviousHash []byte    `json:"previous_hash"`
	Hash         []byte    `json:"hash"`
	Transactions []*txJSON `json:"transactions"`
}

type txJSON struct {
	ID        []byte `json:"id"`
	Sender    []byte `json:"sender"`
	Receiver  []byte `json:"receiver"`
	Amount    int64  `json:"amount"`
	Timestamp int64  `json:"timestamp"`
	SigR      []byte `json:"sig_r,omitempty"`
	SigS      []byte `json:"sig_s,omitempty"`
}

func serializeBlock(b *Block) ([]byte, error) {
	bj := &blockJSON{
		Timestamp:    b.Timestamp,
		PreviousHash: b.PreviousHash,
		Hash:         b.Hash,
		Transactions: make([]*txJSON, len(b.Transactions)),
	}

	for i, tx := range b.Transactions {
		tj := &txJSON{
			ID:        tx.ID,
			Sender:    tx.Sender,
			Receiver:  tx.Receiver,
			Amount:    tx.Amount,
			Timestamp: tx.Timestamp,
		}
		if tx.Signature != nil {
			tj.SigR = tx.Signature.R.Bytes()
			tj.SigS = tx.Signature.S.Bytes()
		}
		bj.Transactions[i] = tj
	}

	return json.Marshal(bj)
}

func deserializeBlock(data []byte) (*Block, error) {
	var bj blockJSON
	if err := json.Unmarshal(data, &bj); err != nil {
		return nil, err
	}

	b := &Block{
		Timestamp:    bj.Timestamp,
		PreviousHash: bj.PreviousHash,
		Hash:         bj.Hash,
		Transactions: make([]*transaction.Transaction, len(bj.Transactions)),
	}

	for i, tj := range bj.Transactions {
		tx := &transaction.Transaction{
			ID:        tj.ID,
			Sender:    tj.Sender,
			Receiver:  tj.Receiver,
			Amount:    tj.Amount,
			Timestamp: tj.Timestamp,
		}
		if tj.SigR != nil && tj.SigS != nil {
			tx.Signature = &wallet.Signature{}
			tx.Signature.R = new(big.Int).SetBytes(tj.SigR)
			tx.Signature.S = new(big.Int).SetBytes(tj.SigS)
		}
		b.Transactions[i] = tx
	}

	return b, nil
}

func (bc *Blockchain) AddBlockWithStorage(block *Block, s *Storage) error {
	if err := bc.AddBlock(block); err != nil {
		return err
	}
	return s.SaveBlock(block)
}

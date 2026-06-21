package blockchain

import (
	"errors"
	"sync"
)

type Blockchain struct {
	blocks []*Block
	mu     sync.RWMutex
}

func New() *Blockchain {
	genesis := NewGenesisBlock()

	return &Blockchain{
		blocks: []*Block{genesis},
	}
}

func (bc *Blockchain) AddBlock(block *Block) error {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	last := bc.blocks[len(bc.blocks)-1]

	if !equalBytes(block.PreviousHash, last.Hash) {
		return errors.New("invalid previous hash: block does not extend current chain")
	}

	expected := block.CalculateHash()
	if !equalBytes(block.Hash, expected) {
		return errors.New("invalid previous hash: hash does bot match block contents")
	}

	if !block.HasValidTransactions() {
		return errors.New("block contains invalid transactions")
	}

	bc.blocks = append(bc.blocks, block)

	return nil
}

func (bc *Blockchain) LastBlock() *Block {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return bc.blocks[len(bc.blocks)-1]
}

func (bc *Blockchain) Height() int {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return len(bc.blocks)
}

func (bc *Blockchain) Blocks() []*Block {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	cp := make([]*Block, len(bc.blocks))
	copy(cp, bc.blocks)
	return cp
}

func (bc *Blockchain) IsValid() bool {
	bc.mu.RLock()
	defer bc.mu.RUnlock()

	for i := 1; i < len(bc.blocks); i++ {
		current := bc.blocks[i]
		previous := bc.blocks[i-1]

		if !equalBytes(current.Hash, current.CalculateHash()) {
			return false
		}

		if !equalBytes(current.PreviousHash, previous.Hash) {
			return false
		}

		if !current.HasValidTransactions() {
			return false
		}
	}

	return true
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

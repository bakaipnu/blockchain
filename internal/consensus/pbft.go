package consensus

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"

	"github.com/bakaipnu/blockchain/internal/blockchain"
)

type MessageType string

const (
	MessagePrePrepare MessageType = "PRE-PREPARE"
	MessagePrepare    MessageType = "PREPARE"
	MessageCommit     MessageType = "COMMIT"
	MessageViewChange MessageType = "VIEW-CHANGE"
)

type Message struct {
	Type     MessageType
	View     int
	Sequence int
	Digest   string
	NodeID   string
	Block    *blockchain.Block
}

type phase int

const (
	phaseIdle phase = iota
	phasePrePrepare
	phasePrepare
	phaseCommitted
)

type roundState struct {
	block    *blockchain.Block
	phase    phase
	prepares map[string]bool
	commits  map[string]bool
}

type Transport interface {
	Send(toNodeID string, message Message) error
	Broadcast(message Message) error
}

type Engine struct {
	nodeID    string
	nodes     []string
	primaryID string
	view      int
	sequence  int
	rounds    map[string]*roundState
	mu        sync.Mutex
	transport Transport
	onCommit  func(block *blockchain.Block) error
}

func NewEngine(nodeID string, nodes []string, transport Transport, onCommit func(*blockchain.Block) error) (*Engine, error) {
	return &Engine{
		nodeID:    nodeID,
		nodes:     nodes,
		primaryID: nodes[0],
		view:      0,
		sequence:  0,
		rounds:    make(map[string]*roundState),
		transport: transport,
		onCommit:  onCommit,
	}, nil
}

func (e *Engine) IsPrimary() bool {
	return e.nodeID == e.primaryID
}

func (e *Engine) ProposeBlock(block *blockchain.Block) error {
	if !e.IsPrimary() {
		return fmt.Errorf("node %s is not primary (primary is %s)", e.nodeID, e.primaryID)
	}

	e.mu.Lock()
	e.sequence++
	sequence := e.sequence
	view := e.view
	e.mu.Unlock()

	digest := blockDigest(block)
	key := roundKey(view, sequence)

	e.mu.Lock()
	e.rounds[key] = &roundState{
		block:    block,
		phase:    phasePrePrepare,
		prepares: make(map[string]bool),
		commits:  make(map[string]bool),
	}

	e.rounds[key].prepares[e.nodeID] = true
	e.rounds[key].commits[e.nodeID] = true
	e.mu.Unlock()

	message := Message{
		Type:     MessagePrePrepare,
		View:     view,
		Sequence: sequence,
		Digest:   digest,
		NodeID:   e.nodeID,
		Block:    block,
	}

	return e.transport.Broadcast(message)
}

func (e *Engine) HandleMessage(message Message) error {
	switch message.Type {
	case MessagePrePrepare:
		return e.handlePrePrepare(message)
	case MessagePrepare:
		return e.handlePrepare(message)
	case MessageCommit:
		return e.handleCommit(message)
	case MessageViewChange:
		return e.handleViewChange(message)
	default:
		return fmt.Errorf("unknown message type: %s", message.Type)
	}
}

func (e *Engine) handlePrePrepare(message Message) error {
	e.mu.Lock()

	if message.View != e.view {
		e.mu.Unlock()
		return fmt.Errorf("pre-prepare: wrong view %d (current %d)", message.View, e.view)
	}

	if message.NodeID != e.primaryID {
		e.mu.Unlock()
		return fmt.Errorf("pre-prepare: sender %s is not primary %s", message.NodeID, e.primaryID)
	}

	if message.Block == nil {
		e.mu.Unlock()
		return errors.New("pre-prepare: block is nil")
	}

	expected := blockDigest(message.Block)
	if message.Digest != expected {
		e.mu.Unlock()
		return fmt.Errorf("pre-prepare: digest mismatch (got %s, expected: %s)", message.Digest, expected)
	}

	key := roundKey(message.View, message.Sequence)
	if _, exists := e.rounds[key]; exists {
		e.mu.Unlock()
		return nil
	}

	e.rounds[key] = &roundState{
		block:    message.Block,
		phase:    phasePrePrepare,
		prepares: make(map[string]bool),
		commits:  make(map[string]bool),
	}

	e.rounds[key].prepares[e.nodeID] = true

	prepare := Message{
		Type:     MessagePrepare,
		View:     message.View,
		Sequence: message.Sequence,
		Digest:   message.Digest,
		NodeID:   e.nodeID,
	}

	e.mu.Unlock()
	return e.transport.Broadcast(prepare)
}

func (e *Engine) handlePrepare(message Message) error {
	e.mu.Lock()

	if message.View != e.view {
		e.mu.Unlock()
		return fmt.Errorf("prepare: wrong view %d", message.View)
	}

	key := roundKey(message.View, message.Sequence)
	round, exists := e.rounds[key]
	if !exists {
		e.mu.Unlock()
		return fmt.Errorf("prepare: unknown round %s", key)
	}

	if message.Digest != blockDigest(round.block) {
		e.mu.Unlock()
		return fmt.Errorf("prepare: digest mismatch from %s", message.NodeID)
	}

	round.prepares[message.NodeID] = true

	if round.phase < phasePrepare && e.hasQuorom(round.prepares) {
		round.phase = phasePrepare
		round.commits[e.nodeID] = true

		commit := Message{
			Type:     MessageCommit,
			View:     message.View,
			Sequence: message.Sequence,
			Digest:   message.Digest,
			NodeID:   e.nodeID,
		}

		e.mu.Unlock()
		return e.transport.Broadcast(commit)
	}

	e.mu.Unlock()
	return nil
}

func (e *Engine) handleCommit(message Message) error {
	e.mu.Lock()

	if message.View != e.view {
		e.mu.Unlock()
		return fmt.Errorf("commit: wrong view %d", message.View)
	}

	key := roundKey(message.View, message.Sequence)
	round, exists := e.rounds[key]
	if !exists {
		e.mu.Unlock()
		return fmt.Errorf("commit: unknown round %s", key)
	}
	if round.phase == phaseCommitted {
		e.mu.Unlock()
		return nil
	}

	round.commits[message.NodeID] = true

	if e.hasQuorom(round.commits) {
		round.phase = phaseCommitted
		block := round.block
		e.mu.Unlock()
		return e.onCommit(block)
	}

	e.mu.Unlock()
	return nil
}

func (e *Engine) handleViewChange(message Message) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if message.View <= e.view {
		return nil
	}

	e.view = message.View
	e.primaryID = e.nodes[e.view%len(e.nodes)]

	return nil
}

func (e *Engine) hasQuorom(votes map[string]bool) bool {
	f := (len(e.nodes) - 1) / 3

	quorom := 2*f + 1
	return len(votes) >= quorom
}

func blockDigest(block *blockchain.Block) string {
	h := sha256.Sum256(block.Hash)
	return hex.EncodeToString(h[:])
}

func roundKey(view, sequence int) string {
	return fmt.Sprintf("%d:%d", view, sequence)
}

func (e *Engine) RequestViewChange() error {
	e.mu.Lock()
	newView := e.view + 1
	e.mu.Unlock()

	message := Message{
		Type:   MessageViewChange,
		View:   newView,
		NodeID: e.nodeID,
	}

	return e.transport.Broadcast(message)
}

func FaultTolerance(n int) int {
	return (n - 1) / 3
}

func QuorumSize(n int) int {
	return 2*FaultTolerance(n) + 1
}

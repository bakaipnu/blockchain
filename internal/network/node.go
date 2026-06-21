package network

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"github.com/bakaipnu/blockchain/internal/blockchain"
	"github.com/bakaipnu/blockchain/internal/consensus"
	"github.com/bakaipnu/blockchain/internal/transaction"
	"github.com/bakaipnu/blockchain/internal/wallet"
)

type NetMsgType string

const (
	NetMsgTransaction NetMsgType = "TX"
	NetMsgBlock       NetMsgType = "BLOCK"
	NetMsgConsensus   NetMsgType = "CONSENSUS"
	NetMsgHandshake   NetMsgType = "HANDSHAKE"
	NetMsgGetChain    NetMsgType = "GET_CHAIN"
	NetMsgChain       NetMsgType = "CHAIN"
)

type NetMessage struct {
	Type    NetMsgType      `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type HandshakePayload struct {
	NodeID  string `json:"node_id"`
	Address string `json:"address"`
}

type peer struct {
	id      string
	address string
	conn    net.Conn
	encoder *json.Encoder
	mu      sync.Mutex
}

func (p *peer) send(msg NetMessage) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.encoder.Encode(msg)
}

type Node struct {
	id        string
	address   string
	wallet    *wallet.Wallet
	bc        *blockchain.Blockchain
	engine    *consensus.Engine
	mempool   []*transaction.Transaction
	mempoolMu sync.Mutex
	peers     map[string]*peer
	peersMu   sync.RWMutex
	listener  net.Listener
	quit      chan struct{}
	wg        sync.WaitGroup
}

func NewNode(id, address string, w *wallet.Wallet, bc *blockchain.Blockchain) *Node {
	n := &Node{
		id:      id,
		address: address,
		wallet:  w,
		bc:      bc,
		peers:   make(map[string]*peer),
		quit:    make(chan struct{}),
	}

	return n
}

func (n *Node) SetConsensusEngine(engine *consensus.Engine) {
	n.engine = engine
}

func (n *Node) Start() error {
	ln, err := net.Listen("tcp", n.address)
	if err != nil {
		return fmt.Errorf("listen %s: %w", n.address, err)
	}
	n.listener = ln
	log.Printf("[%s] listening on %s", n.id, n.address)

	n.wg.Add(1)
	go n.acceptLoop()

	return nil
}

func (n *Node) Stop() {
	close(n.quit)
	if n.listener != nil {
		n.listener.Close()
	}
	n.wg.Wait()
	log.Printf("[%s] stopped", n.id)
}

func (n *Node) acceptLoop() {
	defer n.wg.Done()

	for {
		conn, err := n.listener.Accept()
		if err != nil {
			select {
			case <-n.quit:
				return
			default:
				log.Printf("[%s] accept error: %v", n.id, err)
				continue
			}
		}

		n.wg.Add(1)
		go n.handleConnection(conn)
	}
}

func (n *Node) Connect(address string) error {
	conn, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", address, err)
	}

	encoder := json.NewEncoder(conn)
	hs := n.makeHandshake()
	if err := encoder.Encode(hs); err != nil {
		conn.Close()
		return fmt.Errorf("handshake send: %w", err)
	}

	n.wg.Add(1)
	go n.handleConnection(conn)

	return nil
}

func (n *Node) addPeer(p *peer) {
	n.peersMu.Lock()
	defer n.peersMu.Unlock()
	n.peers[p.id] = p
	log.Printf("[%s] peer connected: %s (%s)", n.id, p.id, p.address)
}

func (n *Node) removePeer(id string) {
	n.peersMu.Lock()
	defer n.peersMu.Unlock()
	delete(n.peers, id)
	log.Printf("[%s] peer disconnected: %s", n.id, id)
}

func (n *Node) PeerCount() int {
	n.peersMu.RLock()
	defer n.peersMu.RUnlock()
	return len(n.peers)
}

func (n *Node) handleConnection(conn net.Conn) {
	defer n.wg.Done()
	defer conn.Close()

	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	var currentPeer *peer

	for {
		select {
		case <-n.quit:
			return
		default:
		}

		var msg NetMessage
		if err := decoder.Decode(&msg); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				break
			}
			log.Printf("[%s] decode error: %v", n.id, err)
			break
		}

		switch msg.Type {

		case NetMsgHandshake:
			var hs HandshakePayload
			if err := json.Unmarshal(msg.Payload, &hs); err != nil {
				log.Printf("[%s] bad handshake: %v", n.id, err)
				return
			}

			n.peersMu.RLock()
			_, alreadyKnown := n.peers[hs.NodeID]
			n.peersMu.RUnlock()

			if !alreadyKnown {
				currentPeer = &peer{
					id:      hs.NodeID,
					address: hs.Address,
					conn:    conn,
					encoder: encoder,
				}
				n.addPeer(currentPeer)
				defer n.removePeer(currentPeer.id)
				_ = currentPeer.send(n.makeHandshake())
			}

		case NetMsgGetChain:
			blocks := n.bc.Blocks()
			payload, _ := json.Marshal(blocks)
			resp := NetMessage{Type: NetMsgChain, Payload: payload}
			_ = encoder.Encode(resp)

		case NetMsgChain:
			n.handleChainSync(msg.Payload)

		case NetMsgTransaction:
			var tx transaction.Transaction
			if err := json.Unmarshal(msg.Payload, &tx); err != nil {
				log.Printf("[%s] bad tx: %v", n.id, err)
				continue
			}
			n.handleIncomingTx(&tx)

		case NetMsgBlock:
			var block blockchain.Block
			if err := json.Unmarshal(msg.Payload, &block); err != nil {
				log.Printf("[%s] bad block: %v", n.id, err)
				continue
			}
			if err := n.bc.AddBlock(&block); err != nil {
				log.Printf("[%s] rejected block: %v", n.id, err)
			}

		case NetMsgConsensus:
			if n.engine == nil {
				continue
			}
			var consensusMsg consensus.Message
			if err := json.Unmarshal(msg.Payload, &consensusMsg); err != nil {
				log.Printf("[%s] bad consensus msg: %v", n.id, err)
				continue
			}
			if err := n.engine.HandleMessage(consensusMsg); err != nil {
				log.Printf("[%s] consensus error: %v", n.id, err)
			}
		}
	}
}

func (n *Node) handleChainSync(payload json.RawMessage) {
	var blocks []*blockchain.Block
	if err := json.Unmarshal(payload, &blocks); err != nil {
		log.Printf("[%s] chain sync decode error: %v", n.id, err)
		return
	}

	if len(blocks) <= n.bc.Height() {
		return
	}

	log.Printf("[%s] received longer chain (%d > %d), syncing...",
		n.id, len(blocks), n.bc.Height())

	for _, block := range blocks[n.bc.Height():] {
		if err := n.bc.AddBlock(block); err != nil {
			log.Printf("[%s] chain sync: failed to add block: %v", n.id, err)
			return
		}
	}

	log.Printf("[%s] chain sync complete, new height=%d", n.id, n.bc.Height())
}

func (n *Node) Send(toNodeID string, msg consensus.Message) error {
	n.peersMu.RLock()
	p, ok := n.peers[toNodeID]
	n.peersMu.RUnlock()

	if !ok {
		return fmt.Errorf("peer %s not found", toNodeID)
	}

	return n.sendConsensusMsg(p, msg)
}

func (n *Node) Broadcast(msg consensus.Message) error {
	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	netMsg := NetMessage{
		Type:    NetMsgConsensus,
		Payload: payload,
	}

	n.peersMu.RLock()
	peers := make([]*peer, 0, len(n.peers))
	for _, p := range n.peers {
		peers = append(peers, p)
	}
	n.peersMu.RUnlock()

	var lastErr error
	for _, p := range peers {
		if err := p.send(netMsg); err != nil {
			log.Printf("[%s] broadcast to %s failed: %v", n.id, p.id, err)
			lastErr = err
		}
	}
	return lastErr
}

func (n *Node) sendConsensusMsg(p *peer, msg consensus.Message) error {
	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return p.send(NetMessage{Type: NetMsgConsensus, Payload: payload})
}

func (n *Node) BroadcastTransaction(tx *transaction.Transaction) error {
	payload, err := json.Marshal(tx)
	if err != nil {
		return err
	}

	msg := NetMessage{Type: NetMsgTransaction, Payload: payload}

	n.peersMu.RLock()
	defer n.peersMu.RUnlock()

	for _, p := range n.peers {
		if err := p.send(msg); err != nil {
			log.Printf("[%s] tx broadcast to %s: %v", n.id, p.id, err)
		}
	}
	return nil
}

func (n *Node) handleIncomingTx(tx *transaction.Transaction) {
	if !tx.IsCoinbase() && !tx.Verify() {
		log.Printf("[%s] rejected invalid tx %x", n.id, tx.ID)
		return
	}

	n.mempoolMu.Lock()
	defer n.mempoolMu.Unlock()

	for _, existing := range n.mempool {
		if bytes.Equal(existing.ID, tx.ID) {
			return
		}
	}

	n.mempool = append(n.mempool, tx)
	log.Printf("[%s] tx %x added to mempool (size: %d)", n.id, tx.ID, len(n.mempool))
}

func (n *Node) DrainMempool() []*transaction.Transaction {
	n.mempoolMu.Lock()
	defer n.mempoolMu.Unlock()

	txs := make([]*transaction.Transaction, len(n.mempool))
	copy(txs, n.mempool)
	n.mempool = nil

	return txs
}

func (n *Node) RemoveFromMempool(committed []*transaction.Transaction) {
	committedIDs := make(map[string]bool, len(committed))
	for _, tx := range committed {
		committedIDs[string(tx.ID)] = true
	}

	n.mempoolMu.Lock()
	defer n.mempoolMu.Unlock()

	filtered := n.mempool[:0]
	for _, tx := range n.mempool {
		if !committedIDs[string(tx.ID)] {
			filtered = append(filtered, tx)
		}
	}
	n.mempool = filtered
}

func (n *Node) MempoolSize() int {
	n.mempoolMu.Lock()
	defer n.mempoolMu.Unlock()
	return len(n.mempool)
}

func (n *Node) makeHandshake() NetMessage {
	payload, _ := json.Marshal(HandshakePayload{
		NodeID:  n.id,
		Address: n.address,
	})
	return NetMessage{Type: NetMsgHandshake, Payload: payload}
}

func (n *Node) ID() string {
	return n.id
}

func (n *Node) Address() string {
	return n.address
}

func (n *Node) AddToMempool(tx *transaction.Transaction) {
	n.handleIncomingTx(tx)
}

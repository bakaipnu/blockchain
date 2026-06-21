package api

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/bakaipnu/blockchain/internal/blockchain"
	"github.com/bakaipnu/blockchain/internal/consensus"
	"github.com/bakaipnu/blockchain/internal/network"
	"github.com/bakaipnu/blockchain/internal/transaction"
	"github.com/bakaipnu/blockchain/internal/wallet"
)

type txRequest struct {
	Sender   string `json:"sender"`
	Receiver string `json:"receiver"`
	Amount   int64  `json:"amount"`
}

type txResponse struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type connectRequest struct {
	Address string `json:"address"`
}

type txView struct {
	ID       string `json:"id"`
	Sender   string `json:"sender"`
	Receiver string `json:"receiver"`
	Amount   int64  `json:"amount"`
}

type blockView struct {
	Height    int      `json:"height"`
	Hash      string   `json:"hash"`
	PrevHash  string   `json:"prev_hash"`
	Timestamp int64    `json:"timestamp"`
	TxCount   int      `json:"tx_count"`
	Txs       []txView `json:"transactions"`
}

type chainResponse struct {
	Height int         `json:"height"`
	Valid  bool        `json:"valid"`
	Blocks []blockView `json:"blocks"`
}

type statusResponse struct {
	NodeID      string `json:"node_id"`
	Address     string `json:"address"`
	PeerCount   int    `json:"peer_count"`
	ChainHeight int    `json:"chain_height"`
	Mempool     int    `json:"mempool_size"`
	IsPrimary   bool   `json:"is_primary"`
	Uptime      string `json:"uptime"`
}

type Server struct {
	node      *network.Node
	bc        *blockchain.Blockchain
	engine    *consensus.Engine
	wallet    *wallet.Wallet
	startTime time.Time
	httpAddr  string
	mux       *http.ServeMux
}

func NewServer(httpAddr string, node *network.Node, bc *blockchain.Blockchain, engine *consensus.Engine, w *wallet.Wallet) *Server {
	s := &Server{
		node:      node,
		bc:        bc,
		engine:    engine,
		wallet:    w,
		startTime: time.Now(),
		httpAddr:  httpAddr,
		mux:       http.NewServeMux(),
	}
	s.registerRoutes()
	return s
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /chain", s.handleGetChain)
	s.mux.HandleFunc("GET /status", s.handleGetStatus)
	s.mux.HandleFunc("POST /transaction", s.handlePostTransaction)
	s.mux.HandleFunc("POST /connect", s.handlePostConnect)
	s.mux.HandleFunc("POST /mine", s.handleMine)
}

func (s *Server) Start() error {
	log.Printf("[api] listening on %s", s.httpAddr)
	return http.ListenAndServe(s.httpAddr, s.mux)
}

func (s *Server) handleGetChain(w http.ResponseWriter, r *http.Request) {
	blocks := s.bc.Blocks()
	views := make([]blockView, len(blocks))

	for i, b := range blocks {
		txs := make([]txView, len(b.Transactions))
		for j, tx := range b.Transactions {
			sender := "coinbase"
			if !tx.IsCoinbase() {
				sender = hex.EncodeToString(tx.Sender)
			}
			txs[j] = txView{
				ID:       hex.EncodeToString(tx.ID),
				Sender:   sender,
				Receiver: hex.EncodeToString(tx.Receiver),
				Amount:   tx.Amount,
			}
		}

		views[i] = blockView{
			Height:    i,
			Hash:      hex.EncodeToString(b.Hash),
			PrevHash:  hex.EncodeToString(b.PreviousHash),
			Timestamp: b.Timestamp,
			TxCount:   len(b.Transactions),
			Txs:       txs,
		}
	}

	writeJSON(w, http.StatusOK, chainResponse{
		Height: s.bc.Height(),
		Valid:  s.bc.IsValid(),
		Blocks: views,
	})
}

func (s *Server) handleGetStatus(w http.ResponseWriter, r *http.Request) {
	isPrimary := false
	if s.engine != nil {
		isPrimary = s.engine.IsPrimary()
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"node_id":        s.node.ID(),
		"address":        s.node.Address(),
		"wallet_address": hex.EncodeToString(s.wallet.Address()),
		"public_key":     hex.EncodeToString(wallet.PublicKeyToBytes(s.wallet.PublicKey)),
		"peer_count":     s.node.PeerCount(),
		"chain_height":   s.bc.Height(),
		"mempool_size":   s.node.MempoolSize(),
		"is_primary":     isPrimary,
		"uptime":         time.Since(s.startTime).Round(time.Second).String(),
	})
}

func (s *Server) handlePostTransaction(w http.ResponseWriter, r *http.Request) {
	var req txRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if req.Amount <= 0 {
		writeError(w, http.StatusBadRequest, "amount must be greater than zero")
		return
	}

	receiverBytes, err := hex.DecodeString(req.Receiver)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid receiver public key")
		return
	}

	receiverKey := wallet.BytesToPublicKey(receiverBytes)
	if receiverKey == nil {
		writeError(w, http.StatusBadRequest, "cannot parse receiver public key")
		return
	}

	receiverWallet := &wallet.Wallet{PublicKey: receiverKey}

	tx, err := transaction.NewTransaction(s.wallet, receiverWallet, req.Amount)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := tx.Sign(s.wallet); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to sign transaction")
		return
	}

	s.node.AddToMempool(tx)

	if err := s.node.BroadcastTransaction(tx); err != nil {
		log.Printf("[api] broadcast tx warning: %v", err)
	}

	if err := s.node.BroadcastTransaction(tx); err != nil {
		log.Printf("[api] broadcast tx warning: %v", err)
	}

	writeJSON(w, http.StatusCreated, txResponse{
		ID:      hex.EncodeToString(tx.ID),
		Status:  "pending",
		Message: "Transaction added to mempool",
	})
}

func (s *Server) handlePostConnect(w http.ResponseWriter, r *http.Request) {
	var req connectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Address == "" {
		writeError(w, http.StatusBadRequest, "address is required")
		return
	}

	if err := s.node.Connect(req.Address); err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("cannot connect to %s: %v", req.Address, err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "connected",
		"address": req.Address,
	})
}

func (s *Server) handleMine(w http.ResponseWriter, r *http.Request) {
	if s.engine == nil {
		writeError(w, http.StatusServiceUnavailable, "consensus engine not configured")
		return
	}

	if !s.engine.IsPrimary() {
		writeError(w, http.StatusForbidden, "only primary node can propose blocks")
		return
	}

	txs := s.node.DrainMempool()

	coinbase := transaction.NewCoinbaseTX(s.wallet, 50)
	txs = append([]*transaction.Transaction{coinbase}, txs...)

	block := blockchain.NewBlock(txs, s.bc.LastBlock().Hash)

	if err := s.engine.ProposeBlock(block); err != nil {
		writeError(w, http.StatusInternalServerError, "propose block failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "proposed",
		"block":    hex.EncodeToString(block.Hash),
		"tx_count": len(txs),
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

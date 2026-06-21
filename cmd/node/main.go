package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/bakaipnu/blockchain/internal/api"
	"github.com/bakaipnu/blockchain/internal/blockchain"
	"github.com/bakaipnu/blockchain/internal/consensus"
	"github.com/bakaipnu/blockchain/internal/network"
	"github.com/bakaipnu/blockchain/internal/wallet"
)

func main() {
	nodeID := flag.String("id", "node-0", "унікальний ідентифікатор вузла")
	p2pAddr := flag.String("p2p", ":3000", "адреса P2P TCP сервера")
	apiAddr := flag.String("api", ":8080", "адреса REST API HTTP сервера")
	nodesFlag := flag.String("nodes", "", "всі вузли мережі: id@addr,id@addr,...")
	peersFlag := flag.String("peers", "", "bootstrap peers: addr,addr,...")
	flag.Parse()

	if err := os.MkdirAll("data", 0755); err != nil {
		log.Fatalf("mkdir data: %v", err)
	}

	allNodeIDs := parseNodeIDs(*nodesFlag, *nodeID, *p2pAddr)

	w, err := wallet.NewWallet()
	if err != nil {
		log.Fatalf("wallet: %v", err)
	}
	log.Printf("[%s] wallet address: %x", *nodeID, w.Address())

	bc, storage, err := blockchain.NewWithStorage("data/" + *nodeID + ".db")
	if err != nil {
		log.Fatalf("storage: %v", err)
	}
	defer storage.Close()

	log.Printf("[%s] blockchain initialized, height=%d", *nodeID, bc.Height())

	node := network.NewNode(*nodeID, *p2pAddr, w, bc)

	var engine *consensus.Engine
	if len(allNodeIDs) >= 4 {
		engine, err = consensus.NewEngine(
			*nodeID,
			allNodeIDs,
			node,
			func(block *blockchain.Block) error {
				if err := bc.AddBlockWithStorage(block, storage); err != nil { // ← зберігає
					log.Printf("[%s] commit block failed: %v", *nodeID, err)
					return err
				}
				node.RemoveFromMempool(block.Transactions)

				log.Printf("[%s] ✓ block committed, chain height=%d", *nodeID, bc.Height())
				return nil
			},
		)
		if err != nil {
			log.Fatalf("consensus engine: %v", err)
		}
		node.SetConsensusEngine(engine)
		log.Printf("[%s] PBFT engine ready (f=%d, quorum=%d)",
			*nodeID, consensus.FaultTolerance(len(allNodeIDs)), consensus.QuorumSize(len(allNodeIDs)))
	} else {
		log.Printf("[%s] PBFT disabled: need ≥4 nodes, got %d", *nodeID, len(allNodeIDs))
	}

	srv := api.NewServer(*apiAddr, node, bc, engine, w)

	if err := node.Start(); err != nil {
		log.Fatalf("p2p start: %v", err)
	}
	log.Printf("[%s] P2P listening on %s", *nodeID, *p2pAddr)

	go func() {
		if err := srv.Start(); err != nil {
			log.Fatalf("api server: %v", err)
		}
	}()
	log.Printf("[%s] REST API listening on %s", *nodeID, *apiAddr)

	if *peersFlag != "" {
		for _, addr := range strings.Split(*peersFlag, ",") {
			addr = strings.TrimSpace(addr)
			if addr == "" {
				continue
			}
			if err := node.Connect(addr); err != nil {
				log.Printf("[%s] connect to %s failed: %v", *nodeID, addr, err)
			} else {
				log.Printf("[%s] connected to peer %s", *nodeID, addr)
			}
		}
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Printf("[%s] shutting down...", *nodeID)
	node.Stop()
	log.Printf("[%s] bye", *nodeID)
}

func parseNodeIDs(nodesFlag, selfID, selfAddr string) []string {
	if nodesFlag == "" {
		return []string{selfID}
	}

	var ids []string
	for _, entry := range strings.Split(nodesFlag, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		parts := strings.SplitN(entry, "@", 2)
		ids = append(ids, parts[0])
	}

	return ids
}

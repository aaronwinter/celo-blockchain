// Copyright 2016 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

// Package les implements the Light Ethereum Subprotocol.
package les

import (
	"fmt"
	"time"

	"github.com/aaronwinter/celo-blockchain/accounts"
	"github.com/aaronwinter/celo-blockchain/common"
	"github.com/aaronwinter/celo-blockchain/common/hexutil"
	"github.com/aaronwinter/celo-blockchain/common/mclock"
	"github.com/aaronwinter/celo-blockchain/consensus"
	istanbulBackend "github.com/aaronwinter/celo-blockchain/consensus/istanbul/backend"
	"github.com/aaronwinter/celo-blockchain/core"
	"github.com/aaronwinter/celo-blockchain/core/bloombits"
	"github.com/aaronwinter/celo-blockchain/core/rawdb"
	"github.com/aaronwinter/celo-blockchain/core/types"
	"github.com/aaronwinter/celo-blockchain/eth"
	"github.com/aaronwinter/celo-blockchain/eth/downloader"
	"github.com/aaronwinter/celo-blockchain/eth/filters"
	"github.com/aaronwinter/celo-blockchain/event"
	"github.com/aaronwinter/celo-blockchain/internal/ethapi"
	lpc "github.com/aaronwinter/celo-blockchain/les/lespay/client"
	"github.com/aaronwinter/celo-blockchain/light"
	"github.com/aaronwinter/celo-blockchain/log"
	"github.com/aaronwinter/celo-blockchain/node"
	"github.com/aaronwinter/celo-blockchain/p2p"
	"github.com/aaronwinter/celo-blockchain/p2p/enode"
	"github.com/aaronwinter/celo-blockchain/params"
	"github.com/aaronwinter/celo-blockchain/rpc"
)

type LightEthereum struct {
	lesCommons

	peers          *serverPeerSet
	reqDist        *requestDistributor
	retriever      *retrieveManager
	odr            *LesOdr
	relay          *lesTxRelay
	handler        *clientHandler
	txPool         *light.TxPool
	blockchain     *light.LightChain
	serverPool     *serverPool
	chainreader    *LightChainReader
	valueTracker   *lpc.ValueTracker
	dialCandidates enode.Iterator
	pruner         *pruner

	bloomRequests chan chan *bloombits.Retrieval // Channel receiving bloom data retrieval requests
	bloomIndexer  *core.ChainIndexer             // Bloom indexer operating during block imports

	ApiBackend     *LesApiBackend
	eventMux       *event.TypeMux
	engine         consensus.Engine
	accountManager *accounts.Manager
	netRPCService  *ethapi.PublicNetAPI

	networkId uint64
	p2pServer *p2p.Server
}

// New creates an instance of the light client.
func New(stack *node.Node, config *eth.Config) (*LightEthereum, error) {
	var chainName string
	syncMode := config.SyncMode
	var fullChainAvailable bool
	if syncMode == downloader.LightSync {
		chainName = "lightchaindata"
		fullChainAvailable = true
	} else if syncMode == downloader.LightestSync {
		chainName = "lightestchaindata"
		fullChainAvailable = false
	} else {
		panic("Unexpected sync mode: " + syncMode.String())
	}

	chainDb, err := stack.OpenDatabase(chainName, config.DatabaseCache, config.DatabaseHandles, "eth/db/chaindata/")
	if err != nil {
		return nil, err
	}
	lespayDb, err := stack.OpenDatabase("lespay", 0, 0, "eth/db/lespay")
	if err != nil {
		return nil, err
	}
	chainConfig, genesisHash, genesisErr := core.SetupGenesisBlockWithOverride(chainDb, config.Genesis,
		config.OverrideEHardfork)
	if _, isCompat := genesisErr.(*params.ConfigCompatError); genesisErr != nil && !isCompat {
		return nil, genesisErr
	}
	log.Info("Initialised chain configuration", "config", chainConfig)

	peers := newServerPeerSet()
	leth := &LightEthereum{
		lesCommons: lesCommons{
			genesis:     genesisHash,
			config:      config,
			chainConfig: chainConfig,
			iConfig:     light.DefaultClientIndexerConfig,
			chainDb:     chainDb,
			closeCh:     make(chan struct{}),
		},
		peers:          peers,
		eventMux:       stack.EventMux(),
		reqDist:        newRequestDistributor(peers, &mclock.System{}),
		accountManager: stack.AccountManager(),
		engine:         eth.CreateConsensusEngine(stack, chainConfig, config, chainDb),
		networkId:      config.NetworkId,
		bloomRequests:  make(chan chan *bloombits.Retrieval),
		valueTracker:   lpc.NewValueTracker(lespayDb, &mclock.System{}, requestList, time.Minute, 1/float64(time.Hour), 1/float64(time.Hour*100), 1/float64(time.Hour*1000)),
		p2pServer:      stack.Server(),
	}
	peers.subscribe((*vtSubscription)(leth.valueTracker))

	dnsdisc, err := leth.setupDiscovery(&stack.Config().P2P)
	if err != nil {
		return nil, err
	}
	leth.serverPool = newServerPool(lespayDb, []byte("serverpool:"), leth.valueTracker, dnsdisc, time.Second, nil, &mclock.System{}, config.UltraLightServers)
	peers.subscribe(leth.serverPool)
	leth.dialCandidates = leth.serverPool.dialIterator

	leth.retriever = newRetrieveManager(peers, leth.reqDist, leth.serverPool.getTimeout)
	leth.relay = newLesTxRelay(peers, leth.retriever)

	leth.odr = NewLesOdr(chainDb, light.DefaultClientIndexerConfig, leth.retriever)
	// If the full chain is not available then indexing each block header isn't possible.
	if fullChainAvailable {
		leth.bloomIndexer = eth.NewBloomIndexer(chainDb, params.BloomBitsBlocksClient, params.HelperTrieConfirmations, fullChainAvailable)
		leth.chtIndexer = light.NewChtIndexer(chainDb, leth.odr, params.CHTFrequency, params.HelperTrieConfirmations, config.LightNoPrune, fullChainAvailable)
		leth.bloomTrieIndexer = light.NewBloomTrieIndexer(chainDb, leth.odr, params.BloomBitsBlocksClient, params.BloomTrieFrequency, config.LightNoPrune, fullChainAvailable)
	}
	leth.odr.SetIndexers(leth.chtIndexer, leth.bloomTrieIndexer, leth.bloomIndexer)

	checkpoint := config.Checkpoint
	if checkpoint == nil {
		checkpoint = params.TrustedCheckpoints[genesisHash]
	}
	// Note: NewLightChain adds the trusted checkpoint so it needs an ODR with
	// indexers already set but not started yet
	if leth.blockchain, err = light.NewLightChain(leth.odr, leth.chainConfig, leth.engine, checkpoint); err != nil {
		return nil, err
	}

	leth.chainReader = leth.blockchain
	leth.txPool = light.NewTxPool(leth.chainConfig, leth.blockchain, leth.relay)

	// Set up checkpoint oracle.
	leth.oracle = leth.setupOracle(stack, genesisHash, config)

	// Note: AddChildIndexer starts the update process for the child
	if leth.bloomIndexer != nil && leth.bloomTrieIndexer != nil {
		leth.bloomIndexer.AddChildIndexer(leth.bloomTrieIndexer)
		leth.bloomIndexer.Start(leth.blockchain)
	}
	if leth.chtIndexer != nil {
		leth.chtIndexer.Start(leth.blockchain)
	}

	// Start a light chain pruner to delete useless historical data.
	leth.pruner = newPruner(chainDb, leth.chtIndexer, leth.bloomTrieIndexer)

	// Rewind the chain in case of an incompatible config upgrade.
	if compat, ok := genesisErr.(*params.ConfigCompatError); ok {
		log.Warn("Rewinding chain to upgrade configuration", "err", compat)
		leth.blockchain.SetHead(compat.RewindTo)
		rawdb.WriteChainConfig(chainDb, genesisHash, chainConfig)
	}

	leth.ApiBackend = &LesApiBackend{stack.Config().ExtRPCEnabled(), leth}

	leth.chainreader = &LightChainReader{
		config:     leth.chainConfig,
		blockchain: leth.blockchain,
	}

	// If the engine is istanbul, then inject the blockchain
	if istanbul, isIstanbul := leth.engine.(*istanbulBackend.Backend); isIstanbul {
		istanbul.SetChain(leth.chainreader, nil, nil)
	}
	// TODO mcortesi (needs etherbase & gatewayFee?)
	leth.handler = newClientHandler(syncMode, config.UltraLightServers, config.UltraLightFraction, checkpoint, leth, config.GatewayFee)
	if leth.handler.ulc != nil {
		log.Warn("Ultra light client is enabled", "trustedNodes", len(leth.handler.ulc.keys), "minTrustedFraction", leth.handler.ulc.fraction)
		leth.blockchain.DisableCheckFreq()
	}

	leth.netRPCService = ethapi.NewPublicNetAPI(leth.p2pServer, leth.config.NetworkId)

	// Register the backend on the node
	stack.RegisterAPIs(leth.APIs())
	stack.RegisterProtocols(leth.Protocols())
	stack.RegisterLifecycle(leth)

	return leth, nil
}

// vtSubscription implements serverPeerSubscriber
type vtSubscription lpc.ValueTracker

// registerPeer implements serverPeerSubscriber
func (v *vtSubscription) registerPeer(p *serverPeer) {
	vt := (*lpc.ValueTracker)(v)
	p.setValueTracker(vt, vt.Register(p.ID()))
	p.updateVtParams()
}

// unregisterPeer implements serverPeerSubscriber
func (v *vtSubscription) unregisterPeer(p *serverPeer) {
	vt := (*lpc.ValueTracker)(v)
	vt.Unregister(p.ID())
	p.setValueTracker(nil, nil)
}

type LightDummyAPI struct{}

// Etherbase is the address that mining rewards will be send to
func (s *LightDummyAPI) Etherbase() (common.Address, error) {
	return common.Address{}, fmt.Errorf("mining is not supported in light mode")
}

// Coinbase is the address that mining rewards will be send to (alias for Etherbase)
func (s *LightDummyAPI) Coinbase() (common.Address, error) {
	return common.Address{}, fmt.Errorf("mining is not supported in light mode")
}

// Hashrate returns the POW hashrate
func (s *LightDummyAPI) Hashrate() hexutil.Uint {
	return 0
}

// Mining returns an indication if this node is currently mining.
func (s *LightDummyAPI) Mining() bool {
	return false
}

// APIs returns the collection of RPC services the ethereum package offers.
// NOTE, some of these services probably need to be moved to somewhere else.
func (s *LightEthereum) APIs() []rpc.API {
	apis := ethapi.GetAPIs(s.ApiBackend)
	apis = append(apis, s.engine.APIs(s.BlockChain().HeaderChain())...)
	return append(apis, []rpc.API{
		{
			Namespace: "eth",
			Version:   "1.0",
			Service:   &LightDummyAPI{},
			Public:    true,
		}, {
			Namespace: "eth",
			Version:   "1.0",
			Service:   downloader.NewPublicDownloaderAPI(s.handler.downloader, s.eventMux),
			Public:    true,
		}, {
			Namespace: "les",
			Version:   "1.0",
			Service:   NewPrivateLightClientAPI(s),
			Public:    false,
		}, {
			Namespace: "eth",
			Version:   "1.0",
			Service:   filters.NewPublicFilterAPI(s.ApiBackend, true),
			Public:    true,
		}, {
			Namespace: "net",
			Version:   "1.0",
			Service:   s.netRPCService,
			Public:    true,
		}, {
			Namespace: "les",
			Version:   "1.0",
			Service:   NewPrivateLightAPI(&s.lesCommons),
			Public:    false,
		}, {
			Namespace: "lespay",
			Version:   "1.0",
			Service:   lpc.NewPrivateClientAPI(s.valueTracker),
			Public:    false,
		},
	}...)
}

func (s *LightEthereum) ResetWithGenesisBlock(gb *types.Block) {
	s.blockchain.ResetWithGenesisBlock(gb)
}

func (s *LightEthereum) BlockChain() *light.LightChain      { return s.blockchain }
func (s *LightEthereum) TxPool() *light.TxPool              { return s.txPool }
func (s *LightEthereum) Engine() consensus.Engine           { return s.engine }
func (s *LightEthereum) LesVersion() int                    { return int(ClientProtocolVersions[0]) }
func (s *LightEthereum) Downloader() *downloader.Downloader { return s.handler.downloader }
func (s *LightEthereum) EventMux() *event.TypeMux           { return s.eventMux }
func (s *LightEthereum) ServerPool() *serverPool            { return s.serverPool }

// Protocols returns all the currently configured network protocols to start.
func (s *LightEthereum) Protocols() []p2p.Protocol {
	return s.makeProtocols(ClientProtocolVersions, s.handler.runPeer, func(id enode.ID) interface{} {
		if p := s.peers.peer(id.String()); p != nil {
			return p.Info()
		}
		return nil
	}, s.dialCandidates)
}

// Start implements node.Lifecycle, starting all internal goroutines needed by the
// light ethereum protocol implementation.
func (s *LightEthereum) Start() error {
	log.Warn("Light client mode is an experimental feature")

	s.serverPool.start()
	// Start bloom request workers.
	s.wg.Add(bloomServiceThreads)
	s.startBloomHandlers(params.BloomBitsBlocksClient)
	s.handler.start()

	return nil
}

func (s *LightEthereum) GetRandomPeerEtherbase() common.Address {
	return s.peers.randomPeerEtherbase()
}

// Stop implements node.Lifecycle, terminating all internal goroutines used by the
// Ethereum protocol.
func (s *LightEthereum) Stop() error {
	close(s.closeCh)
	s.serverPool.stop()
	s.valueTracker.Stop()
	s.peers.close()
	s.reqDist.close()
	s.odr.Stop()
	s.relay.Stop()
	if s.bloomIndexer != nil {
		s.bloomIndexer.Close()
	}
	if s.chtIndexer != nil {
		s.chtIndexer.Close()
	}
	s.blockchain.Stop()
	s.handler.stop()
	s.txPool.Stop()
	s.engine.Close()
	s.pruner.close()
	s.eventMux.Stop()
	s.chainDb.Close()
	s.wg.Wait()
	log.Info("Light ethereum stopped")
	return nil
}

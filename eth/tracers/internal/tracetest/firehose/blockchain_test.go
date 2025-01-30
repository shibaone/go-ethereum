package firehose_test

import (
	"fmt"
	"math/big"
	"os"
	"testing"

	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/trie"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/tests"
	"github.com/stretchr/testify/require"
)

func runPrestateBlock(t *testing.T, prestatePath string, hooks *tracing.Hooks) {
	t.Helper()

	prestate := readPrestateData(t, prestatePath)

	tx := new(types.Transaction)
	require.NoError(t, tx.UnmarshalBinary(common.FromHex(prestate.Input)))
	fmt.Println("tx", tx.Hash().String())

	context := prestate.Context.toBlockContext(prestate.Genesis)

	state := tests.MakePreState(rawdb.NewMemoryDatabase(), prestate.Genesis.Alloc, false, rawdb.HashScheme)

	state.StateDB.SetLogger(hooks)
	state.StateDB.SetTxContext(tx.Hash(), 0)

	// create body iwth trx and uncles
	block := types.NewBlock(&types.Header{
		ParentHash:       prestate.Genesis.ToBlock().Hash(),
		Number:           context.BlockNumber,
		Difficulty:       context.Difficulty,
		Coinbase:         context.Coinbase,
		Time:             context.Time,
		GasLimit:         context.GasLimit,
		BaseFee:          context.BaseFee,
		ParentBeaconRoot: ptr(common.Hash{}),
	}, &types.Body{Transactions: []*types.Transaction{tx}}, nil, trie.NewStackTrie(nil), types.DefaultBlockConfig)

	hooks.OnBlockchainInit(prestate.Genesis.Config)
	hooks.OnBlockStart(tracing.BlockEvent{
		Block: block,
		TD:    prestate.TotalDifficulty,
	})

	usedGas := uint64(0)
	_, err := core.ApplyTransaction(
		prestate.Genesis.Config,
		prestate,
		&context.Coinbase,
		new(core.GasPool).AddGas(block.GasLimit()),
		state.StateDB,
		block.Header(),
		tx,
		&usedGas,
		vm.Config{Tracer: hooks},
	)
	require.NoError(t, err)

	hooks.OnBlockEnd(nil)
}

func newBlockchain(t *testing.T, alloc types.GenesisAlloc, context vm.BlockContext, tracer *tracing.Hooks) (*core.Genesis, *core.BlockChain) {
	t.Helper()

	genesis := &core.Genesis{
		Difficulty: new(big.Int).Sub(context.Difficulty, big.NewInt(1)),
		Timestamp:  context.Time - 1,
		Number:     new(big.Int).Sub(context.BlockNumber, big.NewInt(1)).Uint64(),
		BaseFee:    big.NewInt(params.InitialBaseFee),
		Coinbase:   context.Coinbase,
		Config:     params.AllEthashProtocolChanges,
		Alloc:      alloc,
	}

	log.SetDefault(log.NewLogger(log.NewTerminalHandlerWithLevel(os.Stderr, log.LevelInfo, false)))
	defer log.SetDefault(log.NewLogger(log.DiscardHandler()))

	blockchain, err := core.NewBlockChain(rawdb.NewMemoryDatabase(), core.DefaultCacheConfigWithScheme(rawdb.HashScheme), genesis, nil, ethash.NewFullFaker(), vm.Config{
		Tracer: tracer,
	}, nil)
	require.NoError(t, err)

	return genesis, blockchain
}

type ignoreValidateStateValidator struct {
	core.Validator
}

func (v ignoreValidateStateValidator) ValidateBody(block *types.Block) error {
	return v.Validator.ValidateBody(block)
}

func (v ignoreValidateStateValidator) ValidateState(block *types.Block, statedb *state.StateDB, res *core.ProcessResult, stateless bool) error {
	return nil
}

package tracetest

import (
	"encoding/json"
	"fmt"
	"hash"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/tests"
	"github.com/ethereum/go-ethereum/trie"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/sha3"
)

func TestTracingAPI(t *testing.T) {
	testFiles := []string{
		"./testdata/tracing_api/suicide_exitsing_account_eip6780.json",
	}

	for _, folder := range testFiles {
		name := filepath.Base(folder)

		t.Run(name, func(t *testing.T) {
			output := []string{}
			recordingHooks := recordingTracingHooks(&output)

			runPrestateBlock(t, folder, &tracing.Hooks{
				OnBalanceChange: recordingHooks.OnBalanceChange,
			})

			require.Equal(t, []string{
				"BalanceChange: addr=0x7a238f298D55F8Db27B860eFf1e02d1528b16585, prev=989654811286150941, new=987650299764928425, reason=BalanceDecreaseGasBuy",
				"BalanceChange: addr=0x7a238f298D55F8Db27B860eFf1e02d1528b16585, prev=987650299764928425, new=987650297762750729, reason=BalanceChangeTransfer",
				"BalanceChange: addr=0xB661C9F3034237501433bD7A6587784b7020b254, prev=0, new=2002177696, reason=BalanceChangeTransfer",
				"BalanceChange: addr=0xB661C9F3034237501433bD7A6587784b7020b254, prev=2002177696, new=0, reason=BalanceChangeTransfer",
				"BalanceChange: addr=0x566B022D5794Bc4c656f43659000d30b3BB2456b, prev=0, new=2002177696, reason=BalanceChangeTransfer",
				"BalanceChange: addr=0x566B022D5794Bc4c656f43659000d30b3BB2456b, prev=2002177696, new=0, reason=BalanceDecreaseSelfdestruct",
				"BalanceChange: addr=0x566B022D5794Bc4c656f43659000d30b3BB2456b, prev=0, new=2002177696, reason=BalanceIncreaseSelfdestruct",
				"BalanceChange: addr=0x7a238f298D55F8Db27B860eFf1e02d1528b16585, prev=987650297762750729, new=987669060488253107, reason=BalanceIncreaseGasReturn",
				"BalanceChange: addr=0x2B1D879B5e102e60166202de79537B48E2F18a42, prev=4102593985377377372090, new=4102594067182763277161, reason=BalanceIncreaseRewardTransactionFee",
			}, output)
		})
	}
}

func recordingTracingHooks(recordInto *[]string) *tracing.Hooks {
	record := func(format string, args ...any) {
		*recordInto = append(*recordInto, fmt.Sprintf(format, args...))
	}

	return &tracing.Hooks{
		OnBlockchainInit: func(chainConfig *params.ChainConfig) {
			record("OnBlockchainInit chainConfig=%s", ptrView(chainConfig))
		},
		OnBlockStart: func(event tracing.BlockEvent) {
			record("OnBlockStart (Block={Hash=%s, Number=%d}, TD=%v)", event.Block.Hash(), event.Block.NumberU64())
		},
		OnBlockEnd: func(err error) {
			record("OnBlockEnd (Error=%s)", errorView(err))
		},
		OnTxStart: func(vm *tracing.VMContext, tx *types.Transaction, from common.Address) {
			record("TxStart: tx=%s, from=%s vm={BlockNumber=%s, Time=%d, Coinbase=%s, Random=%s}",
				tx.Hash(), from, vm.BlockNumber, vm.Time, vm.Coinbase, stringerPtrView(vm.Random))
		},
		OnTxEnd: func(receipt *types.Receipt, err error) {
			record("TxEnd: receipt=%v, err=%v", receipt, err)
		},
		OnEnter: func(depth int, typ byte, from common.Address, to common.Address, input []byte, gas uint64, value *big.Int) {
			record("Enter: depth=%d, typ=%d, from=%v, to=%v, input=%x, gas=%d, value=%v", depth, typ, from, to, input, gas, value)
		},
		OnExit: func(depth int, output []byte, gasUsed uint64, err error, reverted bool) {
			record("Exit: depth=%d, output=%x, gasUsed=%d, err=%v, reverted=%v", depth, output, gasUsed, err, reverted)
		},
		OnGasChange: func(old, new uint64, reason tracing.GasChangeReason) {
			record("GasChange: old=%d, new=%d, reason=%v", old, new, reason)
		},
		OnBalanceChange: func(addr common.Address, prev, new *big.Int, reason tracing.BalanceChangeReason) {
			record("BalanceChange: addr=%v, prev=%v, new=%v, reason=%v", addr, prev, new, reason)
		},
		OnNonceChange: func(addr common.Address, prev, new uint64) {
			record("NonceChange: addr=%v, prev=%d, new=%d", addr, prev, new)
		},
		OnCodeChange: func(addr common.Address, prevCodeHash common.Hash, prevCode []byte, codeHash common.Hash, code []byte) {
			record("CodeChange: addr=%v, prevCodeHash=%v, prevCode=%d bytes, codeHash=%v, code=%d bytes", addr, prevCodeHash, len(prevCode), codeHash, len(code))
		},
		OnStorageChange: func(addr common.Address, slot common.Hash, prev, new common.Hash) {
			record("StorageChange: addr=%v, slot=%v, prev=%v, new=%v", addr, slot, prev, new)
		},
		OnLog: func(log *types.Log) {
			record("Log: log (addr=%s)", log.Address)
		},
	}
}

func runPrestateBlock(t *testing.T, prestatePath string, hooks *tracing.Hooks) {
	t.Helper()

	prestate := readPrestateData(t, prestatePath)

	tx := new(types.Transaction)
	require.NoError(t, rlp.DecodeBytes(common.FromHex(prestate.Input), tx))

	context := prestate.Context.toBlockContext(prestate.Genesis)

	testState := tests.MakePreState(rawdb.NewMemoryDatabase(), prestate.Genesis.Alloc, false, rawdb.HashScheme)
	defer testState.Close()

	testState.StateDB.SetTxContext(tx.Hash(), 0)

	block := types.NewBlock(&types.Header{
		ParentHash:       prestate.Genesis.ToBlock().Hash(),
		Number:           context.BlockNumber,
		Difficulty:       context.Difficulty,
		Coinbase:         context.Coinbase,
		Time:             context.Time,
		GasLimit:         context.GasLimit,
		BaseFee:          context.BaseFee,
		ParentBeaconRoot: ptr(common.Hash{}),
	}, &types.Body{
		Transactions: []*types.Transaction{tx},
	}, nil, trie.NewStackTrie(nil))

	if hooks.OnBlockchainInit != nil {
		hooks.OnBlockchainInit(prestate.Genesis.Config)
	}

	if hooks.OnBlockStart != nil {
		hooks.OnBlockStart(tracing.BlockEvent{
			Block: block,
		})
	}

	header := block.Header()
	msg, err := core.TransactionToMessage(tx, types.MakeSigner(prestate.Genesis.Config, header.Number, header.Time), header.BaseFee)
	require.NoError(t, err)

	// // Create a new context to be used in the EVM environment
	blockContext := core.NewEVMBlockContext(block.Header(), prestate, &context.Coinbase)
	vmenv := vm.NewEVM(blockContext, state.NewHookedState(testState.StateDB, hooks), prestate.Genesis.Config, vm.Config{Tracer: hooks})

	usedGas := uint64(0)
	_, err = core.ApplyTransactionWithEVM(
		msg,
		new(core.GasPool).AddGas(block.GasLimit()),
		testState.StateDB,
		header.Number,
		header.Hash(),
		tx,
		&usedGas,
		vmenv,
	)
	require.NoError(t, err)

	if hooks.OnBlockEnd != nil {
		hooks.OnBlockEnd(nil)
	}
}

func readPrestateData(t *testing.T, path string) *prestateData {
	t.Helper()

	// Call tracer test found, read if from disk
	blob, err := os.ReadFile(path)
	require.NoError(t, err)

	test := new(prestateData)
	require.NoError(t, json.Unmarshal(blob, test))

	var genesisWithTD struct {
		Genesis struct {
			TotalDifficulty *math.HexOrDecimal256 `json:"totalDifficulty"`
		} `json:"genesis"`
	}
	if err := json.Unmarshal(blob, &genesisWithTD); err == nil {
		test.TotalDifficulty = (*big.Int)(genesisWithTD.Genesis.TotalDifficulty)
	}

	return test
}

var _ core.ChainContext = (*prestateData)(nil)

type prestateData struct {
	Genesis         *core.Genesis   `json:"genesis"`
	Context         *callContext    `json:"context"`
	Input           string          `json:"input"`
	TotalDifficulty *big.Int        `json:"-"`
	TracerConfig    json.RawMessage `json:"tracerConfig"`

	// Populated after loading
	genesisBlock *types.Block
}

// Config implements core.ChainContext.
func (p *prestateData) Config() *params.ChainConfig {
	return p.Genesis.Config
}

// Engine implements core.ChainContext.
func (p *prestateData) Engine() consensus.Engine {
	return ethash.NewFullFaker()
}

// GetHeader implements core.ChainContext.
func (p *prestateData) GetHeader(hash common.Hash, number uint64) *types.Header {
	if p.Genesis == nil {
		return nil
	}

	if p.genesisBlock == nil {
		p.genesisBlock = p.Genesis.ToBlock()
	}

	if hash == p.genesisBlock.Hash() {
		return p.genesisBlock.Header()
	}

	if number == p.genesisBlock.NumberU64() {
		return p.genesisBlock.Header()
	}

	return nil
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

// testHasher is the helper tool for transaction/receipt list hashing.
// The original hasher is trie, in order to get rid of import cycle,
// use the testing hasher instead.
type testHasher struct {
	hasher hash.Hash
}

// NewHasher returns a new testHasher instance.
func NewHasher() *testHasher {
	return &testHasher{hasher: sha3.NewLegacyKeccak256()}
}

// Reset resets the hash state.
func (h *testHasher) Reset() {
	h.hasher.Reset()
}

// Update updates the hash state with the given key and value.
func (h *testHasher) Update(key, val []byte) error {
	h.hasher.Write(key)
	h.hasher.Write(val)
	return nil
}

// Hash returns the hash value.
func (h *testHasher) Hash() common.Hash {
	return common.BytesToHash(h.hasher.Sum(nil))
}

var _ core.Validator = (*ignoreValidateStateValidator)(nil)

type ignoreValidateStateValidator struct {
	core.Validator
}

func (v ignoreValidateStateValidator) ValidateBody(block *types.Block) error {
	return v.Validator.ValidateBody(block)
}

func (v ignoreValidateStateValidator) ValidateState(block *types.Block, state *state.StateDB, res *core.ProcessResult, stateless bool) error {
	return nil
}

func ptr[T any](v T) *T {
	return &v
}

func ptrView[T any](v *T) string {
	if v == nil {
		return "<nil>"
	}
	return "<set>"
}

func bigIntPtrView(v *big.Int) string {
	if v == nil {
		return "<nil>"
	}

	return v.String()
}

func stringerPtrView[T fmt.Stringer](v *T) string {
	if v == nil {
		return "<nil>"
	}

	return (*v).String()
}

func errorView(v error) string {
	if v == nil {
		return "<no error>"
	}

	return v.Error()
}

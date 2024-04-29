package firehose_test

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/tests"
)

func TestFirehoseIntegrationTest(t *testing.T) {
	key, _ := crypto.HexToECDSA("b71c71a67e1177ad4e901695e1b4b9ee17ae16c6668d313eac2f96dbcda3f291")
	from := crypto.PubkeyToAddress(key.PublicKey)
	gas := uint64(1000000) // 1M gas
	to := common.HexToAddress("0x00000000000000000000000000000000deadbeef")
	signer := types.LatestSignerForChainID(big.NewInt(1337))
	tx, err := types.SignNewTx(key, signer,
		&types.LegacyTx{
			Nonce:    1,
			GasPrice: big.NewInt(500),
			Gas:      gas,
			To:       &to,
		})
	if err != nil {
		t.Fatal(err)
	}
	txContext := vm.TxContext{
		Origin:   from,
		GasPrice: tx.GasPrice(),
	}
	context := vm.BlockContext{
		CanTransfer: core.CanTransfer,
		Transfer:    core.Transfer,
		Coinbase:    common.Address{},
		BlockNumber: new(big.Int).SetUint64(uint64(5)),
		Time:        5,
		Difficulty:  big.NewInt(0xffffffff),
		GasLimit:    gas,
		BaseFee:     big.NewInt(8),
	}
	alloc := types.GenesisAlloc{}
	// The code pushes 'deadbeef' into memory, then the other params, and calls CREATE2, then returns
	// the address
	loop := []byte{
		byte(vm.JUMPDEST), //  [ count ]
		byte(vm.PUSH1), 0, // jumpdestination
		byte(vm.JUMP),
	}
	alloc[common.HexToAddress("0x00000000000000000000000000000000deadbeef")] = types.Account{
		Nonce:   1,
		Code:    loop,
		Balance: big.NewInt(1),
	}
	alloc[from] = types.Account{
		Nonce:   1,
		Code:    []byte{},
		Balance: big.NewInt(500000000000000),
	}
	state := tests.MakePreState(rawdb.NewMemoryDatabase(), alloc, false, rawdb.HashScheme)
	defer state.Close()

	// // Create the tracer, the EVM environment and run it
	// tracer := logger.NewStructLogger(&logger.Config{
	// 	Debug: false,
	// 	//DisableStorage: true,
	// 	//EnableMemory: false,
	// 	//EnableReturnData: false,
	// })

	engine, db, genesis, blockchain := newCanonical()
	processor := core.NewStateProcessor(genesis.Config, blockchain, engine)

	processor.Process(block*types.Block, statedb*state.StateDB, vm.Config{Tracer: tracer.Hooks()})

	// evm := vm.NewEVM(context, txContext, state.StateDB, genesis.Config, vm.Config{Tracer: tracer.Hooks()})
	// msg, err := core.TransactionToMessage(tx, signer, context.BaseFee)
	// if err != nil {
	// 	t.Fatalf("failed to prepare transaction for tracing: %v", err)
	// }

	// snap := state.StateDB.Snapshot()
	// st := core.NewStateTransition(evm, msg, new(core.GasPool).AddGas(tx.Gas()))
	// _, err = st.TransitionDb()
	// if err != nil {
	// 	t.Fatal(err)
	// }
	// state.StateDB.RevertToSnapshot(snap)
}

func newCanonical(t testing.T) (consensus.Engine, ethdb.Database, *core.Genesis, *core.BlockChain) {
	t.Helper()

	var (
		engine  = ethash.NewFullFaker()
		genesis = &Genesis{
			BaseFee: big.NewInt(params.InitialBaseFee),
			Config:  params.AllEthashProtocolChanges,
		}
	)
	// Initialize a fresh chain with only a genesis block
	blockchain, err := core.NewBlockChain(rawdb.NewMemoryDatabase(), core.DefaultCacheConfigWithScheme(scheme), genesis, nil, engine, vm.Config{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	return engine, rawdb.NewMemoryDatabase(), genesis, blockchain, nil
}

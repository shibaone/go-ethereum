package firehose_test

import (
	"encoding/json"
	"math/big"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/eth/tracers"
	"github.com/stretchr/testify/require"
)

func TestFirehoseChain(t *testing.T) {
	context := vm.BlockContext{
		CanTransfer: core.CanTransfer,
		Transfer:    core.Transfer,
		Coinbase:    common.Address{},
		BlockNumber: new(big.Int).SetUint64(uint64(1)),
		Time:        1,
		Difficulty:  big.NewInt(2),
		GasLimit:    uint64(1000000),
		BaseFee:     big.NewInt(8),
	}

	tracer, err := tracers.NewFirehoseFromRawJSON(json.RawMessage(`{
		"applyBackwardsCompatibility": true,
		"_private": {
			"flushToTestBuffer": true,
			"ignoreGenesisBlock": true
		}
	}`))
	require.NoError(t, err)

	hooks := tracers.NewTracingHooksFromFirehose(tracer)

	genesis, blockchain, engine := newBlockchain(t, types.GenesisAlloc{}, context, hooks)

	_, blocks, _ := core.GenerateChainWithGenesis(genesis, engine, 1, func(i int, b *core.BlockGen) {
		b.SetCoinbase(context.Coinbase)
		b.SetDifficulty(context.Difficulty)
		b.SetParentBeaconRoot(common.Hash{})
	})

	blockchain.SetBlockValidatorAndProcessorForTesting(
		ignoreValidateStateValidator{core.NewBlockValidator(genesis.Config, blockchain)},
		core.NewStateProcessor(genesis.Config, blockchain.HeaderChain()),
	)

	n, err := blockchain.InsertChain(blocks)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	genesisLine, blockLines, unknownLines := readTracerFirehoseLines(t, tracer)
	require.Len(t, unknownLines, 0, "Lines:\n%s", strings.Join(slicesMap(unknownLines, func(l unknownLine) string { return "- '" + string(l) + "'" }), "\n"))
	require.NotNil(t, genesisLine)
	blockLines.assertEquals(t, filepath.Join("testdata", t.Name()),
		firehoseBlockLineParams{"1", "cf7d0082c6616f6079dd9ed467fbc925a9032978f65caadc250d9a443436530b", "0", "845bad515694a416bab4b8d44e22cf97a8c894a8502110ab807883940e185ce0", "0", "10000000000"},
	)
}

func TestFirehosePrestate(t *testing.T) {
	testFolders := []string{
		"./testdata/TestFirehosePrestate/keccak256_too_few_memory_bytes_get_padded",
		"./testdata/TestFirehosePrestate/keccak256_wrong_diff",
		"./testdata/TestFirehosePrestate/suicide_double_withdraw",
		"./testdata/TestFirehosePrestate/extra_account_creations",
	}

	for _, folder := range testFolders {
		name := filepath.Base(folder)

		t.Run(name, func(t *testing.T) {
			tracer, err := tracers.NewFirehoseFromRawJSON(json.RawMessage(`{
				"applyBackwardsCompatibility": true,
				"_private": {
					"flushToTestBuffer": true
				}
			}`))
			require.NoError(t, err)

			runPrestateBlock(t, filepath.Join(folder, "prestate.json"), tracers.NewTracingHooksFromFirehose(tracer))

			genesisLine, blockLines, unknownLines := readTracerFirehoseLines(t, tracer)
			require.Len(t, unknownLines, 0, "Lines:\n%s", strings.Join(slicesMap(unknownLines, func(l unknownLine) string { return "- '" + string(l) + "'" }), "\n"))
			require.NotNil(t, genesisLine)
			blockLines.assertOnlyBlockEquals(t, folder, 1)
		})
	}

}

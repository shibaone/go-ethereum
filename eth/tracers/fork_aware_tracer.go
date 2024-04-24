package tracers

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/streamingfast/bstream"
	"github.com/streamingfast/bstream/forkable"
)

type config struct {
	ForkdbPath       string `json:"forkdbPath"`
	OutputPath       string `json:"outputPath"`
	FromSnapshotSync bool   `json:"fromSnapshotSync"`
}

func init() {
	LiveDirectory.Register("fork", func(cfg json.RawMessage) (*tracing.Hooks, error) {
		var config config
		if len([]byte(cfg)) > 0 {
			if err := json.Unmarshal(cfg, &config); err != nil {
				return nil, fmt.Errorf("failed to parse Firehose config: %w", err)
			}
		}

		tracer, err := newForAwareTracer(&config)
		if err != nil {
			return nil, fmt.Errorf("failed to create fork aware tracer: %w", err)
		}

		return &tracing.Hooks{
			OnGenesisBlock: tracer.OnGenesisBlock,
			OnBlockStart:   tracer.OnBlockStart,
			OnBlockEnd:     tracer.OnBlockEnd,
			OnEnter:        tracer.OnEnter,
			OnClose:        tracer.OnClose,
		}, nil
	})
}

type ForkAwareTracer struct {
	config               *config
	activeBlock          *BlockValueSum
	activeFinalizedBlock *types.Header
	forkDB               *forkable.ForkDB
}

var _ forkable.ObjectJSONMarshallable = (*BlockValueSum)(nil)

type BlockValueSum struct {
	BlockNum   uint64      `json:"block_num"`
	BlockHash  common.Hash `json:"block_hash"`
	ParentHash common.Hash `json:"parent_hash"`
	TotalValue *BigInt     `json:"total_value"`
}

// JSONMarshallable implements forkable.ObjectJSONMarshallable.
func (b *BlockValueSum) JSONMarshallable() {
}

func (b *BlockValueSum) BlockRef() bstream.BlockRef {
	return newBlockRef(b.BlockNum, b.BlockHash)
}

func (b *BlockValueSum) PreviousBlockRef() bstream.BlockRef {
	return newBlockRef(b.BlockNum-1, b.ParentHash)
}

type BigInt big.Int

func (b *BigInt) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf(`"0x%s"`, (*big.Int)(b).Text(16))), nil
}

func (b *BigInt) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}

	i := new(big.Int)
	if _, ok := i.SetString(s, 0); !ok {
		return fmt.Errorf("failed to parse big.Int from string: %s", s)
	}

	*b = BigInt(*i)
	return nil
}

func newForAwareTracer(config *config) (*ForkAwareTracer, error) {
	if err := os.MkdirAll(filepath.Dir(config.ForkdbPath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create forkdb directory: %w", err)
	}

	if err := os.MkdirAll(config.OutputPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output path directory: %w", err)
	}

	data, err := os.ReadFile(config.ForkdbPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to read forkdb file: %w", err)
	}

	forkDB := forkable.NewForkDB()
	if data != nil {
		if err := forkDB.Deserialize(data, func() any { return &BlockValueSum{} }); err != nil {
			return nil, fmt.Errorf("failed to deserialize forkdb file: %w", err)
		}
	}

	return &ForkAwareTracer{config, nil, nil, forkDB}, nil
}

func (t *ForkAwareTracer) OnGenesisBlock(genesis *types.Block, alloc types.GenesisAlloc) {
	t.forkDB.InitLIB(blockToRef(genesis))
}

func (t *ForkAwareTracer) OnBlockStart(event tracing.BlockEvent) {
	if t.config.FromSnapshotSync && !t.forkDB.HasLIB() {
		// We are in the first block after a snapshot sync, we need to initialize the LIB to the current block
		t.forkDB.InitLIB(blockToRef(event.Block))
	}

	t.activeBlock = &BlockValueSum{BlockNum: event.Block.NumberU64(), BlockHash: event.Block.Hash(), ParentHash: event.Block.ParentHash(), TotalValue: (*BigInt)(big.NewInt(0))}
	t.activeFinalizedBlock = event.Finalized
}

func (t *ForkAwareTracer) OnBlockEnd(err error) {
	blockRef := t.activeBlock.BlockRef()
	t.forkDB.AddLink(blockRef, t.activeBlock.PreviousBlockRef().ID(), t.activeBlock)

	newLIB := t.determineLIB()
	if newLIB != nil {
		libRef := bstream.NewBlockRef(t.forkDB.LIBID(), t.forkDB.LIBNum())
		t.forkDB.SetLIB(t.activeBlock.BlockRef(), t.determineLIB().Num())

		finalizedBlocks, _ := t.forkDB.CompleteSegment(libRef)

		for _, block := range finalizedBlocks {
			outputFile := filepath.Join(t.config.OutputPath, fmt.Sprintf("%09d_finalized.json", block.BlockNum))
			blockData := block.Object.(*BlockValueSum)
			data, err := json.MarshalIndent(blockData, "", "  ")
			if err != nil {
				panic(fmt.Errorf("failed to marshal block data: %w", err))
			}

			if err := os.WriteFile(outputFile, data, 0644); err != nil {
				panic(fmt.Errorf("failed to write block data to file: %w", err))
			}
		}

		t.forkDB.PurgeBeforeLIB(0)
	}

	t.activeBlock = nil
	t.activeFinalizedBlock = nil
}

func (t *ForkAwareTracer) determineLIB() (out bstream.BlockRef) {
	finalizedValue := "<nil>"
	if t.activeFinalizedBlock != nil {
		finalizedValue = headerToRef(t.activeFinalizedBlock).String()
	}
	fmt.Printf("Determining LIB to use: finalized: %s\n", finalizedValue)
	defer func() {
		fmt.Printf("LIB to use: %s\n", out.String())
	}()

	// This can happen when import mode without beacon chain
	// being connected (`geth --synctarget`, `geth import ...`, etc). For now
	// refusing to purge anything until finalized block is set.
	if t.activeFinalizedBlock == nil {
		return nil
	}

	// This is normal condition since the beacon chain finalized block while doing a full
	// sync is always in the future of the current block. If it's the case, we assume the
	// parent block is the new finalized block.
	//
	// Is this actually correct? I don't think so, pretty sure Geth could still import
	// a wrong "segment" that would need to be reverted... If this happens, the ForkDB
	// will be in a broken state
	if t.activeFinalizedBlock.Number.Uint64() > t.activeBlock.BlockNum {
		return t.activeBlock.PreviousBlockRef()
	}

	return headerToRef(t.activeFinalizedBlock)
}

func (t *ForkAwareTracer) OnClose() {
	data, err := t.forkDB.Serialize()
	if err != nil {
		panic(fmt.Errorf("failed to serialize forkdb: %w", err))
	}

	if err := os.WriteFile(t.config.ForkdbPath, data, 0644); err != nil {
		panic(fmt.Errorf("failed to write forkdb to file: %w", err))
	}
}

func (t *ForkAwareTracer) OnEnter(depth int, typ byte, from common.Address, to common.Address, input []byte, gas uint64, value *big.Int) {
	// This is wrong as it doesn't take into account failed calls and failure higher in the call stack,
	// for demo purposes, we will ignore this fact for now and simply concentrate on the fork handling.
	if value != nil {
		(*big.Int)(t.activeBlock.TotalValue).Add((*big.Int)(t.activeBlock.TotalValue), value)
	}
}

func newBlockRef(number uint64, hash common.Hash) bstream.BlockRef {
	return bstream.NewBlockRef(hex.EncodeToString(hash.Bytes()), number)
}

func blockToRef(block *types.Block) bstream.BlockRef {
	return newBlockRef(block.NumberU64(), block.Hash())
}

func headerToRef(header *types.Header) bstream.BlockRef {
	return newBlockRef(header.Number.Uint64(), header.Hash())
}

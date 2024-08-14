// Copyright 2015 The go-ethereum Authors
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

package core

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/misc"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/systemcontracts"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/firehose"
	"github.com/ethereum/go-ethereum/params"
)

// StateProcessor is a basic Processor, which takes care of transitioning
// state from one point to another.
//
// StateProcessor implements Processor.
type StateProcessor struct {
	config *params.ChainConfig // Chain configuration options
	bc     *BlockChain         // Canonical block chain
	engine consensus.Engine    // Consensus engine used for block rewards
}

// NewStateProcessor initialises a new StateProcessor.
func NewStateProcessor(config *params.ChainConfig, bc *BlockChain, engine consensus.Engine) *StateProcessor {
	return &StateProcessor{
		config: config,
		bc:     bc,
		engine: engine,
	}
}

// Process processes the state changes according to the Ethereum rules by running
// the transaction messages using the statedb and applying any rewards to both
// the processor (coinbase) and any included uncles.
//
// Process returns the receipts and logs accumulated during the process and
// returns the amount of gas that was used in the process. If any of the
// transactions failed to execute due to insufficient gas it will return an error.
func (p *StateProcessor) Process(block *types.Block, statedb *state.StateDB, cfg vm.Config, firehoseContext *firehose.Context) (*state.StateDB, types.Receipts, []*types.Log, uint64, error) {
	var (
		usedGas     = new(uint64)
		header      = block.Header()
		blockHash   = block.Hash()
		blockNumber = block.Number()
		allLogs     []*types.Log
		gp          = new(GasPool).AddGas(block.GasLimit())
	)

	if firehoseContext.Enabled() {
		firehoseContext.StartBlock(block)
	}

	var receipts = make([]*types.Receipt, 0)
	// Mutate the block and state according to any hard-fork specs
	if p.config.DAOForkSupport && p.config.DAOForkBlock != nil && p.config.DAOForkBlock.Cmp(block.Number()) == 0 {
		misc.ApplyDAOHardFork(statedb, firehoseContext)
	}

	lastBlock := p.bc.GetBlockByHash(block.ParentHash())
	if lastBlock == nil {
		return statedb, nil, nil, 0, errors.New("could not get parent block")
	}
	if !p.config.IsFeynman(block.Number(), block.Time()) {
		// Handle upgrade build-in system contract code
		systemcontracts.UpgradeBuildInSystemContract(p.config, blockNumber, lastBlock.Time(), block.Time(), statedb, firehoseContext)
	}

	var (
		context = NewEVMBlockContext(header, p.bc, nil)
		vmenv   = vm.NewEVM(context, vm.TxContext{}, statedb, p.config, cfg, firehoseContext)
		signer  = types.MakeSigner(p.config, header.Number, header.Time)
		txNum   = len(block.Transactions())
	)
	if beaconRoot := block.BeaconRoot(); beaconRoot != nil {
		ProcessBeaconBlockRoot(*beaconRoot, vmenv, statedb, firehoseContext)
	}

	// Iterate over and process the individual transactions
	posa, isPoSA := p.engine.(consensus.PoSA)
	commonTxs := make([]*types.Transaction, 0, txNum)

	// initialise bloom processors
	var bloomProcessors ReceiptProcessor
	if firehoseContext.Enabled() {
		// We cannot use the async receipt bloom generator in deep mind because we output the receipt
		// with the transaction and cannot wait for all transactions to finish. We could do it but
		// requires changes on the reading part to add back log blooms at the end of block.
		bloomProcessors = NewReceiptBloomGenerator()
	} else {
		bloomProcessors = NewAsyncReceiptBloomGenerator(txNum)
	}

	statedb.MarkFullProcessed()

	// usually do have two tx, one for validator set contract, another for system reward contract.
	systemTxs := make([]*types.Transaction, 0, 2)

	txFirehoseContext := firehoseContext
	if txFirehoseContext.Enabled() {
		txFirehoseContext = firehose.NewTransactionContextWithBuffer(firehose.TxSyncBuffer)
	}

	for i, tx := range block.Transactions() {
		if isPoSA {
			if isSystemTx, err := posa.IsSystemTransaction(tx, block.Header()); err != nil {
				// Trapped later at 'Process' call site at which point the block is canceled
				bloomProcessors.Close()
				return statedb, nil, nil, 0, err
			} else if isSystemTx {
				systemTxs = append(systemTxs, tx)
				continue
			}
		}
		if p.config.IsCancun(block.Number(), block.Time()) {
			if len(systemTxs) > 0 {
				// systemTxs should be always at the end of block.
				return statedb, nil, nil, 0, fmt.Errorf("normal tx %d [%v] after systemTx", i, tx.Hash().Hex())
			}
		}

		msg, err := TransactionToMessage(tx, signer, header.BaseFee)
		if err != nil {
			// Trapped later at 'Process' call site at which point the block is canceled
			bloomProcessors.Close()
			return statedb, nil, nil, 0, err
		}

		if txFirehoseContext.Enabled() {
			txFirehoseContext.StartTransaction(tx, uint(i), block.Header().BaseFee)
			txFirehoseContext.RecordTrxFrom(msg.From)
		}

		statedb.SetTxContext(tx.Hash(), i)

		receipt, err := applyTransaction(msg, p.config, gp, statedb, blockNumber, blockHash, tx, usedGas, vmenv, txFirehoseContext, bloomProcessors)
		if err != nil {
			// Trapped later at 'Process' call site at which point the block is canceled
			bloomProcessors.Close()
			return statedb, nil, nil, 0, fmt.Errorf("could not apply tx %d [%v]: %w", i, tx.Hash().Hex(), err)
		}

		if txFirehoseContext.Enabled() {
			txFirehoseContext.EndTransaction(receipt)

			// We must flush using the "global" context here, since the speculative context don't hold the real global lock
			firehoseContext.FlushTransaction(txFirehoseContext)
		}

		commonTxs = append(commonTxs, tx)
		receipts = append(receipts, receipt)
	}
	if v, ok := bloomProcessors.(*AsyncReceiptBloomGenerator); ok {
		v.Close()
	}

	// Finalize block is a bit special since it can be enabled without the full firehose sync.
	// As such, if firehose is enabled, we log it and us the firehose context. Otherwise if
	// block progress is enabled.
	if firehoseContext.Enabled() {
		firehoseContext.FinalizeBlock(block)
	} else if firehose.BlockProgressEnabled {
		firehose.SyncContext().FinalizeBlock(block)
	}

	// Fail if Shanghai not enabled and len(withdrawals) is non-zero.
	withdrawals := block.Withdrawals()
	if len(withdrawals) > 0 && !p.config.IsShanghai(block.Number(), block.Time()) {
		return nil, nil, nil, 0, errors.New("withdrawals before shanghai")
	}

	// Finalize the block, applying any consensus engine specific extras (e.g. block rewards)
	err := p.engine.Finalize(p.bc, header, statedb, &commonTxs, block.Uncles(), withdrawals, &receipts, &systemTxs, usedGas, firehoseContext)
	if err != nil {
		// Trapped later at 'Process' call site at which point the block is canceled
		return statedb, receipts, allLogs, *usedGas, err
	}
	for _, receipt := range receipts {
		allLogs = append(allLogs, receipt.Logs...)
	}

	return statedb, receipts, allLogs, *usedGas, nil
}

func applyTransaction(msg *Message, config *params.ChainConfig, gp *GasPool, statedb *state.StateDB, blockNumber *big.Int, blockHash common.Hash, tx *types.Transaction, usedGas *uint64, evm *vm.EVM, txFirehoseContext *firehose.Context, receiptProcessors ...ReceiptProcessor) (*types.Receipt, error) {
	// Create a new context to be used in the EVM environment.
	txContext := NewEVMTxContext(msg)
	evm.Reset(txContext, statedb, txFirehoseContext)

	// Apply the transaction to the current state (included in the env).
	result, err := ApplyMessage(evm, msg, gp)
	if err != nil {
		return nil, err
	}

	// Update the state with pending changes.
	var root []byte
	if config.IsByzantium(blockNumber) {
		statedb.Finalise(true)
	} else {
		root = statedb.IntermediateRoot(config.IsEIP158(blockNumber)).Bytes()
	}
	*usedGas += result.UsedGas

	// Create a new receipt for the transaction, storing the intermediate root and gas used
	// by the tx.
	receipt := &types.Receipt{Type: tx.Type(), PostState: root, CumulativeGasUsed: *usedGas}
	if result.Failed() {
		receipt.Status = types.ReceiptStatusFailed
	} else {
		receipt.Status = types.ReceiptStatusSuccessful
	}
	receipt.TxHash = tx.Hash()
	receipt.GasUsed = result.UsedGas

	if tx.Type() == types.BlobTxType {
		receipt.BlobGasUsed = uint64(len(tx.BlobHashes()) * params.BlobTxBlobGasPerBlob)
		receipt.BlobGasPrice = evm.Context.BlobBaseFee
	}

	// If the transaction created a contract, store the creation address in the receipt.
	if msg.To == nil {
		receipt.ContractAddress = crypto.CreateAddress(evm.TxContext.Origin, tx.Nonce())
	}

	// Set the receipt logs and create the bloom filter.
	receipt.Logs = statedb.GetLogs(tx.Hash(), blockNumber.Uint64(), blockHash)
	receipt.BlockHash = blockHash
	receipt.BlockNumber = blockNumber
	receipt.TransactionIndex = uint(statedb.TxIndex())
	for _, receiptProcessor := range receiptProcessors {
		receiptProcessor.Apply(receipt)
	}
	return receipt, err
}

// ApplyTransaction attempts to apply a transaction to the given state database
// and uses the input parameters for its environment. It returns the receipt
// for the transaction, gas used and an error if the transaction failed,
// indicating the block was invalid.
func ApplyTransaction(config *params.ChainConfig, bc ChainContext, author *common.Address, gp *GasPool, statedb *state.StateDB, header *types.Header, tx *types.Transaction, usedGas *uint64, cfg vm.Config, txFirehoseContext *firehose.Context, receiptProcessors ...ReceiptProcessor) (*types.Receipt, error) {
	msg, err := TransactionToMessage(tx, types.MakeSigner(config, header.Number, header.Time), header.BaseFee)
	if err != nil {
		return nil, err
	}
	// Create a new context to be used in the EVM environment
	blockContext := NewEVMBlockContext(header, bc, author)
	txContext := NewEVMTxContext(msg)
	vmenv := vm.NewEVM(blockContext, txContext, statedb, config, cfg, txFirehoseContext)
	defer func() {
		ite := vmenv.Interpreter()
		vm.EVMInterpreterPool.Put(ite)
		vm.EvmPool.Put(vmenv)
	}()
	return applyTransaction(msg, config, gp, statedb, header.Number, header.Hash(), tx, usedGas, vmenv, txFirehoseContext, receiptProcessors...)
}

// ProcessBeaconBlockRoot applies the EIP-4788 system call to the beacon block root
// contract. This method is exported to be used in tests.
func ProcessBeaconBlockRoot(beaconRoot common.Hash, vmenv *vm.EVM, statedb *state.StateDB, firehoseContext *firehose.Context) {
	// Return immediately if beaconRoot equals the zero hash when using the Parlia engine.
	if beaconRoot == (common.Hash{}) {
		if chainConfig := vmenv.ChainConfig(); chainConfig != nil && chainConfig.Parlia != nil {
			return
		}
	}

	firehoseContext.StartSystemCall()
	defer firehoseContext.EndSystemCall()

	// If EIP-4788 is enabled, we need to invoke the beaconroot storage contract with
	// the new root
	msg := &Message{
		From:      params.SystemAddress,
		GasLimit:  30_000_000,
		GasPrice:  common.Big0,
		GasFeeCap: common.Big0,
		GasTipCap: common.Big0,
		To:        &params.BeaconRootsAddress,
		Data:      beaconRoot[:],
	}
	vmenv.Reset(NewEVMTxContext(msg), statedb, firehoseContext)
	statedb.AddAddressToAccessList(params.BeaconRootsAddress)
	_, _, _ = vmenv.Call(vm.AccountRef(msg.From), *msg.To, msg.Data, 30_000_000, common.U2560)
	statedb.Finalise(true)
}

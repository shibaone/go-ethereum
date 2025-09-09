package core

import "github.com/ethereum/go-ethereum/core/tracing"

// Used for Firehose
func (hc *HeaderChain) GetTracingHooks() *tracing.Hooks { return hc.logger }
func (bc *BlockChain) GetTracingHooks() *tracing.Hooks  { return bc.logger }

package core

import "github.com/ethereum/go-ethereum/core/tracing"

func (bc *BlockChain) GetTracingHooks() *tracing.Hooks { return bc.logger }

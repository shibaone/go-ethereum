package tracing

// String representations of the GasChangeReason and BalanceChangeReason enums
func (r GasChangeReason) String() string {
	switch r {
	case GasChangeTxInitialBalance:
		return "tx.initialBalance"
	case GasChangeTxIntrinsicGas:
		return "tx.intrinsicGas"
	case GasChangeTxRefunds:
		return "tx.refunds"
	case GasChangeTxLeftOverReturned:
		return "tx.leftOverReturned"
	default:
		return "unknown"
	}
}

func (r BalanceChangeReason) String() string {
	switch r {
	case BalanceDecreaseGasBuy:
		return "gasBuy"
	case BalanceChangeTransfer:
		return "transfer"
	case BalanceIncreaseRewardTransactionFee:
		return "rewardTransactionFee"
	case BalanceIncreaseGasReturn:
		return "gasReturn"
	case BalanceChangePolygonBurn:
		return "polygonBurn"
	default:
		return "unknown"
	}
}

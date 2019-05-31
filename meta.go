package main

import "time"

// real depth
type Depth struct {
	Buy       float64
	Sell      float64
	UpdatedAt time.Time // depth 最后一次更新的时间
}

// 订单行为
type ActionType int

func (self ActionType) String() string {
	return ActionName[self-1]
}

// 交易方向
type TraderSide int

func (self TraderSide) String() string {
	if self == 0 {
		return "Unknow"
	}
	return TraderSideName[self-1]
}

// type values
const (
	ACTION_PO ActionType = iota + 1
	ACTION_CO
	ACTION_POS
	ACTION_WALLET
	ACTION_CLOSING
)
const (
	TraderBuy TraderSide = iota + 1
	TraderSell
)

// type to string
var (
	ActionName     = []string{"PLACE_ORDER", "CANCEL_ORDER", "GET_POS", "GET_WALLET", "CLOSING_POS"}
	TraderSideName = []string{"BUY", "SELL"}
)

type ActionOrder struct {
	Action ActionType
	Amount float64
	Price  float64
	Side   TraderSide
	ID     string
	Time   time.Time
}

// position info
type PositionInfo struct {
	AvgEntryPrice float64 // 持仓价格
	AvgEntryQty   float64 // 持仓量
	ClosingPrice  float64 // 强平价格
}

// place order params
type PlaceOrderParams struct {
	Price  float64
	Amount float64
}

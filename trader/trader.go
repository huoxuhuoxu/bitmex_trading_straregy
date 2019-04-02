package main

import (
	"sync"
	"time"

	goex "github.com/huoxuhuoxu/GoEx"
	"github.com/huoxuhuoxu/GoEx/bitmex"
	"github.com/huoxuhuoxu/UseGoexPackaging/conn"
)

type Trader struct {
	coupler *Coupler

	mDepth                *goex.Depth    // bitmex 买1/卖1 信息
	bmRWMutex             *sync.RWMutex  // bitmex 相关读写锁
	bmCancelMutex         *sync.Mutex    // bitmex 撤单相关读写锁
	maxPos                int            // 持仓警戒线
	poStep, coStep        time.Duration  // 下单, 撤单间隔
	coDiffStep            int64          // 撤离下的单子与当前的间隔
	closingStep           float64        // 平仓间隔
	pos                   float64        // 当前仓位
	posPrice              float64        // 持仓价格
	closingPrice          float64        // 平仓价
	exchangeAPI           *conn.Conn     // 交易所 - API对象
	bitmexExchangeAPI     *bitmex.Bitmex // bitmex 合约API 对象
	apiKey, secretKey     string         // 交易所 - Key
	curA, curB            string         // 交易对
	buyAmount, sellAmount float64        // 下单量
	baseAmount            float64        // 下单基数
	isRunning             bool           // 可运行的
	isInit                bool           // 完成初始化
	isClosing             bool           // 正在进行平仓
	// pendingOrders         map[string]*goex.PendingOrder // 待成交订单ID
	mmTimestamp int64 // 根据市场变动进行反应, 间隔30s
}

// 同步当前orderbook
func (self *Coupler) SetOrderBook() {
	panic("no")
}

// 下单
func (self *Trader) PlaceOrder(side string, amount, price float64) {

}

// 撤单
func (self *Trader) CancelOrder(id string) {

}

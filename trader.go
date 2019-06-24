package main

import (
	"log"
	"net/http"
	"sync"
	"time"

	goex "github.com/huoxuhuoxu/GoEx"
	"github.com/huoxuhuoxu/GoEx/bitmex"
	"github.com/huoxuhuoxu/UseGoexPackaging/conn"
)

type Trader struct {
	*MainControl                     // 主控
	ApiKey, SecretKey string         // key
	Depth             *Depth         // bitmex depth
	ProcessLock       *sync.RWMutex  // 执行流程锁
	MaxPos            float64        // 最大持仓
	Exchange          *conn.Conn     // 交易所 API 对象
	Contract          *bitmex.Bitmex // 交易所合约 API 对象
	Currency          [2]string      // 交易对
	BaseAmount        float64        // 下单基础量
	BasePrice         float64        // 中间价间距
	isRunning         bool           // 发生意外
	*PositionInfo                    // 账号运行时
	isClosingPos      bool           // 是否处于平仓中
}

func NewTrader(apiKey, secretKey string, mc *MainControl, isDebug bool) *Trader {
	self := &Trader{
		MainControl:  mc,
		ApiKey:       apiKey,
		SecretKey:    secretKey,
		Depth:        &Depth{},
		ProcessLock:  &sync.RWMutex{},
		MaxPos:       3000,
		Exchange:     nil,
		Contract:     nil,
		Currency:     [2]string{"XBT", "USD"},
		BaseAmount:   50,
		BasePrice:    50,
		isRunning:    true,
		PositionInfo: &PositionInfo{},
	}

	self.Exchange = conn.NewConn(
		goex.BITMEX,
		apiKey,
		secretKey,
		map[string]string{
			"curA": self.Currency[0],
			"curB": self.Currency[1],
		},
	)
	self.Contract = bitmex.New(&http.Client{}, apiKey, secretKey)

	return self
}

func (self *Trader) Running() {
	// 运行前将现有挂单全部撤离
	orders, err := self.Exchange.UnfinishOrders()
	if err != nil {
		self.Output.Error("get unfinish orders failed", err)
	} else {
		if len(orders) > 0 {
			self.Output.Info("ready before running, cancel old order", len(orders))
			for _, order := range orders {
				_, err := self.Exchange.CancelOrder(order.OrderID2)
				if err != nil {
					self.Output.Error("ready before running, cancel order failed", err)
				}
			}
		}
	}

	// running
	self.wsReceiveMessage()
	go self.priceIsError()
}

// ws 出现介价格错误时的丢弃 后续处理
func (self *Trader) priceIsError() {
	go func() {
		self.Output.Log("price is error running ...")
		chanTick := time.Tick(time.Minute)
		for {
			<-chanTick
			select {
			case <-self.Ctx.Done():
				self.Output.Log("price is error, closed")
				return
			default:
				func() {
					self.ProcessLock.RLock()
					defer self.ProcessLock.RUnlock()
					if time.Now().Sub(self.Depth.UpdatedAt) > time.Minute*5 {
						self.Output.Warn("price is error, restart program!")
						self.Sr.RestartProcess()
					}
				}()
			}
		}
	}()
}

// ws异常处理
func (self *Trader) wsExceptHandler(wsConn *conn.WsConn, err error) {
	self.Output.Error("ws 连接发生异常", err)
	for {
		err := wsConn.ReConnectByArtificial()
		if err != nil {
			self.Output.Warn("reconn failed", err)
			time.Sleep(time.Second)
			continue
		}
		self.ProcessLock.Lock()
		self.isRunning = true
		self.ProcessLock.Unlock()
		break
	}
}

// 接收ws推送的depth
func (self *Trader) wsReceiveMessage() {
	wsConn, err := conn.NewWsConn(
		goex.BITMEX,
		self.ApiKey,
		self.SecretKey,
		conn.RT_EXCHANGE_WS,
		nil,
	)
	if err != nil {
		self.Output.Error("new ws conn", err)
		self.Sr.RestartProcess()
		return
	}
	wsConn.SetSubscribe([]string{"orderBookL2_25:XBTUSD"})

	err = wsConn.Connect()
	if err != nil {
		self.Output.Error("ws error", err)
		self.Sr.RestartProcess()
		return
	}

	wsConn.ReceiveMessage(func(data interface{}) {
		switch data.(type) {
		case goex.DepthPair:
			depthPair := data.(goex.DepthPair)
			self.ProcessLock.Lock()
			defer self.ProcessLock.Unlock()
			if !self.isRunning {
				self.Output.Warn("ws conn ReConnectByArtificial ...")
				return
			}
			if err != nil {
				self.Depth = &Depth{}
				self.isRunning = false
				go self.wsExceptHandler(wsConn, err)
				return
			}

			// 长时间价格没有变动, 视为交易所行情信息出错了, 重启
			if depthPair.Buy == self.Depth.Buy && depthPair.Sell == self.Depth.Sell {
				var t time.Time
				if self.Depth.UpdatedAt != t {
					t = time.Now()
					if t.Sub(self.Depth.UpdatedAt) > time.Minute*4 {
						self.Output.Warn("depth error, 长时间没有变动过了!")
						self.Sr.RestartProcess()
					}
				}
				return
			}

			// depth
			self.Depth.Buy = depthPair.Buy
			self.Depth.Sell = depthPair.Sell
			self.Depth.UpdatedAt = time.Now()
			self.Output.Logf("real depth %.1f %.1f", self.Depth.Buy, self.Depth.Sell)
		}
	}, func(err error) {
		self.wsExceptHandler(wsConn, err)
	})
}

// 下单
func (self *Trader) po() {
	self.ProcessLock.RLock()
	bid1 := self.Depth.Buy
	ask1 := self.Depth.Sell
	self.ProcessLock.RUnlock()

	log.Println(bid1, ask1)
}

// 撤单
func (self *Trader) co() {

}

// 平仓
func (self *Trader) closingPos() {

}

// 获取持仓
func (self *Trader) getPos() {

}

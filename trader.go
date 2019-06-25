package main

import (
	"math"
	"net/http"
	"os"
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
	isDebug           bool           // 调试模式
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
		BasePrice:    30,
		isRunning:    true,
		PositionInfo: &PositionInfo{},
		isClosingPos: false,
		isDebug:      isDebug,
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
	go self.po()
	go self.getPos()
	go self.closingPos()
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
					if time.Now().Sub(self.Depth.UpdatedAt) >= time.Minute*10 {
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
					if t.Sub(self.Depth.UpdatedAt) > time.Minute*5 {
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
	// 等待 depth 完成初始化
	self.Output.Log("waiting 30s, po init ...")
	time.Sleep(time.Second * 30)

	// 定时下单/撤单
	var chanTick <-chan time.Time
	if self.isDebug {
		chanTick = time.Tick(time.Minute * 5)
	} else {
		chanTick = time.Tick(time.Hour)
	}

	for {
		// 优先撤单
		self.co()

		// 执行下单
		self.ProcessLock.RLock()
		bid1 := self.Depth.Buy
		ask1 := self.Depth.Sell
		isRunning := self.isRunning
		self.ProcessLock.RUnlock()

		if !isRunning {
			self.Output.Warn("err: 处于不可运行状态, 等待一分钟后重新尝试 ...")
			time.Sleep(time.Minute)
			continue
		}

		var bidList, askList [10]float64
		for i := 0; i < 10; i++ {
			diff := self.BasePrice * float64(i+1)
			bidList[i] = bid1 - diff
			askList[i] = ask1 + diff
		}

		for _, bidPrice := range bidList {
			for {
				order, err := self.Exchange.PlaceAnOrder("BUY", self.BaseAmount, bidPrice)
				if err != nil {
					self.Output.Errorf("po buy err: %+v", err)
					time.Sleep(time.Second)
					continue
				}
				self.Output.Infof("po buy %+v", order)
				break
			}
		}
		for _, askPrice := range askList {
			for {
				order, err := self.Exchange.PlaceAnOrder("SELL", self.BaseAmount, askPrice)
				if err != nil {
					self.Output.Errorf("po buy err: %+v", err)
					time.Sleep(time.Second)
					continue
				}
				self.Output.Infof("po sell %+v", order)
				break
			}
		}

		<-chanTick
	}
}

// 撤单
func (self *Trader) co() {
	for {
		orders, err := self.Exchange.UnfinishOrders()
		if err != nil {
			self.Output.Errorf("unfinish err: %+v", err)
			time.Sleep(time.Second)
			continue
		}

		self.Output.Warn("co length", len(orders))
		for _, order := range orders {
			for {
				_, err := self.Exchange.CancelOrder(order.OrderID2)
				if err != nil {
					self.Output.Warnf("co err: %+v", err)
					time.Sleep(time.Second)
					continue
				}
				break
			}
		}

		break
	}
}

// 平仓
func (self *Trader) closingPos() {
	chanTick := time.Tick(time.Minute)
	for {
		<-chanTick
		// 已处于平仓状态
		self.ProcessLock.Lock()
		if self.isClosingPos {
			self.Output.Warn("warn, lock is locked ...")
			self.ProcessLock.Unlock()
			continue
		}
		self.isClosingPos = true
		// 200个点, 直接平掉
		middlePrice := math.Ceil((self.Depth.Buy + self.Depth.Sell) / 2)
		if self.PositionInfo.AvgEntryPrice != 0 && math.Abs(self.PositionInfo.AvgEntryPrice-middlePrice) > 300 {
			if self.PositionInfo.AvgEntryQty > 0 {
				// 持有多头头寸
				order, err := self.Exchange.PlaceAnOrder("SELL", self.PositionInfo.AvgEntryQty, middlePrice-self.BasePrice)
				self.Output.Infof("closing sell pos: %+v %+v", order, err)
			} else {
				// 持有空头头寸
				order, err := self.Exchange.PlaceAnOrder("BUY", math.Abs(self.PositionInfo.AvgEntryQty), middlePrice+self.BasePrice)
				self.Output.Infof("closing buy pos: %+v %+v", order, err)
			}
		}
		// 单子下下去了, 理论上会立刻成交, 但是如果发生意外, 两分钟已经检查, 发起撤单了, 防止单边平仓单挂多了
		// 三分钟后解锁
		time.AfterFunc(time.Minute*3, func() {
			self.ProcessLock.Lock()
			self.isClosingPos = false
			self.Output.Warn("warn, lock is NO ...")
			self.ProcessLock.Unlock()
		})

		self.ProcessLock.Unlock()
	}
}

// 获取持仓
func (self *Trader) getPos() {
	chanTick := time.Tick(time.Minute)
	for {
		<-chanTick
		for {
			data, err := self.Contract.GetPosition(goex.NewCurrencyPair(goex.NewCurrency(self.Currency[0], ""), goex.NewCurrency(self.Currency[1], "")))
			if err != nil {
				self.Output.Warn("get position failed", err)
				time.Sleep(time.Second)
				continue
			}

			tmpPair := data.(map[string]interface{})
			var openingPrice, currentQty, closingPrice float64
			if tmpPair["avgEntryPrice"] != nil {
				openingPrice = tmpPair["avgEntryPrice"].(float64)
			}
			if tmpPair["currentQty"] != nil {
				currentQty = tmpPair["currentQty"].(float64)
			}
			if tmpPair["marginCallPrice"] != nil {
				closingPrice = tmpPair["marginCallPrice"].(float64)
			}

			self.ProcessLock.Lock()
			self.PositionInfo = &PositionInfo{
				openingPrice,
				currentQty,
				closingPrice,
			}
			self.Output.Infof("pos info %+v", self.PositionInfo)

			if math.Abs(self.PositionInfo.AvgEntryQty) > self.MaxPos {
				self.Output.Error("持仓大于警戒线, 退出程序！！！")
				os.Exit(0)
			}

			self.ProcessLock.Unlock()

			break
		}
	}
}

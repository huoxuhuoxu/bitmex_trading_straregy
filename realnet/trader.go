package main

import (
	"math"
	"net/http"
	"sync"
	"time"

	goex "github.com/huoxuhuoxu/GoEx"
	"github.com/huoxuhuoxu/GoEx/bitmex"
	"github.com/huoxuhuoxu/UseGoexPackaging/conn"
)

type Trader struct {
	*MainControl                              // 主控
	ApiKey, SecretKey string                  // key
	Depth             *Depth                  // bitmex depth
	OrderBook         *OrderBook              // bitmex orderbook
	ProcessLock       *sync.RWMutex           // 执行流程锁
	AlertPos          float64                 // 持仓禁戒线(调整挂单比例)
	MaxPos            float64                 // 最大持仓
	MinDiffPrice      float64                 // 基于市场价的最小偏移量
	MaxDiffPrice      float64                 // 基于市场的最大偏移量
	TimeStep          time.Duration           // 撤单, 下单 执行检查间隔
	CancelOrderStep   time.Duration           // 间隔一段时间后撤单
	Exchange          *conn.Conn              // 交易所 API 对象
	Contract          *bitmex.Bitmex          // 交易所合约 API 对象
	Currency          [2]string               // 交易对
	BaseAmount        float64                 // 下单基础量
	*PositionInfo                             // 账号运行时
	chanOrders        chan *ActionOrder       // 订单处理管道
	poOrders          map[string]*ActionOrder // 下成功的订单
}

func NewTrader(apiKey, secretKey string, mc *MainControl, isDebug bool) *Trader {
	self := &Trader{
		MainControl:     mc,
		ApiKey:          apiKey,
		SecretKey:       secretKey,
		Depth:           &Depth{},
		OrderBook:       &OrderBook{},
		ProcessLock:     &sync.RWMutex{},
		AlertPos:        1000,
		MaxPos:          5000,
		MinDiffPrice:    3.5,
		MaxDiffPrice:    18,
		TimeStep:        time.Second * 45,
		CancelOrderStep: time.Second * 225,
		Exchange:        nil,
		Contract:        nil,
		Currency:        [2]string{"XBT", "USD"},
		BaseAmount:      50,
		PositionInfo:    &PositionInfo{},
		chanOrders:      make(chan *ActionOrder, 1),
		poOrders:        make(map[string]*ActionOrder, 0),
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
	self.wsReceiveMessage()
	go self.handerList()
	go self.getWallet()
	go self.getPosition()
	go self.readyPlaceOrders()
}

// ws异常处理
func (self *Trader) wsExceptHandler(wsConn *conn.WsConn, isRunning *bool, err error) {
	self.Output.Error("ws 连接发生异常", err)
	for {
		err := wsConn.ReConnectByArtificial()
		if err != nil {
			time.Sleep(time.Second)
			continue
		}
		self.ProcessLock.Lock()
		*isRunning = true
		self.ProcessLock.Unlock()
		break
	}
}

// 接收ws推送的orderbook
func (self *Trader) wsReceiveMessage() {
	wsConn, err := conn.NewWsConn(
		goex.BITMEX,
		self.ApiKey,
		self.SecretKey,
		conn.RT_EXCHANGE_WS,
		nil,
	)
	if err != nil {
		self.Output.Fatal("new ws conn", err)
	}
	wsConn.SetSubscribe([]string{"orderBookL2_25:XBTUSD"})

	err = wsConn.Connect()
	if err != nil {
		self.Output.Fatal("ws error", err)
	}

	var isRunning bool = true
	wsConn.ReceiveMessage(func(data interface{}) {
		switch data.(type) {
		case goex.DepthPair:
			depthPair := data.(goex.DepthPair)
			dc, err := wsConn.CompleteDepth(depthPair.Symbol)

			self.ProcessLock.Lock()
			defer self.ProcessLock.Unlock()
			if !isRunning {
				self.Output.Warn("ws conn ReConnectByArtificial ...")
				return
			}
			if err != nil {
				self.Depth = &Depth{}
				isRunning = false
				self.wsExceptHandler(wsConn, &isRunning, err)
				return
			}

			// depth
			if depthPair.Buy != self.Depth.Buy || depthPair.Sell != self.Depth.Sell {
				self.Depth.Buy = depthPair.Buy
				self.Depth.Sell = depthPair.Sell
				self.Output.Logf("real depth %+v", self.Depth)
			}

			// orderbook - 10 gp
			tmpOrderbook := self.OrderBook
			var asks, bids []GearPosition
			for i, ask := range dc.AskList {
				if i >= 10 {
					break
				}
				asks = append(asks, GearPosition{ask.Price, ask.Amount})
			}
			tmpOrderbook.Asks = asks
			for i, bid := range dc.BidList {
				if i >= 10 {
					break
				}
				bids = append(bids, GearPosition{bid.Price, bid.Amount})
			}
			tmpOrderbook.Bids = bids
		}
	}, func(err error) {
		self.wsExceptHandler(wsConn, &isRunning, err)
	})
}

// 处理下单/撤单请求, 列队化
func (self *Trader) handerList() {
	var (
		list              = make([]*ActionOrder, 0)
		singleRunningLock = &sync.RWMutex{}
	)

	go func() {
		for {
			time.Sleep(time.Millisecond * 500)
			select {
			case <-self.Ctx.Done():
				self.Output.Log("chan action order, closed")
				return
			default:
				var tmpOrder *ActionOrder
				singleRunningLock.Lock()
				if len(list) > 0 {
					tmpOrder = list[0]
					list = list[1:]
					self.Output.Log("action orders waiting length", len(list))
					self.Output.Logf("current order %+v", tmpOrder)
				}
				singleRunningLock.Unlock()

				if tmpOrder == nil {
					continue
				}

				switch tmpOrder.Action {
				case ACTION_PO:
					for {
						order, err := self.Exchange.PlaceAnOrder(tmpOrder.Side.String(), tmpOrder.Amount, tmpOrder.Price)
						if err != nil {
							self.Output.Errorf("place order %s failed, %s", tmpOrder.Side.String(), err)
							time.Sleep(time.Millisecond * 500)
							continue
						}

						tmpOrder.Time = time.Now()
						tmpOrder.ID = order.OrderID2
						self.ProcessLock.Lock()
						self.poOrders[tmpOrder.ID] = tmpOrder
						self.ProcessLock.Unlock()
						break
					}

				case ACTION_CO:
					order, err := self.Exchange.CancelOrder(tmpOrder.ID)
					if err != nil {
						self.Output.Warnf("cancel order %s failed, %s", tmpOrder.Side.String(), err)
						continue
					}
					self.Output.Infof("cancel order %s succ, %+v", tmpOrder.Side.String(), order)

				case ACTION_POS:
					data, err := self.Contract.GetPosition(goex.NewCurrencyPair(goex.NewCurrency(self.Currency[0], ""), goex.NewCurrency(self.Currency[1], "")))
					if err != nil {
						self.Output.Warn("get position failed", err)
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
					self.Output.Logf("pos info %+v", self.PositionInfo)
					self.ProcessLock.Unlock()

				case ACTION_WALLET:
					wallet, err := self.Exchange.AccountInfo()
					if err != nil {
						self.Output.Warn("get wallet failed", err)
						continue
					}

					for pairName, subAccount := range wallet.SubAccounts {
						self.Output.Info("参考:", pairName, subAccount.Amount/100000000)
					}
				}
			}
		}
	}()

	go func() {
		for {
			if tmpOrder, ok := <-self.chanOrders; ok {
				singleRunningLock.Lock()
				list = append(list, tmpOrder)
				singleRunningLock.Unlock()
			}
		}
	}()
}

// 生成下单参数
func (self *Trader) readyPlaceOrders() {
	chanTick := time.Tick(self.TimeStep)
	for {
		<-chanTick
		if ok := func() bool {
			self.ProcessLock.Lock()
			defer self.ProcessLock.Unlock()
			ordersLen := len(self.poOrders)
			if ordersLen > 0 {
				self.Output.Log("po orders length", ordersLen)
				n := time.Now()
				count := 0
				for ID, poOrder := range self.poOrders {
					if n.Sub(poOrder.Time) > self.CancelOrderStep {
						delete(self.poOrders, ID)
						poOrder.Action = ACTION_CO
						self.Output.Infof("ready cancel order %+v", poOrder)
						self.chanOrders <- poOrder
					}
					count++
				}
			}
			if self.Depth.Sell == 0 || self.Depth.Buy == 0 {
				return false
			}
			return true
		}(); !ok {
			continue
		}

		bidParams, askParams := self.calculateReasonablePrice()
		buyOrder := &ActionOrder{
			Action: ACTION_PO,
			Amount: bidParams.Amount,
			Price:  bidParams.Price,
			Side:   TraderBuy,
		}
		sellOrder := &ActionOrder{
			Action: ACTION_PO,
			Amount: askParams.Amount,
			Price:  askParams.Price,
			Side:   TraderSell,
		}
		// log.Printf("%+v %+v", buyOrder, sellOrder)

		self.chanOrders <- buyOrder
		self.chanOrders <- sellOrder
	}
}

// 获取当前持仓情况
func (self *Trader) getPosition() {
	chanTick := time.Tick(self.TimeStep)
	for {
		select {
		case <-self.Ctx.Done():
			self.Output.Log("chan get pos, closed")
			return
		default:
			posAction := &ActionOrder{
				Action: ACTION_POS,
			}
			self.chanOrders <- posAction
		}
		<-chanTick
	}
}

// 获取账户信息
func (self *Trader) getWallet() {
	chanTick := time.Tick(time.Minute)
	for {
		select {
		case <-self.Ctx.Done():
			self.Output.Log("chan get wallet, closed")
			return
		default:
			wallet := &ActionOrder{
				Action: ACTION_WALLET,
			}
			self.chanOrders <- wallet
		}
		<-chanTick
	}
}

// 策略 - 计算根据持仓计算合理价格
func (self *Trader) calculateReasonablePrice() (*PlaceOrderParams, *PlaceOrderParams) {
	var (
		bidPrice, askPrice, bidAmount, askAmount, middlePrice, reasonablePrice float64
	)
	self.ProcessLock.RLock()
	middlePrice = (self.Depth.Buy + self.Depth.Sell) / 2

	if len(self.OrderBook.Asks) >= 10 && len(self.OrderBook.Bids) >= 10 {
		var t, t2 float64
		for _, ask := range self.OrderBook.Asks {
			t += ask.Amount * ask.Price
			t2 += ask.Amount
		}
		for _, bid := range self.OrderBook.Bids {
			t += bid.Amount * bid.Price
			t2 += bid.Amount
		}
		reasonablePrice = t / t2
		reasonablePrice = math.Ceil(middlePrice + (middlePrice - reasonablePrice))
		self.Output.Info("合理价格", reasonablePrice)
	} else {
		reasonablePrice = math.Floor(middlePrice + 0.5)
		self.Output.Info("中间价", reasonablePrice)
	}

	bidPrice = reasonablePrice - self.MinDiffPrice
	askPrice = reasonablePrice + self.MinDiffPrice
	if self.PositionInfo != nil {
		var diffPrice float64
		qty := math.Abs(self.PositionInfo.AvgEntryQty)
		// 总量大于允许持仓总量
		if qty > self.MaxPos {
			diffPrice = self.MaxDiffPrice
		} else {
			diffQty := qty - self.AlertPos
			// 超过警戒线了
			if diffQty > 0 {
				diffPrice = qty / self.MaxPos * self.MaxDiffPrice
			}
		}

		if diffPrice != 0 {
			if self.PositionInfo.AvgEntryQty > 0 {
				bidPrice = math.Floor(reasonablePrice - diffPrice)
			} else {
				askPrice = math.Ceil(reasonablePrice + diffPrice)
			}
		}
	}
	self.ProcessLock.RUnlock()

	bidAmount = self.BaseAmount
	askAmount = self.BaseAmount

	return &PlaceOrderParams{bidPrice, bidAmount}, &PlaceOrderParams{askPrice, askAmount}
}

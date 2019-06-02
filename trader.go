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
	isRunning         bool                    // 发生意外
	*PositionInfo                             // 账号运行时
	chanOrders        chan *ActionOrder       // 订单处理管道
	poOrders          map[string]*ActionOrder // 下成功的订单
	unfinishOrders    []goex.Order            // 待成交的列队
	isClosingPos      bool                    // 是否处于平仓中
	*Volatility
}

func NewTrader(apiKey, secretKey string, mc *MainControl, isDebug bool) *Trader {
	self := &Trader{
		MainControl:     mc,
		ApiKey:          apiKey,
		SecretKey:       secretKey,
		Depth:           &Depth{},
		ProcessLock:     &sync.RWMutex{},
		AlertPos:        1500,
		MaxPos:          4000,
		MinDiffPrice:    10,
		MaxDiffPrice:    30,
		TimeStep:        time.Second * 60,
		CancelOrderStep: time.Second * 420, // 7分钟
		Exchange:        nil,
		Contract:        nil,
		Currency:        [2]string{"XBT", "USD"},
		BaseAmount:      50,
		isRunning:       true,
		PositionInfo:    &PositionInfo{},
		chanOrders:      make(chan *ActionOrder, 1),
		poOrders:        make(map[string]*ActionOrder, 0),
		Volatility:      NewVolatility(20),
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
	go self.handerList()
	go self.getPosition()
	go self.readyPlaceOrders()
	go self.intervalClosingPos()
	go self.getUnfinishOrders()
	go self.priceIsError()
	go self.getWallet()
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

// 处理下单/撤单请求, 列队化
func (self *Trader) handerList() {
	var (
		list              = make([]*ActionOrder, 0)
		singleRunningLock = &sync.RWMutex{}
	)

	go func() {
		for {
			time.Sleep(time.Millisecond * 100)
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
					_, err := self.Exchange.CancelOrder(tmpOrder.ID)
					if err != nil {
						self.Output.Warnf("cancel order %s failed, %s", tmpOrder.Side.String(), err)
						continue
					}

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
					self.Output.Infof("pos info %+v", self.PositionInfo)
					self.ProcessLock.Unlock()

				case ACTION_CLOSING:
					for {
						order, err := self.Exchange.PlaceAnOrder(tmpOrder.Side.String(), tmpOrder.Amount, tmpOrder.Price)
						if err != nil {
							self.Output.Errorf("closing pos, place order %s failed, %s", tmpOrder.Side.String(), err)
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

				case ACTION_UNFINISH:
					orders, err := self.Exchange.UnfinishOrders()
					if err != nil {
						self.Output.Error("get unfinish orders failed", err)
						continue
					}
					self.unfinishOrders = orders

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
						self.chanOrders <- poOrder
					}
					count++
				}
			}
			if !self.isRunning {
				return false
			}
			if self.Depth == nil || self.Depth.Sell == 0 || self.Depth.Buy == 0 {
				return false
			}
			return true
		}(); !ok {
			continue
		}

		bidParams, askParams, err := self.calculateReasonablePrice()
		if err != nil {
			self.Output.Warn(err)
			switch err.(type) {
			case *VolatilityError:
				self.ProcessLock.RLock()
				avgEntryPrice := self.PositionInfo.AvgEntryPrice
				marketPrice := math.Ceil((self.Depth.Sell + self.Depth.Buy) / 2)
				self.ProcessLock.RUnlock()

				if avgEntryPrice != 0 && math.Abs(marketPrice-avgEntryPrice) > 30 {
					self.closingPos("波动率过高, 市场价 高于/低于 持有价格的30个点")
				}
			}
			continue
		}

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

		self.chanOrders <- buyOrder
		self.chanOrders <- sellOrder
	}
}

// 获取当前持仓情况
func (self *Trader) getPosition() {
	chanTick := time.Tick(time.Second * 15)
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
	chanTick := time.Tick(time.Minute * 5)
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

// 获取未完成订单列表
func (self *Trader) getUnfinishOrders() {
	chanTick := time.Tick(time.Second * 30)
	for {
		select {
		case <-self.Ctx.Done():
			self.Output.Log("chan unfinish orders, closed")
			return
		default:
			unfinish := &ActionOrder{
				Action: ACTION_UNFINISH,
			}
			self.chanOrders <- unfinish
		}
		<-chanTick
	}
}

// 平仓
func (self *Trader) intervalClosingPos() {
	// 长时间定时平仓: 防止爆仓
	go func() {
		self.Output.Log("closing pos 1 running ...")
		chanTick := time.Tick(time.Hour * 2)
		for {
			<-chanTick
			select {
			case <-self.Ctx.Done():
				self.Output.Log("chan closing pos 1, closed")
				return
			default:
				self.ProcessLock.RLock()
				avgEntryQty := self.PositionInfo.AvgEntryQty
				self.ProcessLock.RUnlock()

				if math.Abs(avgEntryQty) > self.AlertPos {
					self.closingPos("定时平仓")
				}
			}
		}
	}()

	// 超过30个点, 平仓
	go func() {
		self.Output.Log("closing pos 3 running ...")
		chanTick := time.Tick(time.Minute * 2)
		for {
			<-chanTick
			select {
			case <-self.Ctx.Done():
				self.Output.Log("chan closing pos 3, closed")
				return
			default:
				self.ProcessLock.RLock()
				avgEntryPrice := self.PositionInfo.AvgEntryPrice
				marketPrice := math.Ceil((self.Depth.Sell + self.Depth.Buy) / 2)
				self.ProcessLock.RUnlock()

				if avgEntryPrice != 0 && math.Abs(marketPrice-avgEntryPrice) > 30 {
					self.closingPos("市场价高于/低于持有价格的30个点")
				}
			}
		}
	}()
}

// 策略 - 计算根据持仓计算合理价格
func (self *Trader) calculateReasonablePrice() (*PlaceOrderParams, *PlaceOrderParams, error) {
	self.ProcessLock.RLock()
	defer self.ProcessLock.RUnlock()

	// 市场中间价
	var reasonablePrice = math.Floor((self.Depth.Buy+self.Depth.Sell)/2 + 0.5)
	self.Output.Info("中间价", reasonablePrice)

	// 波动率
	if pastVol, ok := self.IsHighVolatility(reasonablePrice); ok {
		ve := &VolatilityError{pastVol, reasonablePrice}
		return nil, nil, ve
	} else {
		self.Output.Logf("volatility: %.1f", pastVol-reasonablePrice)
	}

	/*
		@README
			仓位平衡

		1. 计算挂单列表上两边的待成交量
		2. 与当前持仓计算差值, 得到某方向需要的增加量
	*/
	var bidAmount = self.BaseAmount
	var askAmount = self.BaseAmount
	var tmpAskAmount, tmpBidAmount, tmpDiffAmount float64
	for _, order := range self.unfinishOrders {
		switch order.Side {
		case goex.SELL:
			tmpAskAmount -= order.Amount
		case goex.BUY:
			tmpBidAmount += order.Amount
		}
	}
	// 待成交列表, 正: 买量多, 负: 卖量多
	if self.AvgEntryQty > 0 {
		tmpBidAmount += self.AvgEntryQty
	}
	if self.AvgEntryQty < 0 {
		tmpAskAmount += self.AvgEntryQty
	}

	tmpDiffAmount = tmpBidAmount + tmpAskAmount
	self.Output.Warn("diff 持仓+待成交, 偏差", tmpDiffAmount)
	if tmpDiffAmount > 0 {
		askAmount += tmpDiffAmount
	}
	if tmpDiffAmount < 0 {
		bidAmount += math.Abs(tmpDiffAmount)
	}

	// 补丁!!!, 在没撤单前, 成交会算进偏差内, 需要处理变更
	if bidAmount >= 150 {
		bidAmount = 150
	}
	if askAmount >= 150 {
		askAmount = 150
	}

	/*
		@README
			计算两边的合理价格

		持仓为空时, 持仓价格为0, 以市场价格为准
		持仓不为空时, 下单价格
			1. 持仓 能够获利的情况下, 按持仓价为准
			2. 持仓 不能够获利情况下, 按市场价为准
	*/
	var bidPrice = self.Depth.Buy - self.MinDiffPrice
	var askPrice = self.Depth.Sell + self.MinDiffPrice
	if self.AvgEntryPrice > 0 {
		// 多头头寸
		if self.AvgEntryQty > 0 {
			if self.AvgEntryPrice <= bidPrice {
				askPrice = math.Floor(self.AvgEntryPrice + self.MinDiffPrice)
			}
		}
		// 空头头寸
		if self.AvgEntryQty < 0 {
			if self.AvgEntryPrice >= askPrice {
				bidPrice = math.Ceil(self.AvgEntryPrice - self.MinDiffPrice)
			}
		}

		return &PlaceOrderParams{bidPrice, bidAmount}, &PlaceOrderParams{askPrice, askAmount}, nil
	}

	// 按照量偏移, 计算价格偏移
	if self.PositionInfo != nil {
		qty := math.Abs(self.PositionInfo.AvgEntryQty)
		if qty > self.AlertPos {
			diffPrice := qty / self.MaxPos * self.MaxDiffPrice
			if self.PositionInfo.AvgEntryQty > 0 {
				bidPrice = math.Floor(reasonablePrice - diffPrice)
			} else {
				askPrice = math.Ceil(reasonablePrice + diffPrice)
			}
		}
	}

	return &PlaceOrderParams{bidPrice, bidAmount}, &PlaceOrderParams{askPrice, askAmount}, nil
}

// 平仓
func (self *Trader) closingPos(closePosName string) {
	self.ProcessLock.Lock()
	avgEntryQty := self.PositionInfo.AvgEntryQty
	marketPrice := math.Ceil((self.Depth.Sell + self.Depth.Buy) / 2)
	if self.isClosingPos {
		self.Output.Warn("处于平仓中, give jumper")
		self.ProcessLock.Unlock()
		return
	}
	self.isClosingPos = true
	self.ProcessLock.Unlock()

	closingPos := &ActionOrder{
		Action: ACTION_CLOSING,
		Amount: math.Abs(avgEntryQty),
	}

	if avgEntryQty > 0 {
		closingPos.Side = TraderSell
		closingPos.Price = math.Ceil(marketPrice - 5)
	}
	if avgEntryQty < 0 {
		closingPos.Side = TraderBuy
		closingPos.Price = math.Floor(marketPrice + 5)
	}

	self.chanOrders <- closingPos
	self.Output.Warn("closing pos, 开始平仓", closePosName, closingPos.Side, closingPos.Price, math.Abs(avgEntryQty))

	// 30s 结束平仓状态
	go func() {
		time.AfterFunc(time.Second*30, func() {
			self.ProcessLock.Lock()
			self.isClosingPos = false
			self.ProcessLock.Unlock()
		})
	}()
}

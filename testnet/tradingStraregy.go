package main

import (
	"log"
	"math"
	"net/http"
	"sync"
	"time"

	goex "github.com/huoxuhuoxu/GoEx"
	"github.com/huoxuhuoxu/GoEx/bitmex"
	"github.com/huoxuhuoxu/UseGoexPackaging/conn"
)

type Depth struct {
	Buy  float64
	Sell float64
}

type PendingOrder struct {
	createOrderTimestamp int64 // 下单的时间
}

type TradingStraregy struct {
	bmDepth               *Depth                   // bitmex 买1/卖1 信息
	bmRWMutex             *sync.RWMutex            // bitmex 相关读写锁
	bmCancelMutex         *sync.Mutex              // bitmex 撤单相关读写锁
	maxPos                int                      // 持仓警戒线
	poStep, coStep        time.Duration            // 下单, 撤单间隔
	coDiffStep            int64                    // 撤离下的单子与当前的间隔
	closingStep           float64                  // 平仓间隔
	pos                   float64                  // 当前仓位
	posPrice              float64                  // 持仓价格
	closingPrice          float64                  // 平仓价
	exchangeAPI           *conn.Conn               // 交易所 - API对象
	bitmexExchangeAPI     *bitmex.Bitmex           // bitmex 合约API 对象
	apiKey, secretKey     string                   // 交易所 - Key
	curA, curB            string                   // 交易对
	buyAmount, sellAmount float64                  // 下单量
	baseAmount            float64                  // 下单基数
	isRunning             bool                     // 可运行的
	isInit                bool                     // 完成初始化
	isClosing             bool                     // 正在进行平仓
	pendingOrders         map[string]*PendingOrder // 待成交订单ID
	mmTimestamp           int64                    // 根据市场变动进行反应, 间隔30s
}

func NewTS(apiKey, secretKey, curA, curB string) *TradingStraregy {
	self := &TradingStraregy{}
	self.bmDepth = &Depth{}
	self.bmRWMutex = &sync.RWMutex{}
	self.bmCancelMutex = &sync.Mutex{}
	self.isRunning = false
	self.isInit = false
	self.isClosing = false

	self.maxPos = 20000
	self.poStep = time.Second * 60
	self.coStep = time.Minute * 30
	self.coDiffStep = 15 * 60
	self.closingStep = 30.0

	self.baseAmount = 100
	self.buyAmount = self.baseAmount * 1
	self.sellAmount = self.baseAmount * 1

	self.curA = curA
	self.curB = curB
	self.apiKey = apiKey
	self.secretKey = secretKey

	self.exchangeAPI = conn.NewConn(
		goex.BITMEX,
		apiKey,
		secretKey,
		map[string]string{
			"curA": curA,
			"curB": curB,
		})
	self.bitmexExchangeAPI = bitmex.New(&http.Client{}, apiKey, secretKey)

	self.pendingOrders = make(map[string]*PendingOrder)

	return self
}

// 启动
func (self *TradingStraregy) Running() {
	go self.GetPosition()
	go self.ReadyPlaceOrders()
	go self.CancelOrders()
}

// 设置是否可运行
func (self *TradingStraregy) SetRunning(isRunning bool) {
	self.isRunning = isRunning
}

// 获取当前账户运行时仓位信息
func (self *TradingStraregy) GetPosition() {
	tickChan := time.Tick(time.Second * 30)
	for {
		go func() {
			count := 0
			for {
				count++
				if count >= 3 {
					return
				}
				data, err := self.bitmexExchangeAPI.GetPosition(goex.NewCurrencyPair(goex.NewCurrency(self.curA, ""), goex.NewCurrency(self.curB, "")))
				if err != nil {
					log.Println("error", err)
					time.Sleep(time.Second * 10)
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

				self.SetPos(openingPrice, currentQty, closingPrice)
				return
			}
		}()
		<-tickChan
	}
}

// 设置仓位信息
func (self *TradingStraregy) SetPos(op, cq, cp float64) {
	log.Println("仓位信息", op, cq, cp)

	// 当前持仓与合理持仓差值, 是否持仓越界
	diffPos := self.maxPos - int(math.Abs(cq))
	ba := 1.0
	sa := 1.0

	// 合理区间 20000 ~ -20000 持仓
	if diffPos >= 0 {
		log.Println("仓位在合理区间浮动")
	}

	// 多头
	if cq > 0 && diffPos < 0 {
		ba = 0.5
		log.Println("持仓为多方头寸, 调整多单下单量")
	}
	// 空头
	if cq < 0 && diffPos < 0 {
		sa = 0.5
		log.Println("持仓为空方头寸, 调整空单下单量")
	}

	self.bmRWMutex.Lock()
	self.pos = cq
	self.posPrice = op
	self.closingPrice = cp
	self.buyAmount = ba * self.baseAmount
	self.sellAmount = sa * self.baseAmount
	self.isInit = true
	self.bmRWMutex.Unlock()
}

// 设置盘口信息
func (self *TradingStraregy) SetDepth(buy, sell float64, dc goex.DepthComplete) {
	self.bmRWMutex.Lock()
	self.bmDepth.Buy = buy
	self.bmDepth.Sell = sell
	self.bmRWMutex.Unlock()
	// log.Println(buy, sell)

	go self.HanlderMarket(buy, sell, dc)
}

// 一切正常运作
func (self *TradingStraregy) Ready() bool {
	return self.isInit && self.isRunning
}

// 准备下下对单
func (self *TradingStraregy) ReadyPlaceOrders() {
	tickChan := time.Tick(self.poStep)
	for {
		<-tickChan
		if self.Ready() {
			self.bmRWMutex.RLock()
			middlePrice := math.Floor((self.bmDepth.Buy+self.bmDepth.Sell)/2 + 0.5)
			log.Println(self.bmDepth.Buy, self.bmDepth.Sell)
			self.bmRWMutex.RUnlock()

			go self.ClosingPos(middlePrice)
			self.PlaceOrders(middlePrice)

			log.Println("complete place order ticker !")
		}
	}
}

// 下对单
func (self *TradingStraregy) PlaceOrders(middlePrice float64) {
	var buyID, sellID string
	wg := sync.WaitGroup{}
	go func() {
		defer wg.Done()
		for {
			orderID := self.PlaceOrder("BUY", self.buyAmount, middlePrice-3)
			if orderID == "" {
				time.Sleep(time.Second * 1)
				continue
			}
			buyID = orderID
			return
		}
	}()
	go func() {
		defer wg.Done()
		time.Sleep(time.Second * 1)
		for {
			orderID := self.PlaceOrder("SELL", self.sellAmount, middlePrice+3)
			if orderID == "" {
				time.Sleep(time.Second * 1)
				continue
			}
			sellID = orderID
			return
		}
	}()
	wg.Add(2)
	wg.Wait()

	self.AddCancelOrders(sellID)
	self.AddCancelOrders(buyID)
}

// 下单
func (self *TradingStraregy) PlaceOrder(side string, amount, price float64) string {
	order, err := self.exchangeAPI.PlaceAnOrder(side, amount, price)
	if err != nil {
		log.Println(err)
		return ""
	}
	log.Println(order)
	return order.OrderID2
}

// 添加进撤单列表
func (self *TradingStraregy) AddCancelOrders(id string) {
	if id != "" {
		self.bmCancelMutex.Lock()
		tmp := &PendingOrder{time.Now().Unix()}
		self.pendingOrders[id] = tmp
		self.bmCancelMutex.Unlock()
	}
}

// 撤单
func (self *TradingStraregy) CancelOrders() {
	tickChan := time.Tick(self.coStep)
	for {
		<-tickChan
		orders, err := self.exchangeAPI.UnfinishOrders()
		log.Println("开始撤单 ...", err)
		if err == nil {
			// now := time.Now().Unix()
			self.bmCancelMutex.Lock()
			for _, order := range orders {
				time.Sleep(time.Second * 1)
				id := order.OrderID2
				if _, ok := self.pendingOrders[id]; ok {
					// diffDur := now - tmp.createOrderTimestamp
					// log.Println("撤单差值", diffDur, self.coDiffStep)
					// if diffDur >= self.coDiffStep {
					// 	b, err := self.exchangeAPI.CancelOrder(id)
					// 	log.Println("撤单", b, err)
					// 	delete(self.pendingOrders, id)
					// }

					b, err := self.exchangeAPI.CancelOrder(id)
					log.Println("撤单", b, err)
					delete(self.pendingOrders, id)
				} else {
					b, err := self.exchangeAPI.CancelOrder(id)
					log.Println("旧单撤单", b, err)
				}
			}
			log.Println("剩余没有撤离的订单数", len(self.pendingOrders))
			self.bmCancelMutex.Unlock()

			// 重启平仓判断
			self.isClosing = false
		}
	}
}

// 平仓
func (self *TradingStraregy) ClosingPos(middlePrice float64) {
	if self.isClosing {
		log.Println("aa 处于平仓中 ...")
		return
	}

	diffPrice := self.posPrice - middlePrice
	if math.Abs(diffPrice) >= self.closingStep {
		log.Println("bb 启动平仓逻辑", self.posPrice, middlePrice)

		var side string
		if self.pos > 0 {
			// 多方头寸
			if diffPrice <= -6 {
				log.Println("cc 平多方头寸", diffPrice)
				side = "SELL"
			}
		} else {
			// 空方头寸
			if diffPrice >= 6 {
				log.Println("dd 平空方头寸", diffPrice)
				side = "BUY"
			}
		}

		if side != "" {
			self.isClosing = true
			for {
				id := self.PlaceOrder(side, math.Abs(self.pos), middlePrice)
				if id == "" {
					continue
				}
				self.AddCancelOrders(id)
			}
		}
	}
}

// 对市场的应对机制
func (self *TradingStraregy) HanlderMarket(buy, sell float64, dc goex.DepthComplete) {
	if !self.Ready() {
		return
	}

	// 持仓 -30000 ~ 30000 浮动时, 可以根据市场进行应变
	if math.Abs(self.pos) > float64(self.maxPos) {
		log.Println("当前持仓过大, 暂时停止对市场的应对机制", self.pos)
		return
	}

	if len(dc.BidList) == 0 || len(dc.AskList) == 0 {
		log.Println("发生了大变动, 某边为空")
		return
	}

	now := time.Now().Unix()
	if now-self.mmTimestamp >= 10 {
		var totalBuy, totalSell, totalRatio, firstRatio, depth24Ratio float64

		for _, tmp := range dc.BidList {
			totalBuy += tmp.Amount
		}
		for _, tmp := range dc.AskList {
			totalSell += tmp.Amount
		}

		tmpBuyAm := dc.BidList[len(dc.BidList)-1].Amount
		tmpSellAm := dc.AskList[0].Amount

		totalRatio = totalBuy / totalSell
		firstRatio = tmpBuyAm / tmpSellAm
		depth24Ratio = (totalBuy - buy) / (totalSell - sell)

		// 多方强
		if totalRatio >= 1.5 && firstRatio >= 1.5 && depth24Ratio >= 1.5 {
			log.Println("趋势挂多单")
			// 消除对单的6个档位, 挂买2
			go self.PlaceOrders(buy + 3 - 0.5)
		}

		// 空方强
		if totalRatio <= 0.5 && firstRatio <= 0.5 && depth24Ratio <= 0.5 {
			log.Println("趋势挂空单")
			// 挂卖2
			go self.PlaceOrders(sell - 3 + 0.5)
		}

		log.Printf("总体多空趋势 %.4f \r\n", totalRatio)
		log.Printf("盘口1档多空趋势 %.4f \r\n", firstRatio)
		log.Printf("盘口2-25档多空趋势 %.4f \r\n", depth24Ratio)
		log.Println("\r\n")

		self.mmTimestamp = now
	}
}

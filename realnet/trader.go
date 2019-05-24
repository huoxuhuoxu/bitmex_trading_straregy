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

type Depth struct {
	Buy  float64
	Sell float64
}

// 订单行为
type ActionType int

func (self ActionType) String() string {
	return ActionName[self-1]
}

// 交易方向
type TraderSide int

func (self TraderSide) String() string {
	return TraderSideName[self-1]
}

// type values
const (
	ACTION_PO ActionType = iota + 1
	ACTION_CO
)
const (
	TraderBuy TraderSide = iota + 1
	TraderSell
)

// type to string
var (
	ActionName     = []string{"PLACE_ORDER", "CANCEL_ORDER"}
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

type Trader struct {
	*MainControl                              // 主控
	ApiKey, SecretKey string                  // key
	Depth             *Depth                  // bitmex depth
	ProcessLook       *sync.RWMutex           // 执行流程锁
	MaxPos            int                     // 持仓禁戒线(调整挂单比例, 准备渐进式平仓)
	TimeStep          time.Duration           // 撤单, 下单 执行检查间隔
	CancelOrderStep   time.Duration           // 间隔一段时间后撤单
	Exchange          *conn.Conn              // 交易所 API 对象
	Contract          *bitmex.Bitmex          // 交易所合约 API 对象
	Currency          [2]string               // 交易对
	BaseAmount        float64                 // 下单基础量
	chanOrders        chan *ActionOrder       // 订单处理管道
	poOrders          map[string]*ActionOrder // 下成功的订单
}

func NewTrader(apiKey, secretKey string, mc *MainControl, isDebug bool) *Trader {
	self := &Trader{
		MainControl:     mc,
		ApiKey:          apiKey,
		SecretKey:       secretKey,
		Depth:           &Depth{},
		ProcessLook:     &sync.RWMutex{},
		MaxPos:          1500,
		TimeStep:        time.Minute * 2,
		CancelOrderStep: time.Minute * 10,
		Exchange:        nil,
		Contract:        nil,
		Currency:        [2]string{"XBT", "USD"},
		BaseAmount:      50,
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
	self.WsReceiveMessage()
	go self.handerList()
	go self.ReadyPlaceOrders()
}

func (self *Trader) WsReceiveMessage() {
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

	wsConn.ReceiveMessage(func(data interface{}) {
		switch data.(type) {
		case goex.DepthPair:
			depthPair := data.(goex.DepthPair)
			self.ProcessLook.Lock()
			if depthPair.Buy == self.Depth.Buy && depthPair.Sell == self.Depth.Sell {
				self.ProcessLook.Unlock()
				return
			}
			self.Depth.Buy = depthPair.Buy
			self.Depth.Sell = depthPair.Sell
			self.Output.Logf("real depth %+v", self.Depth)
			self.ProcessLook.Unlock()
		}
	}, func(err error) {
		self.Output.Error("ws 连接发生异常", err)
		for {
			err := wsConn.ReConnectByArtificial()
			if err != nil {
				time.Sleep(time.Second)
				continue
			}
			break
		}
	})
}

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
						self.ProcessLook.Lock()
						self.poOrders[tmpOrder.ID] = tmpOrder
						self.ProcessLook.Unlock()
						break
					}

				case ACTION_CO:
					order, err := self.Exchange.CancelOrder(tmpOrder.ID)
					if err != nil {
						self.Output.Warnf("cancel order %s failed, %s", tmpOrder.Side.String(), err)
						continue
					}
					self.Output.Infof("cancel order %s succ, %+v", tmpOrder.Side.String(), order)
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

func (self *Trader) ReadyPlaceOrders() {
	chanTick := time.Tick(self.TimeStep)
	for {
		<-chanTick
		self.ProcessLook.Lock()
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
			self.ProcessLook.Unlock()
			continue
		}

		middlePrice := math.Floor((self.Depth.Buy+self.Depth.Sell)/2 + 0.5)
		self.ProcessLook.Unlock()

		buyOrder := &ActionOrder{
			Action: ACTION_PO,
			Amount: self.BaseAmount,
			Price:  middlePrice - 4,
			Side:   TraderBuy,
		}

		sellOrder := &ActionOrder{
			Action: ACTION_PO,
			Amount: self.BaseAmount,
			Price:  middlePrice + 4,
			Side:   TraderSell,
		}

		// log.Printf("%+v %+v", buyOrder, sellOrder)

		self.chanOrders <- buyOrder
		self.chanOrders <- sellOrder
	}
}

package main

import (
	"errors"
	"net/http"
	"sync"
	"time"

	goex "github.com/huoxuhuoxu/GoEx"
	"github.com/huoxuhuoxu/GoEx/bitmex"
	"github.com/huoxuhuoxu/UseGoexPackaging/conn"
)

type TransSystem struct {
	*MainControl
	currency  [2]string
	depth     [2]float64
	updatedAt time.Time

	analysis             *Analysis
	currentAnalysisTrend AnalysisTrend

	processLock *sync.Mutex

	bitmexWs   *conn.WsConn
	bitmexApi  *bitmex.Bitmex
	bitmexApi2 *conn.Conn

	baseAmount float64
}

func NewTransSystem(mc *MainControl, isUat bool) (*TransSystem, error) {
	keys, err := readKeys(isUat)
	if err != nil {
		return nil, err
	}
	API_KEY := keys[0]
	SECRET_KEY := keys[1]

	self := &TransSystem{
		MainControl: mc,
		currency:    [2]string{"XBT", "USD"},
		depth:       [2]float64{},
		processLock: &sync.Mutex{},
		baseAmount:  500,
	}

	bitmexWs, err := conn.NewWsConn(
		goex.BITMEX,
		API_KEY,
		SECRET_KEY,
		conn.RT_EXCHANGE_WS,
		nil,
	)
	if err != nil {
		return nil, err
	}
	bitmexApi2 := conn.NewConn(
		goex.BITMEX,
		API_KEY,
		SECRET_KEY,
		map[string]string{
			"curA": self.currency[0],
			"curB": self.currency[1],
		},
	)

	self.bitmexWs = bitmexWs
	self.bitmexApi = bitmex.New(&http.Client{}, API_KEY, SECRET_KEY)
	self.bitmexApi2 = bitmexApi2

	analysis, err := NewAnalysis(mc)
	if err != nil {
		return nil, err
	}
	self.analysis = analysis

	return self, nil
}

func (self *TransSystem) Running() {
	self.Output.Log("analysis ready")
	err := self.analysis.Running()
	if err != nil {
		self.Output.Error(err)
		self.Sr.RestartProcess()
	}
	self.Output.Log("analysis succ")

	self.wsReceiveMessage()

	go func() {
		c := time.Tick(60 * time.Second)
	loop:
		for {
			<-c
			self.Output.Log("loop, GetAnalysisRet")
			tmpCurrentAnalysisTrend := self.analysis.GetAnalysisRet()
			switch tmpCurrentAnalysisTrend {
			case RISE:
				self.Output.Info("上升")
			case FALL:
				self.Output.Info("下跌")
			case CONCUSSION:
				self.Output.Info("震荡")
				fallthrough
			case UNKNOW:
				fallthrough
			default:
				self.Output.Info("analysis trend", tmpCurrentAnalysisTrend)
				continue loop
			}

			self.processLock.Lock()
			if self.depth[0] != 0 && self.depth[1] != 0 {
				if time.Now().Sub(self.updatedAt) > 2*time.Minute {
					self.Output.Error("ws except, depth updated then 2 minutes")
					self.Sr.RestartProcess()
					return
				}
			}
			if self.depth[0] == 0 && self.depth[1] == 0 {
				self.processLock.Unlock()
				continue loop
			}
			self.processLock.Unlock()

			go self.windControl(tmpCurrentAnalysisTrend)
		}
	}()
}

func (self *TransSystem) windControl(at AnalysisTrend) {
	pi, err := self.getPos()
	if err != nil {
		self.Output.Warn(err)
		return
	}

	self.processLock.Lock()
	bid1 := self.depth[0]
	ask1 := self.depth[1]
	self.processLock.Unlock()

	if pi.AvgEntryQty != 0 {
		if pi.AvgEntryQty > 0 {
			diff := bid1 - pi.AvgEntryPrice
			if diff >= 10 && at == FALL {
				o, e := self.bitmexApi2.PlaceAnOrder("SELL", self.baseAmount, bid1)
				self.Output.Logf("po %+v, %+v", o, e)
			} else {
				if diff <= -50 && at == FALL {
					o, e := self.bitmexApi2.PlaceAnOrder("SELL", self.baseAmount, bid1)
					self.Output.Warnf("closing po %+v, %+v", o, e)
				}
			}
			self.Output.Log("diff", diff)
		} else {
			diff := pi.AvgEntryPrice - ask1
			if diff >= 10 && at == RISE {
				o, e := self.bitmexApi2.PlaceAnOrder("BUY", self.baseAmount, ask1)
				self.Output.Logf("po %+v, %+v", o, e)
			} else {
				if diff <= -50 && at == RISE {
					o, e := self.bitmexApi2.PlaceAnOrder("BUY", self.baseAmount, ask1)
					self.Output.Warnf("closing po %+v, %+v", o, e)
				}
			}
			self.Output.Log("diff", diff)
		}
	} else {
		switch at {
		case RISE:
			o, e := self.bitmexApi2.PlaceAnOrder("BUY", self.baseAmount, ask1)
			self.Output.Logf("po %+v, %+v", o, e)
		case FALL:
			o, e := self.bitmexApi2.PlaceAnOrder("SELL", self.baseAmount, bid1)
			self.Output.Logf("po %+v, %+v", o, e)
		}
	}

	// cancel all
	for i := 0; i < 10; i++ {
		orders, err := self.bitmexApi2.UnfinishOrders()
		if err != nil {
			self.Output.Errorf("unfinish err: %+v", err)
			time.Sleep(time.Second)
			continue
		}

		self.Output.Log("co length", len(orders))
		for _, order := range orders {
			for i := 0; i < 10; i++ {
				_, err := self.bitmexApi2.CancelOrder(order.OrderID2)
				if err != nil {
					self.Output.Logf("co err: %+v", err)
					time.Sleep(time.Second)
					continue
				}
				break
			}
		}
		break
	}
}

func (self *TransSystem) wsReceiveMessage() {
	self.bitmexWs.SetSubscribe([]string{"orderBookL2_25:XBTUSD"})
	err := self.bitmexWs.Connect()
	if err != nil {
		self.Output.Error(err)
		self.Sr.RestartProcess()
	}

	self.bitmexWs.ReceiveMessage(func(data interface{}) {
		switch data.(type) {
		case goex.DepthPair:
			depthPair := data.(goex.DepthPair)
			self.processLock.Lock()
			defer self.processLock.Unlock()

			self.depth = [2]float64{depthPair.Buy, depthPair.Sell}
			self.updatedAt = time.Now()
		}
	}, func(err error) {
		self.Output.Error("bitmex-ws except", err)
		for {
			err := self.bitmexWs.ReConnectByArtificial()
			if err != nil {
				self.Output.Warn("reconn failed", err)
				time.Sleep(time.Second)
				continue
			}
			break
		}
	})
}

// 获取持仓
func (self *TransSystem) getPos() (*PositionInfo, error) {
	for i := 0; i < 10; i++ {
		data, err := self.bitmexApi.GetPosition(
			goex.NewCurrencyPair(
				goex.NewCurrency(self.currency[0], ""),
				goex.NewCurrency(self.currency[1], ""),
			),
		)
		if err != nil {
			time.Sleep(time.Second)
			continue
		}

		tmpPair := data.(map[string]interface{})
		var pi = &PositionInfo{}
		if tmpPair["avgEntryPrice"] != nil {
			pi.AvgEntryPrice = tmpPair["avgEntryPrice"].(float64)
		}
		if tmpPair["currentQty"] != nil {
			pi.AvgEntryQty = tmpPair["currentQty"].(float64)
		}
		if tmpPair["marginCallPrice"] != nil {
			pi.ClosingPrice = tmpPair["marginCallPrice"].(float64)
		}

		return pi, nil
	}

	return nil, errors.New("get position failed")
}

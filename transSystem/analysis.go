package main

import (
	"fmt"
	"sync"
	"time"

	goex "github.com/huoxuhuoxu/GoEx"
	"github.com/huoxuhuoxu/UseGoexPackaging/conn"
)

type Analysis struct {
	*MainControl

	currency       [2]string
	depthMps       []float64
	depthCachedLen int

	chanDataT    chan interface{}
	chanAnalysis chan interface{}

	analysisTrend      [10]AnalysisTrend
	analysisTrendIndex uint8
	atTWMutex          *sync.RWMutex

	binanceWs  *conn.WsConn
	binanceApi *conn.Conn
}

func NewAnalysis(mc *MainControl) (*Analysis, error) {
	depthCachedLen := 60

	self := &Analysis{
		MainControl:        mc,
		currency:           [2]string{"BTC", "USDT"},
		depthMps:           make([]float64, depthCachedLen),
		depthCachedLen:     depthCachedLen,
		chanDataT:          make(chan interface{}, 1),
		chanAnalysis:       make(chan interface{}, 1),
		analysisTrend:      [10]AnalysisTrend{},
		analysisTrendIndex: 0,
		atTWMutex:          &sync.RWMutex{},
	}

	binanceWs, err := conn.NewWsConn(
		goex.BINANCE,
		"",
		"",
		conn.RT_EXCHANGE_WS,
		nil,
	)
	if err != nil {
		return nil, err
	}

	binanceApi := conn.NewConn(
		goex.BINANCE,
		"",
		"",
		map[string]string{
			"curA": self.currency[0],
			"curB": self.currency[1],
		},
	)

	self.binanceWs = binanceWs
	self.binanceApi = binanceApi

	return self, nil
}

func (self *Analysis) Running() error {
	err := self.handlerWs()
	if err != nil {
		return err
	}

	self.dataCollection()
	self.autoAnalysisCached()

	return nil
}

func (self *Analysis) GetAnalysisRet() AnalysisTrend {
	self.atTWMutex.RLock()
	defer self.atTWMutex.RUnlock()

	at := make(map[AnalysisTrend]int)
	for _, v := range self.analysisTrend {
		if v == 0 {
			self.Output.Log("jumper over")
			return UNKNOW
		}
		if _, ok := at[v]; !ok {
			at[v] = 0
		}
		at[v]++
	}

	if at[RISE] == at[FALL] {
		return CONCUSSION
	}

	if at[CONCUSSION] >= at[RISE] && at[CONCUSSION] >= at[FALL] {
		return CONCUSSION
	}

	if at[RISE] > at[FALL] {
		return RISE
	} else {
		return FALL
	}
}

// handler cached-data, 10s/c
func (self *Analysis) autoAnalysisCached() {
	go func() {
		chanTick := time.Tick(10 * time.Second)
		for {
			<-chanTick
			self.Output.Log("loop, autoAnalysisCached")
			self.chanDataT <- &AnalysisResult{}
			ret := <-self.chanAnalysis
			switch ret.(type) {
			case AnalysisTrend:
				tmpAnalysisTrend := ret.(AnalysisTrend)
				self.atTWMutex.Lock()
				self.analysisTrend[self.analysisTrendIndex] = tmpAnalysisTrend
				self.atTWMutex.Unlock()
				self.analysisTrendIndex++
				if self.analysisTrendIndex > 9 {
					self.analysisTrendIndex = 0
				}
			}
		}
	}()
}

// data collection
func (self *Analysis) dataCollection() {
	go func() {
		for {
			select {
			case <-self.Ctx.Done():
				self.Output.Log("data-collection exit.")
				return
			case data, ok := <-self.chanDataT:
				if !ok {
					self.Output.Log("data-collection chan closed.")
					return
				}

				switch data.(type) {
				case goex.DepthPair:
					depthPair := data.(goex.DepthPair)
					mp := int((depthPair.Buy + depthPair.Sell) / 2)
					self.appendCachedSlice(&self.depthMps, float64(mp))
					// self.Output.Log(self.depthMps)

				case *AnalysisResult:
					if self.depthMps[self.depthCachedLen-1] == 0 {
						self.chanAnalysis <- nil
						break
					}

					uniqueMps := make([]float64, 0)
					trendDistribution := make(map[float64]int)
					for _, mp := range self.depthMps {
						uniqueLen := len(uniqueMps)
						if uniqueLen > 0 {
							if uniqueMps[uniqueLen-1] == mp {
								trendDistribution[mp]++
								continue
							}
						}
						if _, ok := trendDistribution[mp]; ok {
							trendDistribution[mp]++
						}
						uniqueMps = append(uniqueMps, mp)
					}

					// 1. 判断趋势
					// 2. 找出价格停留位置, 作为分钟级别筹码聚集

					// self.Output.Infof("\r\n%+v", uniqueMps)
					// self.Output.Infof("\r\n%+v", trendDistribution)

					diffRet := uniqueMps[0] - uniqueMps[len(uniqueMps)-1]
					var tmpV, tmpC, mpV float64
					for k, v := range trendDistribution {
						tmpV += k * float64(v)
						tmpC += float64(v)
					}
					mpV = tmpV / tmpC

					if diffRet > 0 && mpV <= uniqueMps[0] {
						self.chanAnalysis <- RISE
						// self.Output.Warnf("\r\n%s", "上升趋势")
					} else if diffRet < 0 && mpV >= uniqueMps[0] {
						self.chanAnalysis <- FALL
						// self.Output.Warnf("\r\n%s", "下降趋势")
					} else {
						self.chanAnalysis <- CONCUSSION
						// self.Output.Warnf("\r\n%s", "震荡")
					}

					// self.Output.Warnf("\r\n%.0f", mpV)
				}
			}
		}
	}()
}

// receive ws-message
func (self *Analysis) handlerWs() error {
	subScribeV := fmt.Sprintf("%s@depth5", self.currency[0]+self.currency[1])
	self.binanceWs.SetSubscribe([]string{subScribeV})
	err := self.binanceWs.Connect()
	if err != nil {
		return err
	}

	self.binanceWs.ReceiveMessage(func(data interface{}) {
		switch data.(type) {
		case goex.DepthPair:
			self.chanDataT <- data
		}
	}, func(err error) {
		handlerAfterDisconnect(err, self.binanceWs)
	})

	return nil
}

// common-append item to slice
func (self *Analysis) appendCachedSlice(cached *[]float64, v float64) {
	tmpCached := []float64{v}
	tmpCached = append(tmpCached, (*cached)[0:self.depthCachedLen-1]...)
	*cached = tmpCached
}

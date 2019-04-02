package main

import (
	"log"
	"time"

	goex "github.com/huoxuhuoxu/GoEx"
	"github.com/huoxuhuoxu/UseGoexPackaging/conn"
)

func WsData() {
	bitmexWs, _ = conn.NewWsConn(
		goex.BITMEX,
		apiKey,
		secretKey,
		conn.RT_EXCHANGE_WS,
		map[string]string{})
	bitmexWs.SetSubscribe([]string{"orderBookL2_25:XBTUSD"})

	// 连接
	err = bitmexWs.Connect()
	if err != nil {
		log.Println("ws error", err)
		time.Sleep(time.Second * 1)
		sr.RestartProcess()
	}

	// 接收数据
	bitmexWs.ReceiveMessage(func(data interface{}) {
		switch data.(type) {
		case goex.DepthExcept:
			ts.SetRunning(false)
			except := data.(goex.DepthExcept)
			log.Println("depth exception", except.String())

		case goex.DepthPair:
			depthPair := data.(goex.DepthPair)
			dc, err := bitmexWs.CompleteDepth(depthPair.Symbol)
			if err != nil {
				ts.SetRunning(false)
				log.Println("complete exception", err)
				return
			}
			ts.SetRunning(true)
			ts.SetDepth(depthPair.Buy, depthPair.Sell, dc)

		default:
			log.Println("useless information", data)
		}
	}, func(err error) {
		log.Println("ws error", err)
		time.Sleep(time.Second * 1)
		sr.RestartProcess()
	})
}

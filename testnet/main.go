package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/huoxuhuoxu/GoEx/bitmex"
	"github.com/huoxuhuoxu/UseGoexPackaging/conn"
)

const (
	apiKey    = ""
	secretKey = ""
)

var (
	bitmexWs *conn.WsConn     // bitmex - ws 对象
	ts       *TradingStraregy // 割韭菜主体对象
	sr       *SystemRp        // 重启对象

	err error
)

func main() {
	// log.SetFlags(0)
	bitmex.EnterTestMode()

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, syscall.SIGINT, syscall.SIGTERM)

	sr = NewSystemRp()
	ts = NewTS(apiKey, secretKey, "BTC", "USD")
	go WsData()
	ts.Running()

	log.Println("init succ")

	for {
		<-interrupt
		log.Println("exit")
		return
	}
}

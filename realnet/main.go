package main

import (
	"flag"
	"log"

	"github.com/huoxuhuoxu/GoEx/bitmex"
)

const (
	API_KEY    = ""
	SECRET_KEY = ""
)

var (
	isDebug = flag.Bool("debug", false, "running model")

	mc  *MainControl
	err error
)

func main() {
	flag.Parse()
	bitmex.EnterTestMode()

	mc, err = NewMainCtrl(*isDebug)
	if err != nil {
		log.Fatal("new main ctrl failed", err)
	}
	defer mc.Output.Close()

	trader := NewTrader(API_KEY, SECRET_KEY, mc, *isDebug)
	trader.Running()

	mc.Output.Log("succ, starting ...")
end:
	for {
		select {
		case <-mc.Interrupt:
			mc.Output.Log("ctrl + c, ctrl + d, exit")
			break end
		}
	}

	mc.Output.Log("end, exit.")
}

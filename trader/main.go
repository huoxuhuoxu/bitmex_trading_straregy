package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/huoxuhuoxu/UseGoexPackaging/conn"
	"github.com/huoxuhuoxu/UseGoexPackaging/diyLog"
)

var (
	dg       *diyLog.DiyLog
	bitmexWs *conn.WsConn

	debug = flag.Bool("debug", false, "running model")
)

func init() {
	log.SetFlags(0)
	flag.Parse()

	if *debug {
		dg, _ = diyLog.NewDiyLog(diyLog.DEBUG, diyLog.STDOUT)
	} else {
		dg, _ = diyLog.NewDiyLog(diyLog.DEBUG, diyLog.FILE_RECORD)
		dg.SetOutputParams("logs", "")
	}
}

func main() {
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, syscall.SIGINT, syscall.SIGTERM)

	for {
		<-interrupt
		break
	}
	dg.Log("exit, end ...")
}

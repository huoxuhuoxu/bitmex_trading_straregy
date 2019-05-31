package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/huoxuhuoxu/UseGoexPackaging/diyLog"
	"github.com/huoxuhuoxu/UseGoexPackaging/systemRp"
)

type MainControl struct {
	Output    *diyLog.DiyLog
	Sr        *systemRp.SystemRp
	Ctx       context.Context
	CtxCancel context.CancelFunc
	Interrupt chan os.Signal
}

func NewMainCtrl(isDebug bool) (*MainControl, error) {
	var (
		mc  *MainControl
		err error
	)

	func() {
		defer func() {
			if tmpErr := recover(); tmpErr != nil {
				errMsg := fmt.Sprintf("initial task failed, %s", tmpErr)
				err = errors.New(errMsg)
			}
		}()

		mc = &MainControl{}
		// log
		if isDebug {
			mc.Output, _ = diyLog.NewDiyLog(diyLog.DEBUG, diyLog.STDOUT)
		} else {
			mc.Output, _ = diyLog.NewDiyLog(diyLog.DEBUG, diyLog.FILE_RECORD)
			mc.Output.SetOutputParams("logs", "")
		}

		// process restart
		mc.Sr = systemRp.NewSystemRp(time.Hour * 12)
		mc.Sr.SubscribeBeforeFunc(mc.Output.Close)

		// 主控制流
		mc.Ctx, mc.CtxCancel = context.WithCancel(context.Background())

		// 监听信号, SIGINT, SIGTERM
		mc.Interrupt = make(chan os.Signal, 1)
		signal.Notify(mc.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	}()

	return mc, err
}

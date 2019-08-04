package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/huoxuhuoxu/UseGoexPackaging/diyLog"
	"github.com/huoxuhuoxu/UseGoexPackaging/diySmtp"
	"github.com/huoxuhuoxu/UseGoexPackaging/systemRp"
)

const (
	TRANS_SYSTEM_NAME = "TRANS_SYSTEM"
)

type MainControl struct {
	Output    *diyLog.DiyLog
	Sr        *systemRp.SystemRp
	Sm        *diySmtp.Smtp
	Ctx       context.Context
	CtxCancel context.CancelFunc
	Interrupt chan os.Signal
}

func NewMainCtrl(isUat, isDebug bool) (*MainControl, error) {
	var (
		mc   *MainControl
		err  error
		preV string
	)

	// separate environment
	if isUat {
		preV = "UAT_" + TRANS_SYSTEM_NAME
	} else {
		preV = "PRO_" + TRANS_SYSTEM_NAME
	}

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
			mc.Output.SetOutputParams("logs", preV+"-")
		}

		// process restart
		mc.Sr = systemRp.NewSystemRp(time.Hour * time.Duration(12))
		mc.Sr.SubscribeBeforeFunc(mc.Output.Close)

		// email configure
		emailUn := os.Getenv("SERVER_EMAIL_UN")
		emailPd := os.Getenv("SERVER_EMAIL_PD")
		emailService := os.Getenv("SERVER_EMAIL_SERVICE")
		emailClientStr := os.Getenv("CLIENT_EMAILS")
		emailClients := strings.Split(emailClientStr, ",")

		mc.Sm = diySmtp.NewSmtp(preV, emailUn, emailPd, emailService, "587")
		mc.Sm.SetToSend(emailClients)

		// 主控制流
		mc.Ctx, mc.CtxCancel = context.WithCancel(context.Background())

		// 监听信号, SIGINT, SIGTERM
		mc.Interrupt = make(chan os.Signal, 1)
		signal.Notify(mc.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	}()

	return mc, err
}

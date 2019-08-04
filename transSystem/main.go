package main

import (
	"flag"

	"github.com/joho/godotenv"
)

var (
	isDebug bool
	isUat   bool

	mc          *MainControl
	transSystem *TransSystem

	err error
)

func init() {
	err = godotenv.Load("./transSystem/.env")
	handlerError(err)

	flag.BoolVar(&isDebug, "debug", false, "debug model")
	flag.BoolVar(&isUat, "uat", false, "uat env")
}

func main() {
	flag.Parse()

	mc, err = NewMainCtrl(isUat, isDebug)
	handlerError(err)

	transSystem, err = NewTransSystem(mc, isUat)
	handlerError(err)

	transSystem.Running()

end:
	for {
		select {
		case <-mc.Interrupt:
			break end
		case <-mc.Ctx.Done():
			break end
		}
	}

	mc.Output.Log("end ...")
}

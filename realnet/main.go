package main

import (
	"flag"
	"io/ioutil"
	"log"
	"os"
	"strings"

	"github.com/huoxuhuoxu/GoEx/bitmex"
)

var (
	API_KEY    = ""
	SECRET_KEY = ""

	isDebug = flag.Bool("debug", false, "running model")

	isTest = flag.Bool("modeTest", false, "env, test or pro")

	mc  *MainControl
	err error
)

func main() {
	flag.Parse()
	// env: test
	if *isTest {
		bitmex.EnterTestMode()
	}
	log.Println(*isTest, *isDebug)

	mc, err = NewMainCtrl(*isDebug)
	if err != nil {
		log.Fatal("new main ctrl failed", err)
	}
	defer mc.Output.Close()

	fs, err := os.Open("./realnet/keys.txt")
	if err != nil {
		mc.Output.Fatal("open keys-file failed", err)
	}
	byteKeys, err := ioutil.ReadAll(fs)
	if err != nil {
		mc.Output.Fatal("read fs failed", err)
	}

	str := string(byteKeys)
	rets := strings.Split(str, "\n")

	API_KEY = rets[0]
	SECRET_KEY = rets[1]

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

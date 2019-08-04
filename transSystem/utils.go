package main

import (
	"io/ioutil"
	"log"
	"os"
	"strings"
	"time"

	"github.com/huoxuhuoxu/GoEx/bitmex"
	"github.com/huoxuhuoxu/UseGoexPackaging/conn"
)

func handlerError(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func handlerAfterDisconnect(err error, exchangeWs *conn.WsConn) {
	mc.Output.Error(err)
	for {
		err := exchangeWs.ReConnectByArtificial()
		if err != nil {
			time.Sleep(time.Second)
			continue
		}
		break
	}
}

func readKeys(isUat bool) ([]string, error) {
	var pathname string
	if isUat {
		pathname = "./keys2.txt"
		bitmex.EnterTestMode()
	} else {
		pathname = "./keys.txt"
	}

	fs, err := os.Open(pathname)
	if err != nil {
		return nil, err
	}

	byteKeys, err := ioutil.ReadAll(fs)
	if err != nil {
		return nil, err
	}

	str := string(byteKeys)
	rets := strings.Split(str, "\n")

	return rets, nil
}

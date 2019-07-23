package main

import (
	"log"
	"time"

	goex "github.com/huoxuhuoxu/GoEx"
	"github.com/huoxuhuoxu/UseGoexPackaging/conn"
)

func main() {
	exchangeAPI := conn.NewConn(goex.BINANCE, "", "", map[string]string{
		"curA": "BTC",
		"curB": "USDT",
	})

	get1day(exchangeAPI)
	time.Sleep(3 * time.Second)

	get3day(exchangeAPI)
	time.Sleep(3 * time.Second)

	get10day(exchangeAPI)
	time.Sleep(3 * time.Second)

	get20day(exchangeAPI)
	time.Sleep(3 * time.Second)

	get30day(exchangeAPI)
	time.Sleep(3 * time.Second)

	get60day(exchangeAPI)
	time.Sleep(3 * time.Second)

	get90day(exchangeAPI)
	time.Sleep(3 * time.Second)

}

func get1day(exchangeAPI *conn.Conn) {
	// 5m
	num := 12 * 24 * 1
	klines, err := exchangeAPI.Kline(3, num, (time.Now().Unix()-1*24*60*60)*1000)
	if err != nil {
		log.Fatal(err)
	}

	cul("1day", klines, num)
}

func get3day(exchangeAPI *conn.Conn) {
	// 5m
	num := 12 * 24 * 3
	klines, err := exchangeAPI.Kline(3, num, (time.Now().Unix()-3*24*60*60)*1000)
	if err != nil {
		log.Fatal(err)
	}

	cul("3day", klines, num)
}

func get10day(exchangeAPI *conn.Conn) {
	// 15m
	num := 4 * 24 * 10
	klines, err := exchangeAPI.Kline(4, num, (time.Now().Unix()-10*24*60*60)*1000)
	if err != nil {
		log.Fatal(err)
	}

	cul("10day", klines, num)
}

func get20day(exchangeAPI *conn.Conn) {
	// 30m
	num := 2 * 24 * 20
	klines, err := exchangeAPI.Kline(5, num, (time.Now().Unix()-20*24*60*60)*1000)
	if err != nil {
		log.Fatal(err)
	}

	cul("20day", klines, num)
}

func get30day(exchangeAPI *conn.Conn) {
	// 1h
	num := 24 * 30
	klines, err := exchangeAPI.Kline(6, num, (time.Now().Unix()-30*24*60*60)*1000)
	if err != nil {
		log.Fatal(err)
	}

	cul("30day", klines, num)
}

func get60day(exchangeAPI *conn.Conn) {
	// 2h
	num := 12 * 60
	klines, err := exchangeAPI.Kline(7, num, (time.Now().Unix()-60*24*60*60)*1000)
	if err != nil {
		log.Fatal(err)
	}

	// log.Printf("%+v", klines)

	cul("60day", klines, num)
}

func get90day(exchangeAPI *conn.Conn) {
	// 4h
	num := 12 * 90
	klines, err := exchangeAPI.Kline(8, num, (time.Now().Unix()-90*24*60*60)*1000)
	if err != nil {
		log.Fatal(err)
	}

	cul("90day", klines, num)
}

// -----------------------------
func cul(title string, klines []goex.Kline, num int) {
	var (
		high, low, mp_t float64
	)

	for _, kline := range klines {
		if kline.High > high {
			high = kline.High
		}
		if kline.Low < low || low == 0 {
			low = kline.Low
		}
		mp_t += kline.Close
	}

	mp := mp_t / float64(num)

	diff := high - low
	volatility := diff / low * 100

	log.Printf("%6s Low: %.2f, High: %.2f, Mp: %.2f, Vl: %.2f%%",
		title,
		low,
		high,
		mp,
		volatility,
	)
}

package main

import (
	"fmt"
	"math"
	"sync"
	"time"
)

type VolatilityError struct {
	PastV   float64
	marketV float64
}

func (self *VolatilityError) Error() string {
	return fmt.Sprintf("volatility is high: %.1f", self.PastV-self.marketV)
}

type Volatility struct {
	UpdatedAt     time.Time   // 最新更新时间
	Value         float64     // 记录值
	AlertValue    float64     // 波动率警戒值
	Technological *sync.Mutex // 单执行流锁
}

func NewVolatility(alertValue float64) *Volatility {
	self := &Volatility{
		AlertValue:    alertValue,
		Technological: &sync.Mutex{},
	}

	return self
}

func (self *Volatility) IsHighVolatility(value float64) (float64, bool) {
	self.Technological.Lock()
	defer self.Technological.Unlock()

	tmpV := self.Value
	self.Value = value
	self.UpdatedAt = time.Now()

	if tmpV != 0 && math.Abs(tmpV-value) >= self.AlertValue {
		return tmpV, true
	}

	return tmpV, false
}

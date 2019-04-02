package main

// 仓位管理
type Pos struct {
	pos          float64 // 仓位
	posPrice     float64 // 持仓价格
	closingPrice float64 // 平仓价格
	assets       float64 // 初始资产
}

// 订单在档位的序号管理
type GearPosition struct {
	price float64 // 档位价格
	list  []int   // 排队序号, 待成交, 一个档位可能存在多个挂单
}

// 订单状态
type OrderStatus struct {
	id         int64   // 订单号
	amount     float64 // 量
	price      float64 // 价格
	dealAmount float64 // 成交量
	dealPrice  float64 // 成交价格
	status     string  // 订单状态
}

// 撮合器
type Coupler struct {
	pos    Pos
	gearPs map[float64]*GearPosition
	orders map[int64]*OrderStatus
}

func NewCoupler() *Coupler {
	self := &Coupler{}

	// ws, err := conn.NewWsConn(
	// 	goex.BITMEX,
	// 	"",
	// 	"",
	// 	conn.RT_EXCHANGE_WS,
	// 	nil,
	// )
	// if err != nil {
	// 	log.Fatal("coupler", err)
	// }

	// self.ws = ws
	self.pos = Pos{assets: 10}

	return self
}

// 同步当前orderbook
func (self *Coupler) SetOrderBook() {
	panic("no")
}

// 限价单, 下单
func (self *Coupler) PlaceOrder(side string, amount, price float64) {
	panic("no")
}

// 市价单, 下单
func (self *Coupler) PlaceMarketOrder(side string, amount, price float64) {
	panic("no")
}

// 撤单
func (self *Coupler) CancelOrder(id string) (bool, error) {
	panic("no")
}

// 查询未成交订单
func (self *Coupler) UnfinishOrders() []*OrderStatus {
	panic("no")
}

// 查询仓位信息
func (self *Coupler) GetPos() Pos {
	panic("no")
}

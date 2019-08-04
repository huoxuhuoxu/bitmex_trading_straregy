package main

type AnalysisTrend int

const (
	UNKNOW AnalysisTrend = 0
	RISE   AnalysisTrend = iota + 1
	FALL
	CONCUSSION
)

type AnalysisResult struct {
}

// position info
type PositionInfo struct {
	AvgEntryPrice float64 // 持仓价格
	AvgEntryQty   float64 // 持仓量
	ClosingPrice  float64 // 强平价格
}

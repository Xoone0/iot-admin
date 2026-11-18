package admin

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/RenJieWeia/iot-admin/internal/staging"
)

// 分析模块：基于现有快照粗表（snapshot_coarse）、接入流水表（raw_message / raw_item）、
// 异常计数表（exception_count）及数据域/物模型/终端/网关表实时聚合计算，不新增统计物理表。
//
// 对齐《智慧能源管理平台》能耗监测全模块能力，提供五个只读分析接口：
//   - GET /admin/v1/analytics/overview   能耗总览、分类分项占比、能流图（桑基图）与用能排名
//   - GET /admin/v1/analytics/series     时序趋势、表码差值用量(Δ)/瞬时读数双模式、同环比(YoY/MoM)、尖峰平谷分时与报表矩阵
//   - GET /admin/v1/analytics/anomalies  用能异常监测（超均值50%突增、日用量超标、非工作时段用能、跑冒滴漏总分表平衡、表码倒走）
//   - GET /admin/v1/analytics/quality    数据质量与清洗异常分析
//   - GET /admin/v1/analytics/health     设备与网关运行健康分析

const (
	analyticsMaxTerminals = 20
	analyticsDefaultTerms = 8
	analyticsMaxPoints    = 5000
	analyticsMaxWindow    = 90 * 24 * time.Hour
	analyticsOverviewSpan = 3660 * 24 * time.Hour

	analyticsSpanRaw   = 7 * 24 * time.Hour
	analyticsSpanHour  = 31 * 24 * time.Hour
	analyticsSpanDay   = 366 * 24 * time.Hour
	analyticsSpanMonth = 3650 * 24 * time.Hour
	analyticsSpanYear  = 3650 * 24 * time.Hour
)

// analyticsSeriesSpan 返回趋势分析各数据源允许的最大时间跨度。
func analyticsSeriesSpan(source string) (time.Duration, bool) {
	switch source {
	case "raw":
		return analyticsSpanRaw, true
	case "hour":
		return analyticsSpanHour, true
	case "day":
		return analyticsSpanDay, true
	case "month":
		return analyticsSpanMonth, true
	case "year":
		return analyticsSpanYear, true
	default:
		return 0, false
	}
}

// ── 能源折算系数与分时时段辅助函数 ──────────────────────────────────────────

// classifyTouPeriod 按 Asia/Shanghai 本地整点划分尖峰平谷分时电价时段（4.8 费用与尖峰平谷分析）：
//   - sharp  (尖峰): 10:00-12:00, 19:00-21:00
//   - peak   (高峰): 08:00-10:00, 14:00-19:00
//   - flat   (平段): 12:00-14:00, 21:00-23:00
//   - valley (低谷): 23:00-08:00
func classifyTouPeriod(ts time.Time) string {
	h := ts.In(staging.Zone).Hour()
	switch {
	case (h >= 10 && h < 12) || (h >= 19 && h < 21):
		return "sharp"
	case (h >= 8 && h < 10) || (h >= 14 && h < 19):
		return "peak"
	case (h >= 12 && h < 14) || (h >= 21 && h < 23):
		return "flat"
	default:
		return "valley"
	}
}

// parseOffHoursRanges 解析非工作时间段配置（如 "23:00-07:00,12:00-13:30"），返回分钟区间列表。
func parseOffHoursRanges(spec string) [][2]int {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		spec = "23:00-07:00,12:00-13:30"
	}
	var out [][2]int
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		bounds := strings.Split(part, "-")
		if len(bounds) != 2 {
			continue
		}
		sMin, ok1 := parseHHMM(bounds[0])
		eMin, ok2 := parseHHMM(bounds[1])
		if ok1 && ok2 {
			out = append(out, [2]int{sMin, eMin})
		}
	}
	if len(out) == 0 {
		out = [][2]int{{23 * 60, 7 * 60}, {12 * 60, 13*60 + 30}}
	}
	return out
}

func parseHHMM(s string) (int, bool) {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, false
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || h < 0 || h > 24 || m < 0 || m >= 60 {
		return 0, false
	}
	if h == 24 && m > 0 {
		return 0, false
	}
	return h*60 + m, true
}

// isTimeInOffHours 判断时刻 ts（按 Asia/Shanghai）是否落在非工作时间段内。
func isTimeInOffHours(ts time.Time, ranges [][2]int) bool {
	lt := ts.In(staging.Zone)
	cur := lt.Hour()*60 + lt.Minute()
	for _, r := range ranges {
		start, end := r[0], r[1]
		if start == end {
			continue
		}
		if start < end {
			if cur >= start && cur < end {
				return true
			}
		} else {
			// 跨零点区间，如 23:00 -> 07:00
			if cur >= start || cur < end {
				return true
			}
		}
	}
	return false
}

// domainEnergyFactors 根据数据域/设备类型/单位推导标准煤系数(kgce)与碳排放系数(kgCO2)及默认单位。
// 参考 GB/T 2589《综合能耗计算通则》：
//   - 电 (kWh): 0.1229 kgce/kWh, 0.5703 kgCO2/kWh
//   - 水 (m³/t): 0.0857 kgce/m³, 0.1680 kgCO2/m³
//   - 燃气 (m³): 1.2143 kgce/m³, 2.1620 kgCO2/m³
//   - 蒸汽 (t):  97.14 kgce/t,   180.00 kgCO2/t
//   - 热/冷 (GJ): 34.12 kgce/GJ, 110.00 kgCO2/GJ（若单位为 kWh 则按 0.1229）
func domainEnergyFactors(domainCode, domainName, typeCode, typeName, unit string) (coalFactor, carbonFactor float64, stdUnit string) {
	combined := strings.ToLower(domainCode + " " + domainName + " " + typeCode + " " + typeName)
	uLower := strings.ToLower(strings.TrimSpace(unit))

	switch {
	case strings.Contains(combined, "water") || strings.Contains(combined, "水"):
		stdUnit = unit
		if stdUnit == "" {
			stdUnit = "m³"
		}
		return 0.0857, 0.168, stdUnit
	case strings.Contains(combined, "gas") || strings.Contains(combined, "气") || strings.Contains(combined, "燃气"):
		stdUnit = unit
		if stdUnit == "" {
			stdUnit = "m³"
		}
		return 1.2143, 2.162, stdUnit
	case strings.Contains(combined, "steam") || strings.Contains(combined, "蒸汽"):
		stdUnit = unit
		if stdUnit == "" {
			stdUnit = "t"
		}
		return 97.14, 180.0, stdUnit
	case strings.Contains(combined, "heat") || strings.Contains(combined, "cold") ||
		strings.Contains(combined, "cool") || strings.Contains(combined, "热") ||
		strings.Contains(combined, "冷") || strings.Contains(combined, "空调"):
		if strings.Contains(uLower, "gj") {
			return 34.12, 110.0, "GJ"
		}
		stdUnit = unit
		if stdUnit == "" {
			stdUnit = "kWh"
		}
		return 0.1229, 0.5703, stdUnit
	default:
		stdUnit = unit
		if stdUnit == "" {
			stdUnit = "kWh"
		}
		return 0.1229, 0.5703, stdUnit
	}
}

// isPrimaryMeteringProperty 判断字段是否为累计表码/能量主计量字段（优先匹配电能、流量、热量、冷量、累计值）。
func isPrimaryMeteringProperty(itemCode, itemName, unit string) int {
	c := strings.ToLower(itemCode + " " + itemName)
	u := strings.ToLower(strings.TrimSpace(unit))
	score := 10
	if strings.Contains(c, "total") || strings.Contains(c, "energy") || strings.Contains(c, "ep") ||
		strings.Contains(c, "cum") || strings.Contains(c, "flow") || strings.Contains(c, "usage") ||
		strings.Contains(c, "consumption") || strings.Contains(c, "总") || strings.Contains(c, "累计") ||
		strings.Contains(c, "电度") || strings.Contains(c, "电量") || strings.Contains(c, "水量") ||
		strings.Contains(c, "气量") || strings.Contains(c, "热量") || strings.Contains(c, "冷量") ||
		strings.Contains(c, "正向有功") {
		score += 50
	}
	if u == "kwh" || u == "mwh" || u == "m³" || u == "m3" || u == "gj" || u == "t" {
		score += 30
	}
	if strings.Contains(c, "volt") || strings.Contains(c, "curr") || strings.Contains(c, "temp") ||
		strings.Contains(c, "freq") || strings.Contains(c, "factor") ||
		strings.Contains(c, "电压") || strings.Contains(c, "电流") || strings.Contains(c, "温度") ||
		strings.Contains(c, "频率") || strings.Contains(c, "因数") {
		score -= 20
	}
	return score
}

// ── 1. 时序趋势、表码差值用量(Δ)、同环比与尖峰平谷分析 ─────────────────────────

type analyticsPropertyMeta struct {
	TypeID     int64    `json:"typeId"`
	TypeName   string   `json:"typeName"`
	TypeCode   string   `json:"typeCode"`
	DomainName string   `json:"domainName,omitempty"`
	ItemCode   string   `json:"itemCode"`
	ItemName   string   `json:"itemName"`
	ValueType  string   `json:"valueType"`
	Unit       string   `json:"unit,omitempty"`
	Precision  *int     `json:"precision,omitempty"`
	MinValue   *float64 `json:"minValue,omitempty"`
	MaxValue   *float64 `json:"maxValue,omitempty"`
}

type analyticsPoint struct {
	Time          string   `json:"time"`
	Num           *float64 `json:"num,omitempty"`
	Text          *string  `json:"text,omitempty"`
	Quality       int16    `json:"quality"`
	QualityReason string   `json:"qualityReason,omitempty"`
	// 能耗扩展字段：支持「累计表码差值用量(Δ = 期末 - 期初)」与「原始读数」双模式
	Delta        *float64 `json:"delta,omitempty"`
	RawDelta     *float64 `json:"rawDelta,omitempty"`
	Rollback     bool     `json:"rollback,omitempty"`
	SpikeAnomaly bool     `json:"spikeAnomaly,omitempty"`
	TouPeriod    string   `json:"touPeriod,omitempty"`
}

type analyticsTouBreakdown struct {
	SharpUsage  float64 `json:"sharpUsage"`
	PeakUsage   float64 `json:"peakUsage"`
	FlatUsage   float64 `json:"flatUsage"`
	ValleyUsage float64 `json:"valleyUsage"`
}

type analyticsSeriesStats struct {
	Count           int      `json:"count"`
	NumericCount    int      `json:"numericCount"`
	BadQualityCount int      `json:"badQualityCount"`
	First           *float64 `json:"first,omitempty"`
	Last            *float64 `json:"last,omitempty"`
	Min             *float64 `json:"min,omitempty"`
	Max             *float64 `json:"max,omitempty"`
	Avg             *float64 `json:"avg,omitempty"`
	Sum             *float64 `json:"sum,omitempty"`
	StdDev          *float64 `json:"stdDev,omitempty"`
	Delta           *float64 `json:"delta,omitempty"`
	ChangeRate      *float64 `json:"changeRate,omitempty"`
	// 能耗扩展统计指标
	UsageSum      *float64               `json:"usageSum,omitempty"`
	UsageAvg      *float64               `json:"usageAvg,omitempty"`
	UsageMax      *float64               `json:"usageMax,omitempty"`
	UsageMaxTime  string                 `json:"usageMaxTime,omitempty"`
	SpikeCount    int                    `json:"spikeCount"`
	RollbackCount int                    `json:"rollbackCount"`
	OffHoursUsage *float64               `json:"offHoursUsage,omitempty"`
	PrevUsageSum  *float64               `json:"prevUsageSum,omitempty"`
	CompareRate   *float64               `json:"compareRate,omitempty"`
	TouBreakdown  *analyticsTouBreakdown `json:"touBreakdown,omitempty"`
}

type analyticsTerminalSeries struct {
	TerminalID   string               `json:"terminalId"`
	TerminalName string               `json:"terminalName"`
	GatewayID    string               `json:"gatewayId"`
	Online       bool                 `json:"online"`
	Enabled      bool                 `json:"enabled"`
	Stats        analyticsSeriesStats `json:"stats"`
	Points       []analyticsPoint     `json:"points"`
}

type analyticsFleetStats struct {
	TerminalCount   int                    `json:"terminalCount"`
	TotalPoints     int                    `json:"totalPoints"`
	BadQualityCount int                    `json:"badQualityCount"`
	Min             *float64               `json:"min,omitempty"`
	Max             *float64               `json:"max,omitempty"`
	Avg             *float64               `json:"avg,omitempty"`
	TotalUsage      *float64               `json:"totalUsage,omitempty"`
	PrevTotalUsage  *float64               `json:"prevTotalUsage,omitempty"`
	CompareRate     *float64               `json:"compareRate,omitempty"`
	SpikeCount      int                    `json:"spikeCount"`
	RollbackCount   int                    `json:"rollbackCount"`
	TouBreakdown    *analyticsTouBreakdown `json:"touBreakdown,omitempty"`
}

type analyticsAggBucket struct {
	Time         string   `json:"time"`
	Usage        float64  `json:"usage"`
	ReadingAvg   *float64 `json:"readingAvg,omitempty"`
	ReadingSum   *float64 `json:"readingSum,omitempty"`
	PrevUsage    *float64 `json:"prevUsage,omitempty"`
	ChangeRate   *float64 `json:"changeRate,omitempty"`
	TouPeriod    string   `json:"touPeriod"`
	SpikeAnomaly bool     `json:"spikeAnomaly,omitempty"`
}

type analyticsSeriesPayload struct {
	Property          analyticsPropertyMeta     `json:"property"`
	Source            string                    `json:"source"`
	Mode              string                    `json:"mode"`
	Compare           string                    `json:"compare,omitempty"`
	From              string                    `json:"from"`
	To                string                    `json:"to"`
	PrevFrom          string                    `json:"prevFrom,omitempty"`
	PrevTo            string                    `json:"prevTo,omitempty"`
	FleetStats        analyticsFleetStats       `json:"fleetStats"`
	AggregatedBuckets []analyticsAggBucket      `json:"aggregatedBuckets,omitempty"`
	Series            []analyticsTerminalSeries `json:"series"`
}

// enrichPointsWithDelta 为按时间升序排列的数据点补充差值用量(Δ)、表码倒走标记与尖峰平谷分时标签。
func enrichPointsWithDelta(points []analyticsPoint, baseline *float64) []analyticsPoint {
	prevNum := baseline
	for i := range points {
		if ts, err := time.Parse(time.RFC3339, points[i].Time); err == nil {
			points[i].TouPeriod = classifyTouPeriod(ts)
		}
		ptNum := points[i].Num
		if ptNum == nil || math.IsNaN(*ptNum) || math.IsInf(*ptNum, 0) {
			continue
		}
		if prevNum != nil {
			rawD := *ptNum - *prevNum
			points[i].RawDelta = &rawD
			if rawD < -1e-9 {
				points[i].Rollback = true
				zero := 0.0
				points[i].Delta = &zero
			} else {
				cleanD := math.Max(0, rawD)
				points[i].Delta = &cleanD
			}
		}
		prevNum = ptNum
	}
	return points
}

// computeSeriesStats 根据按时间升序排列的数据点计算单终端统计聚合指标及能耗指标。
func computeSeriesStats(points []analyticsPoint) analyticsSeriesStats {
	stats := analyticsSeriesStats{
		Count: len(points),
	}
	var nums []float64
	defaultOffRanges := parseOffHoursRanges("")
	var (
		hasDelta     bool
		usageSum     float64
		usageMax     float64
		usageMaxTime string
		deltaCount   int
		offHoursSum  float64
		tou          analyticsTouBreakdown
	)

	for i := range points {
		pt := &points[i]
		if pt.Quality > 0 {
			stats.BadQualityCount++
		}
		if pt.TouPeriod == "" {
			if ts, err := time.Parse(time.RFC3339, pt.Time); err == nil {
				pt.TouPeriod = classifyTouPeriod(ts)
			}
		}
		if pt.Num != nil && !math.IsNaN(*pt.Num) && !math.IsInf(*pt.Num, 0) {
			nums = append(nums, *pt.Num)
		}
		if pt.Rollback {
			stats.RollbackCount++
		}
		if pt.Delta != nil && !math.IsNaN(*pt.Delta) && !math.IsInf(*pt.Delta, 0) {
			d := *pt.Delta
			if !hasDelta || d > usageMax {
				usageMax = d
				usageMaxTime = pt.Time
			}
			hasDelta = true
			usageSum += d
			deltaCount++
			switch pt.TouPeriod {
			case "sharp":
				tou.SharpUsage += d
			case "peak":
				tou.PeakUsage += d
			case "flat":
				tou.FlatUsage += d
			default:
				tou.ValleyUsage += d
			}
			if ts, err := time.Parse(time.RFC3339, pt.Time); err == nil && isTimeInOffHours(ts, defaultOffRanges) {
				offHoursSum += d
			}
		}
	}
	stats.NumericCount = len(nums)
	if len(nums) == 0 {
		return stats
	}

	first := nums[0]
	last := nums[len(nums)-1]
	minVal := nums[0]
	maxVal := nums[0]
	sumVal := 0.0
	for _, v := range nums {
		if v < minVal {
			minVal = v
		}
		if v > maxVal {
			maxVal = v
		}
		sumVal += v
	}
	avgVal := sumVal / float64(len(nums))

	var varianceSum float64
	for _, v := range nums {
		diff := v - avgVal
		varianceSum += diff * diff
	}
	stdDev := math.Sqrt(varianceSum / float64(len(nums)))

	stats.First = &first
	stats.Last = &last
	stats.Min = &minVal
	stats.Max = &maxVal
	stats.Avg = &avgVal
	stats.Sum = &sumVal
	stats.StdDev = &stdDev

	if len(nums) >= 2 {
		delta := last - first
		stats.Delta = &delta
		if math.Abs(first) > 1e-12 {
			rate := (delta / math.Abs(first)) * 100.0
			stats.ChangeRate = &rate
		}
	}

	// 若尚未调用 enrichPointsWithDelta 填充单点 Delta，但存在至少 2 个数值点，则自动按相邻点补齐
	if !hasDelta && len(nums) >= 2 {
		enrichPointsWithDelta(points, nil)
		for i := range points {
			pt := &points[i]
			if pt.Rollback {
				stats.RollbackCount++
			}
			if pt.Delta != nil {
				d := *pt.Delta
				if !hasDelta || d > usageMax {
					usageMax = d
					usageMaxTime = pt.Time
				}
				hasDelta = true
				usageSum += d
				deltaCount++
				switch pt.TouPeriod {
				case "sharp":
					tou.SharpUsage += d
				case "peak":
					tou.PeakUsage += d
				case "flat":
					tou.FlatUsage += d
				default:
					tou.ValleyUsage += d
				}
				if ts, err := time.Parse(time.RFC3339, pt.Time); err == nil && isTimeInOffHours(ts, defaultOffRanges) {
					offHoursSum += d
				}
			}
		}
	}

	if hasDelta && deltaCount > 0 {
		uAvg := usageSum / float64(deltaCount)
		stats.UsageSum = &usageSum
		stats.UsageAvg = &uAvg
		stats.UsageMax = &usageMax
		stats.UsageMaxTime = usageMaxTime
		stats.OffHoursUsage = &offHoursSum
		stats.TouBreakdown = &tou

		// 手册 4.6.3 异常判定规则：超过平均值 50% (即 > 1.5 * uAvg) 列为用能突增异常
		if uAvg > 1e-9 {
			threshold := uAvg * 1.5
			for i := range points {
				if points[i].Delta != nil && *points[i].Delta > threshold {
					points[i].SpikeAnomaly = true
					stats.SpikeCount++
				}
			}
		}
	}

	return stats
}

// computeFleetStats 汇总多终端整体统计指标与分时用能。
func computeFleetStats(series []analyticsTerminalSeries) analyticsFleetStats {
	fleet := analyticsFleetStats{
		TerminalCount: len(series),
	}
	var (
		hasNum        bool
		minVal        float64
		maxVal        float64
		totalSum      float64
		totalNum      int
		hasUsage      bool
		totalUsage    float64
		hasPrevUsage  bool
		prevUsageSum  float64
		tou           analyticsTouBreakdown
	)
	for _, s := range series {
		fleet.TotalPoints += s.Stats.Count
		fleet.BadQualityCount += s.Stats.BadQualityCount
		fleet.SpikeCount += s.Stats.SpikeCount
		fleet.RollbackCount += s.Stats.RollbackCount
		if s.Stats.NumericCount > 0 && s.Stats.Min != nil && s.Stats.Max != nil && s.Stats.Sum != nil {
			if !hasNum {
				minVal = *s.Stats.Min
				maxVal = *s.Stats.Max
				hasNum = true
			} else {
				if *s.Stats.Min < minVal {
					minVal = *s.Stats.Min
				}
				if *s.Stats.Max > maxVal {
					maxVal = *s.Stats.Max
				}
			}
			totalSum += *s.Stats.Sum
			totalNum += s.Stats.NumericCount
		}
		if s.Stats.UsageSum != nil {
			hasUsage = true
			totalUsage += *s.Stats.UsageSum
		}
		if s.Stats.PrevUsageSum != nil {
			hasPrevUsage = true
			prevUsageSum += *s.Stats.PrevUsageSum
		}
		if s.Stats.TouBreakdown != nil {
			tou.SharpUsage += s.Stats.TouBreakdown.SharpUsage
			tou.PeakUsage += s.Stats.TouBreakdown.PeakUsage
			tou.FlatUsage += s.Stats.TouBreakdown.FlatUsage
			tou.ValleyUsage += s.Stats.TouBreakdown.ValleyUsage
		}
	}
	if hasNum && totalNum > 0 {
		avgVal := totalSum / float64(totalNum)
		fleet.Min = &minVal
		fleet.Max = &maxVal
		fleet.Avg = &avgVal
	}
	if hasUsage {
		fleet.TotalUsage = &totalUsage
		fleet.TouBreakdown = &tou
	}
	if hasPrevUsage {
		fleet.PrevTotalUsage = &prevUsageSum
		if prevUsageSum > 1e-9 && hasUsage {
			rate := (totalUsage - prevUsageSum) / prevUsageSum * 100.0
			fleet.CompareRate = &rate
		}
	}
	return fleet
}

// buildAggregatedBuckets 将多终端时序数据按时间槽对齐聚合，生成用于趋势柱状图、同环比对比图、尖峰平谷图的时间桶。
func buildAggregatedBuckets(series []analyticsTerminalSeries, prevBucketsByIndex []float64) []analyticsAggBucket {
	type slotAcc struct {
		timeStr    string
		usage      float64
		readingSum float64
		readingCnt int
		touPeriod  string
	}
	slotMap := map[string]*slotAcc{}
	var keys []string
	for _, s := range series {
		for _, pt := range s.Points {
			acc, exists := slotMap[pt.Time]
			if !exists {
				acc = &slotAcc{
					timeStr:   pt.Time,
					touPeriod: pt.TouPeriod,
				}
				slotMap[pt.Time] = acc
				keys = append(keys, pt.Time)
			}
			if pt.Delta != nil {
				acc.usage += *pt.Delta
			}
			if pt.Num != nil && !math.IsNaN(*pt.Num) && !math.IsInf(*pt.Num, 0) {
				acc.readingSum += *pt.Num
				acc.readingCnt++
			}
		}
	}
	sort.Strings(keys)
	out := make([]analyticsAggBucket, 0, len(keys))
	var totalUsage float64
	for i, k := range keys {
		acc := slotMap[k]
		b := analyticsAggBucket{
			Time:      acc.timeStr,
			Usage:     acc.usage,
			TouPeriod: acc.touPeriod,
		}
		if acc.readingCnt > 0 {
			rSum := acc.readingSum
			rAvg := acc.readingSum / float64(acc.readingCnt)
			b.ReadingSum = &rSum
			b.ReadingAvg = &rAvg
		}
		if i < len(prevBucketsByIndex) {
			pv := prevBucketsByIndex[i]
			b.PrevUsage = &pv
			if pv > 1e-9 {
				rate := (b.Usage - pv) / pv * 100.0
				b.ChangeRate = &rate
			}
		}
		totalUsage += b.Usage
		out = append(out, b)
	}
	if len(out) > 0 {
		meanUsage := totalUsage / float64(len(out))
		if meanUsage > 1e-9 {
			for i := range out {
				if out[i].Usage > meanUsage*1.5 {
					out[i].SpikeAnomaly = true
				}
			}
		}
	}
	return out
}

// analyticsSeries 处理 GET /admin/v1/analytics/series。
func (a *API) analyticsSeries(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rawTypeID := strings.TrimSpace(q.Get("type_id"))
	if rawTypeID == "" {
		writeError(w, http.StatusBadRequest, "invalid_query: 缺少 type_id")
		return
	}
	typeID, err := strconv.ParseInt(rawTypeID, 10, 64)
	if err != nil || typeID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_query: type_id 必须是正整数")
		return
	}

	itemCode := strings.TrimSpace(q.Get("item_code"))
	if itemCode == "" {
		writeError(w, http.StatusBadRequest, "invalid_query: 缺少 item_code")
		return
	}

	source := strings.TrimSpace(q.Get("source"))
	if source == "" {
		source = "hour"
	}
	span, ok := analyticsSeriesSpan(source)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_query: source 只能是 raw/hour/day/month/year")
		return
	}

	mode := strings.TrimSpace(q.Get("mode"))
	if mode == "" {
		mode = "delta"
	}
	if mode != "delta" && mode != "reading" {
		writeError(w, http.StatusBadRequest, "invalid_query: mode 只能是 delta/reading")
		return
	}

	compareMode := strings.TrimSpace(q.Get("compare"))
	if compareMode != "" && compareMode != "none" && compareMode != "mom" && compareMode != "yoy" {
		writeError(w, http.StatusBadRequest, "invalid_query: compare 只能是 none/mom/yoy")
		return
	}

	from, to, err := parseAnalyticsWindow(q.Get("from"), q.Get("to"), span, source)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var requestedTerminals []string
	if rawTermIDs := strings.TrimSpace(q.Get("terminal_ids")); rawTermIDs != "" {
		seen := map[string]struct{}{}
		for _, part := range strings.Split(rawTermIDs, ",") {
			id := strings.TrimSpace(part)
			if id == "" {
				continue
			}
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			requestedTerminals = append(requestedTerminals, id)
		}
		if len(requestedTerminals) > analyticsMaxTerminals {
			writeError(w, http.StatusBadRequest,
				fmt.Sprintf("invalid_query: 单次对比终端数不能超过 %d", analyticsMaxTerminals))
			return
		}
	}

	ctx := r.Context()

	// 1) 查询物模型与字段定义。
	var prop analyticsPropertyMeta
	err = a.Pool.QueryRow(ctx, `
		select p.type_id, coalesce(t.name, ''), t.code, coalesce(dd.name, ''),
		       p.item_code, p.name, p.value_type,
		       coalesce(p.unit, ''), p.precision, p.min_value, p.max_value
		from thing_model_property p
		join device_type t on t.id = p.type_id
		left join data_domain dd on dd.id = t.domain_id
		where p.type_id = $1 and p.item_code = $2`,
		typeID, itemCode).Scan(
		&prop.TypeID, &prop.TypeName, &prop.TypeCode, &prop.DomainName,
		&prop.ItemCode, &prop.ItemName, &prop.ValueType,
		&prop.Unit, &prop.Precision, &prop.MinValue, &prop.MaxValue,
	)
	if err == pgx.ErrNoRows {
		writeError(w, http.StatusNotFound, "property_not_found")
		return
	}
	if err != nil {
		writeDBError(w, err)
		return
	}

	// 2) 解析目标终端列表（未显式指定时默认取该物模型下前 analyticsDefaultTerms 台活跃终端）。
	var termRows pgx.Rows
	if len(requestedTerminals) > 0 {
		termRows, err = a.Pool.Query(ctx, `
			select id, coalesce(name, ''), gateway_id, online, enabled
			from terminal
			where type_id = $1 and id = any($2::text[]) and deleted_at is null
			order by id`,
			typeID, requestedTerminals)
	} else {
		termRows, err = a.Pool.Query(ctx, `
			select id, coalesce(name, ''), gateway_id, online, enabled
			from terminal
			where type_id = $1 and deleted_at is null
			order by online desc, id asc
			limit $2`,
			typeID, analyticsDefaultTerms)
	}
	if err != nil {
		writeDBError(w, err)
		return
	}

	series := make([]analyticsTerminalSeries, 0, analyticsDefaultTerms)
	termIndex := map[string]int{}
	for termRows.Next() {
		var s analyticsTerminalSeries
		if err := termRows.Scan(&s.TerminalID, &s.TerminalName, &s.GatewayID, &s.Online, &s.Enabled); err != nil {
			termRows.Close()
			writeDBError(w, err)
			return
		}
		s.Points = make([]analyticsPoint, 0)
		termIndex[s.TerminalID] = len(series)
		series = append(series, s)
	}
	termRows.Close()
	if err := termRows.Err(); err != nil {
		writeDBError(w, err)
		return
	}

	var (
		prevFromStr        string
		prevToStr          string
		prevBucketsByIndex []float64
	)

	// 3) 拉取时序点并组装统计指标。
	if len(series) > 0 {
		terminalIDs := make([]string, len(series))
		for i, s := range series {
			terminalIDs[i] = s.TerminalID
		}

		// 查询窗口起点之前的最近一条快照基准读数，使首个时间槽也能计算准确的期末-期初差值 Δ。
		baselines := map[string]*float64{}
		if source != "raw" {
			baseRows, bErr := a.Pool.Query(ctx, `
				select distinct on (terminal_id) terminal_id, value_num
				from snapshot_coarse
				where granularity = $1
				  and terminal_id = any($2::text[])
				  and item_code = $3
				  and segment_start < $4
				  and value_num is not null
				order by terminal_id, segment_start desc`,
				source, terminalIDs, itemCode, from)
			if bErr == nil {
				for baseRows.Next() {
					var (
						tid string
						val float64
					)
					if err := baseRows.Scan(&tid, &val); err == nil {
						v := val
						baselines[tid] = &v
					}
				}
				baseRows.Close()
			}
		}

		var pointRows pgx.Rows
		if source == "raw" {
			pointRows, err = a.Pool.Query(ctx, `
				select terminal_id, coalesce(value_time, received_at) as ts,
				       value_num, value_text, quality, coalesce(quality_reason, '')
				from raw_item
				where terminal_id = any($1::text[])
				  and item_code = $2
				  and status = 'written'
				  and received_at >= $3 and received_at <= $4
				order by terminal_id, ts asc
				limit $5`,
				terminalIDs, itemCode, from, to, analyticsMaxPoints)
		} else {
			pointRows, err = a.Pool.Query(ctx, `
				select terminal_id, segment_start as ts,
				       value_num, value_text, quality, coalesce(quality_reason, '')
				from snapshot_coarse
				where granularity = $1
				  and terminal_id = any($2::text[])
				  and item_code = $3
				  and segment_start >= $4 and segment_start <= $5
				order by terminal_id, segment_start asc
				limit $6`,
				source, terminalIDs, itemCode, from, to, analyticsMaxPoints)
		}
		if err != nil {
			writeDBError(w, err)
			return
		}
		for pointRows.Next() {
			var (
				termID string
				ts     time.Time
				pt     analyticsPoint
			)
			if err := pointRows.Scan(&termID, &ts, &pt.Num, &pt.Text, &pt.Quality, &pt.QualityReason); err != nil {
				pointRows.Close()
				writeDBError(w, err)
				return
			}
			pt.Time = ts.UTC().Format(time.RFC3339)
			if idx, ok := termIndex[termID]; ok {
				series[idx].Points = append(series[idx].Points, pt)
			}
		}
		pointRows.Close()
		if err := pointRows.Err(); err != nil {
			writeDBError(w, err)
			return
		}

		for i := range series {
			tid := series[i].TerminalID
			series[i].Points = enrichPointsWithDelta(series[i].Points, baselines[tid])
			series[i].Stats = computeSeriesStats(series[i].Points)
		}

		// 4) 若开启同环比对比（compare = mom 或 yoy），拉取对比窗口并计算对比期用能。
		if compareMode == "mom" || compareMode == "yoy" {
			var prevFrom, prevTo time.Time
			if compareMode == "mom" {
				dur := to.Sub(from)
				prevFrom = from.Add(-dur)
				prevTo = from
			} else {
				prevFrom = from.AddDate(-1, 0, 0)
				prevTo = to.AddDate(-1, 0, 0)
			}
			prevFromStr = prevFrom.UTC().Format(time.RFC3339)
			prevToStr = prevTo.UTC().Format(time.RFC3339)

			prevPointsByTerm := map[string][]analyticsPoint{}
			var prevRows pgx.Rows
			if source == "raw" {
				prevRows, err = a.Pool.Query(ctx, `
					select terminal_id, coalesce(value_time, received_at) as ts, value_num
					from raw_item
					where terminal_id = any($1::text[])
					  and item_code = $2
					  and status = 'written'
					  and received_at >= $3 and received_at <= $4
					order by terminal_id, ts asc
					limit $5`,
					terminalIDs, itemCode, prevFrom, prevTo, analyticsMaxPoints)
			} else {
				prevRows, err = a.Pool.Query(ctx, `
					select terminal_id, segment_start as ts, value_num
					from snapshot_coarse
					where granularity = $1
					  and terminal_id = any($2::text[])
					  and item_code = $3
					  and segment_start >= $4 and segment_start <= $5
					order by terminal_id, segment_start asc
					limit $6`,
					source, terminalIDs, itemCode, prevFrom, prevTo, analyticsMaxPoints)
			}
			if err == nil {
				for prevRows.Next() {
					var (
						tid string
						ts  time.Time
						num *float64
					)
					if sErr := prevRows.Scan(&tid, &ts, &num); sErr == nil {
						prevPointsByTerm[tid] = append(prevPointsByTerm[tid], analyticsPoint{
							Time: ts.UTC().Format(time.RFC3339),
							Num:  num,
						})
					}
				}
				prevRows.Close()
			}

			prevSlotMap := map[string]float64{}
			var prevSlotKeys []string
			for i := range series {
				tid := series[i].TerminalID
				pts := enrichPointsWithDelta(prevPointsByTerm[tid], nil)
				pStats := computeSeriesStats(pts)
				var pSum float64
				if pStats.UsageSum != nil {
					pSum = *pStats.UsageSum
				} else if pStats.Delta != nil && *pStats.Delta > 0 {
					pSum = *pStats.Delta
				}
				series[i].Stats.PrevUsageSum = &pSum
				if pSum > 1e-9 && series[i].Stats.UsageSum != nil {
					rate := (*series[i].Stats.UsageSum - pSum) / pSum * 100.0
					series[i].Stats.CompareRate = &rate
				}
				for _, pt := range pts {
					if pt.Delta != nil {
						if _, ok := prevSlotMap[pt.Time]; !ok {
							prevSlotKeys = append(prevSlotKeys, pt.Time)
						}
						prevSlotMap[pt.Time] += *pt.Delta
					}
				}
			}
			sort.Strings(prevSlotKeys)
			for _, k := range prevSlotKeys {
				prevBucketsByIndex = append(prevBucketsByIndex, prevSlotMap[k])
			}
		}
	}

	aggBuckets := buildAggregatedBuckets(series, prevBucketsByIndex)

	writeJSON(w, http.StatusOK, Response{
		Code:    0,
		Message: "ok",
		Data: analyticsSeriesPayload{
			Property:          prop,
			Source:            source,
			Mode:              mode,
			Compare:           compareMode,
			From:              from.UTC().Format(time.RFC3339),
			To:                to.UTC().Format(time.RFC3339),
			PrevFrom:          prevFromStr,
			PrevTo:            prevToStr,
			FleetStats:        computeFleetStats(series),
			AggregatedBuckets: aggBuckets,
			Series:            series,
		},
	})
}

// ── 1b. 能耗总览、分类分项与能流图（4.5 能耗总览 & 4.6.1/4.6.2 能流图 & 4.6.6 分类分项） ──

type analyticsOverviewBucket struct {
	Time       string  `json:"time"`
	CurrentVal float64 `json:"currentVal"`
	PrevVal    float64 `json:"prevVal"`
}

type analyticsDomainOverview struct {
	DomainID         int64                     `json:"domainId"`
	DomainCode       string                    `json:"domainCode"`
	DomainName       string                    `json:"domainName"`
	Unit             string                    `json:"unit"`
	CoalFactor       float64                   `json:"coalFactor"`
	CarbonFactor     float64                   `json:"carbonFactor"`
	CurrentUsage     float64                   `json:"currentUsage"`
	PrevUsage        float64                   `json:"prevUsage"`
	ChangeRate       *float64                  `json:"changeRate,omitempty"`
	StandardCoalKgce float64                   `json:"standardCoalKgce"`
	CarbonKgCO2      float64                   `json:"carbonKgCO2"`
	SharePercent     float64                   `json:"sharePercent"`
	TerminalCount    int                       `json:"terminalCount"`
	OnlineCount      int                       `json:"onlineCount"`
	Buckets          []analyticsOverviewBucket `json:"buckets"`
}

type analyticsTypeEnergySummary struct {
	TypeID           int64    `json:"typeId"`
	TypeCode         string   `json:"typeCode"`
	TypeName         string   `json:"typeName"`
	DomainName       string   `json:"domainName"`
	PrimaryItemCode  string   `json:"primaryItemCode"`
	PrimaryItemName  string   `json:"primaryItemName"`
	Unit             string   `json:"unit"`
	TerminalCount    int      `json:"terminalCount"`
	OnlineCount      int      `json:"onlineCount"`
	CurrentUsage     float64  `json:"currentUsage"`
	PrevUsage        float64  `json:"prevUsage"`
	ChangeRate       *float64 `json:"changeRate,omitempty"`
	StandardCoalKgce float64  `json:"standardCoalKgce"`
	SharePercent     float64  `json:"sharePercent"`
}

type analyticsGatewayEnergySummary struct {
	GatewayID        string  `json:"gatewayId"`
	GatewayName      string  `json:"gatewayName"`
	Online           bool    `json:"online"`
	TerminalCount    int     `json:"terminalCount"`
	OnlineCount      int     `json:"onlineCount"`
	CurrentUsage     float64 `json:"currentUsage"`
	StandardCoalKgce float64 `json:"standardCoalKgce"`
	SharePercent     float64 `json:"sharePercent"`
}

type analyticsTerminalEnergyRank struct {
	TerminalID       string   `json:"terminalId"`
	TerminalName     string   `json:"terminalName"`
	GatewayID        string   `json:"gatewayId"`
	TypeID           int64    `json:"typeId"`
	TypeName         string   `json:"typeName"`
	DomainName       string   `json:"domainName"`
	ItemCode         string   `json:"itemCode"`
	Unit             string   `json:"unit"`
	Online           bool     `json:"online"`
	FirstReading     *float64 `json:"firstReading,omitempty"`
	LastReading      *float64 `json:"lastReading,omitempty"`
	Usage            float64  `json:"usage"`
	PrevUsage        float64  `json:"prevUsage"`
	ChangeRate       *float64 `json:"changeRate,omitempty"`
	StandardCoalKgce float64  `json:"standardCoalKgce"`
	SharePercent     float64  `json:"sharePercent"`
}

type analyticsSankeyNode struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Category string  `json:"category"` // domain | device_type | gateway | terminal
	Value    float64 `json:"value"`    // 折算标准煤 kgce 或本域用量
	RawValue float64 `json:"rawValue"`
	Unit     string  `json:"unit"`
}

type analyticsSankeyLink struct {
	Source   string  `json:"source"`
	Target   string  `json:"target"`
	Value    float64 `json:"value"` // 折标煤 kgce（用于统一多能流宽度）
	RawValue float64 `json:"rawValue"`
	Unit     string  `json:"unit"`
}

type analyticsOverviewAlarmSummary struct {
	Spike50PctCount      int   `json:"spike50PctCount"`
	RollbackCount        int   `json:"rollbackCount"`
	QualityIssueCount    int64 `json:"qualityIssueCount"`
	OfflineTerminalCount int   `json:"offlineTerminalCount"`
}

type analyticsOverviewPayload struct {
	From                  string                          `json:"from"`
	To                    string                          `json:"to"`
	PrevFrom              string                          `json:"prevFrom"`
	PrevTo                string                          `json:"prevTo"`
	Granularity           string                          `json:"granularity"`
	TotalStandardCoalKgce float64                         `json:"totalStandardCoalKgce"`
	PrevStandardCoalKgce  float64                         `json:"prevStandardCoalKgce"`
	CoalChangeRate        *float64                        `json:"coalChangeRate,omitempty"`
	TotalCarbonKgCO2      float64                         `json:"totalCarbonKgCO2"`
	PrevCarbonKgCO2       float64                         `json:"prevCarbonKgCO2"`
	CarbonChangeRate      *float64                        `json:"carbonChangeRate,omitempty"`
	TotalTerminals        int                             `json:"totalTerminals"`
	OnlineTerminals       int                             `json:"onlineTerminals"`
	TerminalOnlineRate    float64                         `json:"terminalOnlineRate"`
	Domains               []analyticsDomainOverview       `json:"domains"`
	ByDeviceType          []analyticsTypeEnergySummary    `json:"byDeviceType"`
	ByGateway             []analyticsGatewayEnergySummary `json:"byGateway"`
	TopTerminals          []analyticsTerminalEnergyRank   `json:"topTerminals"`
	SankeyNodes           []analyticsSankeyNode           `json:"sankeyNodes"`
	SankeyLinks           []analyticsSankeyLink           `json:"sankeyLinks"`
	AlarmSummary          analyticsOverviewAlarmSummary   `json:"alarmSummary"`
}

// inferOverviewGranularity 根据时间跨度自动选择快照聚合档位。
func inferOverviewGranularity(rawGran string, dur time.Duration) (string, bool) {
	g := strings.TrimSpace(rawGran)
	if g != "" {
		switch g {
		case "hour", "day", "month", "year":
			return g, true
		default:
			return "", false
		}
	}
	switch {
	case dur <= 48*time.Hour:
		return "hour", true
	case dur <= 62*24*time.Hour:
		return "day", true
	case dur <= 730*24*time.Hour:
		return "month", true
	default:
		return "year", true
	}
}

// analyticsOverview 处理 GET /admin/v1/analytics/overview（4.5 能耗总览 & 4.6.1 能流图 & 4.6.6 分类分项）。
func (a *API) analyticsOverview(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, to, err := parseAnalyticsWindow(q.Get("from"), q.Get("to"), analyticsOverviewSpan, "overview")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	granularity, ok := inferOverviewGranularity(q.Get("granularity"), to.Sub(from))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_query: granularity 只能是 hour/day/month/year")
		return
	}

	dur := to.Sub(from)
	prevFrom := from.Add(-dur)
	prevTo := from
	ctx := r.Context()

	// 1) 加载全部物模型、所属数据域与主计量属性。
	type typeMetaInfo struct {
		typeID       int64
		typeCode     string
		typeName     string
		domainID     int64
		domainCode   string
		domainName   string
		primaryCode  string
		primaryName  string
		unit         string
		coalFactor   float64
		carbonFactor float64
		bestScore    int
	}
	typeRows, err := a.Pool.Query(ctx, `
		select dt.id, dt.code, coalesce(dt.name, ''),
		       coalesce(dd.id, 0), coalesce(dd.code, 'default'), coalesce(dd.name, '综合用能'),
		       coalesce(p.item_code, ''), coalesce(p.name, ''), coalesce(p.unit, '')
		from device_type dt
		left join data_domain dd on dd.id = dt.domain_id
		left join thing_model_property p on p.type_id = dt.id
		order by dt.id asc, p.item_code asc`)
	if err != nil {
		writeDBError(w, err)
		return
	}
	typesByID := map[int64]*typeMetaInfo{}
	var typeOrder []int64
	for typeRows.Next() {
		var (
			tid, did                   int64
			tCode, tName, dCode, dName string
			iCode, iName, unit         string
		)
		if err := typeRows.Scan(&tid, &tCode, &tName, &did, &dCode, &dName, &iCode, &iName, &unit); err != nil {
			typeRows.Close()
			writeDBError(w, err)
			return
		}
		info, exists := typesByID[tid]
		if !exists {
			coalF, carbonF, stdU := domainEnergyFactors(dCode, dName, tCode, tName, unit)
			info = &typeMetaInfo{
				typeID:       tid,
				typeCode:     tCode,
				typeName:     tName,
				domainID:     did,
				domainCode:   dCode,
				domainName:   dName,
				primaryCode:  iCode,
				primaryName:  iName,
				unit:         stdU,
				coalFactor:   coalF,
				carbonFactor: carbonF,
				bestScore:    -999,
			}
			if iCode != "" {
				info.bestScore = isPrimaryMeteringProperty(iCode, iName, unit)
			}
			typesByID[tid] = info
			typeOrder = append(typeOrder, tid)
		} else if iCode != "" {
			score := isPrimaryMeteringProperty(iCode, iName, unit)
			if score > info.bestScore {
				info.bestScore = score
				info.primaryCode = iCode
				info.primaryName = iName
				coalF, carbonF, stdU := domainEnergyFactors(dCode, dName, tCode, tName, unit)
				info.unit = stdU
				info.coalFactor = coalF
				info.carbonFactor = carbonF
			}
		}
	}
	typeRows.Close()
	if err := typeRows.Err(); err != nil {
		writeDBError(w, err)
		return
	}

	// 2) 加载所有网关与终端信息。
	gwNames := map[string]string{}
	gwOnline := map[string]bool{}
	gwRows, err := a.Pool.Query(ctx, `select gateway_id, coalesce(name, ''), online from gateway order by gateway_id asc`)
	if err != nil {
		writeDBError(w, err)
		return
	}
	for gwRows.Next() {
		var gid, gname string
		var on bool
		if err := gwRows.Scan(&gid, &gname, &on); err != nil {
			gwRows.Close()
			writeDBError(w, err)
			return
		}
		gwNames[gid] = gname
		gwOnline[gid] = on
	}
	gwRows.Close()

	type termMetaInfo struct {
		id        string
		name      string
		typeID    int64
		gatewayID string
		online    bool
		enabled   bool
	}
	termMap := map[string]*termMetaInfo{}
	var termOrder []string
	var totalTerminals, onlineTerminals, offlineTerminals int
	tRows, err := a.Pool.Query(ctx, `
		select id, coalesce(name, ''), type_id, gateway_id, online, enabled
		from terminal
		where deleted_at is null
		order by id asc`)
	if err != nil {
		writeDBError(w, err)
		return
	}
	for tRows.Next() {
		var tm termMetaInfo
		if err := tRows.Scan(&tm.id, &tm.name, &tm.typeID, &tm.gatewayID, &tm.online, &tm.enabled); err != nil {
			tRows.Close()
			writeDBError(w, err)
			return
		}
		termMap[tm.id] = &tm
		termOrder = append(termOrder, tm.id)
		totalTerminals++
		if tm.enabled && tm.online {
			onlineTerminals++
		} else if tm.enabled && !tm.online {
			offlineTerminals++
		}
	}
	tRows.Close()

	// 3) 查询 [prevFrom, to] 区间内的快照点，按 (terminal_id, item_code) 过滤各类型的主计量属性。
	type rawSlotPt struct {
		ts  time.Time
		num float64
	}
	curPtsByTerm := map[string][]rawSlotPt{}
	prevPtsByTerm := map[string][]rawSlotPt{}
	baselineByTerm := map[string]*float64{}

	snapRows, err := a.Pool.Query(ctx, `
		select s.terminal_id, s.item_code, s.segment_start, s.value_num
		from snapshot_coarse s
		where s.granularity = $1
		  and s.segment_start >= $2 and s.segment_start <= $3
		  and s.value_num is not null
		order by s.terminal_id asc, s.segment_start asc`,
		granularity, prevFrom, to)
	if err != nil {
		writeDBError(w, err)
		return
	}
	totalSnapRows := 0
	for snapRows.Next() {
		var (
			tid, icode string
			ts         time.Time
			val        float64
		)
		if err := snapRows.Scan(&tid, &icode, &ts, &val); err != nil {
			snapRows.Close()
			writeDBError(w, err)
			return
		}
		tm, ok := termMap[tid]
		if !ok {
			continue
		}
		tInfo, ok := typesByID[tm.typeID]
		if !ok {
			continue
		}
		if tInfo.primaryCode != "" && icode != tInfo.primaryCode {
			continue
		}
		totalSnapRows++
		if ts.Before(from) {
			prevPtsByTerm[tid] = append(prevPtsByTerm[tid], rawSlotPt{ts: ts, num: val})
			v := val
			baselineByTerm[tid] = &v
		} else {
			curPtsByTerm[tid] = append(curPtsByTerm[tid], rawSlotPt{ts: ts, num: val})
		}
	}
	snapRows.Close()

	// 若粗快照表尚无数据，回退用接入流水 raw_item 聚合，保证开发环境仅有流水时总览同样可用。
	if totalSnapRows == 0 {
		rawFallbackRows, rErr := a.Pool.Query(ctx, `
			select r.terminal_id, r.item_code, coalesce(r.value_time, r.received_at) as ts, r.value_num
			from raw_item r
			where r.status = 'written'
			  and r.received_at >= $1 and r.received_at <= $2
			  and r.value_num is not null
			order by r.terminal_id asc, ts asc
			limit 10000`,
			prevFrom, to)
		if rErr == nil {
			for rawFallbackRows.Next() {
				var (
					tid, icode string
					ts         time.Time
					val        float64
				)
				if err := rawFallbackRows.Scan(&tid, &icode, &ts, &val); err == nil {
					tm, ok := termMap[tid]
					if !ok {
						continue
					}
					tInfo, ok := typesByID[tm.typeID]
					if !ok || (tInfo.primaryCode != "" && icode != tInfo.primaryCode) {
						continue
					}
					if ts.Before(from) {
						prevPtsByTerm[tid] = append(prevPtsByTerm[tid], rawSlotPt{ts: ts, num: val})
						v := val
						baselineByTerm[tid] = &v
					} else {
						curPtsByTerm[tid] = append(curPtsByTerm[tid], rawSlotPt{ts: ts, num: val})
					}
				}
			}
			rawFallbackRows.Close()
		}
	}

	// 4) 计算每台终端在当期与上期的用能量(Δ)、分桶趋势及异常计数。
	type termEnergyCalc struct {
		curUsage     float64
		prevUsage    float64
		firstReading *float64
		lastReading  *float64
		curBuckets   map[string]float64
		prevBuckets  map[string]float64
	}
	calcByTerm := map[string]*termEnergyCalc{}
	var spike50PctCount, rollbackCount int

	for _, tid := range termOrder {
		calc := &termEnergyCalc{
			curBuckets:  map[string]float64{},
			prevBuckets: map[string]float64{},
		}
		// 上期计算
		prevRaw := prevPtsByTerm[tid]
		if len(prevRaw) > 0 {
			pts := make([]analyticsPoint, len(prevRaw))
			for i, p := range prevRaw {
				v := p.num
				pts[i] = analyticsPoint{Time: p.ts.UTC().Format(time.RFC3339), Num: &v}
			}
			pts = enrichPointsWithDelta(pts, nil)
			st := computeSeriesStats(pts)
			if st.UsageSum != nil {
				calc.prevUsage = *st.UsageSum
			}
			for _, pt := range pts {
				if pt.Delta != nil {
					calc.prevBuckets[pt.Time] += *pt.Delta
				}
			}
		}
		// 当期计算
		curRaw := curPtsByTerm[tid]
		if len(curRaw) > 0 {
			pts := make([]analyticsPoint, len(curRaw))
			for i, p := range curRaw {
				v := p.num
				pts[i] = analyticsPoint{Time: p.ts.UTC().Format(time.RFC3339), Num: &v}
			}
			pts = enrichPointsWithDelta(pts, baselineByTerm[tid])
			st := computeSeriesStats(pts)
			calc.firstReading = st.First
			calc.lastReading = st.Last
			if st.UsageSum != nil {
				calc.curUsage = *st.UsageSum
			}
			spike50PctCount += st.SpikeCount
			rollbackCount += st.RollbackCount
			for _, pt := range pts {
				if pt.Delta != nil {
					calc.curBuckets[pt.Time] += *pt.Delta
				}
			}
		}
		calcByTerm[tid] = calc
	}

	// 5) 聚合：按数据域（Domains）、按物模型（ByDeviceType）、按网关（ByGateway）、Top 终端排名。
	domainMap := map[int64]*analyticsDomainOverview{}
	domainCurBuckets := map[int64]map[string]float64{}
	domainPrevBuckets := map[int64][]float64{}
	var domainOrder []int64

	typeSummaryMap := map[int64]*analyticsTypeEnergySummary{}
	for _, tid := range typeOrder {
		tInfo := typesByID[tid]
		if _, ok := domainMap[tInfo.domainID]; !ok {
			domainMap[tInfo.domainID] = &analyticsDomainOverview{
				DomainID:     tInfo.domainID,
				DomainCode:   tInfo.domainCode,
				DomainName:   tInfo.domainName,
				Unit:         tInfo.unit,
				CoalFactor:   tInfo.coalFactor,
				CarbonFactor: tInfo.carbonFactor,
				Buckets:      make([]analyticsOverviewBucket, 0),
			}
			domainCurBuckets[tInfo.domainID] = map[string]float64{}
			domainOrder = append(domainOrder, tInfo.domainID)
		}
		typeSummaryMap[tid] = &analyticsTypeEnergySummary{
			TypeID:          tid,
			TypeCode:        tInfo.typeCode,
			TypeName:        tInfo.typeName,
			DomainName:      tInfo.domainName,
			PrimaryItemCode: tInfo.primaryCode,
			PrimaryItemName: tInfo.primaryName,
			Unit:            tInfo.unit,
		}
	}

	gwSummaryMap := map[string]*analyticsGatewayEnergySummary{}
	var gwOrder []string
	var allRank []analyticsTerminalEnergyRank

	var totalCoal, prevCoal, totalCarbon, prevCarbon float64

	for _, tid := range termOrder {
		tm := termMap[tid]
		tInfo, ok := typesByID[tm.typeID]
		if !ok {
			continue
		}
		calc := calcByTerm[tid]
		coalVal := calc.curUsage * tInfo.coalFactor
		prevCoalVal := calc.prevUsage * tInfo.coalFactor
		carbonVal := calc.curUsage * tInfo.carbonFactor
		prevCarbonVal := calc.prevUsage * tInfo.carbonFactor

		totalCoal += coalVal
		prevCoal += prevCoalVal
		totalCarbon += carbonVal
		prevCarbon += prevCarbonVal

		// Domain 累加
		dOv := domainMap[tInfo.domainID]
		dOv.TerminalCount++
		if tm.enabled && tm.online {
			dOv.OnlineCount++
		}
		dOv.CurrentUsage += calc.curUsage
		dOv.PrevUsage += calc.prevUsage
		dOv.StandardCoalKgce += coalVal
		dOv.CarbonKgCO2 += carbonVal
		for bTime, bVal := range calc.curBuckets {
			domainCurBuckets[tInfo.domainID][bTime] += bVal
		}

		// DeviceType 累加
		tSum := typeSummaryMap[tm.typeID]
		tSum.TerminalCount++
		if tm.enabled && tm.online {
			tSum.OnlineCount++
		}
		tSum.CurrentUsage += calc.curUsage
		tSum.PrevUsage += calc.prevUsage
		tSum.StandardCoalKgce += coalVal

		// Gateway 累加
		gwSum, gExists := gwSummaryMap[tm.gatewayID]
		if !gExists {
			gwSum = &analyticsGatewayEnergySummary{
				GatewayID:   tm.gatewayID,
				GatewayName: gwNames[tm.gatewayID],
				Online:      gwOnline[tm.gatewayID],
			}
			gwSummaryMap[tm.gatewayID] = gwSum
			gwOrder = append(gwOrder, tm.gatewayID)
		}
		gwSum.TerminalCount++
		if tm.enabled && tm.online {
			gwSum.OnlineCount++
		}
		gwSum.CurrentUsage += calc.curUsage
		gwSum.StandardCoalKgce += coalVal

		// Terminal Rank
		rankRow := analyticsTerminalEnergyRank{
			TerminalID:       tm.id,
			TerminalName:     tm.name,
			GatewayID:        tm.gatewayID,
			TypeID:           tm.typeID,
			TypeName:         tInfo.typeName,
			DomainName:       tInfo.domainName,
			ItemCode:         tInfo.primaryCode,
			Unit:             tInfo.unit,
			Online:           tm.online,
			FirstReading:     calc.firstReading,
			LastReading:      calc.lastReading,
			Usage:            calc.curUsage,
			PrevUsage:        calc.prevUsage,
			StandardCoalKgce: coalVal,
		}
		if calc.prevUsage > 1e-9 {
			rate := (calc.curUsage - calc.prevUsage) / calc.prevUsage * 100.0
			rankRow.ChangeRate = &rate
		}
		allRank = append(allRank, rankRow)
	}
	_ = domainPrevBuckets

	// 计算各维度占比与环比增长率
	domains := make([]analyticsDomainOverview, 0, len(domainOrder))
	for _, did := range domainOrder {
		d := domainMap[did]
		if d.PrevUsage > 1e-9 {
			rate := (d.CurrentUsage - d.PrevUsage) / d.PrevUsage * 100.0
			d.ChangeRate = &rate
		}
		if totalCoal > 1e-9 {
			d.SharePercent = d.StandardCoalKgce / totalCoal * 100.0
		}
		var bKeys []string
		for k := range domainCurBuckets[did] {
			bKeys = append(bKeys, k)
		}
		sort.Strings(bKeys)
		for _, k := range bKeys {
			d.Buckets = append(d.Buckets, analyticsOverviewBucket{
				Time:       k,
				CurrentVal: domainCurBuckets[did][k],
			})
		}
		domains = append(domains, *d)
	}

	byDeviceType := make([]analyticsTypeEnergySummary, 0, len(typeOrder))
	for _, tid := range typeOrder {
		ts := typeSummaryMap[tid]
		if ts.PrevUsage > 1e-9 {
			rate := (ts.CurrentUsage - ts.PrevUsage) / ts.PrevUsage * 100.0
			ts.ChangeRate = &rate
		}
		if totalCoal > 1e-9 {
			ts.SharePercent = ts.StandardCoalKgce / totalCoal * 100.0
		}
		byDeviceType = append(byDeviceType, *ts)
	}

	byGateway := make([]analyticsGatewayEnergySummary, 0, len(gwOrder))
	for _, gid := range gwOrder {
		gs := gwSummaryMap[gid]
		if totalCoal > 1e-9 {
			gs.SharePercent = gs.StandardCoalKgce / totalCoal * 100.0
		}
		byGateway = append(byGateway, *gs)
	}

	sort.SliceStable(allRank, func(i, j int) bool {
		if math.Abs(allRank[i].StandardCoalKgce-allRank[j].StandardCoalKgce) > 1e-9 {
			return allRank[i].StandardCoalKgce > allRank[j].StandardCoalKgce
		}
		return allRank[i].TerminalID < allRank[j].TerminalID
	})
	for i := range allRank {
		if totalCoal > 1e-9 {
			allRank[i].SharePercent = allRank[i].StandardCoalKgce / totalCoal * 100.0
		}
	}
	topTerminals := allRank
	if len(topTerminals) > 15 {
		topTerminals = topTerminals[:15]
	}

	// 6) 构建桑基能流图节点与流量边（4.6.1 能流图：数据域 -> 物模型 -> 网关 -> 终端）。
	sankeyNodes := make([]analyticsSankeyNode, 0, 32)
	sankeyLinks := make([]analyticsSankeyLink, 0, 48)
	addedNode := map[string]bool{}
	addNode := func(id, name, cat string, val, rawVal float64, unit string) {
		if addedNode[id] {
			return
		}
		addedNode[id] = true
		sankeyNodes = append(sankeyNodes, analyticsSankeyNode{
			ID:       id,
			Name:     name,
			Category: cat,
			Value:    val,
			RawValue: rawVal,
			Unit:     unit,
		})
	}

	for _, d := range domains {
		dNodeID := fmt.Sprintf("domain:%d", d.DomainID)
		addNode(dNodeID, d.DomainName, "domain", d.StandardCoalKgce, d.CurrentUsage, d.Unit)
	}
	for _, tid := range typeOrder {
		tInfo := typesByID[tid]
		tSum := typeSummaryMap[tid]
		dNodeID := fmt.Sprintf("domain:%d", tInfo.domainID)
		tNodeID := fmt.Sprintf("type:%d", tid)
		addNode(tNodeID, tInfo.typeName, "device_type", tSum.StandardCoalKgce, tSum.CurrentUsage, tInfo.unit)
		sankeyLinks = append(sankeyLinks, analyticsSankeyLink{
			Source:   dNodeID,
			Target:   tNodeID,
			Value:    tSum.StandardCoalKgce,
			RawValue: tSum.CurrentUsage,
			Unit:     tInfo.unit,
		})
	}
	// Type -> Gateway 聚合流
	typeGwKey := struct {
		tid int64
		gid string
	}{}
	_ = typeGwKey
	type tgPair struct {
		tid int64
		gid string
	}
	tgCoal := map[tgPair]float64{}
	tgRaw := map[tgPair]float64{}
	for _, tid := range termOrder {
		tm := termMap[tid]
		tInfo, ok := typesByID[tm.typeID]
		if !ok {
			continue
		}
		calc := calcByTerm[tid]
		pair := tgPair{tid: tm.typeID, gid: tm.gatewayID}
		tgCoal[pair] += calc.curUsage * tInfo.coalFactor
		tgRaw[pair] += calc.curUsage
	}
	for _, gid := range gwOrder {
		gs := gwSummaryMap[gid]
		gName := gs.GatewayName
		if gName == "" {
			gName = gid
		}
		addNode("gw:"+gid, gName, "gateway", gs.StandardCoalKgce, gs.CurrentUsage, "kgce")
	}
	for _, tid := range typeOrder {
		tInfo := typesByID[tid]
		for _, gid := range gwOrder {
			pair := tgPair{tid: tid, gid: gid}
			if raw, ok := tgRaw[pair]; ok {
				sankeyLinks = append(sankeyLinks, analyticsSankeyLink{
					Source:   fmt.Sprintf("type:%d", tid),
					Target:   "gw:" + gid,
					Value:    tgCoal[pair],
					RawValue: raw,
					Unit:     tInfo.unit,
				})
			}
		}
	}
	for _, tr := range topTerminals {
		tName := tr.TerminalName
		if tName == "" {
			tName = tr.TerminalID
		}
		termNodeID := "term:" + tr.TerminalID
		addNode(termNodeID, tName, "terminal", tr.StandardCoalKgce, tr.Usage, tr.Unit)
		if tr.GatewayID != "" {
			sankeyLinks = append(sankeyLinks, analyticsSankeyLink{
				Source:   "gw:" + tr.GatewayID,
				Target:   termNodeID,
				Value:    tr.StandardCoalKgce,
				RawValue: tr.Usage,
				Unit:     tr.Unit,
			})
		}
	}

	// 7) 查询时间窗内的质量异常总数。
	var qualityIssueCount int64
	_ = a.Pool.QueryRow(ctx, `
		select coalesce(sum(count), 0)
		from exception_count
		where hour_start >= $1 and hour_start <= $2`,
		from, to).Scan(&qualityIssueCount)

	var coalChangeRate, carbonChangeRate *float64
	if prevCoal > 1e-9 {
		r := (totalCoal - prevCoal) / prevCoal * 100.0
		coalChangeRate = &r
	}
	if prevCarbon > 1e-9 {
		r := (totalCarbon - prevCarbon) / prevCarbon * 100.0
		carbonChangeRate = &r
	}
	var onlineRate float64
	if totalTerminals > 0 {
		onlineRate = float64(onlineTerminals) / float64(totalTerminals) * 100.0
	}

	writeJSON(w, http.StatusOK, Response{
		Code:    0,
		Message: "ok",
		Data: analyticsOverviewPayload{
			From:                  from.UTC().Format(time.RFC3339),
			To:                    to.UTC().Format(time.RFC3339),
			PrevFrom:              prevFrom.UTC().Format(time.RFC3339),
			PrevTo:                prevTo.UTC().Format(time.RFC3339),
			Granularity:           granularity,
			TotalStandardCoalKgce: totalCoal,
			PrevStandardCoalKgce:  prevCoal,
			CoalChangeRate:        coalChangeRate,
			TotalCarbonKgCO2:      totalCarbon,
			PrevCarbonKgCO2:       prevCarbon,
			CarbonChangeRate:      carbonChangeRate,
			TotalTerminals:        totalTerminals,
			OnlineTerminals:       onlineTerminals,
			TerminalOnlineRate:    onlineRate,
			Domains:               domains,
			ByDeviceType:          byDeviceType,
			ByGateway:             byGateway,
			TopTerminals:          topTerminals,
			SankeyNodes:           sankeyNodes,
			SankeyLinks:           sankeyLinks,
			AlarmSummary: analyticsOverviewAlarmSummary{
				Spike50PctCount:      spike50PctCount,
				RollbackCount:        rollbackCount,
				QualityIssueCount:    qualityIssueCount,
				OfflineTerminalCount: offlineTerminals,
			},
		},
	})
}

// ── 1c. 用能异常监测与总分表跑冒滴漏平衡分析（4.11 告警设置 & 4.12 告警监测 & 4.6.2 能源平衡） ──

type analyticsEnergyAlarmSummary struct {
	OverLimitCount       int `json:"overLimitCount"`       // 4.11.1 能耗超标（日/周期累计超上限）
	Spike50PctCount      int `json:"spike50PctCount"`      // 4.6.3  同点超均值 50% 用能突增
	OffHoursAnomalyCount int `json:"offHoursAnomalyCount"` // 4.11.1 能耗异常（非工作时段违规用能）
	LeakageAnomalyCount  int `json:"leakageAnomalyCount"`  // 4.11.1 跑冒滴漏（总分表差额超限）
	RollbackCount        int `json:"rollbackCount"`        // 表码倒走 / 换表重置
	RangeOutCount        int `json:"rangeOutCount"`        // 物模型上下限越界
}

type analyticsEnergyAlarmEvent struct {
	AlarmType     string  `json:"alarmType"` // over_limit | spike_50pct | off_hours | leakage | meter_rollback | range_out
	Label         string  `json:"label"`
	Tone          string  `json:"tone"` // danger | warn | info
	TerminalID    string  `json:"terminalId"`
	TerminalName  string  `json:"terminalName"`
	GatewayID     string  `json:"gatewayId"`
	TypeName      string  `json:"typeName"`
	ItemCode      string  `json:"itemCode"`
	ItemName      string  `json:"itemName"`
	Unit          string  `json:"unit"`
	Time          string  `json:"time"`
	ActualValue   float64 `json:"actualValue"`
	BaselineValue float64 `json:"baselineValue"`
	DeviationPct  float64 `json:"deviationPct"`
	Detail        string  `json:"detail"`
}

type analyticsBalanceBucket struct {
	Time          string  `json:"time"`
	MasterUsage   float64 `json:"masterUsage"`
	SubUsageSum   float64 `json:"subUsageSum"`
	DiffUsage     float64 `json:"diffUsage"`
	DiffPercent   float64 `json:"diffPercent"`
	LeakageAlarm  bool    `json:"leakageAlarm"`
}

type analyticsBalanceResult struct {
	MasterTerminalID   string                   `json:"masterTerminalId"`
	MasterTerminalName string                   `json:"masterTerminalName"`
	SubTerminalIDs     []string                 `json:"subTerminalIds"`
	Unit               string                   `json:"unit"`
	ThresholdPct       float64                  `json:"thresholdPct"`
	MasterTotalUsage   float64                  `json:"masterTotalUsage"`
	SubTotalUsage      float64                  `json:"subTotalUsage"`
	TotalDiffUsage     float64                  `json:"totalDiffUsage"`
	TotalDiffPercent   float64                  `json:"totalDiffPercent"`
	AlarmSlotCount     int                      `json:"alarmSlotCount"`
	Buckets            []analyticsBalanceBucket `json:"buckets"`
}

type analyticsAnomaliesPayload struct {
	From            string                      `json:"from"`
	To              string                      `json:"to"`
	Granularity     string                      `json:"granularity"`
	OffHoursSpec    string                      `json:"offHoursSpec"`
	Summary         analyticsEnergyAlarmSummary `json:"summary"`
	Events          []analyticsEnergyAlarmEvent `json:"events"`
	BalanceAnalysis *analyticsBalanceResult     `json:"balanceAnalysis,omitempty"`
}

// analyticsAnomalies 处理 GET /admin/v1/analytics/anomalies。
func (a *API) analyticsAnomalies(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, to, err := parseAnalyticsWindow(q.Get("from"), q.Get("to"), analyticsOverviewSpan, "anomalies")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	granularity := strings.TrimSpace(q.Get("granularity"))
	if granularity == "" {
		granularity = "hour"
	}
	if granularity != "hour" && granularity != "day" && granularity != "month" {
		writeError(w, http.StatusBadRequest, "invalid_query: granularity 只能是 hour/day/month")
		return
	}

	var typeFilter int64
	if rawType := strings.TrimSpace(q.Get("type_id")); rawType != "" {
		parsed, err := strconv.ParseInt(rawType, 10, 64)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_query: type_id 必须是正整数")
			return
		}
		typeFilter = parsed
	}
	gatewayFilter := strings.TrimSpace(q.Get("gateway_id"))
	itemFilter := strings.TrimSpace(q.Get("item_code"))

	var dailyLimit float64
	if rawLimit := strings.TrimSpace(q.Get("daily_limit")); rawLimit != "" {
		if v, err := strconv.ParseFloat(rawLimit, 64); err == nil && v > 0 {
			dailyLimit = v
		}
	}
	var offHoursLimit float64
	if rawOffLimit := strings.TrimSpace(q.Get("off_hours_limit")); rawOffLimit != "" {
		if v, err := strconv.ParseFloat(rawOffLimit, 64); err == nil && v > 0 {
			offHoursLimit = v
		}
	}
	offHoursSpec := strings.TrimSpace(q.Get("off_hours"))
	if offHoursSpec == "" {
		offHoursSpec = "23:00-07:00,12:00-13:30"
	}
	offRanges := parseOffHoursRanges(offHoursSpec)

	leakageThresholdPct := 5.0
	if rawLeak := strings.TrimSpace(q.Get("leakage_threshold_pct")); rawLeak != "" {
		if v, err := strconv.ParseFloat(rawLeak, 64); err == nil && v >= 0 {
			leakageThresholdPct = v
		}
	}
	masterTermID := strings.TrimSpace(q.Get("master_terminal_id"))
	var subTermIDs []string
	if rawSubs := strings.TrimSpace(q.Get("sub_terminal_ids")); rawSubs != "" {
		for _, p := range strings.Split(rawSubs, ",") {
			if id := strings.TrimSpace(p); id != "" && id != masterTermID {
				subTermIDs = append(subTermIDs, id)
			}
		}
	}

	ctx := r.Context()

	// 1) 加载终端及物模型属性定义。
	type propDef struct {
		typeID   int64
		typeName string
		itemCode string
		itemName string
		unit     string
		minVal   *float64
		maxVal   *float64
		score    int
	}
	propRows, err := a.Pool.Query(ctx, `
		select p.type_id, coalesce(dt.name, ''), p.item_code, coalesce(p.name, ''),
		       coalesce(p.unit, ''), p.min_value, p.max_value
		from thing_model_property p
		join device_type dt on dt.id = p.type_id
		where ($1 = 0::bigint or p.type_id = $1)
		  and ($2 = '' or p.item_code = $2)
		order by p.type_id asc, p.item_code asc`,
		typeFilter, itemFilter)
	if err != nil {
		writeDBError(w, err)
		return
	}
	primaryPropByType := map[int64]propDef{}
	propByKey := map[string]propDef{}
	for propRows.Next() {
		var pd propDef
		if err := propRows.Scan(&pd.typeID, &pd.typeName, &pd.itemCode, &pd.itemName, &pd.unit, &pd.minVal, &pd.maxVal); err != nil {
			propRows.Close()
			writeDBError(w, err)
			return
		}
		pd.score = isPrimaryMeteringProperty(pd.itemCode, pd.itemName, pd.unit)
		key := fmt.Sprintf("%d:%s", pd.typeID, pd.itemCode)
		propByKey[key] = pd
		if cur, exists := primaryPropByType[pd.typeID]; !exists || pd.score > cur.score {
			primaryPropByType[pd.typeID] = pd
		}
	}
	propRows.Close()

	type termInfo struct {
		id        string
		name      string
		typeID    int64
		typeName  string
		gatewayID string
	}
	termRows, err := a.Pool.Query(ctx, `
		select t.id, coalesce(t.name, ''), t.type_id, coalesce(dt.name, ''), t.gateway_id
		from terminal t
		left join device_type dt on dt.id = t.type_id
		where t.deleted_at is null
		  and ($1 = 0::bigint or t.type_id = $1)
		  and ($2 = '' or t.gateway_id = $2)
		order by t.id asc`,
		typeFilter, gatewayFilter)
	if err != nil {
		writeDBError(w, err)
		return
	}
	termsByID := map[string]termInfo{}
	var termIDs []string
	for termRows.Next() {
		var ti termInfo
		if err := termRows.Scan(&ti.id, &ti.name, &ti.typeID, &ti.typeName, &ti.gatewayID); err != nil {
			termRows.Close()
			writeDBError(w, err)
			return
		}
		termsByID[ti.id] = ti
		termIDs = append(termIDs, ti.id)
	}
	termRows.Close()

	var (
		summary analyticsEnergyAlarmSummary
		events  = make([]analyticsEnergyAlarmEvent, 0, 32)
	)

	// 2) 拉取快照序列并执行五类异常检测。
	ptsByTerm := map[string][]analyticsPoint{}
	if len(termIDs) > 0 {
		snapRows, sErr := a.Pool.Query(ctx, `
			select terminal_id, item_code, segment_start, value_num, quality, coalesce(quality_reason, '')
			from snapshot_coarse
			where granularity = $1
			  and terminal_id = any($2::text[])
			  and segment_start >= $3 and segment_start <= $4
			  and value_num is not null
			order by terminal_id asc, segment_start asc
			limit 10000`,
			granularity, termIDs, from, to)
		if sErr != nil {
			writeDBError(w, sErr)
			return
		}
		for snapRows.Next() {
			var (
				tid, icode, qReason string
				ts                  time.Time
				val                 float64
				qual                int16
			)
			if err := snapRows.Scan(&tid, &icode, &ts, &val, &qual, &qReason); err != nil {
				snapRows.Close()
				writeDBError(w, err)
				return
			}
			ti, ok := termsByID[tid]
			if !ok {
				continue
			}
			if itemFilter != "" {
				if icode != itemFilter {
					continue
				}
			} else if prim, ok := primaryPropByType[ti.typeID]; ok && prim.itemCode != "" && icode != prim.itemCode {
				continue
			}
			v := val
			ptsByTerm[tid] = append(ptsByTerm[tid], analyticsPoint{
				Time:          ts.UTC().Format(time.RFC3339),
				Num:           &v,
				Quality:       qual,
				QualityReason: qReason,
			})
		}
		snapRows.Close()
	}

	// 计算车队平均总用量（当未显式传 daily_limit 时，以 1.8 倍车队均值作为默认超标线）
	var fleetUsageSum float64
	var fleetActiveTerms int
	statsByTerm := map[string]analyticsSeriesStats{}
	for _, tid := range termIDs {
		pts := enrichPointsWithDelta(ptsByTerm[tid], nil)
		ptsByTerm[tid] = pts
		st := computeSeriesStats(pts)
		statsByTerm[tid] = st
		if st.UsageSum != nil && *st.UsageSum > 0 {
			fleetUsageSum += *st.UsageSum
			fleetActiveTerms++
		}
	}
	effectiveOverLimit := dailyLimit
	if effectiveOverLimit <= 0 && fleetActiveTerms > 0 {
		effectiveOverLimit = (fleetUsageSum / float64(fleetActiveTerms)) * 1.8
	}

	for _, tid := range termIDs {
		ti := termsByID[tid]
		pts := ptsByTerm[tid]
		st := statsByTerm[tid]
		pd := primaryPropByType[ti.typeID]
		if itemFilter != "" {
			if customPd, ok := propByKey[fmt.Sprintf("%d:%s", ti.typeID, itemFilter)]; ok {
				pd = customPd
			}
		}

		// (a) 4.11.1 能耗超标检测（周期/日累计用量 > 上限阈值）
		if effectiveOverLimit > 0 && st.UsageSum != nil && *st.UsageSum > effectiveOverLimit {
			dev := (*st.UsageSum - effectiveOverLimit) / effectiveOverLimit * 100.0
			summary.OverLimitCount++
			events = append(events, analyticsEnergyAlarmEvent{
				AlarmType:     "over_limit",
				Label:         "能耗超标告警",
				Tone:          "danger",
				TerminalID:    ti.id,
				TerminalName:  ti.name,
				GatewayID:     ti.gatewayID,
				TypeName:      ti.typeName,
				ItemCode:      pd.itemCode,
				ItemName:      pd.itemName,
				Unit:          pd.unit,
				Time:          st.UsageMaxTime,
				ActualValue:   *st.UsageSum,
				BaselineValue: effectiveOverLimit,
				DeviationPct:  dev,
				Detail:        fmt.Sprintf("区间累计用能 %.2f %s，超过设定上限阈值 %.2f %s (+%.1f%%)", *st.UsageSum, pd.unit, effectiveOverLimit, pd.unit, dev),
			})
		}

		// (b) 4.11.1 非工作时段用能异常（夜间/午休累计用能超标）
		var offHoursSum float64
		var lastOffTime string
		for _, pt := range pts {
			if pt.Delta != nil && *pt.Delta > 0 {
				if ts, err := time.Parse(time.RFC3339, pt.Time); err == nil && isTimeInOffHours(ts, offRanges) {
					offHoursSum += *pt.Delta
					lastOffTime = pt.Time
				}
			}
		}
		effOffLimit := offHoursLimit
		if effOffLimit <= 0 && st.UsageSum != nil && *st.UsageSum > 0 {
			effOffLimit = *st.UsageSum * 0.35
		}
		if effOffLimit > 0 && offHoursSum > effOffLimit {
			dev := (offHoursSum - effOffLimit) / effOffLimit * 100.0
			summary.OffHoursAnomalyCount++
			events = append(events, analyticsEnergyAlarmEvent{
				AlarmType:     "off_hours",
				Label:         "非工作时段用能异常",
				Tone:          "warn",
				TerminalID:    ti.id,
				TerminalName:  ti.name,
				GatewayID:     ti.gatewayID,
				TypeName:      ti.typeName,
				ItemCode:      pd.itemCode,
				ItemName:      pd.itemName,
				Unit:          pd.unit,
				Time:          lastOffTime,
				ActualValue:   offHoursSum,
				BaselineValue: effOffLimit,
				DeviationPct:  dev,
				Detail:        fmt.Sprintf("非工作时段(%s)累计用能 %.2f %s，超出控制基准 %.2f %s (+%.1f%%)", offHoursSpec, offHoursSum, pd.unit, effOffLimit, pd.unit, dev),
			})
		}

		// (c) 逐点检测：4.6.3 同点超均值 50% 突增、表码倒走、物模型上下限越界
		for _, pt := range pts {
			if pt.SpikeAnomaly && pt.Delta != nil && st.UsageAvg != nil && *st.UsageAvg > 0 {
				dev := (*pt.Delta - *st.UsageAvg) / *st.UsageAvg * 100.0
				summary.Spike50PctCount++
				events = append(events, analyticsEnergyAlarmEvent{
					AlarmType:     "spike_50pct",
					Label:         "超均值50%用能突增",
					Tone:          "warn",
					TerminalID:    ti.id,
					TerminalName:  ti.name,
					GatewayID:     ti.gatewayID,
					TypeName:      ti.typeName,
					ItemCode:      pd.itemCode,
					ItemName:      pd.itemName,
					Unit:          pd.unit,
					Time:          pt.Time,
					ActualValue:   *pt.Delta,
					BaselineValue: *st.UsageAvg,
					DeviationPct:  dev,
					Detail:        fmt.Sprintf("时段用能 %.2f %s，超过同期均值 %.2f %s 的 50%% (+%.1f%%)", *pt.Delta, pd.unit, *st.UsageAvg, pd.unit, dev),
				})
			}
			if pt.Rollback && pt.RawDelta != nil {
				summary.RollbackCount++
				events = append(events, analyticsEnergyAlarmEvent{
					AlarmType:     "meter_rollback",
					Label:         "表码倒走/重置异常",
					Tone:          "danger",
					TerminalID:    ti.id,
					TerminalName:  ti.name,
					GatewayID:     ti.gatewayID,
					TypeName:      ti.typeName,
					ItemCode:      pd.itemCode,
					ItemName:      pd.itemName,
					Unit:          pd.unit,
					Time:          pt.Time,
					ActualValue:   *pt.RawDelta,
					BaselineValue: 0,
					DeviationPct:  -100,
					Detail:        fmt.Sprintf("表计读数较上一周期回退 %.2f %s（请核查是否换表或通讯错位）", *pt.RawDelta, pd.unit),
				})
			}
			if pt.Num != nil {
				if pd.maxVal != nil && *pt.Num > *pd.maxVal {
					dev := (*pt.Num - *pd.maxVal) / math.Max(1, math.Abs(*pd.maxVal)) * 100.0
					summary.RangeOutCount++
					events = append(events, analyticsEnergyAlarmEvent{
						AlarmType:     "range_out",
						Label:         "物模型上限越界",
						Tone:          "danger",
						TerminalID:    ti.id,
						TerminalName:  ti.name,
						GatewayID:     ti.gatewayID,
						TypeName:      ti.typeName,
						ItemCode:      pd.itemCode,
						ItemName:      pd.itemName,
						Unit:          pd.unit,
						Time:          pt.Time,
						ActualValue:   *pt.Num,
						BaselineValue: *pd.maxVal,
						DeviationPct:  dev,
						Detail:        fmt.Sprintf("实测读数 %.2f %s 超过物模型上限 %.2f %s", *pt.Num, pd.unit, *pd.maxVal, pd.unit),
					})
				} else if pd.minVal != nil && *pt.Num < *pd.minVal {
					dev := (*pd.minVal - *pt.Num) / math.Max(1, math.Abs(*pd.minVal)) * 100.0
					summary.RangeOutCount++
					events = append(events, analyticsEnergyAlarmEvent{
						AlarmType:     "range_out",
						Label:         "物模型下限越界",
						Tone:          "warn",
						TerminalID:    ti.id,
						TerminalName:  ti.name,
						GatewayID:     ti.gatewayID,
						TypeName:      ti.typeName,
						ItemCode:      pd.itemCode,
						ItemName:      pd.itemName,
						Unit:          pd.unit,
						Time:          pt.Time,
						ActualValue:   *pt.Num,
						BaselineValue: *pd.minVal,
						DeviationPct:  dev,
						Detail:        fmt.Sprintf("实测读数 %.2f %s 低于物模型下限 %.2f %s", *pt.Num, pd.unit, *pd.minVal, pd.unit),
					})
				}
			}
		}
	}

	// 3) 4.11.1 跑冒滴漏 & 4.6.2 能源平衡分析（总表 vs 分表加总差额）。
	// 若未显式指定总分表且当前筛选集有 >=2 台终端，默认以首台终端作为示例总表、其余作为分表。
	if masterTermID == "" && len(termIDs) >= 2 {
		masterTermID = termIDs[0]
		subTermIDs = termIDs[1:]
	}
	var balanceRes *analyticsBalanceResult
	if masterTermID != "" && len(subTermIDs) > 0 {
		mInfo := termsByID[masterTermID]
		pd := primaryPropByType[mInfo.typeID]
		masterSlot := map[string]float64{}
		subSlot := map[string]float64{}
		slotSet := map[string]bool{}

		for _, pt := range ptsByTerm[masterTermID] {
			if pt.Delta != nil {
				masterSlot[pt.Time] += *pt.Delta
				slotSet[pt.Time] = true
			}
		}
		for _, sid := range subTermIDs {
			for _, pt := range ptsByTerm[sid] {
				if pt.Delta != nil {
					subSlot[pt.Time] += *pt.Delta
					slotSet[pt.Time] = true
				}
			}
		}
		var slotKeys []string
		for k := range slotSet {
			slotKeys = append(slotKeys, k)
		}
		sort.Strings(slotKeys)

		buckets := make([]analyticsBalanceBucket, 0, len(slotKeys))
		var mTotal, sTotal float64
		var alarmSlots int
		for _, k := range slotKeys {
			mv := masterSlot[k]
			sv := subSlot[k]
			diff := mv - sv
			var diffPct float64
			if mv > 1e-9 {
				diffPct = diff / mv * 100.0
			} else if sv > 1e-9 {
				diffPct = -100.0
			}
			isAlarm := math.Abs(diffPct) > leakageThresholdPct && math.Abs(diff) > 1e-6
			if isAlarm {
				alarmSlots++
				summary.LeakageAnomalyCount++
				events = append(events, analyticsEnergyAlarmEvent{
					AlarmType:     "leakage",
					Label:         "跑冒滴漏/支路失衡",
					Tone:          "danger",
					TerminalID:    masterTermID,
					TerminalName:  mInfo.name,
					GatewayID:     mInfo.gatewayID,
					TypeName:      mInfo.typeName,
					ItemCode:      pd.itemCode,
					ItemName:      pd.itemName,
					Unit:          pd.unit,
					Time:          k,
					ActualValue:   mv,
					BaselineValue: sv,
					DeviationPct:  diffPct,
					Detail:        fmt.Sprintf("总表用量 %.2f %s，分表合计 %.2f %s，差额 %.2f %s (偏差率 %.1f%% > 阈值 %.1f%%)", mv, pd.unit, sv, pd.unit, diff, pd.unit, diffPct, leakageThresholdPct),
				})
			}
			mTotal += mv
			sTotal += sv
			buckets = append(buckets, analyticsBalanceBucket{
				Time:         k,
				MasterUsage:  mv,
				SubUsageSum:  sv,
				DiffUsage:    diff,
				DiffPercent:  diffPct,
				LeakageAlarm: isAlarm,
			})
		}
		totalDiff := mTotal - sTotal
		var totalDiffPct float64
		if mTotal > 1e-9 {
			totalDiffPct = totalDiff / mTotal * 100.0
		}
		balanceRes = &analyticsBalanceResult{
			MasterTerminalID:   masterTermID,
			MasterTerminalName: mInfo.name,
			SubTerminalIDs:     subTermIDs,
			Unit:               pd.unit,
			ThresholdPct:       leakageThresholdPct,
			MasterTotalUsage:   mTotal,
			SubTotalUsage:      sTotal,
			TotalDiffUsage:     totalDiff,
			TotalDiffPercent:   totalDiffPct,
			AlarmSlotCount:     alarmSlots,
			Buckets:            buckets,
		}
	}

	// 按时间倒序排列告警事件，最多返回 100 条
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].Time != events[j].Time {
			return events[i].Time > events[j].Time
		}
		return events[i].DeviationPct > events[j].DeviationPct
	})
	if len(events) > 100 {
		events = events[:100]
	}

	writeJSON(w, http.StatusOK, Response{
		Code:    0,
		Message: "ok",
		Data: analyticsAnomaliesPayload{
			From:            from.UTC().Format(time.RFC3339),
			To:              to.UTC().Format(time.RFC3339),
			Granularity:     granularity,
			OffHoursSpec:    offHoursSpec,
			Summary:         summary,
			Events:          events,
			BalanceAnalysis: balanceRes,
		},
	})
}

// ── 2. 数据质量与异常分析 ────────────────────────────────────────────────────

type analyticsRawSummary struct {
	TotalItems            int64    `json:"totalItems"`
	WrittenCount          int64    `json:"writtenCount"`
	CleanPassCount        int64    `json:"cleanPassCount"`
	QualityViolationCount int64    `json:"qualityViolationCount"`
	DroppedCount          int64    `json:"droppedCount"`
	IgnoredCount          int64    `json:"ignoredCount"`
	RangeOutCount         int64    `json:"rangeOutCount"`
	RuleHitCount          int64    `json:"ruleHitCount"`
	UnitOutCount          int64    `json:"unitOutCount"`
	PassRate              *float64 `json:"passRate,omitempty"`
	EffectiveWriteRate    *float64 `json:"effectiveWriteRate,omitempty"`
}

type analyticsExceptionDist struct {
	ErrorType     string `json:"errorType"`
	Label         string `json:"label"`
	Tone          string `json:"tone"`
	Count         int64  `json:"count"`
	TerminalCount int64  `json:"terminalCount"`
	LastSeen      string `json:"lastSeen,omitempty"`
}

type analyticsQualityTrendBucket struct {
	Bucket                string `json:"bucket"`
	TotalExceptions       int64  `json:"totalExceptions"`
	QualityViolationCount int64  `json:"qualityViolationCount"`
	CleanFailedCount      int64  `json:"cleanFailedCount"`
	LateDroppedCount      int64  `json:"lateDroppedCount"`
}

type analyticsTopTerminal struct {
	TerminalID        string `json:"terminalId"`
	TerminalName      string `json:"terminalName"`
	GatewayID         string `json:"gatewayId"`
	TypeName          string `json:"typeName"`
	TotalCount        int64  `json:"totalCount"`
	QualityCount      int64  `json:"qualityCount"`
	CleanFailedCount  int64  `json:"cleanFailedCount"`
	LateDroppedCount  int64  `json:"lateDroppedCount"`
	PrimaryErrorType  string `json:"primaryErrorType"`
	PrimaryErrorLabel string `json:"primaryErrorLabel"`
	SampleDigest      string `json:"sampleDigest,omitempty"`
	LastSeen          string `json:"lastSeen,omitempty"`
}

type analyticsTopItem struct {
	ItemCode              string `json:"itemCode"`
	DroppedCount          int64  `json:"droppedCount"`
	QualityViolationCount int64  `json:"qualityViolationCount"`
	TotalIssues           int64  `json:"totalIssues"`
	SampleReason          string `json:"sampleReason,omitempty"`
}

type analyticsQualityPayload struct {
	From                string                        `json:"from"`
	To                  string                        `json:"to"`
	BucketUnit          string                        `json:"bucketUnit"`
	TotalExceptionCount int64                         `json:"totalExceptionCount"`
	AffectedTerminals   int64                         `json:"affectedTerminals"`
	RawSummary          analyticsRawSummary           `json:"rawSummary"`
	ExceptionsByType    []analyticsExceptionDist      `json:"exceptionsByType"`
	Trend               []analyticsQualityTrendBucket `json:"trend"`
	TopTerminals        []analyticsTopTerminal        `json:"topTerminals"`
	TopItems            []analyticsTopItem            `json:"topItems"`
}

// eventTypeMeta 查找异常类型的中文标签与语义色。
func eventTypeMeta(code string) (label, tone string) {
	for _, opt := range eventTypeOptions {
		if opt.Value == code {
			return opt.Label, opt.Tone
		}
	}
	return code, "neutral"
}

// enrichRawSummaryRates 计算合格率与有效入库率百分比。
func enrichRawSummaryRates(s *analyticsRawSummary) {
	if s.TotalItems <= 0 {
		return
	}
	pass := float64(s.CleanPassCount) / float64(s.TotalItems) * 100.0
	written := float64(s.WrittenCount) / float64(s.TotalItems) * 100.0
	s.PassRate = &pass
	s.EffectiveWriteRate = &written
}

// analyticsQuality 处理 GET /admin/v1/analytics/quality。
func (a *API) analyticsQuality(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, to, err := parseAnalyticsWindow(q.Get("from"), q.Get("to"), analyticsMaxWindow, "quality")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	gatewayID := strings.TrimSpace(q.Get("gateway_id"))
	var typeID int64
	if rawType := strings.TrimSpace(q.Get("type_id")); rawType != "" {
		parsed, err := strconv.ParseInt(rawType, 10, 64)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_query: type_id 必须是正整数")
			return
		}
		typeID = parsed
	}

	ctx := r.Context()

	// 1) 接入流水质量汇总（raw_item）。
	var rawSum analyticsRawSummary
	err = a.Pool.QueryRow(ctx, `
		select count(*),
		       count(*) filter (where r.status = 'written'),
		       count(*) filter (where r.status = 'written' and r.quality = 0),
		       count(*) filter (where r.status = 'written' and r.quality > 0),
		       count(*) filter (where r.status = 'dropped'),
		       count(*) filter (where r.status = 'ignored'),
		       count(*) filter (where (r.quality & 1) <> 0),
		       count(*) filter (where (r.quality & 2) <> 0),
		       count(*) filter (where (r.quality & 4) <> 0)
		from raw_item r
		left join terminal t on t.id = r.terminal_id
		where r.received_at >= $1 and r.received_at <= $2
		  and ($3 = '' or t.gateway_id = $3)
		  and ($4 = 0::bigint or t.type_id = $4)`,
		from, to, gatewayID, typeID).Scan(
		&rawSum.TotalItems,
		&rawSum.WrittenCount,
		&rawSum.CleanPassCount,
		&rawSum.QualityViolationCount,
		&rawSum.DroppedCount,
		&rawSum.IgnoredCount,
		&rawSum.RangeOutCount,
		&rawSum.RuleHitCount,
		&rawSum.UnitOutCount,
	)
	if err != nil {
		writeDBError(w, err)
		return
	}
	enrichRawSummaryRates(&rawSum)

	// 2) 异常事件按类型分布（exception_count）。
	distRows, err := a.Pool.Query(ctx, `
		select e.error_type,
		       coalesce(sum(e.count), 0) as total_count,
		       count(distinct e.terminal_id) as term_count,
		       max(e.last_seen) as last_seen
		from exception_count e
		left join terminal t on t.id = e.terminal_id
		where e.hour_start >= $1 and e.hour_start <= $2
		  and ($3 = '' or e.gateway_id = $3 or t.gateway_id = $3)
		  and ($4 = 0::bigint or t.type_id = $4)
		group by e.error_type
		order by total_count desc, e.error_type asc`,
		from, to, gatewayID, typeID)
	if err != nil {
		writeDBError(w, err)
		return
	}
	dist := make([]analyticsExceptionDist, 0, len(eventTypeOptions))
	var totalExceptions int64
	for distRows.Next() {
		var (
			item     analyticsExceptionDist
			lastSeen *time.Time
		)
		if err := distRows.Scan(&item.ErrorType, &item.Count, &item.TerminalCount, &lastSeen); err != nil {
			distRows.Close()
			writeDBError(w, err)
			return
		}
		item.Label, item.Tone = eventTypeMeta(item.ErrorType)
		if lastSeen != nil {
			item.LastSeen = lastSeen.UTC().Format(time.RFC3339)
		}
		totalExceptions += item.Count
		dist = append(dist, item)
	}
	distRows.Close()
	if err := distRows.Err(); err != nil {
		writeDBError(w, err)
		return
	}

	// 受影响的不同终端总数。
	var affectedTerminals int64
	if err := a.Pool.QueryRow(ctx, `
		select count(distinct e.terminal_id)
		from exception_count e
		left join terminal t on t.id = e.terminal_id
		where e.hour_start >= $1 and e.hour_start <= $2
		  and ($3 = '' or e.gateway_id = $3 or t.gateway_id = $3)
		  and ($4 = 0::bigint or t.type_id = $4)`,
		from, to, gatewayID, typeID).Scan(&affectedTerminals); err != nil {
		writeDBError(w, err)
		return
	}

	// 3) 异常趋势时间序列（跨度 <= 7 天按小时，否则按日对齐 Asia/Shanghai）。
	bucketUnit := "hour"
	truncExpr := "e.hour_start"
	if to.Sub(from) > 7*24*time.Hour {
		bucketUnit = "day"
		truncExpr = "date_trunc('day', e.hour_start at time zone 'Asia/Shanghai') at time zone 'Asia/Shanghai'"
	}
	trendSQL := fmt.Sprintf(`
		select %s as bucket,
		       coalesce(sum(e.count), 0) as total_exceptions,
		       coalesce(sum(e.count) filter (where e.error_type = 'quality_violation'), 0) as quality_cnt,
		       coalesce(sum(e.count) filter (where e.error_type in ('clean_failed', 'msg_incomplete', 'protocol_error')), 0) as clean_cnt,
		       coalesce(sum(e.count) filter (where e.error_type = 'snapshot_late_dropped'), 0) as late_cnt
		from exception_count e
		left join terminal t on t.id = e.terminal_id
		where e.hour_start >= $1 and e.hour_start <= $2
		  and ($3 = '' or e.gateway_id = $3 or t.gateway_id = $3)
		  and ($4 = 0::bigint or t.type_id = $4)
		group by bucket
		order by bucket asc`, truncExpr)
	trendRows, err := a.Pool.Query(ctx, trendSQL, from, to, gatewayID, typeID)
	if err != nil {
		writeDBError(w, err)
		return
	}
	trend := make([]analyticsQualityTrendBucket, 0, 32)
	for trendRows.Next() {
		var (
			ts   time.Time
			item analyticsQualityTrendBucket
		)
		if err := trendRows.Scan(&ts, &item.TotalExceptions, &item.QualityViolationCount, &item.CleanFailedCount, &item.LateDroppedCount); err != nil {
			trendRows.Close()
			writeDBError(w, err)
			return
		}
		item.Bucket = ts.UTC().Format(time.RFC3339)
		trend = append(trend, item)
	}
	trendRows.Close()
	if err := trendRows.Err(); err != nil {
		writeDBError(w, err)
		return
	}

	// 4) 高频异常终端 Top 15。
	topTermRows, err := a.Pool.Query(ctx, `
		select e.terminal_id,
		       coalesce(max(t.name), ''),
		       coalesce(nullif(max(t.gateway_id), ''), max(e.gateway_id), ''),
		       coalesce(max(dt.name), ''),
		       coalesce(sum(e.count), 0) as total_count,
		       coalesce(sum(e.count) filter (where e.error_type = 'quality_violation'), 0) as quality_cnt,
		       coalesce(sum(e.count) filter (where e.error_type in ('clean_failed', 'msg_incomplete', 'protocol_error')), 0) as clean_cnt,
		       coalesce(sum(e.count) filter (where e.error_type = 'snapshot_late_dropped'), 0) as late_cnt,
		       (array_agg(e.error_type order by e.count desc, e.last_seen desc))[1] as primary_err,
		       coalesce((array_agg(e.sample_digest order by e.last_seen desc) filter (where e.sample_digest is not null and e.sample_digest <> ''))[1], ''),
		       max(e.last_seen) as last_seen
		from exception_count e
		left join terminal t on t.id = e.terminal_id
		left join device_type dt on dt.id = t.type_id
		where e.hour_start >= $1 and e.hour_start <= $2
		  and ($3 = '' or e.gateway_id = $3 or t.gateway_id = $3)
		  and ($4 = 0::bigint or t.type_id = $4)
		group by e.terminal_id
		order by total_count desc, e.terminal_id asc
		limit 15`,
		from, to, gatewayID, typeID)
	if err != nil {
		writeDBError(w, err)
		return
	}
	topTerminals := make([]analyticsTopTerminal, 0, 15)
	for topTermRows.Next() {
		var (
			row      analyticsTopTerminal
			lastSeen *time.Time
		)
		if err := topTermRows.Scan(
			&row.TerminalID, &row.TerminalName, &row.GatewayID, &row.TypeName,
			&row.TotalCount, &row.QualityCount, &row.CleanFailedCount, &row.LateDroppedCount,
			&row.PrimaryErrorType, &row.SampleDigest, &lastSeen,
		); err != nil {
			topTermRows.Close()
			writeDBError(w, err)
			return
		}
		row.PrimaryErrorLabel, _ = eventTypeMeta(row.PrimaryErrorType)
		if lastSeen != nil {
			row.LastSeen = lastSeen.UTC().Format(time.RFC3339)
		}
		topTerminals = append(topTerminals, row)
	}
	topTermRows.Close()
	if err := topTermRows.Err(); err != nil {
		writeDBError(w, err)
		return
	}

	// 5) 高频问题字段 Top 15（来自接入流水 raw_item 的剔除与质量越界）。
	topItemRows, err := a.Pool.Query(ctx, `
		select r.item_code,
		       count(*) filter (where r.status = 'dropped') as dropped_cnt,
		       count(*) filter (where r.status = 'written' and r.quality > 0) as quality_cnt,
		       count(*) as total_issues,
		       coalesce(max(coalesce(nullif(r.quality_reason, ''), nullif(r.reason, ''))), '')
		from raw_item r
		left join terminal t on t.id = r.terminal_id
		where r.received_at >= $1 and r.received_at <= $2
		  and (r.status = 'dropped' or (r.status = 'written' and r.quality > 0))
		  and ($3 = '' or t.gateway_id = $3)
		  and ($4 = 0::bigint or t.type_id = $4)
		group by r.item_code
		order by total_issues desc, r.item_code asc
		limit 15`,
		from, to, gatewayID, typeID)
	if err != nil {
		writeDBError(w, err)
		return
	}
	topItems := make([]analyticsTopItem, 0, 15)
	for topItemRows.Next() {
		var row analyticsTopItem
		if err := topItemRows.Scan(&row.ItemCode, &row.DroppedCount, &row.QualityViolationCount, &row.TotalIssues, &row.SampleReason); err != nil {
			topItemRows.Close()
			writeDBError(w, err)
			return
		}
		topItems = append(topItems, row)
	}
	topItemRows.Close()
	if err := topItemRows.Err(); err != nil {
		writeDBError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, Response{
		Code:    0,
		Message: "ok",
		Data: analyticsQualityPayload{
			From:                from.UTC().Format(time.RFC3339),
			To:                  to.UTC().Format(time.RFC3339),
			BucketUnit:          bucketUnit,
			TotalExceptionCount: totalExceptions,
			AffectedTerminals:   affectedTerminals,
			RawSummary:          rawSum,
			ExceptionsByType:    dist,
			Trend:               trend,
			TopTerminals:        topTerminals,
			TopItems:            topItems,
		},
	})
}

// ── 3. 设备与网关运行健康分析 ────────────────────────────────────────────────

type analyticsHealthOverview struct {
	TotalGateways           int     `json:"totalGateways"`
	EnabledGateways         int     `json:"enabledGateways"`
	OnlineGateways          int     `json:"onlineGateways"`
	GatewayOnlineRate       float64 `json:"gatewayOnlineRate"`
	TotalTerminals          int     `json:"totalTerminals"`
	EnabledTerminals        int     `json:"enabledTerminals"`
	OnlineTerminals         int     `json:"onlineTerminals"`
	TerminalOnlineRate      float64 `json:"terminalOnlineRate"`
	ActiveTerminalsInWindow int     `json:"activeTerminalsInWindow"`
	SilentOnlineTerminals   int     `json:"silentOnlineTerminals"`
	TotalMessagesInWindow   int64   `json:"totalMessagesInWindow"`
	TotalBytesInWindow      int64   `json:"totalBytesInWindow"`
	TotalItemsInWindow      int64   `json:"totalItemsInWindow"`
	LateDroppedInWindow     int64   `json:"lateDroppedInWindow"`
}

type analyticsGatewayHealth struct {
	GatewayID       string `json:"gatewayId"`
	Name            string `json:"name"`
	Enabled         bool   `json:"enabled"`
	Online          bool   `json:"online"`
	LastSeenAt      string `json:"lastSeenAt,omitempty"`
	TerminalTotal   int    `json:"terminalTotal"`
	TerminalEnabled int    `json:"terminalEnabled"`
	TerminalOnline  int    `json:"terminalOnline"`
	MessageCount    int64  `json:"messageCount"`
	ByteSize        int64  `json:"byteSize"`
	ExceptionCount  int64  `json:"exceptionCount"`
	HealthStatus    string `json:"healthStatus"`
}

type analyticsDeviceTypeHealth struct {
	TypeID          int64   `json:"typeId"`
	TypeCode        string  `json:"typeCode"`
	TypeName        string  `json:"typeName"`
	DomainName      string  `json:"domainName"`
	TerminalTotal   int     `json:"terminalTotal"`
	TerminalEnabled int     `json:"terminalEnabled"`
	TerminalOnline  int     `json:"terminalOnline"`
	OnlineRate      float64 `json:"onlineRate"`
}

type analyticsTerminalHealth struct {
	TerminalID         string   `json:"terminalId"`
	Name               string   `json:"name"`
	TypeID             int64    `json:"typeId"`
	TypeName           string   `json:"typeName"`
	GatewayID          string   `json:"gatewayId"`
	Enabled            bool     `json:"enabled"`
	Online             bool     `json:"online"`
	LastSeenAt         string   `json:"lastSeenAt,omitempty"`
	SilenceSeconds     *int64   `json:"silenceSeconds,omitempty"`
	WrittenCount       int64    `json:"writtenCount"`
	DroppedCount       int64    `json:"droppedCount"`
	LateDroppedCount   int64    `json:"lateDroppedCount"`
	ExceptionCount     int64    `json:"exceptionCount"`
	AvgIntervalSeconds *float64 `json:"avgIntervalSeconds,omitempty"`
	HealthStatus       string   `json:"healthStatus"`
}

type analyticsHealthPayload struct {
	From         string                      `json:"from"`
	To           string                      `json:"to"`
	Overview     analyticsHealthOverview     `json:"overview"`
	Gateways     []analyticsGatewayHealth    `json:"gateways"`
	ByDeviceType []analyticsDeviceTypeHealth `json:"byDeviceType"`
	Terminals    []analyticsTerminalHealth   `json:"terminals"`
}

// classifyGatewayHealth 判定网关运行健康状态：disabled | offline | warning | healthy。
func classifyGatewayHealth(enabled, online bool, termEnabled, termOnline int, exceptionCount int64) string {
	if !enabled {
		return "disabled"
	}
	if !online {
		return "offline"
	}
	if exceptionCount > 0 || (termEnabled > 0 && termOnline < termEnabled) {
		return "warning"
	}
	return "healthy"
}

// classifyTerminalHealth 判定终端运行健康状态：disabled | offline | silent | warning | healthy。
func classifyTerminalHealth(enabled, online bool, writtenCount, exceptionCount, lateDroppedCount int64, silenceSeconds *int64) string {
	if !enabled {
		return "disabled"
	}
	if !online {
		return "offline"
	}
	// 在线但在所选时间窗内 0 上报，或距最近上报已超过 1 小时 → 静默预警。
	if writtenCount == 0 || (silenceSeconds != nil && *silenceSeconds > 3600) {
		return "silent"
	}
	if exceptionCount > 0 || lateDroppedCount > 0 {
		return "warning"
	}
	return "healthy"
}

// analyticsHealth 处理 GET /admin/v1/analytics/health。
func (a *API) analyticsHealth(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, to, err := parseAnalyticsWindow(q.Get("from"), q.Get("to"), analyticsMaxWindow, "health")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	gatewayFilter := strings.TrimSpace(q.Get("gateway_id"))
	ctx := r.Context()
	now := time.Now().UTC()

	// 1) 查询所有网关及其下属终端统计、时间窗内报文吞吐与异常数。
	gwRows, err := a.Pool.Query(ctx, `
		select g.gateway_id,
		       g.name,
		       g.enabled,
		       g.online,
		       g.last_seen_at,
		       coalesce(tc.total_cnt, 0),
		       coalesce(tc.enabled_cnt, 0),
		       coalesce(tc.online_cnt, 0),
		       coalesce(mc.msg_cnt, 0),
		       coalesce(mc.byte_sum, 0),
		       coalesce(ec.exc_cnt, 0)
		from gateway g
		left join (
			select gateway_id,
			       count(*) as total_cnt,
			       count(*) filter (where enabled) as enabled_cnt,
			       count(*) filter (where enabled and online) as online_cnt
			from terminal
			where deleted_at is null
			group by gateway_id
		) tc on tc.gateway_id = g.gateway_id
		left join (
			select gateway_id,
			       count(*) as msg_cnt,
			       coalesce(sum(byte_size), 0) as byte_sum
			from raw_message
			where received_at >= $1 and received_at <= $2
			group by gateway_id
		) mc on mc.gateway_id = g.gateway_id
		left join (
			select gateway_id,
			       coalesce(sum(count), 0) as exc_cnt
			from exception_count
			where hour_start >= $1 and hour_start <= $2
			group by gateway_id
		) ec on ec.gateway_id = g.gateway_id
		where ($3 = '' or g.gateway_id = $3)
		order by g.gateway_id asc`,
		from, to, gatewayFilter)
	if err != nil {
		writeDBError(w, err)
		return
	}

	gateways := make([]analyticsGatewayHealth, 0, 16)
	var overview analyticsHealthOverview
	for gwRows.Next() {
		var (
			gw       analyticsGatewayHealth
			lastSeen *time.Time
		)
		if err := gwRows.Scan(
			&gw.GatewayID, &gw.Name, &gw.Enabled, &gw.Online, &lastSeen,
			&gw.TerminalTotal, &gw.TerminalEnabled, &gw.TerminalOnline,
			&gw.MessageCount, &gw.ByteSize, &gw.ExceptionCount,
		); err != nil {
			gwRows.Close()
			writeDBError(w, err)
			return
		}
		if lastSeen != nil {
			gw.LastSeenAt = lastSeen.UTC().Format(time.RFC3339)
		}
		gw.HealthStatus = classifyGatewayHealth(
			gw.Enabled, gw.Online, gw.TerminalEnabled, gw.TerminalOnline, gw.ExceptionCount,
		)
		overview.TotalGateways++
		if gw.Enabled {
			overview.EnabledGateways++
		}
		if gw.Enabled && gw.Online {
			overview.OnlineGateways++
		}
		overview.TotalMessagesInWindow += gw.MessageCount
		overview.TotalBytesInWindow += gw.ByteSize
		gateways = append(gateways, gw)
	}
	gwRows.Close()
	if err := gwRows.Err(); err != nil {
		writeDBError(w, err)
		return
	}
	if overview.EnabledGateways > 0 {
		overview.GatewayOnlineRate = float64(overview.OnlineGateways) / float64(overview.EnabledGateways) * 100.0
	}

	// 2) 按物模型统计终端在线率。
	dtRows, err := a.Pool.Query(ctx, `
		select dt.id,
		       dt.code,
		       dt.name,
		       coalesce(dd.name, ''),
		       count(t.id) as total_cnt,
		       count(t.id) filter (where t.enabled) as enabled_cnt,
		       count(t.id) filter (where t.enabled and t.online) as online_cnt
		from device_type dt
		left join data_domain dd on dd.id = dt.domain_id
		left join terminal t on t.type_id = dt.id and t.deleted_at is null and ($1 = '' or t.gateway_id = $1)
		group by dt.id, dt.code, dt.name, dd.name
		order by dt.id asc`,
		gatewayFilter)
	if err != nil {
		writeDBError(w, err)
		return
	}
	byDeviceType := make([]analyticsDeviceTypeHealth, 0, 16)
	for dtRows.Next() {
		var row analyticsDeviceTypeHealth
		if err := dtRows.Scan(
			&row.TypeID, &row.TypeCode, &row.TypeName, &row.DomainName,
			&row.TerminalTotal, &row.TerminalEnabled, &row.TerminalOnline,
		); err != nil {
			dtRows.Close()
			writeDBError(w, err)
			return
		}
		if row.TerminalEnabled > 0 {
			row.OnlineRate = float64(row.TerminalOnline) / float64(row.TerminalEnabled) * 100.0
		}
		byDeviceType = append(byDeviceType, row)
	}
	dtRows.Close()
	if err := dtRows.Err(); err != nil {
		writeDBError(w, err)
		return
	}

	// 3) 终端健康明细与活跃度指标（最多返回 200 台终端，问题终端优先排序）。
	termRows, err := a.Pool.Query(ctx, `
		select t.id,
		       coalesce(t.name, ''),
		       t.type_id,
		       coalesce(dt.name, ''),
		       t.gateway_id,
		       t.enabled,
		       t.online,
		       t.last_seen_at,
		       coalesce(ri.written_cnt, 0),
		       coalesce(ri.dropped_cnt, 0),
		       ri.first_at,
		       ri.last_at,
		       coalesce(ec.late_cnt, 0),
		       coalesce(ec.total_exc, 0)
		from terminal t
		left join device_type dt on dt.id = t.type_id
		left join (
			select terminal_id,
			       count(*) filter (where status = 'written') as written_cnt,
			       count(*) filter (where status = 'dropped') as dropped_cnt,
			       min(received_at) filter (where status = 'written') as first_at,
			       max(received_at) filter (where status = 'written') as last_at
			from raw_item
			where received_at >= $1 and received_at <= $2
			group by terminal_id
		) ri on ri.terminal_id = t.id
		left join (
			select terminal_id,
			       coalesce(sum(count) filter (where error_type = 'snapshot_late_dropped'), 0) as late_cnt,
			       coalesce(sum(count), 0) as total_exc
			from exception_count
			where hour_start >= $1 and hour_start <= $2
			group by terminal_id
		) ec on ec.terminal_id = t.id
		where t.deleted_at is null
		  and ($3 = '' or t.gateway_id = $3)
		order by t.id asc`,
		from, to, gatewayFilter)
	if err != nil {
		writeDBError(w, err)
		return
	}

	terminals := make([]analyticsTerminalHealth, 0, 64)
	for termRows.Next() {
		var (
			row             analyticsTerminalHealth
			lastSeen        *time.Time
			firstAt, lastAt *time.Time
		)
		if err := termRows.Scan(
			&row.TerminalID, &row.Name, &row.TypeID, &row.TypeName, &row.GatewayID,
			&row.Enabled, &row.Online, &lastSeen,
			&row.WrittenCount, &row.DroppedCount, &firstAt, &lastAt,
			&row.LateDroppedCount, &row.ExceptionCount,
		); err != nil {
			termRows.Close()
			writeDBError(w, err)
			return
		}
		if lastSeen != nil {
			row.LastSeenAt = lastSeen.UTC().Format(time.RFC3339)
			sec := int64(now.Sub(*lastSeen).Seconds())
			if sec < 0 {
				sec = 0
			}
			row.SilenceSeconds = &sec
		}
		if row.WrittenCount >= 2 && firstAt != nil && lastAt != nil && lastAt.After(*firstAt) {
			avgSec := lastAt.Sub(*firstAt).Seconds() / float64(row.WrittenCount-1)
			row.AvgIntervalSeconds = &avgSec
		}
		row.HealthStatus = classifyTerminalHealth(
			row.Enabled, row.Online, row.WrittenCount, row.ExceptionCount, row.LateDroppedCount, row.SilenceSeconds,
		)

		overview.TotalTerminals++
		if row.Enabled {
			overview.EnabledTerminals++
		}
		if row.Enabled && row.Online {
			overview.OnlineTerminals++
		}
		if row.WrittenCount > 0 {
			overview.ActiveTerminalsInWindow++
		}
		if row.HealthStatus == "silent" {
			overview.SilentOnlineTerminals++
		}
		overview.TotalItemsInWindow += row.WrittenCount + row.DroppedCount
		overview.LateDroppedInWindow += row.LateDroppedCount

		terminals = append(terminals, row)
	}
	termRows.Close()
	if err := termRows.Err(); err != nil {
		writeDBError(w, err)
		return
	}
	if overview.EnabledTerminals > 0 {
		overview.TerminalOnlineRate = float64(overview.OnlineTerminals) / float64(overview.EnabledTerminals) * 100.0
	}

	// 将需要关注的亚健康终端（silent / warning / offline）排在前面，最多下发 200 条。
	healthOrder := map[string]int{
		"silent":   0,
		"warning":  1,
		"offline":  2,
		"healthy":  3,
		"disabled": 4,
	}
	sort.SliceStable(terminals, func(i, j int) bool {
		ri, rj := healthOrder[terminals[i].HealthStatus], healthOrder[terminals[j].HealthStatus]
		if ri != rj {
			return ri < rj
		}
		if terminals[i].ExceptionCount != terminals[j].ExceptionCount {
			return terminals[i].ExceptionCount > terminals[j].ExceptionCount
		}
		return terminals[i].TerminalID < terminals[j].TerminalID
	})
	if len(terminals) > 200 {
		terminals = terminals[:200]
	}

	writeJSON(w, http.StatusOK, Response{
		Code:    0,
		Message: "ok",
		Data: analyticsHealthPayload{
			From:         from.UTC().Format(time.RFC3339),
			To:           to.UTC().Format(time.RFC3339),
			Overview:     overview,
			Gateways:     gateways,
			ByDeviceType: byDeviceType,
			Terminals:    terminals,
		},
	})
}

// parseAnalyticsWindow 统一解析与校验 [from, to] 时间窗。
func parseAnalyticsWindow(rawFrom, rawTo string, maxSpan time.Duration, scope string) (time.Time, time.Time, error) {
	from, err := time.Parse(time.RFC3339, strings.TrimSpace(rawFrom))
	if err != nil {
		return time.Time{}, time.Time{}, invalidQuery("from 需要 RFC3339")
	}
	to, err := time.Parse(time.RFC3339, strings.TrimSpace(rawTo))
	if err != nil {
		return time.Time{}, time.Time{}, invalidQuery("to 需要 RFC3339")
	}
	if !to.After(from) {
		return time.Time{}, time.Time{}, invalidQuery("to 必须晚于 from")
	}
	if to.Sub(from) > maxSpan {
		return time.Time{}, time.Time{}, invalidQuery("时间跨度超出上限（%s）", scope)
	}
	return from, to, nil
}

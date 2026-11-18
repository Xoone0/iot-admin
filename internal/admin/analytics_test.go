package admin

import (
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAnalyticsValidation(t *testing.T) {
	handler := New(nil).Handler()
	now := time.Now().UTC()
	from := now.Add(-24 * time.Hour).Format(time.RFC3339)
	to := now.Format(time.RFC3339)
	tooLongRaw := now.Add(-10 * 24 * time.Hour).Format(time.RFC3339)
	tooLongQuality := now.Add(-120 * 24 * time.Hour).Format(time.RFC3339)

	manyTerminals := make([]string, 25)
	for i := range manyTerminals {
		manyTerminals[i] = "T-" + string(rune('A'+i))
	}

	cases := []struct {
		name string
		url  string
	}{
		{"series 缺 type_id", "/admin/v1/analytics/series?item_code=temp&source=hour&from=" + from + "&to=" + to},
		{"series type_id 非正整数", "/admin/v1/analytics/series?type_id=abc&item_code=temp&source=hour&from=" + from + "&to=" + to},
		{"series 缺 item_code", "/admin/v1/analytics/series?type_id=1&source=hour&from=" + from + "&to=" + to},
		{"series source 非法", "/admin/v1/analytics/series?type_id=1&item_code=temp&source=second&from=" + from + "&to=" + to},
		{"series from 非法", "/admin/v1/analytics/series?type_id=1&item_code=temp&source=hour&from=bad&to=" + to},
		{"series to 早于 from", "/admin/v1/analytics/series?type_id=1&item_code=temp&source=hour&from=" + to + "&to=" + from},
		{"series raw 跨度超 7 天", "/admin/v1/analytics/series?type_id=1&item_code=temp&source=raw&from=" + tooLongRaw + "&to=" + to},
		{"series 终端数超上限", "/admin/v1/analytics/series?type_id=1&item_code=temp&source=hour&from=" + from + "&to=" + to + "&terminal_ids=" + strings.Join(manyTerminals, ",")},
		{"quality 缺 from", "/admin/v1/analytics/quality?to=" + to},
		{"quality to 早于 from", "/admin/v1/analytics/quality?from=" + to + "&to=" + from},
		{"quality 跨度超 90 天", "/admin/v1/analytics/quality?from=" + tooLongQuality + "&to=" + to},
		{"quality type_id 非法", "/admin/v1/analytics/quality?from=" + from + "&to=" + to + "&type_id=-1"},
		{"health 缺 to", "/admin/v1/analytics/health?from=" + from},
		{"health 跨度超 90 天", "/admin/v1/analytics/health?from=" + tooLongQuality + "&to=" + to},
		{"series mode 非法", "/admin/v1/analytics/series?type_id=1&item_code=temp&source=hour&mode=invalid&from=" + from + "&to=" + to},
		{"series compare 非法", "/admin/v1/analytics/series?type_id=1&item_code=temp&source=hour&compare=bad&from=" + from + "&to=" + to},
		{"overview 缺 from", "/admin/v1/analytics/overview?to=" + to},
		{"overview granularity 非法", "/admin/v1/analytics/overview?from=" + from + "&to=" + to + "&granularity=second"},
		{"anomalies 缺 to", "/admin/v1/analytics/anomalies?from=" + from},
		{"anomalies granularity 非法", "/admin/v1/analytics/anomalies?from=" + from + "&to=" + to + "&granularity=year"},
		{"anomalies type_id 非法", "/admin/v1/analytics/anomalies?from=" + from + "&to=" + to + "&type_id=abc"},
	}

	for _, tc := range cases {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.url, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s 应返回 400，实际 %d: %s", tc.name, rec.Code, rec.Body.String())
		}
	}
}

func TestAnalyticsSeriesSpans(t *testing.T) {
	want := map[string]time.Duration{
		"raw":   7 * 24 * time.Hour,
		"hour":  31 * 24 * time.Hour,
		"day":   366 * 24 * time.Hour,
		"month": 3650 * 24 * time.Hour,
		"year":  3650 * 24 * time.Hour,
	}
	for src, expected := range want {
		got, ok := analyticsSeriesSpan(src)
		if !ok || got != expected {
			t.Fatalf("analyticsSeriesSpan(%q) = %v/%v，期望 %v", src, got, ok, expected)
		}
	}
	if _, ok := analyticsSeriesSpan("minute"); ok {
		t.Fatal("minute 不应是合法的趋势数据源")
	}
}

func TestComputeSeriesStatsAndFleetStats(t *testing.T) {
	v1, v2, v3 := 20.0, 25.0, 30.0
	pts := []analyticsPoint{
		{Time: "2026-11-10T01:00:00Z", Num: &v1, Quality: 0},
		{Time: "2026-11-10T02:00:00Z", Num: &v2, Quality: 1, QualityReason: "越界"},
		{Time: "2026-11-10T03:00:00Z", Num: &v3, Quality: 0},
	}
	st := computeSeriesStats(pts)
	if st.Count != 3 || st.NumericCount != 3 || st.BadQualityCount != 1 {
		t.Fatalf("计数不符: %+v", st)
	}
	if st.First == nil || *st.First != 20.0 || st.Last == nil || *st.Last != 30.0 {
		t.Fatalf("起止值不符: first=%v last=%v", st.First, st.Last)
	}
	if st.Min == nil || *st.Min != 20.0 || st.Max == nil || *st.Max != 30.0 {
		t.Fatalf("极值不符: min=%v max=%v", st.Min, st.Max)
	}
	if st.Avg == nil || math.Abs(*st.Avg-25.0) > 1e-9 {
		t.Fatalf("均值不符: %v", st.Avg)
	}
	if st.Delta == nil || math.Abs(*st.Delta-10.0) > 1e-9 {
		t.Fatalf("差值不符: %v", st.Delta)
	}
	if st.ChangeRate == nil || math.Abs(*st.ChangeRate-50.0) > 1e-9 {
		t.Fatalf("变化率不符: %v（期望 +50%%）", st.ChangeRate)
	}
	if st.UsageSum == nil || math.Abs(*st.UsageSum-10.0) > 1e-9 {
		t.Fatalf("累计用能 UsageSum = %v，期望 10.0", st.UsageSum)
	}

	v4 := 10.0
	s2Pts := []analyticsPoint{{Time: "2026-11-10T01:00:00Z", Num: &v4, Quality: 2}}
	series := []analyticsTerminalSeries{
		{TerminalID: "T-1", Stats: st, Points: pts},
		{TerminalID: "T-2", Stats: computeSeriesStats(s2Pts), Points: s2Pts},
	}
	fleet := computeFleetStats(series)
	if fleet.TerminalCount != 2 || fleet.TotalPoints != 4 || fleet.BadQualityCount != 2 {
		t.Fatalf("全局计数不符: %+v", fleet)
	}
	if fleet.Min == nil || *fleet.Min != 10.0 || fleet.Max == nil || *fleet.Max != 30.0 {
		t.Fatalf("全局极值不符: min=%v max=%v", fleet.Min, fleet.Max)
	}
	// (20 + 25 + 30 + 10) / 4 = 21.25
	if fleet.Avg == nil || math.Abs(*fleet.Avg-21.25) > 1e-9 {
		t.Fatalf("全局均值 = %v，期望 21.25", fleet.Avg)
	}
}

func TestEnergyDeltaSpikeRollbackAndTOU(t *testing.T) {
	base := 100.0
	// 100 -> 110 (+10), 110 -> 120 (+10), 120 -> 115 (倒走 -5 -> 有效 0), 115 -> 165 (+50 突增)
	p1, p2, p3, p4 := 110.0, 120.0, 115.0, 165.0
	pts := []analyticsPoint{
		{Time: "2026-11-10T02:00:00Z", Num: &p1}, // CST 10:00 -> sharp (尖)
		{Time: "2026-11-10T04:00:00Z", Num: &p2}, // CST 12:00 -> flat (平) & 午休非工作时段
		{Time: "2026-11-10T06:00:00Z", Num: &p3}, // CST 14:00 -> peak (峰)，发生表码倒走
		{Time: "2026-11-10T16:00:00Z", Num: &p4}, // CST 00:00 -> valley (谷) & 夜间非工作时段
	}
	pts = enrichPointsWithDelta(pts, &base)
	st := computeSeriesStats(pts)

	if st.RollbackCount != 1 || !pts[2].Rollback {
		t.Fatalf("期望识别出 1 次表码倒走: rollbackCount=%d pt2=%+v", st.RollbackCount, pts[2])
	}
	// 有效用能 = 10 + 10 + 0 + 50 = 70；均值 = 17.5；第 4 点 50 > 17.5*1.5(26.25) 触发超均值 50% 突增
	if st.UsageSum == nil || math.Abs(*st.UsageSum-70.0) > 1e-9 {
		t.Fatalf("UsageSum = %v，期望 70.0", st.UsageSum)
	}
	if st.SpikeCount != 1 || !pts[3].SpikeAnomaly {
		t.Fatalf("期望第 4 点触发超均值 50%% 突增: spikeCount=%d pt3=%+v", st.SpikeCount, pts[3])
	}
	if st.TouBreakdown == nil || st.TouBreakdown.SharpUsage != 10.0 || st.TouBreakdown.FlatUsage != 10.0 || st.TouBreakdown.ValleyUsage != 50.0 {
		t.Fatalf("尖峰平谷分时拆解不符: %+v", st.TouBreakdown)
	}
	if st.OffHoursUsage == nil || math.Abs(*st.OffHoursUsage-60.0) > 1e-9 {
		t.Fatalf("非工作时段用能 = %v，期望 60.0 (午休 10 + 夜间 50)", st.OffHoursUsage)
	}

	coalF, carbonF, u := domainEnergyFactors("water", "生活用水", "water_meter", "智能水表", "m³")
	if math.Abs(coalF-0.0857) > 1e-6 || math.Abs(carbonF-0.168) > 1e-6 || u != "m³" {
		t.Fatalf("水域折算系数不符: coal=%v carbon=%v unit=%s", coalF, carbonF, u)
	}
	agg := buildAggregatedBuckets([]analyticsTerminalSeries{{TerminalID: "T-1", Points: pts, Stats: st}}, []float64{8.0, 10.0, 5.0, 25.0})
	if len(agg) != 4 || agg[3].ChangeRate == nil || math.Abs(*agg[3].ChangeRate-100.0) > 1e-9 {
		t.Fatalf("聚合时间桶同环比不符: %+v", agg)
	}
}

func TestQualityAndHealthHelpers(t *testing.T) {
	raw := analyticsRawSummary{
		TotalItems:     200,
		WrittenCount:   180,
		CleanPassCount: 160,
	}
	enrichRawSummaryRates(&raw)
	if raw.PassRate == nil || math.Abs(*raw.PassRate-80.0) > 1e-9 {
		t.Fatalf("PassRate = %v，期望 80.0", raw.PassRate)
	}
	if raw.EffectiveWriteRate == nil || math.Abs(*raw.EffectiveWriteRate-90.0) > 1e-9 {
		t.Fatalf("EffectiveWriteRate = %v，期望 90.0", raw.EffectiveWriteRate)
	}

	if lbl, tone := eventTypeMeta("quality_violation"); lbl != "质量违规" || tone != "warning" {
		t.Fatalf("eventTypeMeta(quality_violation) = %q/%q", lbl, tone)
	}

	if got := classifyGatewayHealth(false, true, 2, 2, 0); got != "disabled" {
		t.Fatalf("停用网关应为 disabled，实际 %s", got)
	}
	if got := classifyGatewayHealth(true, false, 2, 0, 0); got != "offline" {
		t.Fatalf("离线网关应为 offline，实际 %s", got)
	}
	if got := classifyGatewayHealth(true, true, 2, 1, 0); got != "warning" {
		t.Fatalf("部分终端离线的网关应为 warning，实际 %s", got)
	}
	if got := classifyGatewayHealth(true, true, 2, 2, 0); got != "healthy" {
		t.Fatalf("正常网关应为 healthy，实际 %s", got)
	}

	shortSilence := int64(60)
	longSilence := int64(7200)
	if got := classifyTerminalHealth(true, true, 10, 0, 0, &shortSilence); got != "healthy" {
		t.Fatalf("活跃在线终端应为 healthy，实际 %s", got)
	}
	if got := classifyTerminalHealth(true, true, 0, 0, 0, &shortSilence); got != "silent" {
		t.Fatalf("时间窗零上报的在线终端应为 silent，实际 %s", got)
	}
	if got := classifyTerminalHealth(true, true, 5, 0, 0, &longSilence); got != "silent" {
		t.Fatalf("超时未上报的在线终端应为 silent，实际 %s", got)
	}
	if got := classifyTerminalHealth(true, true, 5, 2, 0, &shortSilence); got != "warning" {
		t.Fatalf("有异常的在线终端应为 warning，实际 %s", got)
	}
}

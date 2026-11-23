// energy-preview serves generated samples for the isolated building-energy UI.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/RenJieWeia/iot-admin/internal/admin"
	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
)

const overviewPath = "/admin/v1/analytics/building-energy/preview"

func main() {
	server := &http.Server{
		Addr:              "127.0.0.1:18089",
		Handler:           previewHandler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("building energy sample API listening on %s", server.Addr)
	log.Fatal(server.ListenAndServe())
}

func previewHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/v1/_meta", func(w http.ResponseWriter, r *http.Request) {
		writeResponse(w, http.StatusOK, admin.Response{Code: 0, Message: "ok", Data: admin.BuildMeta()})
	})
	mux.HandleFunc("GET "+overviewPath, sampleOverview)
	mux.HandleFunc("GET "+classificationPath, sampleClassification)
	mux.HandleFunc("GET "+trendPath, sampleTrend)
	mux.HandleFunc("GET "+dimensionsPath, sampleDimensions)
	mux.HandleFunc("GET "+balancePath, sampleBalance)
	mux.HandleFunc("GET "+efficiencyPath, sampleEfficiency)
	mux.HandleFunc("GET "+reportsPath, sampleReports)
	mux.HandleFunc("GET "+targetsPath, sampleTargets)
	mux.HandleFunc("GET "+anomaliesPath, sampleAnomalies)
	mux.HandleFunc("GET "+industryPath, sampleIndustry)
	mux.HandleFunc("GET "+industryReportsPath, sampleIndustryReports)
	return mux
}

type overview struct {
	Sample      bool                       `json:"sample"`
	Project     string                     `json:"project"`
	Point       string                     `json:"point"`
	Unit        string                     `json:"unit"`
	Timezone    string                     `json:"timezone"`
	From        time.Time                  `json:"from"`
	To          time.Time                  `json:"to"`
	Granularity buildingenergy.Granularity `json:"granularity"`
	Result      buildingenergy.Result      `json:"result"`
}

func sampleOverview(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	for key, values := range query {
		if (key != "energy" && key != "granularity") || len(values) != 1 {
			writeResponse(w, http.StatusBadRequest, admin.Response{Code: 1, Message: "invalid_query: 仅支持能源和统计粒度"})
			return
		}
	}
	electric := query.Get("energy") == "electricity"
	energy, unit, terminalID, point, missingHour := buildingenergy.Water, "m³", "W-01", "W-01 · 建筑总水表", 9
	switch query.Get("energy") {
	case "electricity":
		energy, unit, terminalID, point, missingHour = buildingenergy.Electricity, "kWh", "E-01", "E-01 · 建筑总电表", 4
	case "water":
	default:
		writeResponse(w, http.StatusBadRequest, admin.Response{Code: 1, Message: "invalid_query: 能源仅支持用电或用水"})
		return
	}
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		writeResponse(w, http.StatusInternalServerError, admin.Response{Code: 1, Message: "project_timezone_unavailable"})
		return
	}
	to := time.Date(2026, 11, 1, 0, 0, 0, 0, location)
	granularity := buildingenergy.Granularity(query.Get("granularity"))
	var from time.Time
	switch granularity {
	case buildingenergy.Hour:
		from = to.AddDate(0, 0, -1)
	case buildingenergy.Day:
		from = to.AddDate(0, 0, -14)
	case buildingenergy.Month:
		from = to.AddDate(0, -3, 0)
	default:
		writeResponse(w, http.StatusBadRequest, admin.Response{Code: 1, Message: "invalid_query: 统计粒度仅支持时、日、月"})
		return
	}
	profile := buildingenergy.PointProfile{
		Point: buildingenergy.PointRef{
			Project: buildingenergy.ProjectKey{TenantID: "preview", ProjectID: "office-a"},
			Key:     buildingenergy.PointKey{TerminalID: terminalID, ItemCode: "total-usage"},
		},
		Kind: buildingenergy.Cumulative, Energy: energy, Unit: unit, Multiplier: 1, Cadence: time.Hour,
		Meta: buildingenergy.VersionMeta{
			Version: "sample-v1", Effective: buildingenergy.Range{From: to.AddDate(0, -3, 0)},
			Source: buildingenergy.SourceRef{System: "preview", ID: "generated-meter-profile"},
		},
	}
	result, err := buildingenergy.Calculate(
		buildingenergy.MeteringData{Profile: profile, Samples: sampleReadings(from, to, electric, missingHour)},
		buildingenergy.Window{
			Range: buildingenergy.Range{From: from, To: to}, Timezone: location.String(),
			AsOf: to, Granularity: granularity,
		},
	)
	if err != nil {
		log.Printf("calculate preview: %v", err)
		writeResponse(w, http.StatusInternalServerError, admin.Response{Code: 1, Message: "sample_calculation_failed"})
		return
	}
	writeResponse(w, http.StatusOK, admin.Response{Code: 0, Message: "ok", Data: overview{
		Sample: true, Project: "示例项目 · 办公楼 A", Point: point, Unit: result.Unit, Timezone: location.String(),
		From: from, To: to, Granularity: granularity, Result: result,
	}})
}

func sampleReadings(from, to time.Time, electric bool, missingHour int) []buildingenergy.Sample {
	var readings []buildingenergy.Sample
	register, epoch := 1000.0, "initial"
	for at := from; !at.After(to); at = at.Add(time.Hour) {
		hour := at.Hour()
		usage := 2 + (hour*7+at.Day()*3+int(at.Month()))%5
		if hour >= 8 && hour <= 19 {
			usage += 5 + (hour*11+at.Day())%7
		}
		if hour == 1 {
			usage = 0
		}
		if electric {
			register += float64(usage)
		} else {
			register += float64(usage) / 4
		}
		if at.Month() == time.October && at.Day() == 31 {
			if hour == missingHour {
				continue
			}
			if hour == 14 {
				register, epoch = 3, "replacement"
			}
		}
		readings = append(readings, buildingenergy.Sample{
			At: at, ReceivedAt: at, Value: register, Epoch: epoch, Quality: buildingenergy.Normal,
			Source: buildingenergy.SourceRef{System: "preview", ID: at.Format(time.RFC3339)},
		})
	}
	return readings
}

func writeResponse(w http.ResponseWriter, status int, payload admin.Response) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

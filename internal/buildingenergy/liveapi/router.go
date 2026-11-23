package liveapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
	"github.com/RenJieWeia/iot-admin/internal/buildingenergy/authguard"
)

// NewRouter constructs the authenticated formal HTTP handler mounted at
// /admin/v1/energy-analysis/. When guard or svc is nil (or guard fails formal
// config checks), every route fails closed with 401 and Cache-Control: no-store.
func NewRouter(guard *authguard.Guard, svc *Service) http.Handler {
	if guard == nil || svc == nil {
		return authguard.NewFormalRouter(guard)
	}
	mux := http.NewServeMux()
	prefix := strings.TrimSuffix(authguard.FormalRoutePrefix, "/")

	// ── P1/P2/P3/P4 Management APIs ─────────────────────────────────────────
	mux.HandleFunc("GET "+prefix+"/management/inventory", func(w http.ResponseWriter, r *http.Request) {
		resp, err := svc.Inventory(r.Context())
		writeResult(w, resp, err)
	})
	mux.HandleFunc("GET "+prefix+"/management/metering-points/history", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		resp, err := svc.MeteringPointHistory(r.Context(), q.Get("terminalId"), q.Get("itemCode"))
		writeResult(w, resp, err)
	})
	mux.HandleFunc("POST "+prefix+"/management/metering-points", func(w http.ResponseWriter, r *http.Request) {
		var req PublishMeteringPointRequest
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		resp, err := svc.PublishMeteringPoint(r.Context(), req)
		writeResult(w, resp, err)
	})
	mux.HandleFunc("POST "+prefix+"/management/metering-points/status", func(w http.ResponseWriter, r *http.Request) {
		var req SetMeteringPointStatusRequest
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		resp, err := svc.SetMeteringPointStatus(r.Context(), req)
		writeResult(w, resp, err)
	})
	mux.HandleFunc("POST "+prefix+"/management/buildings", func(w http.ResponseWriter, r *http.Request) {
		var req UpsertBuildingRequest
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		resp, err := svc.UpsertBuilding(r.Context(), req)
		writeResult(w, resp, err)
	})
	mux.HandleFunc("POST "+prefix+"/management/spaces", func(w http.ResponseWriter, r *http.Request) {
		var req UpsertSpaceRequest
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		resp, err := svc.UpsertSpace(r.Context(), req)
		writeResult(w, resp, err)
	})
	mux.HandleFunc("POST "+prefix+"/management/organizations", func(w http.ResponseWriter, r *http.Request) {
		var req UpsertOrganizationRequest
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		resp, err := svc.UpsertOrganization(r.Context(), req)
		writeResult(w, resp, err)
	})
	mux.HandleFunc("POST "+prefix+"/management/categories", func(w http.ResponseWriter, r *http.Request) {
		var req UpsertCategoryRequest
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		resp, err := svc.UpsertCategory(r.Context(), req)
		writeResult(w, resp, err)
	})
	mux.HandleFunc("POST "+prefix+"/management/category-bindings", func(w http.ResponseWriter, r *http.Request) {
		var req PublishCategoryBindingRequest
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		resp, err := svc.PublishCategoryBinding(r.Context(), req)
		writeResult(w, resp, err)
	})
	mux.HandleFunc("POST "+prefix+"/management/topology", func(w http.ResponseWriter, r *http.Request) {
		var req PublishTopologyRequest
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		resp, err := svc.PublishTopology(r.Context(), req)
		writeResult(w, resp, err)
	})
	mux.HandleFunc("POST "+prefix+"/management/events", func(w http.ResponseWriter, r *http.Request) {
		var req RecordEventRequest
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		resp, err := svc.RecordRegisterEvent(r.Context(), req)
		writeResult(w, resp, err)
	})
	mux.HandleFunc("POST "+prefix+"/management/targets", func(w http.ResponseWriter, r *http.Request) {
		var req PublishTargetRequest
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		resp, err := svc.PublishTarget(r.Context(), req)
		writeResult(w, resp, err)
	})
	mux.HandleFunc("POST "+prefix+"/management/anomaly-rules", func(w http.ResponseWriter, r *http.Request) {
		var req PublishAnomalyRuleRequest
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		resp, err := svc.PublishAnomalyRule(r.Context(), req)
		writeResult(w, resp, err)
	})
	mux.HandleFunc("POST "+prefix+"/management/denominators", func(w http.ResponseWriter, r *http.Request) {
		var req PublishDenominatorRequest
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		resp, err := svc.PublishDenominator(r.Context(), req)
		writeResult(w, resp, err)
	})
	mux.HandleFunc("POST "+prefix+"/management/benchmarks", func(w http.ResponseWriter, r *http.Request) {
		var req PublishBenchmarkRequest
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		resp, err := svc.PublishBenchmark(r.Context(), req)
		writeResult(w, resp, err)
	})
	mux.HandleFunc("POST "+prefix+"/management/assessments/review", func(w http.ResponseWriter, r *http.Request) {
		var req ReviewAssessmentRequest
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		resp, err := svc.ReviewAssessment(r.Context(), req)
		writeResult(w, resp, err)
	})
	mux.HandleFunc("POST "+prefix+"/management/assessments", func(w http.ResponseWriter, r *http.Request) {
		var req UpsertAssessmentRuleRequest
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		resp, err := svc.UpsertAssessmentRule(r.Context(), req)
		writeResult(w, resp, err)
	})
	mux.HandleFunc("POST "+prefix+"/management/retrofits", func(w http.ResponseWriter, r *http.Request) {
		var req CreateRetrofitRequest
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		resp, err := svc.CreateRetrofitProject(r.Context(), req)
		writeResult(w, resp, err)
	})
	mux.HandleFunc("POST "+prefix+"/management/retrofits/update", func(w http.ResponseWriter, r *http.Request) {
		var req UpdateRetrofitRequest
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		resp, err := svc.UpdateRetrofitProject(r.Context(), req)
		writeResult(w, resp, err)
	})
	mux.HandleFunc("POST "+prefix+"/management/retrofits/baseline/lock", func(w http.ResponseWriter, r *http.Request) {
		var req LockRetrofitBaselineRequest
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		resp, err := svc.LockRetrofitBaseline(r.Context(), req)
		writeResult(w, resp, err)
	})
	mux.HandleFunc("POST "+prefix+"/management/retrofits/verify", func(w http.ResponseWriter, r *http.Request) {
		var req VerifyRetrofitRequest
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		resp, err := svc.RecordRetrofitVerification(r.Context(), req)
		writeResult(w, resp, err)
	})

	// ── Real Business Analytics Endpoints (F01–F17) ─────────────────────────
	mux.HandleFunc("GET "+prefix+"/overview", func(w http.ResponseWriter, r *http.Request) { // F01
		q := r.URL.Query()
		resp, err := svc.Overview(r.Context(), q.Get("building"), q.Get("date"), parseInt(q.Get("days"), 1))
		writeResult(w, resp, err)
	})
	mux.HandleFunc("GET "+prefix+"/composition", func(w http.ResponseWriter, r *http.Request) { // F02
		q := r.URL.Query()
		resp, err := svc.Composition(r.Context(), q.Get("building"), q.Get("date"), parseInt(q.Get("days"), 1))
		writeResult(w, resp, err)
	})
	mux.HandleFunc("GET "+prefix+"/objects-overview", func(w http.ResponseWriter, r *http.Request) { // F03
		q := r.URL.Query()
		resp, err := svc.ObjectsOverview(r.Context(), q.Get("building"), buildingenergy.EnergyType(q.Get("energy")), q.Get("date"), parseInt(q.Get("days"), 1))
		writeResult(w, resp, err)
	})
	mux.HandleFunc("GET "+prefix+"/realtime-trend", func(w http.ResponseWriter, r *http.Request) { // F04
		q := r.URL.Query()
		resp, err := svc.RealtimeTrend(r.Context(), q.Get("building"), buildingenergy.EnergyType(q.Get("energy")), q.Get("date"), parseInt(q.Get("days"), 7))
		writeResult(w, resp, err)
	})
	mux.HandleFunc("GET "+prefix+"/targets", func(w http.ResponseWriter, r *http.Request) { // F05 & F14
		q := r.URL.Query()
		resp, err := svc.Targets(r.Context(), q.Get("building"), buildingenergy.EnergyType(q.Get("energy")), parseInt(q.Get("year"), 0))
		writeResult(w, resp, err)
	})
	mux.HandleFunc("GET "+prefix+"/classification", func(w http.ResponseWriter, r *http.Request) { // F06
		q := r.URL.Query()
		resp, err := svc.Classification(r.Context(), q.Get("building"), buildingenergy.EnergyType(q.Get("energy")), q.Get("date"), parseInt(q.Get("days"), 1))
		writeResult(w, resp, err)
	})
	mux.HandleFunc("GET "+prefix+"/dimensions", func(w http.ResponseWriter, r *http.Request) { // F07 & F16
		q := r.URL.Query()
		resp, err := svc.Dimensions(r.Context(), buildingenergy.ObjectKind(q.Get("view")), buildingenergy.EnergyType(q.Get("energy")), q.Get("date"), parseInt(q.Get("days"), 1))
		writeResult(w, resp, err)
	})
	mux.HandleFunc("GET "+prefix+"/trend", func(w http.ResponseWriter, r *http.Request) { // F08
		q := r.URL.Query()
		resp, err := svc.Trend(r.Context(), q.Get("building"), buildingenergy.EnergyType(q.Get("energy")), q.Get("date"))
		writeResult(w, resp, err)
	})
	mux.HandleFunc("GET "+prefix+"/balance", func(w http.ResponseWriter, r *http.Request) { // F09
		q := r.URL.Query()
		resp, err := svc.Balance(r.Context(), q.Get("building"), buildingenergy.EnergyType(q.Get("energy")), q.Get("date"))
		writeResult(w, resp, err)
	})
	mux.HandleFunc("GET "+prefix+"/anomalies", func(w http.ResponseWriter, r *http.Request) { // F10
		q := r.URL.Query()
		resp, err := svc.Anomalies(r.Context(), q.Get("building"), buildingenergy.EnergyType(q.Get("energy")), q.Get("date"))
		writeResult(w, resp, err)
	})
	mux.HandleFunc("GET "+prefix+"/efficiency", func(w http.ResponseWriter, r *http.Request) { // F11
		q := r.URL.Query()
		resp, err := svc.Efficiency(r.Context(), q.Get("building"), buildingenergy.EnergyType(q.Get("energy")), q.Get("date"), parseInt(q.Get("days"), 1))
		writeResult(w, resp, err)
	})
	mux.HandleFunc("GET "+prefix+"/industry/templates", func(w http.ResponseWriter, r *http.Request) { // F12
		q := r.URL.Query()
		resp, err := svc.IndustryTemplates(r.Context(), q.Get("building"), buildingenergy.EnergyType(q.Get("energy")), q.Get("date"), parseInt(q.Get("days"), 1))
		writeResult(w, resp, err)
	})
	mux.HandleFunc("GET "+prefix+"/industry/benchmarks", func(w http.ResponseWriter, r *http.Request) { // F13
		q := r.URL.Query()
		resp, err := svc.IndustryBenchmarks(r.Context(), q.Get("building"), buildingenergy.EnergyType(q.Get("energy")), q.Get("date"), parseInt(q.Get("days"), 1))
		writeResult(w, resp, err)
	})
	mux.HandleFunc("GET "+prefix+"/opportunities", func(w http.ResponseWriter, r *http.Request) { // F17
		q := r.URL.Query()
		resp, err := svc.Opportunities(r.Context(), q.Get("building"), buildingenergy.EnergyType(q.Get("energy")), q.Get("date"))
		writeResult(w, resp, err)
	})
	mux.HandleFunc("GET "+prefix+"/assessment", func(w http.ResponseWriter, r *http.Request) { // F15
		q := r.URL.Query()
		resp, err := svc.Assessment(r.Context(), q.Get("date"))
		writeResult(w, resp, err)
	})
	mux.HandleFunc("GET "+prefix+"/retrofit/baselines", func(w http.ResponseWriter, r *http.Request) { // F18
		resp, err := svc.RetrofitProjects(r.Context(), "F18")
		writeResult(w, resp, err)
	})
	mux.HandleFunc("GET "+prefix+"/retrofit/potential", func(w http.ResponseWriter, r *http.Request) { // F19
		resp, err := svc.RetrofitProjects(r.Context(), "F19")
		writeResult(w, resp, err)
	})
	mux.HandleFunc("GET "+prefix+"/retrofit/verification", func(w http.ResponseWriter, r *http.Request) { // F20
		resp, err := svc.RetrofitProjects(r.Context(), "F20")
		writeResult(w, resp, err)
	})

	// ── Real Reports & Consistent CSV Exports (F21, F22, F23, F24, F25) ─────
	mux.HandleFunc("GET "+prefix+"/reports/consumption", func(w http.ResponseWriter, r *http.Request) { // F21
		q := r.URL.Query()
		resp, err := svc.ConsumptionReport(r.Context(), q.Get("building"), q.Get("date"), parseInt(q.Get("days"), 1))
		writeReportResult(svc, w, r, resp, err, "energy-report-f21-simulated.csv")
	})
	mux.HandleFunc("GET "+prefix+"/reports/metrics", func(w http.ResponseWriter, r *http.Request) { // F22
		q := r.URL.Query()
		resp, err := svc.MetricsReport(r.Context(), q.Get("building"), buildingenergy.EnergyType(q.Get("energy")), q.Get("date"), parseInt(q.Get("days"), 1))
		writeReportResult(svc, w, r, resp, err, "energy-report-f22-simulated.csv")
	})
	mux.HandleFunc("GET "+prefix+"/reports/assessment", func(w http.ResponseWriter, r *http.Request) { // F23
		q := r.URL.Query()
		resp, err := svc.AssessmentReport(r.Context(), q.Get("date"))
		writeReportResult(svc, w, r, resp, err, "energy-report-f23-simulated.csv")
	})
	mux.HandleFunc("GET "+prefix+"/reports/retrofit", func(w http.ResponseWriter, r *http.Request) { // F24
		resp, err := svc.RetrofitReport(r.Context())
		writeReportResult(svc, w, r, resp, err, "energy-report-f24-simulated.csv")
	})
	mux.HandleFunc("GET "+prefix+"/reports/custom", func(w http.ResponseWriter, r *http.Request) { // F25
		q := r.URL.Query()
		var fields []string
		if raw := strings.TrimSpace(q.Get("fields")); raw != "" {
			for _, f := range strings.Split(raw, ",") {
				if s := strings.TrimSpace(f); s != "" {
					fields = append(fields, s)
				}
			}
		}
		resp, err := svc.CustomReport(r.Context(), q.Get("building"), buildingenergy.EnergyType(q.Get("energy")), q.Get("date"), parseInt(q.Get("days"), 1), q.Get("scope"), fields)
		writeReportResult(svc, w, r, resp, err, "energy-report-f25-simulated.csv")
	})

	return guard.Middleware(mux)
}

func writeReportResult(svc *Service, w http.ResponseWriter, r *http.Request, rep LiveReportResponse, err error, filename string) {
	if err != nil {
		writeResult(w, nil, err)
		return
	}
	if strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("format")), "csv") {
		if err := svc.AuthorizeReportExport(r.Context(), rep.BuildingID, buildingenergy.Range{From: rep.From, To: rep.To}); err != nil {
			writeResult(w, nil, err)
			return
		}
		csvBytes := RenderReportCSV(rep)
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(csvBytes)
		return
	}
	writeResult(w, rep, nil)
}

func parseInt(raw string, def int) int {
	if strings.TrimSpace(raw) == "" {
		return def
	}
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || v <= 0 {
		return def
	}
	return v
}

func decodeJSON(r *http.Request, dst any) error {
	if r == nil || r.Body == nil {
		return errors.New("empty request body")
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

func writeResult(w http.ResponseWriter, data any, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, buildingenergy.ErrAccessDenied):
			status = http.StatusForbidden
		case errors.Is(err, ErrNotFound):
			status = http.StatusNotFound
		case errors.Is(err, ErrBadRequest), errors.Is(err, buildingenergy.ErrInvalidScope), errors.Is(err, buildingenergy.ErrInvalidWindow), errors.Is(err, buildingenergy.ErrInvalidProfile):
			status = http.StatusBadRequest
		case errors.Is(err, ErrEnergySchemaNotReady):
			status = http.StatusServiceUnavailable
		}
		writeError(w, status, err.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "message": "ok", "data": data})
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"code": 1, "message": msg})
}

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/RenJieWeia/iot-admin/internal/admin"
)

func TestOverviewRunsSampleStatistics(t *testing.T) {
	handler := previewHandler()
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, overviewPath+"?energy=electricity&granularity=hour", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("status %d: %s", res.Code, res.Body)
	}
	var payload struct {
		Code int      `json:"code"`
		Data overview `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	data := payload.Data
	if payload.Code != 0 || !data.Sample || data.Unit != "kWh" || data.Result.Expected != 24 ||
		data.Result.Valid != 21 || data.Result.Missing != 2 || data.Result.Suspect != 1 || len(data.Result.Buckets) != 24 {
		t.Fatalf("unexpected sample: %+v", data)
	}
}

func TestPreviewRejectsUnknownSourcesAndKeepsOtherRoutesClosed(t *testing.T) {
	handler := previewHandler()
	for _, test := range []struct {
		url  string
		want int
	}{
		{overviewPath + "?energy=steam", http.StatusBadRequest},
		{overviewPath + "?granularity=year", http.StatusBadRequest},
		{overviewPath + "?project_id=some-project", http.StatusBadRequest},
		{"/admin/v1/terminals", http.StatusNotFound},
		{"/admin/v1/_meta", http.StatusOK},
	} {
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, test.url, nil))
		if res.Code != test.want {
			t.Errorf("GET %s: status %d, want %d", test.url, res.Code, test.want)
		}
		if test.url == "/admin/v1/_meta" {
			var response struct {
				Data admin.MetaPayload `json:"data"`
			}
			if err := json.Unmarshal(res.Body.Bytes(), &response); err != nil || len(response.Data.Entities) == 0 {
				t.Errorf("invalid shell metadata: %v", err)
			}
		}
	}
}

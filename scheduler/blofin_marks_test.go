package main

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestBloFinAPIBaseURLMatchesAdapterEndpoint(t *testing.T) {
	t.Setenv("BLOFIN_BASE_URL", "https://openapi.blofin.com/")
	if got := blofinAPIBaseURL(); got != "https://openapi.blofin.com" {
		t.Fatalf("configured base URL = %q, want normalized live endpoint", got)
	}
	t.Setenv("BLOFIN_BASE_URL", "")
	if got := blofinAPIBaseURL(); got != blofinDefaultAPIBaseURL {
		t.Fatalf("default base URL = %q, want adapter-compatible fallback %q", got, blofinDefaultAPIBaseURL)
	}
}

func TestFetchBloFinPerpsMarksUsesConfiguredMarkPriceEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/market/mark-price" {
			t.Errorf("request path = %q, want /api/v1/market/mark-price", r.URL.Path)
		}
		if r.URL.RawQuery != "" {
			t.Errorf("request query = %q, want empty query for all marks", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":"0","msg":"success","data":[{"instId":"SPCX-USDT","indexPrice":"149.17","markPrice":"149.18"},{"instId":"ETH-USDT","indexPrice":"2670.00","markPrice":"2671.25"}]}`))
	}))
	defer server.Close()
	t.Setenv("BLOFIN_BASE_URL", server.URL+"/")

	marks, err := fetchBloFinPerpsMarks([]string{"SPCX", "ETH"})
	if err != nil {
		t.Fatalf("fetchBloFinPerpsMarks: %v", err)
	}
	want := map[string]float64{"SPCX": 149.18, "ETH": 2671.25}
	if !reflect.DeepEqual(marks, want) {
		t.Fatalf("marks = %#v, want %#v", marks, want)
	}
}

func TestCollectAndMergeBloFinPerpsMarks(t *testing.T) {
	strategies := []StrategyConfig{
		{ID: "blofin-spcx", Type: "perps", Platform: "blofin", Args: []string{"ema", "SPCX", "15m"}},
		{ID: "blofin-spcx-duplicate", Type: "perps", Platform: "blofin", Args: []string{"rsi", "SPCX", "1h"}},
		{ID: "blofin-spot", Type: "spot", Platform: "blofin", Args: []string{"spot", "DOGE", "1h"}},
		{ID: "hl-eth", Type: "perps", Platform: "hyperliquid", Args: []string{"ema", "ETH", "15m"}},
	}
	if got, want := collectBloFinPerpsMarkSymbols(strategies), []string{"SPCX"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("BloFin mark symbols = %v, want %v", got, want)
	}
	if got, want := collectPriceSymbols(strategies), []string{"DOGE"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("generic price symbols = %v, want spot-only symbols %v", got, want)
	}

	prices := map[string]float64{"SPCX": 149.17, "DOGE": 0.10}
	mergeBloFinPerpsMarks(prices, map[string]float64{"SPCX": 149.18, "ETH": 2671.25, "ZERO": 0})
	if prices["SPCX"] != 149.18 || prices["ETH"] != 2671.25 {
		t.Fatalf("BloFin mark merge did not apply authoritative marks: %#v", prices)
	}
	if _, ok := prices["ZERO"]; ok {
		t.Fatalf("invalid zero mark should not be merged: %#v", prices)
	}
}

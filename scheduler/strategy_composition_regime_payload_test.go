package main

import (
	"reflect"
	"testing"
	"time"
)

func TestAppendRegimePayloadArgFallsBackWhenSharedBundleMissing(t *testing.T) {
	rc := &RegimeConfig{Enabled: true, Period: 14, ADXThreshold: 20}
	sc := StrategyConfig{
		ID:       "bl-ob-touch-btc-5m",
		Type:     "perps",
		Platform: "blofin",
		Args:     []string{"ob_touch", "BTC", "5m", "--mode=paper"},
	}
	args := append([]string(nil), sc.Args...)

	gen := globalRegimeStore.resetForCycle(time.Now().UTC())
	t.Cleanup(func() { globalRegimeStore.resetForCycle(time.Now().UTC()) })
	got := appendRegimePayloadArg(args, sc, rc)
	if !reflect.DeepEqual(got, args) {
		t.Fatalf("missing shared bundle appended an empty payload: got %v want %v", got, args)
	}

	req, ok := strategyRegimeBundleRequest(sc, rc)
	if !ok {
		t.Fatal("expected BloFin regime bundle request")
	}
	globalRegimeStore.set(&RegimeBundle{
		Key:           req.Key,
		RawRegimeJSON: `{"default":{"regime":"trending_down","score":1.0}}`,
	}, gen)
	got = appendRegimePayloadArg(args, sc, rc)
	want := append(append([]string(nil), args...), "--regime-payload-json", `{"default":{"regime":"trending_down","score":1.0}}`)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("available shared bundle argv = %v, want %v", got, want)
	}
}

func TestRegimePayloadForPositionKeepsInlineFallbackWhenSharedBundleEmpty(t *testing.T) {
	inline := RegimePayload{
		MultiMode: true,
		Windows: map[string]RegimeSnapshot{
			"medium": {Regime: "trending_down_clean"},
		},
	}
	got := regimePayloadForPosition(RegimePayload{}, inline)
	if label := got.PrimaryLabel(&RegimeConfig{Enabled: true, Windows: RegimeWindowsMap{
		"medium": RegimeWindowSpec{Classifier: regimeClassifierComposite, Period: 14},
	}}); label != "trending_down_clean" {
		t.Fatalf("inline regime fallback label = %q", label)
	}

	shared := RegimePayload{Legacy: "ranging"}
	got = regimePayloadForPosition(shared, inline)
	if label := got.PrimaryLabel(nil); label != "ranging" {
		t.Fatalf("non-empty shared regime must win, got %q", label)
	}
}

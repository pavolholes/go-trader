package main

import (
	"errors"
	"testing"
	"time"
)

func TestBloFinCopyEquityRiskBaselinesAndRearmsOnlyAfterThreeTrustedReadings(t *testing.T) {
	cfg := &PortfolioRiskConfig{MaxDrawdownPct: 20, WarnThresholdPct: 80}
	prs := &PortfolioRiskState{PeakValue: 1253.32, CurrentDrawdownPct: 2.28}
	now := time.Date(2026, 9, 27, 21, 0, 0, 0, time.UTC)

	apply := func(equity float64) *scopeCycleRisk {
		sr := &scopeCycleRisk{
			Config:           cfg,
			TotalPV:          equity,
			EquityAvailable:  true,
			EquityTrusted:    true,
			UsesBloFinEquity: true,
			RiskEquitySource: blofinCopyEquitySource,
			EquitySnapshotAt: now,
			EntryHaltOnly:    true,
		}
		applyScopeCycleRisk(sr, prs)
		now = now.Add(time.Minute)
		return sr
	}

	baseline := apply(400)
	if !baseline.EquityBaselineCreated || prs.PeakValue != 400 || prs.CurrentDrawdownPct != 0 {
		t.Fatalf("first Copy snapshot must establish a clean account baseline: sr=%+v state=%+v", baseline, prs)
	}
	if !prs.EntryHaltOnly || prs.EquitySource != blofinCopyEquitySource {
		t.Fatalf("baseline must persist its account source and entry-only policy: %+v", prs)
	}

	latched := apply(320)
	if !latched.KillSwitchActivated || !latched.EntryHold || !prs.KillSwitchActive {
		t.Fatalf("20%% account drawdown must latch entries without flattening: sr=%+v state=%+v", latched, prs)
	}

	for _, equity := range []float64{340, 336, 340, 340, 340} {
		sr := apply(equity)
		if equity == 336 {
			if prs.EquityRearmReadings != 0 || !sr.KillSwitchFired {
				t.Fatalf("16%% is not below the strict re-arm threshold: sr=%+v state=%+v", sr, prs)
			}
			continue
		}
		if equity == 340 && prs.KillSwitchActive && prs.EquityRearmReadings < 3 && !sr.KillSwitchFired {
			t.Fatalf("entry halt cleared before the third consecutive trusted reading: sr=%+v state=%+v", sr, prs)
		}
	}
	if prs.KillSwitchActive || prs.EquityRearmReadings != 0 {
		t.Fatalf("third consecutive trusted reading below 16%% must re-arm: %+v", prs)
	}
	if len(prs.Events) == 0 || prs.Events[len(prs.Events)-1].Type != "auto_rearm" {
		t.Fatalf("automatic re-arm must be persisted in risk events: %+v", prs.Events)
	}
}

func TestBloFinEquityUnavailableHoldsEntriesAndKeepsLatchedState(t *testing.T) {
	cfg := &PortfolioRiskConfig{MaxDrawdownPct: 20, WarnThresholdPct: 80}
	prs := &PortfolioRiskState{PeakValue: 400, EquitySource: blofinCopyEquitySource, EntryHaltOnly: true, KillSwitchActive: true, EquityRearmReadings: 2}
	sr := &scopeCycleRisk{Config: cfg}
	applyBloFinCopyEquitySnapshot(sr, nil, errors.New("API unavailable"))
	applyScopeCycleRisk(sr, prs)

	if !sr.EntryHold || !sr.KillSwitchFired || !prs.KillSwitchActive {
		t.Fatalf("missing trusted equity must hold entries and preserve the latch: sr=%+v state=%+v", sr, prs)
	}
	if prs.EquityRearmReadings != 0 {
		t.Fatalf("an unavailable snapshot cannot count toward re-arm: %+v", prs)
	}
	due := dueStrategiesNotLatched([]StrategyConfig{scopeCfg("live-a", true)}, map[RiskPartition]*scopeCycleRisk{livePartition: sr})
	if len(due) != 1 {
		t.Fatalf("the strategy must still run its exit manager during an entry hold; due=%v", due)
	}
}

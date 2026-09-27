package main

import (
	"fmt"
	"sort"
	"time"
)

type PortfolioScope string

const (
	ScopeLive       PortfolioScope = "live"
	ScopePaper      PortfolioScope = "paper"
	scopeUnassigned PortfolioScope = ""
)

func portfolioScopeFor(sc StrategyConfig) PortfolioScope {
	if isLiveArgs(sc.Args) {
		return ScopeLive
	}
	return ScopePaper
}

func scopeLabel(scope PortfolioScope) string {
	switch scope {
	case ScopeLive:
		return "live"
	case ScopePaper:
		return "paper"
	}
	return "unassigned"
}

func activeScopes(cfgs []StrategyConfig) []PortfolioScope {
	hasLive := false
	hasPaper := false
	for _, sc := range cfgs {
		if portfolioScopeFor(sc) == ScopeLive {
			hasLive = true
		} else {
			hasPaper = true
		}
	}
	var out []PortfolioScope
	if hasLive {
		out = append(out, ScopeLive)
	}
	if hasPaper {
		out = append(out, ScopePaper)
	}
	return out
}

func strategiesInScope(cfgs []StrategyConfig, scope PortfolioScope) []StrategyConfig {
	out := make([]StrategyConfig, 0, len(cfgs))
	for _, sc := range cfgs {
		if portfolioScopeFor(sc) == scope {
			out = append(out, sc)
		}
	}
	return out
}

func scopeOfStrategyID(cfgs []StrategyConfig, id string) (PortfolioScope, bool) {
	for _, sc := range cfgs {
		if sc.ID == id {
			return portfolioScopeFor(sc), true
		}
	}
	return scopeUnassigned, false
}

func filterStatesByScope(states map[string]*StrategyState, cfgs []StrategyConfig, scope PortfolioScope) map[string]*StrategyState {
	out := make(map[string]*StrategyState, len(states))
	for _, sc := range cfgs {
		if portfolioScopeFor(sc) != scope {
			continue
		}
		if ss, ok := states[sc.ID]; ok {
			out[sc.ID] = ss
		}
	}
	return out
}

func stateIDsInScope(states map[string]*StrategyState, cfgs []StrategyConfig, scope PortfolioScope) []string {
	ids := make([]string, 0, len(states))
	for _, sc := range cfgs {
		if portfolioScopeFor(sc) != scope {
			continue
		}
		if _, ok := states[sc.ID]; ok {
			ids = append(ids, sc.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

func scopeStrategyCounts(cfgs []StrategyConfig) (live, paper int) {
	for _, sc := range cfgs {
		if portfolioScopeFor(sc) == ScopeLive {
			live++
		} else {
			paper++
		}
	}
	return live, paper
}

type scopeCycleRisk struct {
	Partition               RiskPartition
	Config                  *PortfolioRiskConfig
	Prs                     *PortfolioRiskState
	TotalPV                 float64
	TotalNotional           float64
	PerpsLoss               float64
	PerpsMargin             float64
	UsedPVFallback          bool
	EquityAvailable         bool
	EquityTrusted           bool
	EquityGuardArmed        bool
	PeakRebaselineAvailable bool
	UsesBloFinEquity        bool
	RiskEquitySource        string
	EquitySnapshotAt        time.Time
	EntryHaltOnly           bool
	EntryHold               bool
	EquityBaselineCreated   bool
	EquityFeedUnavailable   bool
	EquityFeedRecovered     bool
	KillSwitchActivated     bool
	KillSwitchRearmed       bool
	OperatorNotices         []string
	KillSwitchFired         bool
	NotionalBlocked         bool
	DailyLossEntriesHeld    bool
	DailyLossStatus         DailyLossLimitStatus
	ExposureCapStatus       ExposureCapStatus
	Warning                 bool
	WarnBandEntered         bool
	Reason                  string
	CloseApplied            bool
}

func scopeCycleRiskFired(scopeRisk map[RiskPartition]*scopeCycleRisk, part RiskPartition) bool {
	sr, ok := scopeRisk[part]
	return ok && sr != nil && sr.KillSwitchFired
}

func dueStrategiesNotLatched(due []StrategyConfig, scopeRisk map[RiskPartition]*scopeCycleRisk) []StrategyConfig {
	out := make([]StrategyConfig, 0, len(due))
	for _, sc := range due {
		sr := scopeRisk[partitionFor(sc)]
		if sr != nil && sr.KillSwitchFired && !sr.EntryHaltOnly && !sr.EntryHold {
			continue
		}
		out = append(out, sc)
	}
	return out
}

func applyBloFinCopyEquitySnapshot(sr *scopeCycleRisk, snapshot *blofinCopyEquitySnapshot, fetchErr error) {
	if sr == nil {
		return
	}
	sr.UsesBloFinEquity = true
	sr.RiskEquitySource = blofinCopyEquitySource
	sr.EntryHaltOnly = true
	// BloFin's Copy balance endpoint does not expose Cross used-margin or a
	// margin ratio. Do not present the strategy-level leverage estimate as one.
	sr.PerpsLoss = 0
	sr.PerpsMargin = 0
	if fetchErr != nil || snapshot == nil || snapshot.TotalEquity <= 0 {
		sr.EquityAvailable = false
		sr.EquityTrusted = false
		sr.EntryHold = true
		if fetchErr == nil {
			fetchErr = fmt.Errorf("no positive Copy Trading totalEquity snapshot")
		}
		sr.Reason = fmt.Sprintf("BloFin Copy Trading totalEquity unavailable (%v); new/increasing exposure held while existing-position management continues", fetchErr)
		return
	}
	sr.TotalPV = snapshot.TotalEquity
	sr.EquityAvailable = true
	sr.EquityTrusted = true
	sr.PeakRebaselineAvailable = true
	sr.EquitySnapshotAt = snapshot.FetchedAt
}

func measureScopeCycleRisk(
	part RiskPartition,
	pr *PortfolioRiskConfig,
	cfgStrategies []StrategyConfig,
	state *AppState,
	prices map[string]float64,
	riskWalletBalances map[SharedWalletKey]float64,
	sharedWallets map[SharedWalletKey][]string,
	pooledEquityComplete bool,
	usedStaleRiskBalance bool,
	now time.Time,
) *scopeCycleRisk {
	sr := &scopeCycleRisk{Partition: part, Config: pr}
	scopedCfgs := strategiesInPartition(cfgStrategies, part)
	scopedStates := filterStatesByPartition(state.Strategies, cfgStrategies, part)
	if part.IsLive() {
		sr.TotalPV, sr.UsedPVFallback = computeSubsetPortfolioValue(scopedCfgs, state, prices, riskWalletBalances, sharedWallets)
		sr.EquityAvailable = pooledEquityComplete
		sr.EquityTrusted = pooledEquityComplete && !sr.UsedPVFallback && !usedStaleRiskBalance
		sr.PeakRebaselineAvailable = portfolioPeakRebaselineAvailable(sr.UsedPVFallback, usedStaleRiskBalance, pooledEquityComplete)
	} else {
		sr.TotalPV, _ = computeSubsetPortfolioValue(scopedCfgs, state, prices, nil, sharedWallets)
		sr.EquityAvailable = true
		sr.EquityTrusted = true
		sr.PeakRebaselineAvailable = true
	}
	sr.TotalNotional = PortfolioNotional(scopedStates, prices)
	sr.PerpsLoss, sr.PerpsMargin = AggregatePerpsMarginInputs(scopedStates, scopedCfgs, prices)
	sr.DailyLossStatus = evaluateDailyLossLimit(pr, scopedStates, scopedCfgs, now)
	sr.ExposureCapStatus = evaluateExposureCap(pr, scopedStates, scopedCfgs, prices, sr.TotalPV)
	return sr
}

func applyScopeCycleRisk(sr *scopeCycleRisk, prs *PortfolioRiskState) {
	sr.Prs = prs
	wasLatched := prs.KillSwitchActive
	if sr.UsesBloFinEquity {
		prs.EntryHaltOnly = true
		if !sr.EquityAvailable || !sr.EquityTrusted {
			prs.EquityRearmReadings = 0
			if prs.EquityUnavailableSince.IsZero() {
				prs.EquityUnavailableSince = time.Now().UTC()
				sr.EquityFeedUnavailable = true
			}
			sr.EntryHold = true
			sr.KillSwitchFired = prs.KillSwitchActive
			sr.EquityGuardArmed = prs.EquitySource == blofinCopyEquitySource && prs.PeakValue > 0
			return
		}
		if prs.EquitySource != sr.RiskEquitySource {
			priorPeak := prs.PeakValue
			prs.EquitySource = sr.RiskEquitySource
			prs.PeakValue = sr.TotalPV
			prs.CurrentDrawdownPct = 0
			prs.CurrentMarginDrawdownPct = 0
			prs.EquityBaselineAt = sr.EquitySnapshotAt
			prs.EquityRearmReadings = 0
			prs.WarningSent = false
			prs.WarnBandEnteredAt = time.Time{}
			prs.LastWarningEquityDDPct = 0
			prs.LastWarningMarginDDPct = 0
			prs.WarningEquityDeltaPct = 0
			prs.WarningMarginDeltaPct = 0
			addKillSwitchEvent(prs, "equity_baseline", sr.RiskEquitySource, 0, sr.TotalPV, sr.TotalPV,
				fmt.Sprintf("new trusted BloFin Copy Trading totalEquity baseline $%.2f established; previous model-based peak $%.2f discarded", sr.TotalPV, priorPeak))
			sr.EquityBaselineCreated = true
		}
		prs.EquitySnapshotAt = sr.EquitySnapshotAt
		if !prs.EquityUnavailableSince.IsZero() {
			prs.EquityUnavailableSince = time.Time{}
			sr.EquityFeedRecovered = true
		}
	} else if prs.EquitySource == blofinCopyEquitySource {
		// A persisted Copy-account latch cannot be evaluated from the virtual
		// strategy book if the live Copy account disappears from configuration.
		prs.EntryHaltOnly = true
		prs.EquityRearmReadings = 0
		sr.EntryHaltOnly = true
		sr.EntryHold = true
		sr.KillSwitchFired = prs.KillSwitchActive
		sr.Reason = "BloFin Copy Trading equity source is no longer configured; new/increasing exposure held until a trusted account snapshot is restored"
		return
	}
	if prs.EntryHaltOnly {
		sr.EntryHaltOnly = true
	}
	origPeak := prs.PeakValue
	prevWarningSent := prs.WarningSent
	allowed, notionalBlocked, warning, reason := checkPortfolioRiskWithEquityAvailability(
		prs, sr.Config, sr.TotalPV, sr.TotalNotional, sr.PerpsLoss, sr.PerpsMargin, sr.EquityAvailable, sr.EquityTrusted)
	sr.Warning = warning
	sr.WarnBandEntered = warning && !prevWarningSent
	sr.Reason = reason
	if !sr.PeakRebaselineAvailable && prs.PeakValue > origPeak {
		prs.PeakValue = origPeak
	}
	sr.EquityGuardArmed = sr.EquityAvailable && prs.PeakValue > 0
	sr.KillSwitchFired = !allowed
	sr.KillSwitchActivated = !wasLatched && prs.KillSwitchActive
	sr.KillSwitchRearmed = wasLatched && !prs.KillSwitchActive
	if sr.EntryHaltOnly && sr.KillSwitchFired {
		sr.EntryHold = true
	}
	sr.NotionalBlocked = notionalBlocked
	sr.DailyLossEntriesHeld = sr.DailyLossStatus.Tripped
}

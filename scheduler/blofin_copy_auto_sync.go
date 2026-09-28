package main

// BloFin Copy Trading automatic ledger sync.
//
// Root causes fixed here:
//   - The live fill poller (adapter.get_copy_order_fill) only scans 5 pages
//     and never retries in a later cycle. A fill missed once (e.g. DOGE
//     16780203/16780216) stayed missing until a manual offline rebuild.
//   - Funding payments and Copy profit-share settle into totalEquity but were
//     never booked into the trade ledger, so the virtual ledger drifted above
//     the exchange net (~$3.25 over 2026-09-23..28).
//
// This loop runs on startup and every portfolio cycle (bounded by
// blofinCopySyncInterval) thereafter:
//   1. pulls raw closed fills + equity + transfers via
//      shared_scripts/reconcile_blofin_live_history.py --incremental
//      (read-only, never writes any DB),
//   2. inserts fully-missing positions idempotently by exchange order ID,
//   3. books the remaining equity-vs-ledger-vs-transfers residual as an
//      auditable funding adjustment on live-blofin-funding,
//   4. advances watermarks so each window is reconciled exactly once.
//
// Pre-adoption history is never replayed: the first run only establishes the
// equity/ledger baseline (same adoption pattern as the wallet ledger).

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	blofinCopySyncAccount = "copy"
	// The sync runs inside the 5m portfolio loop so missed Copy close fills are
	// reconciled before the next signal can retry a stale virtual close.
	blofinCopySyncInterval = 5 * time.Minute
	blofinCopySyncLookback = 7 * 24 * time.Hour
	// Residuals below this are rounding noise, not funding.
	blofinCopyResidualThreshold = 0.10
	blofinCopySyncScript        = "shared_scripts/reconcile_blofin_live_history.py"
)

type blofinCopySyncOrder struct {
	OrderID      string `json:"orderId"`
	InstID       string `json:"instId"`
	Side         string `json:"side"`
	FilledSize   string `json:"filledSize"`
	AveragePrice string `json:"averagePrice"`
	Fee          string `json:"fee"`
	Pnl          string `json:"pnl"`
	CreateTime   int64  `json:"createTime"`
}

type blofinCopySyncClose struct {
	CloseOrderID string `json:"closeOrderId"`
	Side         string `json:"side"`
	Size         string `json:"size"`
	AveragePrice string `json:"averagePrice"`
	Fee          string `json:"fee"`
	RealizedPnl  string `json:"realizedPnl"`
	OrderTime    int64  `json:"orderTime"`
}

type blofinCopySyncPosition struct {
	OrderID       string                `json:"order_id"`
	InstID        string                `json:"inst_id"`
	Symbol        string                `json:"symbol"`
	Side          string                `json:"side"`
	PositionSide  string                `json:"position_side"`
	Quantity      string                `json:"quantity"`
	OpenPrice     string                `json:"open_price"`
	OpenMs        int64                 `json:"open_ms"`
	CloseMs       int64                 `json:"close_ms"`
	ClosePrice    string                `json:"close_price"`
	RealizedPnL   string                `json:"realized_pnl"`
	CloseType     string                `json:"close_type"`
	ContractValue string                `json:"contract_value"`
	Closes        []blofinCopySyncClose `json:"closes"`
}

type blofinCopySyncTransfer struct {
	TransferID  string `json:"transferId"`
	FromAccount string `json:"fromAccount"`
	ToAccount   string `json:"toAccount"`
	Amount      string `json:"amount"`
	Ts          string `json:"ts"`
}

type blofinCopySyncPayload struct {
	Incremental        bool                           `json:"incremental"`
	SinceMs            int64                          `json:"since_ms"`
	TransfersSinceMs   int64                          `json:"transfers_since_ms"`
	TransfersCursorMs  int64                          `json:"transfers_cursor_ms"`
	TransfersComplete  bool                           `json:"transfers_complete"`
	TransfersError     string                         `json:"transfers_error"`
	ClosedCount        int                            `json:"exchange_closed_positions"`
	Positions          []blofinCopySyncPosition       `json:"positions"`
	Orders             map[string]blofinCopySyncOrder `json:"orders"`
	ExchangeOpen       []map[string]any               `json:"exchange_open_positions"`
	OpenUnrealizedPnL  string                         `json:"open_unrealized_pnl"`
	UnrealizedComplete bool                           `json:"unrealized_complete"`
	Equity             map[string]string              `json:"equity"`
	Transfers          []blofinCopySyncTransfer       `json:"transfers"`
	PositionErrors     []map[string]string            `json:"position_errors"`
}

type blofinCopySyncState struct {
	FillsSinceMs     int64
	TransfersSinceMs int64
	LastRunMs        int64
	LastEquity       float64 // Copy totalEquity less current unrealized PnL
	LastLedger       float64
	LastTradesNet    float64
	LastAdjustments  float64
	LastAccountNet   float64
	PendingResidual  float64
	Found            bool
}

type blofinCopySyncSkip struct {
	OrderID string
	Reason  string
}

type blofinCopySyncOutcome struct {
	PositionsChecked  int
	PositionsInserted int
	TradesInserted    int
	LedgerDelta       float64
	Skipped           []blofinCopySyncSkip
	Touched           []string
	FundingBooked     float64
	FundingDetail     string
	EquityNow         float64
	BaselineAdopted   bool
}

func (o *blofinCopySyncOutcome) touch(id string) {
	for _, existing := range o.Touched {
		if existing == id {
			return
		}
	}
	o.Touched = append(o.Touched, id)
}

func ensureBloFinCopySyncTables(sdb *StateDB) error {
	if sdb == nil || sdb.db == nil {
		return fmt.Errorf("state db unavailable")
	}
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS blofin_copy_sync_state (
    account TEXT NOT NULL DEFAULT 'copy',
    fills_since_ms INTEGER NOT NULL DEFAULT 0,
    transfers_since_ms INTEGER NOT NULL DEFAULT 0,
    last_run_ms INTEGER NOT NULL DEFAULT 0,
    last_equity REAL NOT NULL DEFAULT 0,
    last_ledger REAL NOT NULL DEFAULT 0,
    last_trades_net REAL NOT NULL DEFAULT 0,
    last_adjustments REAL NOT NULL DEFAULT 0,
    last_account_net REAL NOT NULL DEFAULT 0,
    pending_residual_usd REAL NOT NULL DEFAULT 0,
    PRIMARY KEY (account)
);`,
		`CREATE TABLE IF NOT EXISTS blofin_copy_adjustments (
    rowid INTEGER PRIMARY KEY AUTOINCREMENT,
    account TEXT NOT NULL DEFAULT 'copy',
    time_ms INTEGER NOT NULL,
    kind TEXT NOT NULL,
    amount_usd REAL NOT NULL,
    details TEXT NOT NULL DEFAULT '',
    dedup_id TEXT NOT NULL UNIQUE
);`,
	} {
		if _, err := sdb.db.Exec(stmt); err != nil {
			return fmt.Errorf("ensure copy sync table: %w", err)
		}
	}
	hasTransfersCursor, err := sdb.tableHasColumn("blofin_copy_sync_state", "transfers_since_ms")
	if err != nil {
		return fmt.Errorf("inspect Copy sync state schema: %w", err)
	}
	if !hasTransfersCursor {
		if _, err := sdb.db.Exec(`ALTER TABLE blofin_copy_sync_state ADD COLUMN transfers_since_ms INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add Copy transfers cursor: %w", err)
		}
	}
	hasPendingResidual, err := sdb.tableHasColumn("blofin_copy_sync_state", "pending_residual_usd")
	if err != nil {
		return fmt.Errorf("inspect Copy sync residual schema: %w", err)
	}
	if !hasPendingResidual {
		if _, err := sdb.db.Exec(`ALTER TABLE blofin_copy_sync_state ADD COLUMN pending_residual_usd REAL NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add Copy pending residual: %w", err)
		}
	}
	return nil
}

func loadBloFinCopySyncState(sdb *StateDB) (blofinCopySyncState, error) {
	var st blofinCopySyncState
	if err := ensureBloFinCopySyncTables(sdb); err != nil {
		return st, err
	}
	row := sdb.db.QueryRow(`SELECT fills_since_ms, transfers_since_ms, last_run_ms, last_equity, last_ledger, last_trades_net, last_adjustments, last_account_net, pending_residual_usd FROM blofin_copy_sync_state WHERE account = ?`, blofinCopySyncAccount)
	var fills, transfers, run int64
	var eq, ledger, tradesNet, adjustments, accountNet, pendingResidual float64
	if err := row.Scan(&fills, &transfers, &run, &eq, &ledger, &tradesNet, &adjustments, &accountNet, &pendingResidual); err != nil {
		return st, nil
	}
	if transfers <= 0 && run > 0 {
		transfers = run
	}
	st = blofinCopySyncState{FillsSinceMs: fills, TransfersSinceMs: transfers, LastRunMs: run, LastEquity: eq, LastLedger: ledger, LastTradesNet: tradesNet, LastAdjustments: adjustments, LastAccountNet: accountNet, PendingResidual: pendingResidual, Found: true}
	return st, nil
}

func storeBloFinCopySyncState(sdb *StateDB, st blofinCopySyncState) error {
	if err := ensureBloFinCopySyncTables(sdb); err != nil {
		return err
	}
	_, err := sdb.db.Exec(`INSERT INTO blofin_copy_sync_state (account, fills_since_ms, transfers_since_ms, last_run_ms, last_equity, last_ledger, last_trades_net, last_adjustments, last_account_net, pending_residual_usd)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(account) DO UPDATE SET fills_since_ms=excluded.fills_since_ms, transfers_since_ms=excluded.transfers_since_ms, last_run_ms=excluded.last_run_ms, last_equity=excluded.last_equity, last_ledger=excluded.last_ledger, last_trades_net=excluded.last_trades_net, last_adjustments=excluded.last_adjustments, last_account_net=excluded.last_account_net, pending_residual_usd=excluded.pending_residual_usd`,
		blofinCopySyncAccount, st.FillsSinceMs, st.TransfersSinceMs, st.LastRunMs, st.LastEquity, st.LastLedger, st.LastTradesNet, st.LastAdjustments, st.LastAccountNet, st.PendingResidual)
	return err
}

func blofinCopyAdjustmentsTotal(sdb *StateDB) float64 {
	if sdb == nil || sdb.db == nil {
		return 0
	}
	var total float64
	if err := sdb.db.QueryRow(`SELECT COALESCE(SUM(amount_usd), 0) FROM blofin_copy_adjustments WHERE account = ?`, blofinCopySyncAccount).Scan(&total); err != nil {
		return 0
	}
	return total
}

func insertBloFinCopyAdjustment(sdb *StateDB, timeMs int64, kind string, amount float64, details, dedupID string) (bool, error) {
	if err := ensureBloFinCopySyncTables(sdb); err != nil {
		return false, err
	}
	res, err := sdb.db.Exec(`INSERT OR IGNORE INTO blofin_copy_adjustments (account, time_ms, kind, amount_usd, details, dedup_id) VALUES (?, ?, ?, ?, ?, ?)`,
		blofinCopySyncAccount, timeMs, kind, amount, details, dedupID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func blofinCopyLiveStrategies(cfgs []StrategyConfig) ([]StrategyConfig, map[string][]string) {
	var out []StrategyConfig
	bySymbol := make(map[string][]string)
	for _, sc := range cfgs {
		if sc.Platform != "blofin" || sc.Type != "perps" || !isLiveArgs(sc.Args) {
			continue
		}
		if len(sc.Args) < 2 || strings.TrimSpace(sc.Args[1]) == "" {
			continue
		}
		out = append(out, sc)
		sym := strings.ToUpper(strings.TrimSpace(sc.Args[1]))
		bySymbol[sym] = append(bySymbol[sym], sc.ID)
	}
	return out, bySymbol
}

func blofinCopyStrategyHasOrder(ss *StrategyState, orderID string) bool {
	if ss == nil || orderID == "" {
		return false
	}
	for _, tr := range ss.TradeHistory {
		if tr.ExchangeOrderID == orderID {
			return true
		}
	}
	return false
}

// matchBloFinCopyStrategy mirrors the offline rebuild mapping: exact order-ID
// hit first, then nearest same-symbol/side trade within 120s, then a symbol
// with exactly one live strategy. Anything ambiguous is an error, never a guess.
func matchBloFinCopyStrategy(orderID, symbol, side string, openMs int64, state *AppState, bySymbol map[string][]string) (string, error) {
	if orderID != "" && state != nil {
		owners := make(map[string]bool)
		for _, ss := range state.Strategies {
			if ss == nil {
				continue
			}
			if blofinCopyStrategyHasOrder(ss, orderID) {
				owners[ss.ID] = true
			}
		}
		if len(owners) == 1 {
			for id := range owners {
				return id, nil
			}
		}
		if len(owners) > 1 {
			return "", fmt.Errorf("order %s maps to multiple strategies", orderID)
		}
	}
	type candidate struct {
		deltaMs int64
		id      string
	}
	var cands []candidate
	if state != nil {
		for _, ss := range state.Strategies {
			if ss == nil {
				continue
			}
			for _, tr := range ss.TradeHistory {
				if !strings.EqualFold(tr.Symbol, symbol) || !strings.EqualFold(tr.Side, side) {
					continue
				}
				ts := tr.Timestamp.UTC().UnixMilli()
				delta := ts - openMs
				if delta < 0 {
					delta = -delta
				}
				if delta <= 120_000 {
					cands = append(cands, candidate{deltaMs: delta, id: ss.ID})
				}
			}
		}
	}
	if len(cands) > 0 {
		sort.Slice(cands, func(i, j int) bool { return cands[i].deltaMs < cands[j].deltaMs })
		best := cands[0].deltaMs
		owners := make(map[string]bool)
		for _, c := range cands {
			if c.deltaMs != best {
				break
			}
			owners[c.id] = true
		}
		if len(owners) == 1 {
			for id := range owners {
				return id, nil
			}
		}
	}
	if ids := bySymbol[strings.ToUpper(symbol)]; len(ids) == 1 {
		return ids[0], nil
	}
	return "", fmt.Errorf("cannot safely map BloFin order %s (%s) to a strategy", orderID, symbol)
}

func parseBloFinFloat(raw string) (float64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("empty numeric field")
	}
	return strconv.ParseFloat(raw, 64)
}

func matchBloFinCopyOrder(orders map[string]blofinCopySyncOrder, instID string, whenMs int64, side string, qty, price float64) (blofinCopySyncOrder, error) {
	var cands []blofinCopySyncOrder
	for _, row := range orders {
		if !strings.EqualFold(row.InstID, instID) || !strings.EqualFold(row.Side, side) {
			continue
		}
		if blofinCopyAbsInt64(row.CreateTime-whenMs) > 2_000 {
			continue
		}
		q, err := parseBloFinFloat(row.FilledSize)
		if err != nil || math.Abs(q-qty) > math.Max(1e-6, math.Abs(qty)*1e-6) {
			continue
		}
		p, err := parseBloFinFloat(row.AveragePrice)
		if err != nil || math.Abs(p-price) > math.Max(1e-8, math.Abs(price)*1e-8) {
			continue
		}
		cands = append(cands, row)
	}
	if len(cands) != 1 {
		return blofinCopySyncOrder{}, fmt.Errorf("expected one order-history match for %s %s at %d; got %d", instID, side, whenMs, len(cands))
	}
	return cands[0], nil
}

func blofinCopyAbsInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func blofinCopyLedgerDelta(tr Trade) float64 {
	return tradeLedgerDelta(tr)
}

func blofinCopyCloseOrderRecorded(ss *StrategyState, orderID string) bool {
	if ss == nil || orderID == "" {
		return false
	}
	for _, tr := range ss.TradeHistory {
		if tr.IsClose && tr.ExchangeOrderID == orderID {
			return true
		}
	}
	return false
}

// applyBloFinCopyCloseFill books one exchange-confirmed close fill against an
// already-tracked live position. This is the case where the opening parent
// order is already in the DB but a closing fill is missing. It never falls
// back to the requested size or a mark-price close.
func applyBloFinCopyCloseFill(ss *StrategyState, symbol, positionSide string, openMs int64, fill blofinCopySyncClose, order blofinCopySyncOrder, contractValue float64) (float64, error) {
	if ss == nil {
		return 0, fmt.Errorf("strategy state unavailable")
	}
	pos := ss.Positions[symbol]
	if pos == nil {
		return 0, fmt.Errorf("no matching open %s position in strategy state", symbol)
	}
	if !strings.EqualFold(pos.Side, positionSide) {
		return 0, fmt.Errorf("exchange side %s conflicts with DB side %s", positionSide, pos.Side)
	}
	if !pos.OpenedAt.IsZero() && blofinCopyAbsInt64(pos.OpenedAt.UTC().UnixMilli()-openMs) > 2_000 {
		return 0, fmt.Errorf("open timestamp does not identify this parent position")
	}
	qty, err := parseBloFinFloat(fill.Size)
	if err != nil || qty <= 0 || qty > pos.Quantity+1e-8 {
		return 0, fmt.Errorf("invalid close quantity %q for remaining DB quantity %.8f", fill.Size, pos.Quantity)
	}
	price, err := parseBloFinFloat(fill.AveragePrice)
	if err != nil || price <= 0 {
		return 0, fmt.Errorf("invalid close price %q", fill.AveragePrice)
	}
	fee, err := parseBloFinFloat(fill.Fee)
	if err != nil || fee < 0 {
		return 0, fmt.Errorf("invalid close fee %q", fill.Fee)
	}
	pnl, err := parseBloFinFloat(fill.RealizedPnl)
	if err != nil {
		return 0, fmt.Errorf("invalid close realized PnL %q", fill.RealizedPnl)
	}
	if order.OrderID == "" {
		return 0, fmt.Errorf("close leg did not map to canonical order-history ID")
	}
	if blofinCopyCloseOrderRecorded(ss, order.OrderID) {
		return 0, nil
	}
	closeSide := "sell"
	if strings.EqualFold(positionSide, "short") {
		closeSide = "buy"
	}
	if !strings.EqualFold(fill.Side, closeSide) || !strings.EqualFold(order.Side, closeSide) {
		return 0, fmt.Errorf("close side mismatch: detail=%s order=%s want=%s", fill.Side, order.Side, closeSide)
	}
	fillAt := time.UnixMilli(fill.OrderTime).UTC()
	pnlAccumBefore := pos.RealizedPnLAccum
	dailyBefore, dailyDateBefore := ss.RiskState.DailyPnL, ss.RiskState.DailyPnLDate
	consecutiveBefore := ss.RiskState.ConsecutiveLosses
	net := pnl - fee
	positionID := ensurePositionTradeID(ss.ID, symbol, pos)
	multiplier := pos.Multiplier
	if multiplier <= 0 {
		multiplier = contractValue
	}
	trade := Trade{
		Timestamp: fillAt, StrategyID: ss.ID, Symbol: symbol, PositionID: positionID,
		Side: closeSide, Quantity: qty, Price: price, Value: qty * price * multiplier,
		TradeType: "perps", Details: fmt.Sprintf("BloFin incremental Copy fill recovery; positionOrderId=%s detailsCloseId=%s", order.OrderID, fill.CloseOrderID),
		ExchangeOrderID: order.OrderID, ExchangeFee: fee, IsClose: true,
		RealizedPnL: pnl, PnLGross: true, FeeSource: FeeSourceUserFills,
		Regime: pos.Regime, EntryATR: pos.EntryATR, StopLossOID: pos.StopLossOID,
		StopLossTriggerPx: pos.StopLossTriggerPx, StopLossATRMult: pos.StopLossATRMult, TPTiersJSON: pos.TPTiersJSON,
	}
	RecordTrade(ss, trade)
	ss.Cash += net
	if qty >= pos.Quantity-1e-8 {
		recordClosedPosition(ss, pos, price, pnlAccumBefore+net, "signal", fillAt)
		delete(ss.Positions, symbol)
	} else {
		pos.Quantity -= qty
		pos.RealizedPnLAccum = pnlAccumBefore + net
		recordReplayDecision(ss, ReplayDecisionPartialClose, symbol, pos.Side, qty, price, "BloFin auto-sync close fill", fillAt, 0, "")
	}
	RecordTradeResult(&ss.RiskState, net)
	today := time.Now().UTC().Format("2006-01-02")
	if fillAt.Format("2006-01-02") == today {
		ss.RiskState.DailyPnLDate = today
		if dailyDateBefore == today {
			ss.RiskState.DailyPnL = dailyBefore + net
		} else {
			ss.RiskState.DailyPnL = net
		}
	} else {
		ss.RiskState.DailyPnL = dailyBefore
		ss.RiskState.DailyPnLDate = dailyDateBefore
	}
	if net >= 0 {
		ss.RiskState.ConsecutiveLosses = 0
	} else {
		ss.RiskState.ConsecutiveLosses = consecutiveBefore + 1
	}
	return net, nil
}

// applyBloFinCopySyncPayload inserts fully-missing positions and returns the
// inserted ledger delta. It mutates in-memory state only; the caller persists
// touched strategy books. Daily PnL ledgers are preserved for backfilled
// (non-today) fills, matching the offline backfill policy.
func applyBloFinCopySyncPayload(state *AppState, cfgs []StrategyConfig, payload *blofinCopySyncPayload, store *StateStore, now time.Time) (*blofinCopySyncOutcome, error) {
	out := &blofinCopySyncOutcome{}
	if payload == nil {
		return out, fmt.Errorf("nil sync payload")
	}
	_, bySymbol := blofinCopyLiveStrategies(cfgs)
	today := now.UTC().Format("2006-01-02")
	for _, pos := range payload.Positions {
		out.PositionsChecked++
		symbol := strings.ToUpper(strings.TrimSpace(pos.Symbol))
		qty, err := parseBloFinFloat(pos.Quantity)
		if err != nil || qty <= 0 {
			out.Skipped = append(out.Skipped, blofinCopySyncSkip{OrderID: pos.OrderID, Reason: "invalid quantity"})
			continue
		}
		openPx, err := parseBloFinFloat(pos.OpenPrice)
		if err != nil || openPx <= 0 || pos.OpenMs == 0 || pos.CloseMs == 0 {
			out.Skipped = append(out.Skipped, blofinCopySyncSkip{OrderID: pos.OrderID, Reason: "incomplete position"})
			continue
		}
		openSide := strings.ToLower(strings.TrimSpace(pos.Side))
		if openSide != "buy" && openSide != "sell" {
			out.Skipped = append(out.Skipped, blofinCopySyncSkip{OrderID: pos.OrderID, Reason: "invalid side"})
			continue
		}
		positionSide := strings.ToLower(strings.TrimSpace(pos.PositionSide))
		if positionSide != "long" && positionSide != "short" {
			out.Skipped = append(out.Skipped, blofinCopySyncSkip{OrderID: pos.OrderID, Reason: "invalid positionSide"})
			continue
		}
		strategyID, err := matchBloFinCopyStrategy(pos.OrderID, symbol, openSide, pos.OpenMs, state, bySymbol)
		if err != nil {
			out.Skipped = append(out.Skipped, blofinCopySyncSkip{OrderID: pos.OrderID, Reason: err.Error()})
			continue
		}
		ss := state.Strategies[strategyID]
		if ss == nil {
			out.Skipped = append(out.Skipped, blofinCopySyncSkip{OrderID: pos.OrderID, Reason: "strategy state missing: " + strategyID})
			continue
		}
		parentTracked := blofinCopyStrategyHasOrder(ss, pos.OrderID)
		// Validate close legs against the position before touching state.
		var closeQty float64
		legPnl := make([]float64, 0, len(pos.Closes))
		for _, leg := range pos.Closes {
			q, err := parseBloFinFloat(leg.Size)
			if err != nil || q <= 0 {
				err = fmt.Errorf("invalid close leg size")
				_ = err
				closeQty = -1
				break
			}
			p, err := parseBloFinFloat(leg.RealizedPnl)
			if err != nil {
				closeQty = -1
				break
			}
			closeQty += q
			legPnl = append(legPnl, p)
		}
		if closeQty < 0 || math.Abs(closeQty-qty) > math.Max(1e-6, math.Abs(qty)*1e-6) {
			out.Skipped = append(out.Skipped, blofinCopySyncSkip{OrderID: pos.OrderID, Reason: "close quantity mismatch"})
			continue
		}
		positionPnl, err := parseBloFinFloat(pos.RealizedPnL)
		if err != nil {
			out.Skipped = append(out.Skipped, blofinCopySyncSkip{OrderID: pos.OrderID, Reason: "invalid position pnl"})
			continue
		}
		var legsSum float64
		for _, p := range legPnl {
			legsSum += p
		}
		residual := positionPnl - legsSum
		if math.Abs(residual) > 0.10 {
			out.Skipped = append(out.Skipped, blofinCopySyncSkip{OrderID: pos.OrderID, Reason: fmt.Sprintf("close-fill PnL differs by %.4f", residual)})
			continue
		}
		contractValue, err := parseBloFinFloat(pos.ContractValue)
		if err != nil || contractValue <= 0 {
			out.Skipped = append(out.Skipped, blofinCopySyncSkip{OrderID: pos.OrderID, Reason: "invalid contract value"})
			continue
		}
		// Match the opening order: exact order ID wins, else proximity.
		openOrder, ok := payload.Orders[pos.OrderID]
		if !ok {
			openOrder, err = matchBloFinCopyOrder(payload.Orders, pos.InstID, pos.OpenMs, openSide, qty, openPx)
			if err != nil {
				out.Skipped = append(out.Skipped, blofinCopySyncSkip{OrderID: pos.OrderID, Reason: "open order: " + err.Error()})
				continue
			}
		}
		openFee, err := parseBloFinFloat(openOrder.Fee)
		if err != nil {
			out.Skipped = append(out.Skipped, blofinCopySyncSkip{OrderID: pos.OrderID, Reason: "invalid open fee"})
			continue
		}
		type closeFill struct {
			order   blofinCopySyncOrder
			closeID string
			px      float64
			qty     float64
			fee     float64
			pnl     float64
			ms      int64
			side    string
		}
		var fills []closeFill
		for i, leg := range pos.Closes {
			legPx, err := parseBloFinFloat(leg.AveragePrice)
			if err != nil || legPx <= 0 {
				err = fmt.Errorf("invalid close leg price")
				_ = err
				fills = nil
				break
			}
			legQty, _ := parseBloFinFloat(leg.Size)
			legSide := strings.ToLower(strings.TrimSpace(leg.Side))
			legOrder, err := matchBloFinCopyOrder(payload.Orders, pos.InstID, leg.OrderTime, legSide, legQty, legPx)
			if err != nil {
				fills = nil
				break
			}
			historyFee, err := parseBloFinFloat(legOrder.Fee)
			if err != nil {
				fills = nil
				break
			}
			detailFee, err := parseBloFinFloat(leg.Fee)
			if err != nil || math.Abs(historyFee-detailFee) > 1e-6 {
				fills = nil
				break
			}
			pnl := legPnl[i]
			if i == len(pos.Closes)-1 {
				pnl += residual
			}
			fills = append(fills, closeFill{order: legOrder, closeID: leg.CloseOrderID, px: legPx, qty: legQty, fee: historyFee, pnl: pnl, ms: leg.OrderTime, side: legSide})
		}
		if len(fills) != len(pos.Closes) {
			out.Skipped = append(out.Skipped, blofinCopySyncSkip{OrderID: pos.OrderID, Reason: "close order match failed"})
			continue
		}
		if parentTracked {
			// An open-order row is not proof its closing fills were persisted.
			// Apply only missing close OIDs against the matching still-open
			// virtual position; never reinsert the parent/open fee.
			var missing []closeFill
			missingQty := 0.0
			for _, fill := range fills {
				if blofinCopyCloseOrderRecorded(ss, fill.order.OrderID) {
					continue
				}
				missing = append(missing, fill)
				missingQty += fill.qty
			}
			if len(missing) == 0 {
				if remaining := ss.Positions[symbol]; remaining != nil {
					// Same-symbol re-entry after this parent closed is a new
					// position, not stale quantity from the old parent.
					if remaining.OpenedAt.IsZero() || remaining.OpenedAt.UTC().UnixMilli() <= pos.CloseMs+2_000 {
						out.Skipped = append(out.Skipped, blofinCopySyncSkip{OrderID: pos.OrderID, Reason: fmt.Sprintf("exchange parent closed but DB still has %.8f from the same/older position", remaining.Quantity)})
					}
				}
				continue
			}
			virtual := ss.Positions[symbol]
			if virtual == nil || !strings.EqualFold(virtual.Side, positionSide) ||
				(!virtual.OpenedAt.IsZero() && blofinCopyAbsInt64(virtual.OpenedAt.UTC().UnixMilli()-pos.OpenMs) > 2_000) ||
				math.Abs(virtual.Quantity-missingQty) > math.Max(1e-6, missingQty*1e-6) {
				var virtualQty float64
				if virtual != nil {
					virtualQty = virtual.Quantity
				}
				out.Skipped = append(out.Skipped, blofinCopySyncSkip{OrderID: pos.OrderID, Reason: fmt.Sprintf("missing close fills qty %.8f do not match DB position %+v", missingQty, virtualQty)})
				continue
			}
			var failed error
			repairedDelta := 0.0
			for _, fill := range missing {
				net, closeErr := applyBloFinCopyCloseFill(ss, symbol, positionSide, pos.OpenMs, blofinCopySyncClose{
					CloseOrderID: fill.closeID, Side: fill.side, Size: strconv.FormatFloat(fill.qty, 'f', -1, 64),
					AveragePrice: strconv.FormatFloat(fill.px, 'f', -1, 64), Fee: strconv.FormatFloat(fill.fee, 'f', -1, 64),
					RealizedPnl: strconv.FormatFloat(fill.pnl, 'f', -1, 64), OrderTime: fill.ms,
				}, fill.order, contractValue)
				if closeErr != nil {
					failed = closeErr
					break
				}
				out.LedgerDelta += net
				repairedDelta += net
				out.TradesInserted++
			}
			if failed != nil {
				out.Skipped = append(out.Skipped, blofinCopySyncSkip{OrderID: pos.OrderID, Reason: "missing close apply: " + failed.Error()})
				continue
			}
			out.PositionsInserted++
			out.touch(strategyID)
			fmt.Printf("[blofin-sync] repaired %s/%s parent=%s missing_close_fills=%d net=%+.4f\n", strategyID, symbol, pos.OrderID, len(missing), repairedDelta)
			continue
		}
		// All checks passed: append open + close trades and the closed row.
		dailyPnL, dailyDate := ss.RiskState.DailyPnL, ss.RiskState.DailyPnLDate
		positionID := strategyID + ":" + symbol + ":blofin:" + pos.OrderID
		openAt := time.UnixMilli(pos.OpenMs).UTC()
		openTrade := Trade{
			Timestamp: openAt, StrategyID: strategyID, Symbol: symbol, PositionID: positionID,
			Side: openSide, Quantity: qty, Price: openPx, Value: qty * openPx * contractValue,
			TradeType: "perps", Details: "BloFin auto-sync opening fill; orderId=" + pos.OrderID,
			ExchangeOrderID: openOrder.OrderID, ExchangeFee: openFee,
			IsClose: false, RealizedPnL: 0, PnLGross: true, FeeSource: FeeSourceUserFills,
		}
		RecordTrade(ss, openTrade)
		out.LedgerDelta += blofinCopyLedgerDelta(openTrade)
		out.TradesInserted++
		var accum float64
		closePx, _ := parseBloFinFloat(pos.ClosePrice)
		for _, fill := range fills {
			legNet := fill.pnl - fill.fee
			accum += legNet
			closeTrade := Trade{
				Timestamp: time.UnixMilli(fill.ms).UTC(), StrategyID: strategyID, Symbol: symbol, PositionID: positionID,
				Side: fill.side, Quantity: fill.qty, Price: fill.px, Value: fill.qty * fill.px * contractValue,
				TradeType: "perps", Details: "BloFin auto-sync close fill; positionOrderId=" + pos.OrderID,
				ExchangeOrderID: fill.order.OrderID, ExchangeFee: fill.fee,
				IsClose: true, RealizedPnL: fill.pnl, PnLGross: true, FeeSource: FeeSourceUserFills,
			}
			RecordTrade(ss, closeTrade)
			out.LedgerDelta += blofinCopyLedgerDelta(closeTrade)
			out.TradesInserted++
			ss.Cash += legNet
			if legNet >= 0 {
				ss.RiskState.ConsecutiveLosses = 0
			} else {
				ss.RiskState.ConsecutiveLosses++
			}
		}
		ss.Cash -= openFee
		closeAt := time.UnixMilli(pos.CloseMs).UTC()
		closed := ClosedPosition{
			StrategyID: strategyID, Symbol: symbol, Quantity: qty, AvgCost: openPx,
			Side: positionSide, Multiplier: contractValue,
			OpenedAt: openAt, ClosedAt: closeAt, ClosePrice: closePx,
			RealizedPnL: accum, CloseReason: pos.CloseType,
		}
		if closed.CloseReason == "" {
			closed.CloseReason = "signal"
		}
		if !openAt.IsZero() {
			closed.DurationSeconds = int64(closeAt.Sub(openAt).Seconds())
		}
		ss.ClosedPositions = append(ss.ClosedPositions, closed)
		if closeAt.UTC().Format("2006-01-02") != today {
			ss.RiskState.DailyPnL = dailyPnL
			ss.RiskState.DailyPnLDate = dailyDate
		}
		out.PositionsInserted++
		out.touch(strategyID)
		fmt.Printf("[blofin-sync] backfilled %s/%s qty=%.4g net=%+.4f\n", strategyID, symbol, qty, accum-openFee)
	}
	return out, nil
}

// blofinCopyAccountLedgerTotal sums trade-ledger deltas over the live BloFin
// perps books. Funding/profit-share live in blofin_copy_adjustments, never in
// strategy books, so per-strategy stats stay pure trading performance.
func blofinCopyAccountLedgerTotal(state *AppState, cfgs []StrategyConfig) float64 {
	if state == nil {
		return 0
	}
	total := 0.0
	live, _ := blofinCopyLiveStrategies(cfgs)
	for _, sc := range live {
		ss := state.Strategies[sc.ID]
		if ss == nil {
			continue
		}
		for _, tr := range ss.TradeHistory {
			total += blofinCopyLedgerDelta(tr)
		}
	}
	return total
}

func fetchBloFinCopySyncPayload(configPath string, sinceMs, transfersSinceMs int64) (*blofinCopySyncPayload, string, error) {
	stdout, stderr, err := RunPythonScript(blofinCopySyncScript, []string{
		"--config", configPath,
		"--since-ms", strconv.FormatInt(sinceMs, 10),
		"--transfers-since-ms", strconv.FormatInt(transfersSinceMs, 10),
		"--incremental",
	})
	stderrStr := string(stderr)
	if err != nil {
		return nil, stderrStr, fmt.Errorf("copy sync subprocess failed: %w (stderr: %s)", err, stderrStr)
	}
	var payload blofinCopySyncPayload
	if err := json.Unmarshal(stdout, &payload); err != nil {
		return nil, stderrStr, fmt.Errorf("parse copy sync output: %w (stdout: %s)", err, string(stdout))
	}
	if !payload.Incremental {
		return nil, stderrStr, fmt.Errorf("unexpected copy sync envelope")
	}
	return &payload, stderrStr, nil
}

// bookBloFinCopyResidual stores only the residual accumulated across trusted
// settled-equity windows. It is deliberately classified as account_residual:
// BloFin's public private-account API does not expose an event-level Copy
// funding/profit-share bill ledger. It never touches strategy books.
func bookBloFinCopyResidual(sdb *StateDB, residual float64, now time.Time, dedupID string) (float64, string, bool) {
	if math.Abs(residual) < blofinCopyResidualThreshold {
		return 0, "", false
	}
	detail := fmt.Sprintf("BloFin Copy account residual after fills, unrealized PnL and transfers: %+.8f USDT", residual)
	inserted, err := insertBloFinCopyAdjustment(sdb, now.UTC().UnixMilli(), "account_residual", residual, detail, dedupID)
	if err != nil {
		fmt.Printf("[blofin-sync] WARN: funding adjustment persist failed: %v\n", err)
		return 0, "", false
	}
	if !inserted {
		return 0, "", true // deterministic dedup ID: prior attempt already committed it
	}
	fmt.Printf("[blofin-sync] %s\n", detail)
	return residual, detail, true
}

func blofinCopySyncDue(lastRunMs int64, now time.Time) bool {
	if lastRunMs <= 0 {
		return true
	}
	return now.UTC().UnixMilli()-lastRunMs >= int64(blofinCopySyncInterval/time.Millisecond)
}

// maybeRunBloFinCopyAutoSync executes one incremental sync when due. Fetch
// happens unlocked; state mutation + persistence happen under mu. It returns
// true when a sync ran (even if it found nothing new).
var blofinCopySyncAlertState = struct {
	sync.Mutex
	last map[string]time.Time
}{last: make(map[string]time.Time)}

func notifyBloFinCopySync(notifier *MultiNotifier, key, content string) {
	if notifier == nil || strings.TrimSpace(content) == "" {
		return
	}
	now := time.Now().UTC()
	blofinCopySyncAlertState.Lock()
	if last := blofinCopySyncAlertState.last[key]; !last.IsZero() && now.Sub(last) < 6*time.Hour {
		blofinCopySyncAlertState.Unlock()
		return
	}
	blofinCopySyncAlertState.last[key] = now
	blofinCopySyncAlertState.Unlock()
	// Channel-only on purpose: sync findings are operational info, never DMs.
	notifier.SendToChannel("blofin", "perps", content)
}

func maybeRunBloFinCopyAutoSync(cfg *Config, configPath string, state *AppState, store *StateStore, mu *sync.RWMutex, notifier *MultiNotifier, now time.Time) bool {
	live, _ := blofinCopyLiveStrategies(cfg.Strategies)
	if len(live) == 0 || state == nil || store == nil {
		return false
	}
	sdb, err := store.dbForStrategy(live[0].ID)
	if err != nil {
		fmt.Printf("[blofin-sync] WARN: no state file for sync watermarks: %v\n", err)
		return false
	}
	st, err := loadBloFinCopySyncState(sdb)
	if err != nil {
		fmt.Printf("[blofin-sync] WARN: sync state unavailable: %v\n", err)
		return false
	}
	if !blofinCopySyncDue(st.LastRunMs, now) {
		return false
	}
	sinceMs := st.FillsSinceMs
	if sinceMs <= 0 {
		sinceMs = now.UTC().UnixMilli() - int64(blofinCopySyncLookback/time.Millisecond)
	}
	transfersSinceMs := st.TransfersSinceMs
	if transfersSinceMs <= 0 {
		transfersSinceMs = st.LastRunMs
	}
	if transfersSinceMs <= 0 {
		transfersSinceMs = now.UTC().UnixMilli()
	}
	payload, stderrStr, err := fetchBloFinCopySyncPayload(configPath, sinceMs, transfersSinceMs)
	if err != nil {
		fmt.Printf("[blofin-sync] WARN: fetch failed: %v (stderr: %s)\n", err, stderrStr)
		notifyBloFinCopySync(notifier, "fetch-failure", fmt.Sprintf("⚠️ **BloFin Copy sync fetch failed**\n%v\nSync will retry next cycle.", err))
		return false
	}
	if len(payload.PositionErrors) > 0 {
		fmt.Printf("[blofin-sync] WARN: %d exchange rows need operator review (first: %+v)\n", len(payload.PositionErrors), payload.PositionErrors[0])
		notifyBloFinCopySync(notifier, "position-errors", fmt.Sprintf("⚠️ **BloFin Copy sync: %d exchange row(s) need review**\nFirst: %+v\nFill backfill skipped them instead of guessing — check the bot log.", len(payload.PositionErrors), payload.PositionErrors[0]))
	}
	mu.Lock()
	defer mu.Unlock()
	outcome, err := applyBloFinCopySyncPayload(state, cfg.Strategies, payload, store, now)
	if err != nil {
		fmt.Printf("[blofin-sync] WARN: apply failed: %v\n", err)
		notifyBloFinCopySync(notifier, "apply-failure", fmt.Sprintf("⚠️ **BloFin Copy sync could not apply exchange rows**\n%v\nSync will retry next cycle.", err))
		return false
	}
	if len(outcome.Skipped) > 0 {
		fmt.Printf("[blofin-sync] WARN: %d fill(s) skipped (first: %+v)\n", len(outcome.Skipped), outcome.Skipped[0])
		notifyBloFinCopySync(notifier, "skipped-fills", fmt.Sprintf("⚠️ **BloFin Copy sync skipped %d fill(s)**\nFirst: %+v\nMapping was ambiguous, nothing was guessed — check the bot log.", len(outcome.Skipped), outcome.Skipped[0]))
	}
	if outcome.PositionsInserted > 0 {
		notifyBloFinCopySync(notifier, fmt.Sprintf("backfill-%d-%d", outcome.PositionsChecked, now.UTC().Unix()/86400), fmt.Sprintf("ℹ️ **BloFin Copy sync backfilled %d position(s), %d trade(s), ledger %+.4f**\nFills the live poller missed are now in the ledger.",
			outcome.PositionsInserted, outcome.TradesInserted, outcome.LedgerDelta))
	}
	persistOK := true
	for _, id := range outcome.Touched {
		ss := state.Strategies[id]
		if ss == nil {
			continue
		}
		if err := store.SaveStrategyBook(ss); err != nil {
			persistOK = false
			fmt.Printf("[blofin-sync] WARN: persist %s failed: %v\n", id, err)
			notifyBloFinCopySync(notifier, "persist-"+id, fmt.Sprintf("⚠️ **BloFin Copy sync could not persist %s**\n%v\nSync will retry; no cursor advanced.", id, err))
		}
	}
	equityNow, equityErr := parseBloFinFloat(payload.Equity["totalEquity"])
	equityOK := equityErr == nil
	fillsSafe := persistOK && len(payload.PositionErrors) == 0 && len(outcome.Skipped) == 0
	maxCloseMs := st.FillsSinceMs
	if fillsSafe {
		for _, pos := range payload.Positions {
			if pos.CloseMs > maxCloseMs {
				maxCloseMs = pos.CloseMs
			}
		}
	}
	ledgerNow := blofinCopyAccountLedgerTotal(state, cfg.Strategies)
	adjustmentsNow := blofinCopyAdjustmentsTotal(sdb)
	base := st
	base.LastRunMs = now.UTC().UnixMilli()
	base.LastTradesNet = ledgerNow
	base.LastAdjustments = adjustmentsNow
	base.LastAccountNet = ledgerNow + adjustmentsNow
	if fillsSafe {
		base.FillsSinceMs = maxCloseMs
	}
	if !equityOK || equityNow <= 0 || !payload.UnrealizedComplete || !payload.TransfersComplete || !fillsSafe {
		if !equityOK || equityNow <= 0 {
			fmt.Printf("[blofin-sync] WARN: no trustworthy Copy equity; account adjustment deferred\n")
		}
		if !payload.UnrealizedComplete {
			fmt.Printf("[blofin-sync] WARN: open Copy unrealized PnL unavailable; account adjustment deferred\n")
		}
		if !payload.TransfersComplete {
			fmt.Printf("[blofin-sync] WARN: Copy transfers incomplete (%s); account adjustment deferred\n", payload.TransfersError)
			notifyBloFinCopySync(notifier, "transfer-history", "⚠️ **BloFin Copy sync could not read the complete transfer window**\n"+payload.TransfersError+"\nResidual booking is deferred until the transfer window is complete.")
		}
		if err := storeBloFinCopySyncState(sdb, base); err != nil {
			fmt.Printf("[blofin-sync] WARN: sync watermark persist failed: %v\n", err)
		}
		return true
	}
	unrealized, err := parseBloFinFloat(payload.OpenUnrealizedPnL)
	if err != nil {
		fmt.Printf("[blofin-sync] WARN: invalid open Copy unrealized PnL: %v; account adjustment deferred\n", err)
		if err := storeBloFinCopySyncState(sdb, base); err != nil {
			fmt.Printf("[blofin-sync] WARN: sync watermark persist failed: %v\n", err)
		}
		return true
	}
	settledEquity := equityNow - unrealized
	transfersNet := blofinCopyTransfersNet(payload.Transfers)
	if !st.Found || st.LastEquity <= 0 {
		base.LastEquity = settledEquity
		base.LastLedger = ledgerNow
		base.TransfersSinceMs = payload.TransfersCursorMs
		base.PendingResidual = 0
		if err := storeBloFinCopySyncState(sdb, base); err != nil {
			fmt.Printf("[blofin-sync] WARN: baseline persist failed: %v\n", err)
		} else {
			fmt.Printf("[blofin-sync] adopted settled-equity/ledger baseline equity=$%.4f ledger=%+.4f\n", settledEquity, ledgerNow)
		}
		outcome.BaselineAdopted = true
		return true
	}
	ledgerDelta := ledgerNow - st.LastLedger
	residual := (settledEquity - st.LastEquity) - ledgerDelta - transfersNet + st.PendingResidual
	windowID := fmt.Sprintf("after-%d", st.LastRunMs)
	booked, detail, adjustmentHandled := bookBloFinCopyResidual(sdb, residual, now, windowID)
	if adjustmentHandled {
		base.PendingResidual = 0
	} else {
		base.PendingResidual = residual
	}
	if booked != 0 {
		notifyBloFinCopySync(notifier, "account-residual", fmt.Sprintf("ℹ️ **BloFin Copy account residual recorded: $%+.4f**\n%s", booked, detail))
		outcome.FundingBooked = booked
		outcome.FundingDetail = detail
	}
	base.LastEquity = settledEquity
	base.LastLedger = ledgerNow
	base.TransfersSinceMs = payload.TransfersCursorMs
	if base.TransfersSinceMs <= 0 {
		base.TransfersSinceMs = transfersSinceMs
	}
	adjustments := blofinCopyAdjustmentsTotal(sdb)
	base.LastAdjustments = adjustments
	base.LastAccountNet = ledgerNow + adjustments
	if err := storeBloFinCopySyncState(sdb, base); err != nil {
		fmt.Printf("[blofin-sync] WARN: sync state persist failed: %v\n", err)
	}
	fmt.Printf("[blofin-sync] done: checked=%d inserted_positions=%d inserted_trades=%d ledger_delta=%+.4f account_residual=%+.4f account_net=%+.4f\n",
		outcome.PositionsChecked, outcome.PositionsInserted, outcome.TradesInserted, ledgerDelta, outcome.FundingBooked, ledgerNow+adjustments)
	return true
}

func blofinCopyAdjustmentsTotalStore(store *StateStore, cfgs []StrategyConfig) float64 {
	if store == nil {
		return 0
	}
	live, _ := blofinCopyLiveStrategies(cfgs)
	if len(live) == 0 {
		return 0
	}
	sdb, err := store.dbForStrategy(live[0].ID)
	if err != nil {
		return 0
	}
	return blofinCopyAdjustmentsTotal(sdb)
}

func blofinCopyTransfersNet(transfers []blofinCopySyncTransfer) float64 {
	net := 0.0
	for _, row := range transfers {
		amount, err := parseBloFinFloat(row.Amount)
		if err != nil {
			continue
		}
		switch {
		case row.ToAccount == blofinCopySyncAccount:
			net += amount
		case row.FromAccount == blofinCopySyncAccount:
			net -= amount
		}
	}
	return net
}

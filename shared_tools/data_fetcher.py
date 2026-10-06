
import time
from typing import Optional
from datetime import datetime

import os
import requests
import ccxt
import pandas as pd

from storage import store_ohlcv, load_ohlcv


def get_exchange(exchange_id: str = "binanceus") -> ccxt.Exchange:
    exchange_class = getattr(ccxt, exchange_id)
    exchange = exchange_class({
        "enableRateLimit": True,
    })
    return exchange


def fetch_ohlcv(
    symbol: str = "BTC/USDT",
    timeframe: str = "1d",
    since: Optional[str] = None,
    limit: int = 500,
    exchange_id: str = "binanceus",
    store: bool = True,
) -> pd.DataFrame:
    _ex = (exchange_id or "").strip().lower()
    if _ex == "blofin_spot":
        return fetch_ohlcv_blofin_spot(symbol=symbol, timeframe=timeframe, limit=limit, store=store)
    if _ex == "blofin":
        return fetch_ohlcv_blofin(symbol=symbol, timeframe=timeframe, limit=limit, store=store)
    exchange = get_exchange(exchange_id)

    since_ts = None
    if since:
        since_ts = exchange.parse8601(since + "T00:00:00Z")

    raw = exchange.fetch_ohlcv(symbol, timeframe, since=since_ts, limit=limit)

    if not raw:
        return pd.DataFrame(columns=["timestamp", "open", "high", "low", "close", "volume"])

    df = pd.DataFrame(raw, columns=["timestamp", "open", "high", "low", "close", "volume"])

    if store:
        store_ohlcv(df, exchange_id, symbol, timeframe)

    df["datetime"] = pd.to_datetime(df["timestamp"], unit="ms")
    df.set_index("datetime", inplace=True)

    return df


def fetch_ohlcv_blofin(
    symbol: str = "BTC/USDT",
    timeframe: str = "1d",
    limit: int = 200,
    store: bool = True,
) -> pd.DataFrame:
    """OHLCV from BloFin public REST (spot and perps share /market/candles)."""
    import sys
    sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "platforms", "blofin"))
    from adapter import BloFinExchangeAdapter
    base = (symbol or "").split("/")[0].split("-")[0].strip().upper()
    raw = BloFinExchangeAdapter().get_ohlcv(base, timeframe, limit)
    if not raw:
        return pd.DataFrame(columns=["timestamp", "open", "high", "low", "close", "volume"])
    df = pd.DataFrame(raw, columns=["timestamp", "open", "high", "low", "close", "volume"])
    if store:
        store_ohlcv(df, "blofin", symbol, timeframe)
    df["datetime"] = pd.to_datetime(df["timestamp"], unit="ms")
    df.set_index("datetime", inplace=True)
    return df


def fetch_ohlcv_blofin_spot(
    symbol: str = "BTC/USDT",
    timeframe: str = "1d",
    limit: int = 200,
    store: bool = True,
) -> pd.DataFrame:
    """OHLCV from BloFin spot REST (/api/v1/spot/market/candles)."""
    import sys
    sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "platforms", "blofin"))
    from spot_adapter import BloFinSpotExchangeAdapter
    raw = BloFinSpotExchangeAdapter().get_spot_ohlcv(symbol, timeframe, limit)
    if not raw:
        return pd.DataFrame(columns=["timestamp", "open", "high", "low", "close", "volume"])
    df = pd.DataFrame(raw, columns=["timestamp", "open", "high", "low", "close", "volume"])
    if store:
        store_ohlcv(df, "blofin_spot", symbol, timeframe)
    df["datetime"] = pd.to_datetime(df["timestamp"], unit="ms")
    df.set_index("datetime", inplace=True)
    return df


def fetch_full_history(
    symbol: str = "BTC/USDT",
    timeframe: str = "1d",
    since: str = "2020-01-01",
    exchange_id: str = "binanceus",
    store: bool = True,
    limit: int = 500,
) -> pd.DataFrame:
    exchange = get_exchange(exchange_id)
    since_value = str(since or "2020-01-01")
    if "T" in since_value:
        since_iso = since_value
    elif " " in since_value:
        since_iso = since_value.replace(" ", "T", 1) + "Z"
    else:
        since_iso = since_value + "T00:00:00Z"
    if since_iso.endswith("+00:00"):
        since_iso = since_iso[:-6] + "Z"
    since_ts = exchange.parse8601(since_iso)
    now_ts = exchange.milliseconds()

    all_candles = []
    current_since = since_ts

    tf_ms = {
        "1m": 60_000, "5m": 300_000, "15m": 900_000, "30m": 1_800_000,
        "1h": 3_600_000, "2h": 7_200_000, "4h": 14_400_000,
        "1d": 86_400_000, "1w": 604_800_000, "1M": 2_592_000_000,
    }

    print(f"Fetching {symbol} {timeframe} from {since}...")

    rate_limit_retries = 0
    network_retries = 0
    while current_since < now_ts:
        try:
            candles = exchange.fetch_ohlcv(symbol, timeframe, since=current_since,
                                           limit=limit)
            rate_limit_retries = 0
            network_retries = 0
        except ccxt.RateLimitExceeded:
            rate_limit_retries += 1
            if rate_limit_retries >= 5:
                print(f"Rate limit exceeded {rate_limit_retries} times, aborting fetch")
                break
            print(f"Rate limited, sleeping 10s... ({rate_limit_retries}/5)")
            time.sleep(10)
            continue
        except ccxt.NetworkError as e:
            network_retries += 1
            if network_retries >= 5:
                print(f"Network error after {network_retries} retries, aborting fetch: {e}")
                break
            print(f"Network error: {e}, retrying in 5s... ({network_retries}/5)")
            time.sleep(5)
            continue

        if not candles:
            break

        all_candles.extend(candles)

        last_ts = candles[-1][0]
        if last_ts == current_since:
            break
        current_since = last_ts + tf_ms.get(timeframe, 86_400_000)

        time.sleep(exchange.rateLimit / 1000)

    if not all_candles:
        return pd.DataFrame(columns=["timestamp", "open", "high", "low", "close", "volume"])

    df = pd.DataFrame(all_candles, columns=["timestamp", "open", "high", "low", "close", "volume"])
    df.drop_duplicates(subset=["timestamp"], inplace=True)
    df.sort_values("timestamp", inplace=True)
    df.reset_index(drop=True, inplace=True)

    print(f"Fetched {len(df)} candles from {pd.to_datetime(df['timestamp'].iloc[0], unit='ms')} "
          f"to {pd.to_datetime(df['timestamp'].iloc[-1], unit='ms')}")

    if store:
        store_ohlcv(df, exchange_id, symbol, timeframe)

    df["datetime"] = pd.to_datetime(df["timestamp"], unit="ms")
    df.set_index("datetime", inplace=True)

    return df



def fetch_blofin_full_history(
    symbol: str = "BTC/USDT",
    timeframe: str = "1d",
    since: str = "2020-01-01",
    exchange_id: str = "blofin",
    store: bool = True,
) -> pd.DataFrame:
    """Fetch complete historical OHLCV data from BloFin REST API with pagination."""
    blofin_symbol = symbol.replace("/", "-")
    if "-USDT" not in blofin_symbol:
        parts = blofin_symbol.split("-")
        blofin_symbol = parts[0] + "-USDT"

    bar_map = {
        "1m": "1m", "5m": "5m", "15m": "15m", "30m": "30m",
        "1h": "1H", "2h": "2H", "4h": "4H",
        "1d": "1D", "1w": "1W", "1M": "1M",
    }
    bar = bar_map.get(timeframe, "1H")

    base_url = os.environ.get("BLOFIN_BASE_URL", "https://demo-trading-openapi.blofin.com")
    since_dt = pd.Timestamp(since)
    since_dt = since_dt.tz_localize("UTC") if since_dt.tzinfo is None else since_dt.tz_convert("UTC")
    since_ts = int(since_dt.timestamp() * 1000)
    limit = 1440
    all_candles = []
    after = None

    print(f"Fetching BloFin {blofin_symbol} {timeframe} from {since}...")
    net_retries = 0
    while True:
        params = {"instId": blofin_symbol, "bar": bar, "limit": str(limit)}
        if after is not None:
            params["after"] = str(after)
        try:
            resp = requests.get(base_url + "/api/v1/market/candles", params=params, timeout=30)
            net_retries = 0
        except requests.RequestException as e:
            net_retries += 1
            if net_retries >= 5:
                print(f"Network error after {net_retries} retries: {e}")
                break
            time.sleep(5)
            continue
        data = resp.json()
        if data.get("code") != "0":
            print(f"BloFin API error: {data.get("msg")}")
            break
        candles = data.get("data", [])
        if not candles:
            break
        for c in candles:
            try:
                ts = int(c[0])
                if ts < since_ts:
                    continue
                all_candles.append([ts, float(c[1]), float(c[2]), float(c[3]), float(c[4]), float(c[5])])
            except (IndexError, ValueError, TypeError):
                continue
        oldest_ts = min(int(c[0]) for c in candles)
        if after is not None and oldest_ts >= after:
            break
        after = oldest_ts
        if oldest_ts <= since_ts:
            break
        time.sleep(0.2)

    if not all_candles:
        return pd.DataFrame(columns=["timestamp", "open", "high", "low", "close", "volume"])

    df = pd.DataFrame(all_candles, columns=["timestamp", "open", "high", "low", "close", "volume"])
    df.drop_duplicates(subset=["timestamp"], inplace=True)
    df.sort_values("timestamp", inplace=True)
    df.reset_index(drop=True, inplace=True)
    start = pd.to_datetime(df["timestamp"].iloc[0], unit="ms")
    end = pd.to_datetime(df["timestamp"].iloc[-1], unit="ms")
    print(f"Fetched {len(df)} candles from {start} to {end}")

    if store:
        store_ohlcv(df, exchange_id, symbol, timeframe)

    df["datetime"] = pd.to_datetime(df["timestamp"], unit="ms")
    df.set_index("datetime", inplace=True)
    return df

def load_cached_data(
    symbol: str = "BTC/USDT",
    timeframe: str = "1d",
    exchange_id: str = "binanceus",
    start_date: Optional[str] = None,
    end_date: Optional[str] = None,
    store: bool = True,
) -> pd.DataFrame:
    def _utc_ms(value) -> int:
        ts = pd.Timestamp(value)
        ts = ts.tz_localize("UTC") if ts.tzinfo is None else ts.tz_convert("UTC")
        return int(ts.timestamp() * 1000)

    start_ts = None
    end_ts = None
    if start_date:
        start_ts = _utc_ms(start_date)
    if end_date:
        end_ts = _utc_ms(end_date)

    df = load_ohlcv(exchange_id, symbol, timeframe, start_ts, end_ts)

    tf_ms = _timeframe_duration_ms(timeframe)
    if tf_ms <= 0:
        if df.empty:
            raise ValueError(f"unsupported timeframe {timeframe!r} for cached history coverage")
        return df

    now_ms = int(time.time() * 1000)
    target_end_ms = min(end_ts if end_ts is not None else now_ms, now_ms)

    def _bounds(frame: pd.DataFrame):
        if frame is None or frame.empty:
            return None, None
        if "timestamp" in frame.columns:
            values = pd.to_numeric(frame["timestamp"], errors="coerce").dropna()
            if len(values):
                return int(values.min()), int(values.max())
        index = pd.to_datetime(frame.index, utc=True, errors="coerce")
        index = index[~index.isna()]
        if len(index):
            stamps = (index.astype("int64") // 1_000_000).tolist()
            return int(min(stamps)), int(max(stamps))
        return None, None

    first_ts, last_ts = _bounds(df)
    has_left_gap = (
        start_ts is not None and first_ts is not None and first_ts > start_ts + tf_ms
    )
    stale_tail = (
        last_ts is not None and target_end_ms - last_ts > 2 * tf_ms
    )

    if df.empty or has_left_gap or stale_tail:
        if df.empty:
            fetch_from_ms = start_ts
            since = start_date or "2020-01-01"
            print(f"No cached data for {symbol} {timeframe}, fetching from exchange...")
        elif has_left_gap:
            fetch_from_ms = start_ts
            since = start_date or pd.to_datetime(start_ts, unit="ms", utc=True).strftime("%Y-%m-%d")
            print(f"Cached data for {symbol} {timeframe} starts late; fetching the missing left range from {since}...")
        else:
            fetch_from_ms = last_ts + tf_ms
            since = pd.to_datetime(fetch_from_ms, unit="ms", utc=True).strftime("%Y-%m-%dT%H:%M:%SZ")
            print(f"Cached data for {symbol} {timeframe} ends at {pd.to_datetime(last_ts, unit='ms', utc=True)}; refreshing missing tail...")

        if exchange_id == "blofin":
            fetched = fetch_blofin_full_history(symbol, timeframe, since, exchange_id, store=store)
        else:
            fetched = fetch_full_history(symbol, timeframe, since, exchange_id, store=store)

        if fetched is not None and not fetched.empty:
            df = pd.concat([df, fetched]).sort_index()
            if "timestamp" in df.columns:
                df = df.loc[~df["timestamp"].duplicated(keep="last")]
            else:
                df = df.loc[~df.index.duplicated(keep="last")]
            if "timestamp" in df.columns:
                if start_ts is not None:
                    df = df.loc[pd.to_numeric(df["timestamp"], errors="coerce") >= start_ts]
                if end_ts is not None:
                    df = df.loc[pd.to_numeric(df["timestamp"], errors="coerce") <= end_ts]
            else:
                index = pd.DatetimeIndex(df.index)
                start_bound = pd.to_datetime(start_ts, unit="ms", utc=index.tz is not None) if start_ts is not None else None
                end_bound = pd.to_datetime(end_ts, unit="ms", utc=index.tz is not None) if end_ts is not None else None
                if start_bound is not None:
                    df = df.loc[index >= start_bound]
                if end_bound is not None:
                    df = df.loc[index <= end_bound]

    first_ts, last_ts = _bounds(df)
    stale_tail = last_ts is None or target_end_ms - last_ts > 2 * tf_ms
    has_left_gap = start_ts is not None and first_ts is not None and first_ts > start_ts + tf_ms
    if stale_tail or has_left_gap:
        first_label = "missing" if first_ts is None else str(pd.to_datetime(first_ts, unit="ms", utc=True))
        last_label = "missing" if last_ts is None else str(pd.to_datetime(last_ts, unit="ms", utc=True))
        raise ValueError(
            f"stale OHLCV range for {symbol} {timeframe}: cached/fetched range "
            f"{first_label}..{last_label} does not cover requested period "
            f"{start_date or 'start'}..{end_date or 'now'}"
        )
    return df


def _timeframe_duration_ms(timeframe: str) -> int:
    value = str(timeframe or "").strip()
    if len(value) < 2:
        return 0
    try:
        count = int(value[:-1])
    except (TypeError, ValueError):
        return 0
    if count <= 0:
        return 0
    raw_unit = value[-1]
    unit = raw_unit.lower()
    scale = {
        "m": 60_000,
        "h": 3_600_000,
        "d": 86_400_000,
        "w": 604_800_000,
    }.get(unit)
    if raw_unit == "M":
        scale = 2_592_000_000
    return count * scale if scale else 0


if __name__ == "__main__":
    df = fetch_ohlcv("BTC/USDT", "1d", limit=30)
    print(f"\nFetched {len(df)} candles:")
    print(df.tail())

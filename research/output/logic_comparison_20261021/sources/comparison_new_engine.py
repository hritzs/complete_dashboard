_COMPARE_FAST = True
import os
os.environ['NUMBA_NUM_THREADS'] = '1'
os.environ['OMP_NUM_THREADS'] = '1'
os.environ['OPENBLAS_NUM_THREADS'] = '1'
os.environ['MKL_NUM_THREADS'] = '1'
import pandas as pd
import numpy as np
import pickle
import math
import re
import glob
import warnings
import concurrent.futures
import gc
from datetime import timedelta
from collections import defaultdict
import traceback
import numpy.polynomial.polynomial
if not hasattr(np.polynomial, 'polynomial'):
    np.polynomial.polynomial = numpy.polynomial.polynomial
import numba as nb
warnings.filterwarnings('ignore')
INPUT_DIR = '/mnt/shared/TickDataIV'
NW_INPUT_DIR = ''
IDV_PATH = '/home/ubuntu/Desktop/api_gs/trading-platform-git-recover/research/output/logic_comparison_20261021/inputs/daily.csv'
PREV_STR_PATH = '/home/ubuntu/Desktop/api_gs/trading-platform-git-recover/research/output/logic_comparison_20261021/inputs/daily.csv'
EVENT_PATH = '/home/ubuntu/Desktop/api_gs/trading-platform-git-recover/research/output/logic_comparison_20261021/inputs/events.xlsx'
OUTPUT_BASE = '/home/ubuntu/Desktop/api_gs/trading-platform-git-recover/research/output/logic_comparison_20261021/runtime'
os.makedirs(OUTPUT_BASE, exist_ok=True)
LUT_DIR = '/home/ubuntu/Desktop/api_gs/trading-platform-git-recover/research/output/logic_comparison_20261021/inputs/luts_new'
LUT_916_PATH = os.path.join(LUT_DIR, 'LUT_0916.csv')
LUT_917_PATH = os.path.join(LUT_DIR, 'LUT_0917.csv')
LUT_918_PATH = os.path.join(LUT_DIR, 'LUT_0918.csv')
LUT_919_PATH = os.path.join(LUT_DIR, 'LUT_0919.csv')
LUT_920_PATH = os.path.join(LUT_DIR, 'LUT_0920_Onwards.csv')
NW_LUT_PATH = os.path.join(LUT_DIR, 'NW_LUT.csv')
START_DATE = pd.Timestamp('2021-01-01')
SKIP_DATES = {pd.Timestamp('2024-04-18')}
GRID_END_SWITCH = pd.Timestamp('2026-08-03')
MASTER_CACHE_PATH = os.path.join(OUTPUT_BASE, 'master_day_cache.pkl')
DAILY_LOG_PATH = os.path.join(OUTPUT_BASE, 'Production_Daily_Log_01012021.csv')
DAILY_FILES_DIR = os.path.join(OUTPUT_BASE, 'DailyFiles')
SAVE_DAILY_FILES = False
ADJ_LOWER = -0.007
ADJ_HIGHER = 0.005
WEIGHTS = (0.2, 0.5, 0.3)
WKND_ADJ = 1.0
LUT_DIMS = (7, 10, 12, 7, 3, 3)
IV_THRESHOLDS = np.array([0.6501, 0.8001, 0.9001, 0.9501, 1.0001, 1.0501, 1.1001, 1.2001, 1.3001])
STR_THRESHOLDS = np.array([0.9501, 1.0001, 1.0501, 1.1001, 1.1501, 1.2001, 1.3001, 1.4001, 1.5001, 1.7501, 2.0001])
BLD_THRESHOLDS = np.array([0.0801, 0.0901, 0.1001, 0.1201, 0.1601, 0.2001])
OG_THRESHOLDS = np.array([-0.7501, 0.7501])
EXPECTED_ON = {'LUT_0916.csv': 1298, 'LUT_0917.csv': 3309, 'LUT_0918.csv': 4534, 'LUT_0919.csv': 3828, 'LUT_0920_Onwards.csv': 2441}
SCAN_FIRST_HHMM = 916
SCAN_LAST_HHMM = 1330
DTE1_LAST_HHMM = 959
EVENT_FIRST_HHMM = 918
EVENT_SIZE = 1.0 / 3.0
VETO_STR = {0: 1.7, 1: 1.275, 2: 1.225, 3: 1.175, 4: 1.15, 5: 1.125}
IV_FLOOR = {0: 0.12, 1: 0.12}
SL_BPS = 14.0
SL_BPS_BY_DTE = {0: 16.0}
TP_HIGH = np.array([30.0, 25.0, 25.0, 20.0, 15.0, 15.0, 15.0], dtype=np.float64)
TP_LOW = np.array([16.0, 13.0, 13.0, 10.0, 8.0, 8.0, 8.0], dtype=np.float64)
TV_THRESH = 0.00015
TV4_THRESH = 0.0006
IV_DROP_THRESH = 0.1
PARAM_HEDGE_DIV = 57
PARAM_STR_D = 4
PARAM_PTS_FLOOR = 0.0008
PARAM_HEDGE_FRAC = 0.75
PARAM_INTRA_MULT = 1.2
PARAM_INTRA_FRAC = 1.0
DELTA_THRESH = {0: 0.25, 1: 0.2}
PNL_SCALE = 25000.0
ENTRY_SLIP = {0: 1.0, 1: 0.5}
ENTRY_SLIP_DEF = 0.25
HEDGE_SLIP_BPS = {0: 0.4, 1: 0.3}
HEDGE_SLIP_DEF = 0.2
N_OPT_SECONDS = 30
N_FUT_SECONDS = 3
NW_ENABLED = True
NW_AFTER_HHMM = 920
NW_ENTRY_T = 921
NW_EXIT_T = 1515
NW_SL_BPS = 15.0
NW_HEDGE_THR = 0.1
NW_HEDGE_COST_BPS = 0.2
NW_DIMS = (2, 4, 4, 3)
NW_RAT_EDGES = [1.0, 1.2, 1.35]
NW_LVL_EDGES = [0.12, 0.14, 0.17]
NW_OG_EDGES = [-0.75, 0.7501]
NW_EXPECTED_ON = 23
EXIT_REASONS = {0: 'EOD', 1: 'SL', 2: 'TP', 3: 'TV', 4: 'TV4', 5: 'IV_DROP'}
CHECK_FALLBACK = 'prev'
MINUTE_GRID = 'clock'
PRICE_LOGIC_TAG = 'prod_prev_tick_hf075_im12_d025_020_same_v1' + ('_clockgrid' if MINUTE_GRID == 'clock' else '')
LOG_COLS = ['Date', 'PnL', 'FY', 'Entry_Time', 'Profiler', 'Leg', 'Trades', 'Gross_PnL', 'Entry_Cost', 'Hedge_Cost', 'Hedge_Notional', 'Strike', 'Entry_U', 'Exit_Time', 'Exit_Reason', 'Min_Hedges', 'Intra_Hedges', 'Coord']

def _clean(v):
    return str(v).replace(' ', '').strip()

def _edge(label):
    m = re.findall('-?\\d+\\.?\\d*', str(label))
    return float(m[-1]) if m else None

def _band(label):
    """3-band axis: '<..' -> 0, '>..' -> 2, 'a to b' -> 1 (OG gap and ADJ)."""
    s = _clean(label)
    if '<' in s and 'to' not in s and ('Between' not in s):
        return 0
    if '>' in s and 'to' not in s and ('Between' not in s):
        return 2
    return 1

def load_lut_from_csv(csv_path):
    name = os.path.basename(csv_path)
    print(f'Loading LUT: {name}...')
    if not os.path.exists(csv_path):
        raise FileNotFoundError(f'LUT not found: {csv_path} - set LUT_DIR to the production LUT folder')
    df = pd.read_csv(csv_path, sep=None, engine='python', encoding='utf-8-sig', dtype=str)
    df.columns = df.columns.str.strip()
    DTE_MAP = {'1': 0, '2': 1, '3': 2, '4': 3, '5': 4, '6': 5, '7': 6}
    IV_MAP = {'<=0.65': 0, '0.65-0.80': 1, '0.80-0.90': 2, '0.90-0.95': 3, '0.95-1.00': 4, '1.00-1.05': 5, '1.05-1.10': 6, '1.10-1.20': 7, '1.20-1.30': 8, '>1.30': 9, '0.66-0.80': 1, '0.81-0.90': 2, '0.91-0.95': 3, '0.96-1.00': 4, '1.01-1.05': 5, '1.06-1.10': 6, '1.11-1.20': 7, '1.21-1.30': 8}
    STR_MAP = {'<=0.95': 0, '0.95-1.00': 1, '1.00-1.05': 2, '1.05-1.10': 3, '1.10-1.15': 4, '1.15-1.20': 5, '1.20-1.30': 6, '1.30-1.40': 7, '1.40-1.50': 8, '1.50-1.75': 9, '1.75-2.00': 10, '>2.00': 11, '0.96-1.00': 1, '1.01-1.05': 2, '1.06-1.10': 3, '1.11-1.15': 4, '1.16-1.20': 5, '1.21-1.30': 6, '1.31-1.40': 7, '1.41-1.50': 8, '1.51-1.75': 9, '1.76-2.00': 10}
    BLD_MAP = {'<=0.08': 0, '0.08-0.09': 1, '0.09-0.10': 2, '0.10-0.12': 3, '0.12-0.16': 4, '0.16-0.20': 5, '>0.20': 6, '<0.08': 0, '>=0.20': 6}
    adj_edges = sorted({_edge(x) for x in df['Adj_IV_Chg'].dropna().unique() if _edge(x) is not None})
    if any((min(abs(e - ADJ_LOWER), abs(e - ADJ_HIGHER)) > 1e-09 for e in adj_edges)):
        raise ValueError(f'{name}: ADJ edges {adj_edges} differ from ADJ_LOWER/ADJ_HIGHER {ADJ_LOWER}/{ADJ_HIGHER}')
    LUT = np.zeros(LUT_DIMS, dtype=bool)
    yes = df['Trade'].astype(str).str.strip().str.upper().isin(['YES', 'TRUE', '1'])
    bad = []
    for idx, row in df[yes].iterrows():
        try:
            d = DTE_MAP[str(row.get('DTE', '')).replace('.0', '').strip()]
            i = IV_MAP[_clean(row.get('IV_Ratio', ''))]
            s = STR_MAP[_clean(row.get('Straddle_Ratio', ''))]
            b = BLD_MAP[_clean(row.get('Build_IV', ''))]
            og = _band(row.get('Norm_OG_Gap', ''))
            a = _band(row.get('Adj_IV_Chg', ''))
            LUT[d, i, s, b, og, a] = True
        except KeyError:
            bad.append(idx)
    if bad:
        r = df.loc[bad[0]]
        raise ValueError(f"{name}: {len(bad)} YES rows have labels outside the production grid, e.g. IV_Ratio={r.get('IV_Ratio')} Straddle_Ratio={r.get('Straddle_Ratio')} Build_IV={r.get('Build_IV')}. This is not a 7x10x12x7x3x3 table.")
    n_on = int(LUT.sum())
    if name in EXPECTED_ON and n_on != EXPECTED_ON[name]:
        print(f'  [WARN] {name}: {n_on} ON cells (production table has {EXPECTED_ON[name]}) - a different table is loaded')
    if len(df) == int(np.prod(LUT_DIMS)) and (not np.array_equal(yes.values.reshape(LUT_DIMS), LUT)):
        print(f'  [WARN] {name}: label mapping and row order disagree - check the file')
    print(f'  {n_on} ON cells')
    return LUT

def load_nw_lut_from_csv(csv_path):
    name = os.path.basename(csv_path)
    print(f'Loading LUT: {name}...')
    if not os.path.exists(csv_path):
        raise FileNotFoundError(f'NW LUT not found: {csv_path} (or set NW_ENABLED = False)')
    df = pd.read_csv(csv_path, sep=None, engine='python', encoding='utf-8-sig', dtype=str)
    df.columns = df.columns.str.strip()
    D_MAP = {'1': 0, '2': 1}
    R_MAP = {'<1.00': 0, '1.00-1.20': 1, '1.20-1.35': 2, '>1.35': 3}
    L_MAP = {'<0.12': 0, '0.12-0.14': 1, '0.14-0.17': 2, '>0.17': 3}
    LUT = np.zeros(NW_DIMS, dtype=bool)
    yes = df['Trade'].astype(str).str.strip().str.upper().isin(['YES', 'TRUE', '1'])
    for idx, row in df[yes].iterrows():
        try:
            LUT[D_MAP[str(row['CurWeek_DTE']).replace('.0', '').strip()], R_MAP[_clean(row['NW_IV_Ratio'])], L_MAP[_clean(row['NW_Build_IV'])], _band(row['Norm_OG_Gap'])] = True
        except KeyError:
            raise ValueError(f'{name}: row {idx} has labels outside the NW grid: {row.to_dict()}')
    n_on = int(LUT.sum())
    if n_on != NW_EXPECTED_ON:
        print(f'  [WARN] {name}: {n_on} ON cells (production has {NW_EXPECTED_ON})')
    if len(df) == int(np.prod(NW_DIMS)) and (not np.array_equal(yes.values.reshape(NW_DIMS), LUT)):
        print(f'  [WARN] {name}: label mapping and row order disagree - check the file')
    print(f'  {n_on} ON cells')
    return LUT

@nb.njit(fastmath=True, cache=True)
def _norm_cdf(x):
    return 0.5 * (1.0 + math.erf(x / 1.4142135623730951))

@nb.njit(fastmath=True, cache=True)
def _norm_pdf(x):
    return 0.3989422804014327 * math.exp(-0.5 * x * x)

@nb.njit(fastmath=True, cache=True)
def _bs_price_kernel(is_call, S, K, T, r, sig):
    if T <= 0 or sig <= 0:
        return 0.0
    sq = math.sqrt(T)
    d1 = (math.log(S / K) + (r + 0.5 * sig ** 2) * T) / (sig * sq)
    d2 = d1 - sig * sq
    if is_call:
        return S * _norm_cdf(d1) - K * math.exp(-r * T) * _norm_cdf(d2)
    else:
        return K * math.exp(-r * T) * _norm_cdf(-d2) - S * _norm_cdf(-d1)

@nb.njit(fastmath=True, cache=True)
def _iv_solver_kernel(is_call, price, S, K, T):
    if price <= 0 or T <= 0:
        return np.nan
    low, high = (1e-06, 5.0)
    pl = _bs_price_kernel(is_call, S, K, T, 0, low)
    ph = _bs_price_kernel(is_call, S, K, T, 0, high)
    if pl > price or ph < price:
        return np.nan
    for _ in range(30):
        mid = 0.5 * (low + high)
        pm = _bs_price_kernel(is_call, S, K, T, 0, mid)
        if abs(pm - price) < 1e-05:
            return mid
        if pm < price:
            low = mid
        else:
            high = mid
    return 0.5 * (low + high)

@nb.njit(fastmath=True, cache=True)
def compute_iv_array(S_arr, K, T_arr, CE_arr, PE_arr):
    n = len(S_arr)
    out = np.empty(n, dtype=np.float64)
    for i in range(n):
        t = max(T_arr[i], 1e-05)
        if S_arr[i] < K:
            p = CE_arr[i]
            if math.isnan(p) or p <= 0.1:
                out[i] = np.nan
            else:
                out[i] = _iv_solver_kernel(True, p, S_arr[i], K, t)
        else:
            p = PE_arr[i]
            if math.isnan(p) or p <= 0.1:
                out[i] = np.nan
            else:
                out[i] = _iv_solver_kernel(False, p, S_arr[i], K, t)
    return out

@nb.njit(cache=True)
def opt_delta_gamma_arr(S, K, T, sig):
    """Short-straddle delta (-dc - dp) and gamma (-2g) per second. NaN-safe."""
    n = S.shape[0]
    od = np.empty(n, dtype=np.float64)
    og = np.empty(n, dtype=np.float64)
    for i in range(n):
        if math.isnan(S[i]) or math.isnan(K[i]) or math.isnan(T[i]) or math.isnan(sig[i]) or (T[i] <= 0.0) or (sig[i] <= 0.0) or (S[i] <= 0.0) or (K[i] <= 0.0):
            od[i] = np.nan
            og[i] = np.nan
            continue
        sq = math.sqrt(T[i])
        d1 = (math.log(S[i] / K[i]) + 0.5 * sig[i] * sig[i] * T[i]) / (sig[i] * sq)
        cd = _norm_cdf(d1)
        od[i] = -cd - (cd - 1.0)
        og[i] = -2.0 * (_norm_pdf(d1) / (S[i] * sig[i] * sq))
    return (od, og)

@nb.njit(cache=True)
def hedge_walk(opt_delta, opt_gamma, pts_allowed, is_min_start, hedge_frac, intra_mult, intra_frac, delta_thresh):
    """PRODUCTION second-by-second futures hedge (one strike, no roll).
    Second 0 = entry: futures = -option delta (flat).
    delta_thresh > 0 (DTE1 / DTE2): |NetDelta| rule
        minute start : |net| > thr        -> trade to (1 - hedge_frac) x thr on the SAME side
        intra-minute : |net| > mult x thr -> trade to (1 - intra_frac) x thr (1.0 = flat)
    delta_thresh = 0 (DTE3-7): points rule, PtsOut = |net / gamma|
        minute start : PtsOut > PtsAllowed        -> keep (1 - hedge_frac) x PtsAllowed x gamma
        intra-minute : PtsOut > mult x PtsAllowed -> keep (1 - intra_frac) x PtsAllowed x gamma"""
    n = opt_delta.shape[0]
    fut_tot = np.zeros(n, dtype=np.float64)
    fut_trade = np.zeros(n, dtype=np.float64)
    pos = 0.0
    for i in range(n):
        od = opt_delta[i]
        og = opt_gamma[i]
        if i == 0:
            pos = -od
            fut_trade[0] = pos
        net = od + pos
        if delta_thresh > 0.0:
            if not math.isnan(net):
                if is_min_start[i]:
                    thr = delta_thresh
                    hf = hedge_frac
                else:
                    thr = intra_mult * delta_thresh
                    hf = intra_frac
                if abs(net) > thr:
                    resid = (1.0 - hf) * delta_thresh
                    adj = -net - resid if net < 0.0 else -net + resid
                    pos += adj
                    fut_trade[i] += adj
        elif og != 0.0 and (not (math.isnan(og) or math.isnan(net))):
            pts = abs(net / og)
            pa = pts_allowed[i]
            if is_min_start[i]:
                thr = pa
                hf = hedge_frac
            else:
                thr = intra_mult * pa
                hf = intra_frac
            if pts > thr:
                if net < 0.0:
                    adj = -net + (1.0 - hf) * pa * og
                else:
                    adj = -net - (1.0 - hf) * pa * og
                pos += adj
                fut_trade[i] += adj
        fut_tot[i] = pos
    return (fut_tot, fut_trade)

@nb.njit(cache=True)
def hedge_notional_cum(fut_trade, u_bid, u_ask):
    n = fut_trade.shape[0]
    out = np.empty(n, dtype=np.float64)
    tot = 0.0
    for i in range(n):
        q = fut_trade[i]
        if q != 0.0:
            tot += abs(q) * 0.5 * (u_bid[i] + u_ask[i])
        out[i] = tot
    return out

@nb.njit(cache=True)
def futures_pnl(fut_tot, fut_trade, exec_bid, exec_ask, u_bid, u_ask):
    """Cash from 3-s average fills + liquidation of the open position at bid/ask."""
    n = fut_tot.shape[0]
    out = np.empty(n, dtype=np.float64)
    cash = 0.0
    for i in range(n):
        q = fut_trade[i]
        if q > 0.0:
            cash -= q * exec_ask[i]
        elif q < 0.0:
            cash -= q * exec_bid[i]
        p = fut_tot[i]
        if p > 0.0:
            liq = p * u_bid[i]
        elif p < 0.0:
            liq = p * u_ask[i]
        else:
            liq = 0.0
        out[i] = cash + liq
    return out

@nb.njit(fastmath=True, cache=True)
def apply_exits_fast_precomp(tot_pnl, otm_trade, otm_ref, underlying, time_min, otm_iv, dte, ce_exec_bid, pe_exec_bid, ce_exec_ask, pe_exec_ask, fut_pnl, sl_bps, tp_bps, tv_thresh, tv4_thresh, iv_drop_thresh, entry_underlying):
    """Minute-grid exit test (single strike, no roll). Returns (points, exit_row, reason)."""
    n = len(tot_pnl)
    max_loss_pts = -(entry_underlying * (sl_bps / 10000.0))
    max_prof_pts = entry_underlying * (tp_bps / 10000.0)
    exit_local_idx = -1
    reason = 0
    for i in range(n):
        sl_cond = tot_pnl[i] <= max_loss_pts
        tp_cond = tot_pnl[i] >= max_prof_pts
        tv_cond = 2.0 * otm_trade[i] < tv_thresh * underlying[i]
        tv4_cond = 2.0 * otm_ref[i] < tv4_thresh * underlying[i]
        iv_cond = time_min[i] >= 897 and otm_iv[i] < iv_drop_thresh and (dte[i] <= 1.0)
        if sl_cond or tp_cond or tv_cond or tv4_cond or iv_cond:
            exit_local_idx = i
            if sl_cond:
                reason = 1
            elif tp_cond:
                reason = 2
            elif tv_cond:
                reason = 3
            elif tv4_cond:
                reason = 4
            else:
                reason = 5
            break
    if exit_local_idx != -1:
        ev = ce_exec_bid[0] + pe_exec_bid[0]
        forced_ex_cost = ce_exec_ask[exit_local_idx] + pe_exec_ask[exit_local_idx]
        raw_pnl = ev - forced_ex_cost + fut_pnl[exit_local_idx]
        return (raw_pnl, exit_local_idx, reason)
    return (tot_pnl[n - 1], n - 1, 0)
TICK_COLS = ['Time_dt', 'Time', 'timestamp', 'datetime', 'Strike', 'StrikePrice', 'Type', 'LTP', 'Bid', 'Ask', 'DTE', 'Expiry', 'UnderlyingLTP', 'UnderlyingBid', 'UnderlyingAsk']

def _read_ticks(path):
    try:
        if path.endswith('.parquet'):
            import pyarrow.parquet as pq
            names = pq.ParquetFile(path).schema_arrow.names
            return pd.read_parquet(path, columns=[c for c in TICK_COLS if c in names])
        import pyarrow as pa
        with pa.memory_map(path, 'r') as src:
            names = pa.ipc.open_file(src).schema.names
        return pd.read_feather(path, columns=[c for c in TICK_COLS if c in names])
    except Exception:
        df = pd.read_parquet(path) if path.endswith('.parquet') else pd.read_feather(path)
        return df[[c for c in TICK_COLS if c in df.columns]]

def _split_expiries(df, base_date):
    """Option rows of each expiry on/after the trade date, nearest first."""
    df = df[df['Type'].astype(str).isin(['CE', 'PE'])]
    if 'Expiry' not in df.columns:
        return [df[df['DTE'] <= 6]]
    exp = pd.to_datetime(df['Expiry'])
    exps = sorted((e for e in exp.unique() if pd.Timestamp(e).normalize() >= base_date.normalize()))
    return [df[exp == e] for e in exps]

def load_day_ticks(file_path, base_date):
    """(current-week option rows, next-week option rows or None)."""
    df_all = _read_ticks(file_path)
    df_all['Type'] = df_all['Type'].astype(str)
    parts = _split_expiries(df_all, base_date)
    if not parts:
        return (pd.DataFrame(), None)
    cw = parts[0].reset_index(drop=True)
    nw = parts[1].reset_index(drop=True) if len(parts) > 1 and (not NW_INPUT_DIR) else None
    return (cw, nw)

def load_nw_split(file_path, base_date):
    """Next-week rows from NW_INPUT_DIR\\DDMMYYYY\\NIFTY_NW.feather (local split layout)."""
    ds = os.path.basename(os.path.dirname(file_path))
    for ext in ('.feather', '.parquet'):
        p = os.path.join(NW_INPUT_DIR, ds, 'NIFTY_NW' + ext)
        if os.path.exists(p):
            nw = _read_ticks(p)
            nw['Type'] = nw['Type'].astype(str)
            parts = _split_expiries(nw, base_date)
            return parts[0].reset_index(drop=True) if parts else None
    return None

def get_exact_iv_and_prices_inner_cache(strike, full_idx, df):
    """Per-second CE/PE LTP, Bid, Ask, OTM IV and 30-s execution prices for one
    strike - PREVIOUS-TICK logic for missing seconds (nothing rebuilt from IV)."""
    temp = df[df['_K'] == float(strike)]
    ce_t = temp[temp['Type'] == 'CE']
    pe_t = temp[temp['Type'] == 'PE']

    def _map_col(c):
        d = ce_t.set_index('Second')[c].to_dict()
        p = pe_t.set_index('Second')[c].to_dict()
        return (full_idx['Second'].map(d).ffill().bfill(), full_idx['Second'].map(p).ffill().bfill())
    ce_ltp, pe_ltp = _map_col('LTP')
    ce_bid, pe_bid = _map_col('Bid')
    ce_ask, pe_ask = _map_col('Ask')
    raw_iv = compute_iv_array(full_idx['Underlying'].values, float(strike), full_idx['DTE'].values / 365.0, ce_ltp.values, pe_ltp.values)
    sec = full_idx['Second']
    live = np.where(full_idx['Underlying'].values < float(strike), sec.isin(set(ce_t['Second'])).values, sec.isin(set(pe_t['Second'])).values)
    iv = np.where(live, raw_iv, np.nan)
    bad = (iv > 2.0) | (iv < 0.01)
    iv[bad] = np.nan
    iv = pd.Series(iv).ffill().bfill().values
    ce_bid = ce_bid.fillna(ce_ltp)
    pe_bid = pe_bid.fillna(pe_ltp)
    ce_ask = ce_ask.fillna(ce_ltp)
    pe_ask = pe_ask.fillna(pe_ltp)

    def _exec(s):
        return s.rolling(N_OPT_SECONDS, min_periods=1).mean().shift(-(N_OPT_SECONDS - 1)).fillna(s)
    cxb = _exec(ce_bid)
    pxb = _exec(pe_bid)
    cxa = _exec(ce_ask)
    pxa = _exec(pe_ask)
    return (ce_ltp.values, pe_ltp.values, iv, ce_bid.values, pe_bid.values, ce_ask.values, pe_ask.values, cxb.values, pxb.values, cxa.values, pxa.values)

def simulate_trade(idx_base, entry_idx, exit_idx, strike_data_cache, sl_bps, tp_bps, tv_thresh, tv4_thresh, iv_drop_thresh, date_str, dte_idx):
    """One current-week trade from second entry_idx. Option leg and exits on the
    minute grid; futures hedge walked every second. Returns a dict (normalised Rs)."""
    sim_base = idx_base.iloc[entry_idx:exit_idx + 1].copy().reset_index(drop=True)
    if sim_base.empty:
        return None
    real_start = sim_base['Time'].iloc[0]
    sim_base['safe_idx'] = np.arange(len(sim_base))
    if MINUTE_GRID == 'entry':
        mins = pd.date_range(real_start, sim_base['Time'].iloc[-1], freq='1min')
        tgt_mins = pd.Index([real_start]).union(mins)
        tgt_df = pd.DataFrame({'Time': tgt_mins})
        merged = pd.merge_asof(tgt_df, sim_base[['Time', 'safe_idx']], on='Time', direction='forward')
        tgt_idxs = merged['safe_idx'].dropna().astype(int).values
        tgt_idxs = np.unique(tgt_idxs[tgt_idxs < len(sim_base)])
    else:
        mins = pd.date_range(real_start.floor('1min') + pd.Timedelta(minutes=1), sim_base['Time'].iloc[-1], freq='1min')
        merged = pd.merge_asof(pd.DataFrame({'Time': mins}), sim_base[['Time', 'safe_idx']], on='Time', direction='backward')
        tgt_idxs = np.unique(np.concatenate([[0], merged['safe_idx'].dropna().astype(int).values]))
    sim = sim_base.iloc[tgt_idxs].copy().reset_index(drop=True)
    curr_strike = float(sim['ATMRef'].iloc[0])
    sim['ATMStrike'] = curr_strike
    arrays = strike_data_cache.get(curr_strike)
    if arrays is None:
        return None
    abs_idxs = sim['safe_idx'].values + entry_idx
    sim['CEPRICE'] = arrays[0][abs_idxs]
    sim['PEPRICE'] = arrays[1][abs_idxs]
    sim['OTMIV'] = arrays[2][abs_idxs]
    sim['CEBID'] = arrays[3][abs_idxs]
    sim['PEBID'] = arrays[4][abs_idxs]
    sim['CEASK'] = arrays[5][abs_idxs]
    sim['PEASK'] = arrays[6][abs_idxs]
    sim['CEExecBid'] = arrays[7][abs_idxs]
    sim['PEExecBid'] = arrays[8][abs_idxs]
    sim['CEExecAsk'] = arrays[9][abs_idxs]
    sim['PEExecAsk'] = arrays[10][abs_idxs]
    ev = float(sim['CEExecBid'].iloc[0] + sim['PEExecBid'].iloc[0])
    opt_pnl = ev - (sim['CEASK'].values + sim['PEASK'].values)
    opt_pnl[-1] = ev - (sim['CEExecAsk'].values[-1] + sim['PEExecAsk'].values[-1])
    row_pos = tgt_idxs.astype(np.int64)
    n_sec = int(row_pos[-1]) + 1
    sec_abs = np.arange(entry_idx, entry_idx + n_sec)
    S = np.ascontiguousarray(idx_base['Underlying'].values[sec_abs], dtype=np.float64)
    T = np.maximum(idx_base['DTE'].values[sec_abs] / 365.0, 1e-05).astype(np.float64)
    iv_sec = np.ascontiguousarray(arrays[2][sec_abs], dtype=np.float64)
    od, og = opt_delta_gamma_arr(S, np.full(n_sec, curr_strike), T, iv_sec)
    ivref = idx_base['OTMIVRef'].values[sec_abs].astype(np.float64)
    strad = (idx_base['CERef'].values[sec_abs] + idx_base['PERef'].values[sec_abs]).astype(np.float64)
    pts_allowed = np.maximum(np.minimum(ivref / PARAM_HEDGE_DIV * S, strad / PARAM_STR_D), PARAM_PTS_FLOOR * S)
    is_min = np.zeros(n_sec, dtype=np.bool_)
    is_min[row_pos] = True
    dthr = float(DELTA_THRESH.get(dte_idx, 0.0))
    fut_tot, fut_trade = hedge_walk(od, og, pts_allowed, is_min, PARAM_HEDGE_FRAC, PARAM_INTRA_MULT, PARAM_INTRA_FRAC, dthr)
    ub = np.ascontiguousarray(idx_base['UnderlyingBid'].values[sec_abs], dtype=np.float64)
    ua = np.ascontiguousarray(idx_base['UnderlyingAsk'].values[sec_abs], dtype=np.float64)
    hn_sec = hedge_notional_cum(fut_trade, ub, ua)
    fut_sec = futures_pnl(fut_tot, fut_trade, np.ascontiguousarray(idx_base['FutExecBid'].values[sec_abs], dtype=np.float64), np.ascontiguousarray(idx_base['FutExecAsk'].values[sec_abs], dtype=np.float64), ub, ua)
    fut_pnl = fut_sec[row_pos]
    tot_pnl = opt_pnl + fut_pnl
    U = sim['Underlying'].values.astype(np.float64)
    otm_trade = np.where(U > curr_strike, sim['PEPRICE'].values, sim['CEPRICE'].values).astype(np.float64)
    otm_ref = np.where(U > sim['ATMRef'].values, sim['PERef'].values, sim['CERef'].values).astype(np.float64)
    time_min = (sim['Time'].dt.hour * 60 + sim['Time'].dt.minute).values.astype(np.float64)
    entry_underlying = float(idx_base['Underlying'].iloc[entry_idx])
    raw_pts, exit_row, reason = apply_exits_fast_precomp(tot_pnl.astype(np.float64), otm_trade, otm_ref, U, time_min, sim['OTMIVRef'].values.astype(np.float64), sim['DTE'].values.astype(np.float64), sim['CEExecBid'].values.astype(np.float64), sim['PEExecBid'].values.astype(np.float64), sim['CEExecAsk'].values.astype(np.float64), sim['PEExecAsk'].values.astype(np.float64), fut_pnl.astype(np.float64), sl_bps, tp_bps, tv_thresh, tv4_thresh, iv_drop_thresh, entry_underlying)
    exit_row = int(exit_row)
    cut = int(row_pos[exit_row])
    trd = fut_trade[1:cut + 1] != 0.0
    ism = is_min[1:cut + 1]
    n_min_h = int(np.sum(trd & ism))
    n_intra_h = int(np.sum(trd & ~ism))
    if SAVE_DAILY_FILES:
        owner = np.searchsorted(row_pos, np.arange(n_sec), side='right') - 1
        traded = fut_trade != 0.0
        traded[0] = False
        df_h = sim[['Time', 'Underlying', 'ATMRef', 'ATMStrike', 'CEPRICE', 'PEPRICE', 'CEASK', 'PEASK', 'CEExecBid', 'PEExecBid', 'CEExecAsk', 'PEExecAsk', 'OTMIV', 'OTMIVRef', 'DTE']].copy()
        df_h['OptDelta'] = od[row_pos]
        df_h['OptGamma'] = og[row_pos]
        df_h['PtsAllowed'] = pts_allowed[row_pos]
        df_h['DeltaThresh'] = dthr
        df_h['FutTot'] = fut_tot[row_pos]
        df_h['NetDelta'] = od[row_pos] + fut_tot[row_pos]
        with np.errstate(divide='ignore', invalid='ignore'):
            df_h['PtsOut'] = np.abs(df_h['NetDelta'].values / df_h['OptGamma'].values)
        df_h['MinHedges'] = np.bincount(owner[traded & is_min], minlength=len(sim))
        df_h['IntraHedges'] = np.bincount(owner[traded & ~is_min], minlength=len(sim))
        df_h['HedgeNotional'] = hn_sec[row_pos]
        df_h['OptPnL'] = opt_pnl
        df_h['FutPnL'] = fut_pnl
        df_h['TotPnL'] = tot_pnl
        df_h['Exit'] = ''
        df_h.loc[exit_row, 'Exit'] = EXIT_REASONS[int(reason)]
        nd_before = od + fut_tot - fut_trade
        df_h['RowType'] = 'Minute'
        df_h.loc[0, 'RowType'] = 'Entry'
        df_h['HedgeQty'] = fut_trade[row_pos]
        df_h['NetDeltaBefore'] = nd_before[row_pos]
        with np.errstate(divide='ignore', invalid='ignore'):
            df_h['PtsOutBefore'] = np.abs(nd_before[row_pos] / og[row_pos])
        hard = np.nonzero(traded & ~is_min)[0]
        if len(hard):
            a = sec_abs[hard]
            ih = pd.DataFrame({'Time': idx_base['Time'].values[a], 'Underlying': S[hard], 'ATMRef': idx_base['ATMRef'].values[a], 'ATMStrike': curr_strike, 'CEPRICE': arrays[0][a], 'PEPRICE': arrays[1][a], 'CEASK': arrays[5][a], 'PEASK': arrays[6][a], 'CEExecBid': arrays[7][a], 'PEExecBid': arrays[8][a], 'CEExecAsk': arrays[9][a], 'PEExecAsk': arrays[10][a], 'OTMIV': arrays[2][a], 'OTMIVRef': idx_base['OTMIVRef'].values[a], 'DTE': idx_base['DTE'].values[a], 'OptDelta': od[hard], 'OptGamma': og[hard], 'PtsAllowed': pts_allowed[hard], 'DeltaThresh': dthr, 'FutTot': fut_tot[hard], 'NetDelta': od[hard] + fut_tot[hard]})
            with np.errstate(divide='ignore', invalid='ignore'):
                ih['PtsOut'] = np.abs(ih['NetDelta'].values / ih['OptGamma'].values)
                ih['PtsOutBefore'] = np.abs(nd_before[hard] / og[hard])
            ih['MinHedges'] = np.nan
            ih['IntraHedges'] = np.nan
            ih['HedgeNotional'] = hn_sec[hard]
            ih['OptPnL'] = ev - (arrays[5][a] + arrays[6][a])
            ih['FutPnL'] = fut_sec[hard]
            ih['TotPnL'] = ih['OptPnL'] + ih['FutPnL']
            ih['Exit'] = ''
            ih['RowType'] = 'Hard hedge'
            ih['HedgeQty'] = fut_trade[hard]
            ih['NetDeltaBefore'] = nd_before[hard]
            df_h = pd.concat([df_h, ih[df_h.columns]], ignore_index=True).sort_values('Time', kind='stable').reset_index(drop=True)
        os.makedirs(DAILY_FILES_DIR, exist_ok=True)
        df_h.to_parquet(os.path.join(DAILY_FILES_DIR, f"indexdf{date_str}_{idx_base.iloc[entry_idx]['Time_hhmm']}.parquet"), index=False)
    sc = PNL_SCALE / entry_underlying
    return dict(gross=float(raw_pts * sc), hedge_notional=float(hn_sec[row_pos][exit_row] * sc), strike=curr_strike, entry_underlying=entry_underlying, entry_credit=float(ev * sc), sl_bps=float(sl_bps), tp_bps=float(tp_bps), exit_time=sim['Time'].iloc[exit_row].strftime('%H:%M:%S'), exit_reason=EXIT_REASONS[int(reason)], min_hedges=n_min_h, intra_hedges=n_intra_h)

def _bs_py(is_call, S, K, T, sig):
    sq = math.sqrt(T)
    d1 = (math.log(S / K) + 0.5 * sig * sig * T) / (sig * sq)
    d2 = d1 - sig * sq
    nc = lambda x: 0.5 * (1.0 + math.erf(x / 1.4142135623730951))
    return S * nc(d1) - K * nc(d2) if is_call else K * nc(-d2) - S * nc(-d1)

def _iv_py(is_call, price, S, K, T):
    if not (price > 0.1 and T > 0 and (S > 0)):
        return np.nan
    lo, hi = (1e-06, 5.0)
    if _bs_py(is_call, S, K, T, lo) > price or _bs_py(is_call, S, K, T, hi) < price:
        return np.nan
    for _ in range(30):
        mid = 0.5 * (lo + hi)
        pm = _bs_py(is_call, S, K, T, mid)
        if abs(pm - price) < 1e-05:
            return mid
        lo, hi = (mid, hi) if pm < price else (lo, mid)
    return 0.5 * (lo + hi)

def nw_day(d):
    """09:16 NW features + the NW trade's PnL from the next-week option rows
    (columns Time_dt, Strike, Type, LTP, Bid, Ask, DTE, UnderlyingLTP).
    Returns (dict, None) or (None, reason)."""
    if d is None:
        return (None, 'nw_no_data')
    if d.empty:
        return (None, 'nw_empty')
    d = d.copy()
    if d['Time_dt'].dt.tz is not None:
        d['Time_dt'] = d['Time_dt'].dt.tz_localize(None)
    d['Type'] = d['Type'].astype(str)
    d = d[d.Type.isin(['CE', 'PE'])]
    d['sec'] = (d.Time_dt.dt.hour * 3600 + d.Time_dt.dt.minute * 60 + d.Time_dt.dt.second).astype(int)
    g = d.dropna(subset=['UnderlyingLTP']).groupby('sec').agg(u=('UnderlyingLTP', 'first'), dte=('DTE', 'first'))
    g = g[(g.index >= 9 * 3600 + 15 * 60) & (g.index <= 15 * 3600 + 27 * 60)]
    if g.empty:
        return (None, 'nw_no_underlying')
    secs = g.index.values

    def snap(hhmm):
        return int(np.searchsorted(secs, hhmm // 100 * 3600 + hhmm % 100 * 60, side='right')) - 1

    def first_at_or_after(hhmm):
        return int(np.searchsorted(secs, hhmm // 100 * 3600 + hhmm % 100 * 60, side='left'))
    p916 = snap(916)
    if p916 < 0:
        return (None, 'nw_no_0916_snapshot')
    s916 = secs[p916]
    u916 = float(g.u.iloc[p916])
    pre = d[(d.sec <= s916) & (d.LTP > 0)]
    have = pre.groupby(['Strike', 'Type']).size().unstack()
    if have is None or 'CE' not in have or 'PE' not in have:
        return (None, 'nw_no_atm_quotes')
    ok = have[['CE', 'PE']].dropna().index.values
    ok = ok[ok > 0]
    if not len(ok):
        return (None, 'nw_no_atm_quotes')
    K = float(ok[np.argmin(np.abs(ok - u916))])
    A = d[d.Strike == K]
    leg = {}
    for typ in ('CE', 'PE'):
        x = A[A.Type == typ].groupby('sec').last().reindex(secs)
        x['traded'] = x['LTP'].notna()
        for c in ('LTP', 'Bid', 'Ask'):
            x[c] = x[c].where(x[c] > 0).ffill()
        x['Bid'] = x['Bid'].fillna(x['LTP'])
        x['Ask'] = x['Ask'].fillna(x['LTP'])
        leg[typ] = x
    S = g.u.values.astype(float)
    T = np.maximum(g.dte.values / 365.0, 1e-05)
    ce, pe = (leg['CE'], leg['PE'])
    iv = np.full(len(secs), np.nan)
    ce_tr, pe_tr = (ce['traded'].values, pe['traded'].values)
    ce_l, pe_l = (ce['LTP'].values, pe['LTP'].values)
    for i in range(len(secs)):
        if S[i] < K:
            if ce_tr[i]:
                iv[i] = _iv_py(True, ce_l[i], S[i], K, T[i])
        elif pe_tr[i]:
            iv[i] = _iv_py(False, pe_l[i], S[i], K, T[i])
    iv[(iv > 2.0) | (iv < 0.01)] = np.nan
    iv = pd.Series(iv).ffill().values

    def xavg(s):
        return s.rolling(N_OPT_SECONDS, min_periods=1).mean().shift(-(N_OPT_SECONDS - 1)).fillna(s).values
    cb, pb = (ce['Bid'].reset_index(drop=True), pe['Bid'].reset_index(drop=True))
    ca, pa_ = (ce['Ask'].reset_index(drop=True), pe['Ask'].reset_index(drop=True))
    cxb, pxb, cxa, pxa = (xavg(cb), xavg(pb), xavg(ca), xavg(pa_))
    if not np.isfinite(iv[p916]):
        return (None, 'nw_no_0916_iv')
    for v in (cb.iat[p916], ca.iat[p916], pb.iat[p916], pa_.iat[p916]):
        if not np.isfinite(v):
            return (None, 'nw_no_0916_quotes')
    rows = []
    for t in [h * 100 + m for h in range(9, 16) for m in range(60)]:
        if t < NW_ENTRY_T or t > NW_EXIT_T:
            continue
        p = snap(t)
        q = first_at_or_after(t)
        if p < 0 or q >= len(secs):
            continue
        v = (cb.iat[p], ca.iat[p], pb.iat[p], pa_.iat[p], iv[p])
        if not all((np.isfinite(x) for x in v)):
            continue
        rows.append((t, v[0], v[1], v[2], v[3], v[4], S[p], float(g.dte.iloc[p]), cxb[q], pxb[q], cxa[q], pxa[q]))
    out = dict(nw_dte=round(float(g.dte.iloc[p916]), 3), iv_916=float(iv[p916]), strike=K, u_916=u916, pnl=float('nan'), exit_time=None, exit_reason=None)
    if len(rows) < 10:
        return (out, None)
    e = rows[0]
    u0 = e[6]
    credit = e[8] + e[9]
    sl_pts = -u0 * NW_SL_BPS / 10000.0
    pos = fut = hn = 0.0
    prev_u = u0
    ex = rows[-1]
    why = 'EOD'
    for r in rows:
        fut += pos * (r[6] - prev_u)
        prev_u = r[6]
        mid = (r[1] + r[2] + r[3] + r[4]) / 2
        if credit - mid + fut <= sl_pts:
            ex = r
            why = 'SL'
            break
        Tm = max(r[7] / 365.0, 1e-05)
        sg = max(r[5], 0.0001)
        d1 = (math.log(r[6] / K) + 0.5 * sg * sg * Tm) / (sg * math.sqrt(Tm))
        net = 1.0 - 2.0 * (0.5 * (1.0 + math.erf(d1 / 1.4142135623730951))) + pos
        if abs(net) > NW_HEDGE_THR:
            pos += -net
            hn += abs(net) * r[6]
    sc = PNL_SCALE / u0
    out.update(pnl=(credit - (ex[10] + ex[11]) + fut) * sc - NW_HEDGE_COST_BPS / 10000.0 * hn * sc, exit_time=f'{ex[0] // 100:02d}:{ex[0] % 100:02d}', exit_reason=why, entry_credit=credit * sc)
    return (out, None)

def nw_cell(base_date, dte_idx, weighted_idv, og_pct, nw_adj_iv, nw_expiry, nwm):
    """NW_LUT.csv coordinates (dte_idx, ratio, level, gap) or (None, features, reason)."""
    nw_dte = nwm['nw_dte']
    cal = int(np.ceil(nw_dte))
    d0 = np.datetime64(base_date.date(), 'D')
    wknd = cal - int(np.busday_count(d0, d0 + np.timedelta64(cal, 'D')))
    trading = float(np.round(nw_dte - wknd))
    adjf = math.sqrt(nw_dte / trading) if trading > 0 else float('nan')
    biv = nwm['iv_916'] * adjf
    ratio = biv / weighted_idv
    og_iv = nw_adj_iv
    if nw_expiry:
        tick_exp = base_date + pd.Timedelta(days=float(np.round(nw_dte) - 1))
        if pd.Timestamp(nw_expiry).normalize() != tick_exp.normalize():
            og_iv = float('nan')
    norm_og = og_pct * (0.19 / og_iv) if np.isfinite(og_iv) and og_iv > 0 else float('nan')
    feats = dict(nw_ratio=ratio, nw_build_iv=biv, nw_norm_og=norm_og)
    if not (np.isfinite(norm_og) and np.isfinite(ratio)):
        return (None, feats, 'nw_no_gap_feature')
    cell = (int(dte_idx), int(np.searchsorted(NW_RAT_EDGES, ratio)), int(np.searchsorted(NW_LVL_EDGES, biv)), int(np.searchsorted(NW_OG_EDGES, norm_og)))
    return (cell, feats, None)

def get_prev_idvs_local(current_date, idv_df):
    check = current_date - timedelta(days=0)
    for _ in range(30):
        m = idv_df[(idv_df['Date'] == check) & idv_df['IDV_Pure'].notna() & idv_df['IDV_With_Prev'].notna()]
        if not m.empty:
            return (float(m.iloc[-1]['IDV_Pure']), float(m.iloc[-1]['IDV_With_Prev']))
        check -= timedelta(days=1)
    raise ValueError('No IDV')

def worker_v_curve_evaluator(args):
    """
    Extracts 5 specific minute-by-minute profiles: 916, 917, 918, 919, and 920+
    (production veto / IV floor applied), simulates each first-occurrence entry
    with the production hedge, and evaluates the NW leg on DTE1-2 days.
    Saves to a temp cache file.
    """
    chunk_files, idv_history, master_prev_lookup, event_dates, worker_id, output_base = args
    try:
        day_cache = {}
        total_files = len(chunk_files)
        print(f'[INFO] Core {worker_id} compiled math, starting {total_files} files...')
        for i, file_path in enumerate(chunk_files):
            try:
                date_str = os.path.basename(os.path.dirname(file_path))
                day = int(date_str[:2])
                month = int(date_str[2:4])
                year = int(date_str[4:])
                base_date = pd.Timestamp(year=year, month=month, day=day)
                if month >= 4:
                    fy_label = f'FY{str(year)[-2:]}-{str(year + 1)[-2:]}'
                else:
                    fy_label = f'FY{str(year - 1)[-2:]}-{str(year)[-2:]}'
                is_event = base_date.date() in event_dates
                date_str_key = base_date.strftime('%Y-%m-%d')
                prev_straddle_row = master_prev_lookup.get(date_str_key)
                if not prev_straddle_row:
                    continue
                prev_straddle = float(prev_straddle_row['Straddle'])
                adj_iv_chg = float(prev_straddle_row['Normalized_Adj_Chg'])
                yesterday_iv = float(prev_straddle_row['Yesterday_IV'])
                prev_future = float(prev_straddle_row['Future'])
                if not all((np.isfinite(v) for v in (prev_straddle, adj_iv_chg, yesterday_iv, prev_future))):
                    continue
                try:
                    prev_pure, prev_with_prev = get_prev_idvs_local(base_date, idv_history)
                except:
                    continue
                if not (prev_pure > 0 and prev_with_prev > 0):
                    continue
                weighted_idv = WEIGHTS[0] * prev_pure + WEIGHTS[1] * prev_with_prev + WEIGHTS[2] * yesterday_iv
                if not weighted_idv > 0:
                    continue
                df, nw_df = load_day_ticks(file_path, base_date)
                if df.empty:
                    continue
                time_col = next((c for c in ['Time_dt', 'Time', 'timestamp', 'datetime', 'Date'] if c in df.columns), None)
                if time_col is None:
                    continue
                if not pd.api.types.is_datetime64_any_dtype(df[time_col]):
                    df[time_col] = pd.to_datetime(df[time_col])
                if df[time_col].dt.tz is not None:
                    df[time_col] = df[time_col].dt.tz_localize(None)
                df['Second'] = df[time_col].dt.time
                df['Time'] = df[time_col]
                strike_col = 'Strike' if 'Strike' in df.columns else 'StrikePrice'
                df['_K'] = pd.to_numeric(df[strike_col], errors='coerce')
                start_ts = base_date.replace(hour=9, minute=15, second=0)
                if base_date < GRID_END_SWITCH:
                    end_ts = base_date.replace(hour=15, minute=27, second=0)
                else:
                    end_ts = base_date.replace(hour=15, minute=37, second=0)
                idx_base = pd.DataFrame({'Time': pd.date_range(start_ts, end_ts, freq='1s')})
                idx_base['Second'] = idx_base['Time'].dt.time
                grp = df.groupby('Second')
                idx_base['Underlying'] = idx_base['Second'].map(grp['UnderlyingLTP'].first())
                idx_base['UnderlyingBid'] = idx_base['Second'].map(grp['UnderlyingBid'].first())
                idx_base['UnderlyingAsk'] = idx_base['Second'].map(grp['UnderlyingAsk'].first())
                idx_base['DTE'] = idx_base['Second'].map(grp['DTE'].first())
                idx_base.dropna(subset=['Underlying'], inplace=True)
                idx_base.reset_index(drop=True, inplace=True)
                if idx_base.empty:
                    continue
                idx_base['Time_hhmm'] = idx_base['Time'].dt.hour * 100 + idx_base['Time'].dt.minute
                underlying_0916_series = idx_base[idx_base['Time_hhmm'] >= 916]['Underlying']
                if underlying_0916_series.empty:
                    continue
                underlying_0916 = float(underlying_0916_series.values[0])
                if CHECK_FALLBACK == 'prev':
                    pre_0916 = idx_base[idx_base['Time'] <= base_date.replace(hour=9, minute=16, second=0)]
                    if not pre_0916.empty:
                        underlying_0916 = float(pre_0916['Underlying'].values[-1])
                og_pct = (underlying_0916 - prev_future) / prev_future * 100.0
                norm_og = og_pct * (0.19 / yesterday_iv)
                if pd.isna(norm_og):
                    continue
                idx_base['UnderlyingBid'] = idx_base['UnderlyingBid'].fillna(idx_base['Underlying'])
                idx_base['UnderlyingAsk'] = idx_base['UnderlyingAsk'].fillna(idx_base['Underlying'])

                def _fut_exec(s):
                    return s.rolling(N_FUT_SECONDS, min_periods=1).mean().shift(-(N_FUT_SECONDS - 1)).fillna(s)
                idx_base['FutExecBid'] = _fut_exec(idx_base['UnderlyingBid'])
                idx_base['FutExecAsk'] = _fut_exec(idx_base['UnderlyingAsk'])
                idx_base['ATMRef'] = (idx_base['Underlying'] / 50).round() * 50
                unique_refs = idx_base['ATMRef'].unique()
                strike_data_cache = {st: get_exact_iv_and_prices_inner_cache(st, idx_base, df) for st in unique_refs}
                idx_base['OTMIVRef'] = np.nan
                idx_base['CERef'] = np.nan
                idx_base['PERef'] = np.nan
                for st, arrays in strike_data_cache.items():
                    m = (idx_base['ATMRef'] == st).values
                    idx_base.loc[m, 'OTMIVRef'] = arrays[2][m]
                    idx_base.loc[m, 'CERef'] = arrays[0][m]
                    idx_base.loc[m, 'PERef'] = arrays[1][m]
                idx_base['Straddle'] = idx_base['CERef'] + idx_base['PERef']
                idx_base['Minute'] = idx_base['Time'].dt.floor('1min')
                if CHECK_FALLBACK == 'prev':
                    mins_df = pd.DataFrame({'Minute': np.sort(idx_base['Minute'].unique())})
                    feat_src = idx_base[['Time', 'CERef', 'PERef', 'OTMIVRef', 'DTE']].rename(columns={'Time': 'FeatTime'})
                    minute_bars = pd.merge_asof(mins_df, feat_src, left_on='Minute', right_on='FeatTime', direction='backward')
                else:
                    minute_bars = idx_base.groupby('Minute').first().reset_index()
                minute_bars['current_straddle'] = minute_bars['CERef'] + minute_bars['PERef']
                minute_bars['str_rat'] = prev_straddle / minute_bars['current_straddle'].replace(0, np.nan)
                minute_bars['build_iv'] = minute_bars['OTMIVRef']
                minute_bars = minute_bars.dropna(subset=['str_rat', 'build_iv', 'DTE'])
                if minute_bars.empty:
                    continue
                minute_bars = minute_bars[minute_bars['DTE'] > 0].reset_index(drop=True)
                if minute_bars.empty:
                    continue
                minute_bars['time_hhmm'] = minute_bars['Minute'].dt.hour * 100 + minute_bars['Minute'].dt.minute
                og_idx = int(np.searchsorted(OG_THRESHOLDS, norm_og, side='right'))
                a_idx = int(np.searchsorted(np.array([ADJ_LOWER, ADJ_HIGHER]), adj_iv_chg, side='right'))
                dates_np = np.array([base_date.date()], dtype='datetime64[D]')
                exp_offset = int(np.ceil(minute_bars['DTE'].iloc[0]))
                exp_dates_np = dates_np + np.timedelta64(exp_offset, 'D')
                bus_days = np.busday_count(dates_np, exp_dates_np)[0]
                min_to_idx = {mn: grp.index[0] for mn, grp in idx_base.groupby('Minute')}
                day_data = {'fy_label': fy_label, 'is_event': is_event, 'dte_idx': None, 'scans': [], 'pnl_cache': {}, 'nw': None, 'price_logic': PRICE_LOGIC_TAG}
                unique_916 = set()
                unique_917 = set()
                unique_918 = set()
                unique_919 = set()
                unique_920 = set()
                unique_sets = [unique_916, unique_917, unique_918, unique_919, unique_920]
                scan_window = minute_bars[(minute_bars['time_hhmm'] >= SCAN_FIRST_HHMM) & (minute_bars['time_hhmm'] <= SCAN_LAST_HHMM)]
                for _, row in scan_window.iterrows():
                    target_time = int(row['time_hhmm'])
                    if is_event and target_time < EVENT_FIRST_HHMM:
                        continue
                    raw_dte = float(row['DTE'])
                    weekend_days = int(np.ceil(raw_dte)) - bus_days
                    trading_dte = max(0.0, raw_dte - WKND_ADJ) if weekend_days > 0 else raw_dte
                    adj_factor = math.sqrt(raw_dte / trading_dte) if raw_dte > 0 and trading_dte > 0 and (raw_dte != trading_dte) else 1.0
                    dte_idx = int(np.clip(np.ceil(trading_dte) - 1, 0, 6))
                    if dte_idx == 0 and target_time > DTE1_LAST_HHMM:
                        break
                    target_minute = pd.Timestamp(row['Minute'])
                    entry_idx = min_to_idx.get(target_minute)
                    if entry_idx is None:
                        continue
                    if day_data['dte_idx'] is None:
                        day_data['dte_idx'] = dte_idx
                    adjusted_build_iv = float(row['build_iv']) * adj_factor
                    str_rat = float(row['str_rat'])
                    adj_iv_idv = adjusted_build_iv / weighted_idv
                    veto = VETO_STR.get(dte_idx)
                    if veto is not None and str_rat > veto:
                        continue
                    floor = IV_FLOOR.get(dte_idx)
                    if floor is not None and adjusted_build_iv < floor:
                        continue
                    iv_idx = int(np.searchsorted(IV_THRESHOLDS, adj_iv_idv, side='right'))
                    str_idx = int(np.searchsorted(STR_THRESHOLDS, str_rat, side='right'))
                    bld_idx = int(np.searchsorted(BLD_THRESHOLDS, adjusted_build_iv, side='right'))
                    coord = (dte_idx, iv_idx, str_idx, bld_idx, og_idx, a_idx)
                    t_idx = 0 if target_time == 916 else 1 if target_time == 917 else 2 if target_time == 918 else 3 if target_time == 919 else 4
                    if _COMPARE_FAST and (not _COMPARE_LUTS[t_idx][coord]):
                        continue
                    if coord in unique_sets[t_idx]:
                        continue
                    if entry_idx not in day_data['pnl_cache']:
                        rounded_bld = round(adjusted_build_iv, 2)
                        fraction = (rounded_bld - 0.08) / 0.12
                        tp_high = TP_HIGH[dte_idx]
                        tp_low = TP_LOW[dte_idx]
                        base_tp_bps = tp_low + fraction * (tp_high - tp_low)
                        tp_bps = float(max(1.0, round(base_tp_bps)))
                        sl_bps = float(SL_BPS_BY_DTE.get(dte_idx, SL_BPS))
                        exit_idx = len(idx_base) - 1
                        sim = simulate_trade(idx_base, entry_idx, exit_idx, strike_data_cache, sl_bps, tp_bps, TV_THRESH, TV4_THRESH, IV_DROP_THRESH, date_str, dte_idx)
                        if sim is None:
                            continue
                        gross = sim['gross'] / 3.0 if is_event else sim['gross']
                        hedge_notional = sim['hedge_notional'] / 3.0 if is_event else sim['hedge_notional']
                        entry_cost = ENTRY_SLIP.get(dte_idx, ENTRY_SLIP_DEF)
                        hedge_cost = HEDGE_SLIP_BPS.get(dte_idx, HEDGE_SLIP_DEF) / 10000.0 * hedge_notional
                        pnl = gross - entry_cost - hedge_cost
                        trades = EVENT_SIZE if is_event else 1.0
                        day_data['pnl_cache'][entry_idx] = {'pnl': pnl, 'trades': trades, 'gross': gross, 'entry_cost': entry_cost, 'hedge_cost': hedge_cost, 'hedge_notional': hedge_notional, 'strike': sim['strike'], 'entry_u': sim['entry_underlying'], 'sl_bps': sim['sl_bps'], 'tp_bps': sim['tp_bps'], 'exit_time': sim['exit_time'], 'exit_reason': sim['exit_reason'], 'min_hedges': sim['min_hedges'], 'intra_hedges': sim['intra_hedges'], 'build_iv': adjusted_build_iv, 'str_rat': str_rat, 'iv_ratio': adj_iv_idv}
                    unique_sets[t_idx].add(coord)
                    pnl_data = day_data['pnl_cache'][entry_idx]
                    data_point = {'coord': coord, 't_idx': t_idx, 'entry_idx': entry_idx, 'pnl': pnl_data['pnl'], 'trades': pnl_data['trades'], 'time_str': target_minute.strftime('%H:%M'), 'time_hhmm': target_time}
                    day_data['scans'].append(data_point)
                    if _COMPARE_FAST:
                        break
                if NW_ENABLED and day_data['dte_idx'] is not None and (day_data['dte_idx'] <= 1):
                    if NW_INPUT_DIR:
                        nw_df = load_nw_split(file_path, base_date)
                    if nw_df is not None and (not nw_df.empty):
                        ntc = next((c for c in ['Time_dt', 'Time', 'timestamp', 'datetime'] if c in nw_df.columns), None)
                        nsc = 'Strike' if 'Strike' in nw_df.columns else 'StrikePrice'
                        if ntc is None:
                            nw_df = None
                        else:
                            nw_df = nw_df.rename(columns={ntc: 'Time_dt', nsc: 'Strike'})
                            if not pd.api.types.is_datetime64_any_dtype(nw_df['Time_dt']):
                                nw_df['Time_dt'] = pd.to_datetime(nw_df['Time_dt'])
                            nw_df['Strike'] = pd.to_numeric(nw_df['Strike'], errors='coerce')
                    nwm, why = nw_day(nw_df)
                    if nwm is None:
                        day_data['nw'] = {'cell': None, 'pnl': float('nan'), 'note': why}
                    else:
                        cell, feats, why = nw_cell(base_date, day_data['dte_idx'], weighted_idv, og_pct, float(prev_straddle_row.get('NW_Adj_IV', float('nan'))), prev_straddle_row.get('NW_Expiry'), nwm)
                        day_data['nw'] = {'cell': cell, 'pnl': nwm['pnl'], 'strike': nwm['strike'], 'exit_time': nwm['exit_time'], 'exit_reason': nwm['exit_reason'], 'entry_u': nwm['u_916'], 'note': why or '', **feats}
                if day_data['scans'] or day_data['nw'] is not None:
                    day_cache[base_date.strftime('%Y-%m-%d')] = day_data
                del df, nw_df, idx_base, strike_data_cache, minute_bars, scan_window
                gc.collect()
                if (i + 1) % 25 == 0:
                    print(f'   [Core {worker_id}] Processed {i + 1}/{total_files} files...')
            except Exception as e:
                print(f'   [Core {worker_id}] [WARN] {os.path.basename(os.path.dirname(file_path))}: {type(e).__name__}: {e}')
        print(f'[SUCCESS] Core {worker_id} completely finished. Found {len(day_cache)} valid days.')
        temp_file_path = os.path.join(output_base, f'temp_cache_{worker_id}.pkl')
        with open(temp_file_path, 'wb') as f:
            pickle.dump(day_cache, f)
        return (worker_id, None)
    except Exception as e:
        error_trace = traceback.format_exc()
        return (worker_id, error_trace)

def _to_dates(s):
    """Log dates are written as YYYY-MM-DD; read those exactly (dayfirst=True on an
    ISO date swaps day and month whenever the day is 1-12). Anything else
    (an old DD-MM-YYYY log) is read day-first."""
    s = pd.Series(s).astype(str).str.strip()
    out = pd.to_datetime(s, format='%Y-%m-%d', errors='coerce')
    rest = out.isna()
    if rest.any():
        out[rest] = pd.to_datetime(s[rest], format='mixed', dayfirst=True, errors='coerce')
    return out

def get_extraction_resume_date(prev_day_cache, prev_log_df):
    """
    Determines where Phase 2 extraction should resume from by checking two
    existing outputs:
      1. The merged extraction cache from the last run (master_day_cache.pkl,
         already loaded as prev_day_cache) -- via its date keys.
      2. The final trade-sweep summary log (Production_Daily_Log_...csv,
         already loaded as prev_log_df) -- via its 'Date' column.

    Returns (cutoff_date, exclusive):
      exclusive=True  -> only extract files dated strictly after cutoff_date
      exclusive=False -> nothing found anywhere, fall back to START_DATE (inclusive)

    When both sources have data, the EARLIER of the two max-dates is used as
    the cutoff, so neither the extraction cache nor the summary log is left
    behind.
    """
    max_cache_date = None
    if prev_day_cache:
        try:
            max_cache_date = pd.to_datetime(max(prev_day_cache.keys()))
        except (ValueError, TypeError):
            max_cache_date = None
    max_log_date = None
    if prev_log_df is not None and (not prev_log_df.empty):
        max_log_date = _to_dates(prev_log_df['Date']).max()
    candidates = [d for d in [max_cache_date, max_log_date] if d is not None and (not pd.isna(d))]
    if not candidates:
        return (START_DATE, False)
    return (min(candidates), True)

def main():
    print('==================================================')
    print('>>> PRODUCTION CSV EXECUTION ENGINE (DATA MINE + TRADE) <<<')
    print(f'>>> hedge {PARAM_HEDGE_FRAC:.2f} minute / {PARAM_INTRA_MULT:.1f}x intra / DTE1-2 delta {DELTA_THRESH[0]:.2f} / {DELTA_THRESH[1]:.2f} (same side)')
    print('==================================================')
    print('\n[PHASE 1] Loading Pre-Optimized CSVs...')
    L16 = load_lut_from_csv(LUT_916_PATH)
    L17 = load_lut_from_csv(LUT_917_PATH)
    L18 = load_lut_from_csv(LUT_918_PATH)
    L19 = load_lut_from_csv(LUT_919_PATH)
    L20 = load_lut_from_csv(LUT_920_PATH)
    NWL = load_nw_lut_from_csv(NW_LUT_PATH) if NW_ENABLED else np.zeros(NW_DIMS, dtype=bool)
    lut_dict = {0: L16, 1: L17, 2: L18, 3: L19, 4: L20}
    print('\n[PHASE 2] Extracting Tick Data & Building RAM Cache...')
    try:
        _ = compute_iv_array(np.array([100.0]), 100.0, np.array([30.0 / 365.0]), np.array([5.0]), np.array([5.0]))
        d1 = np.array([100.0, 101.0], dtype=np.float64)
        od_, og_ = opt_delta_gamma_arr(d1, d1, np.array([0.1, 0.1]), np.array([0.2, 0.2]))
        _ = hedge_walk(od_, og_, d1, np.array([True, False]), 0.75, 1.2, 1.0, 0.0)
        _ = hedge_notional_cum(d1, d1, d1)
        _ = futures_pnl(d1, d1, d1, d1, d1, d1)
        _ = apply_exits_fast_precomp(d1, d1, d1, d1, d1, d1, d1, d1, d1, d1, d1, d1, 14.0, 30.0, 0.0001, 0.0002, 0.05, 20000.0)
    except Exception as e:
        pass
    try:
        event_dates_df = pd.read_excel(EVENT_PATH, header=None)
        event_dates = set(pd.to_datetime(event_dates_df[0], dayfirst=True).dt.date)
        print(f'  {len(event_dates)} event dates, last {max(event_dates)}')
    except:
        event_dates = set()
        print(f'  [WARN] EventDates not read from {EVENT_PATH} - event days will trade as normal days')
    idv_history = pd.read_csv(IDV_PATH)
    idv_history['Date'] = pd.to_datetime(idv_history['Date'], errors='coerce')
    idv_history = idv_history.sort_values('Date').reset_index(drop=True)
    prev_straddle_df = pd.read_csv(PREV_STR_PATH)
    prev_straddle_df['Date'] = pd.to_datetime(prev_straddle_df['Date'], errors='coerce')
    prev_straddle_df['PrevDate'] = pd.to_datetime(prev_straddle_df['PrevDate'], errors='coerce')
    prev_straddle_df['Prev2Date'] = pd.to_datetime(prev_straddle_df['Prev2Date'], errors='coerce')
    prev_straddle_df['Days_Diff'] = (prev_straddle_df['PrevDate'] - prev_straddle_df['Prev2Date']).dt.days
    gap_cond = prev_straddle_df['Days_Diff'] > 1
    prev_straddle_df['Target_Chg'] = np.where(gap_cond, prev_straddle_df['Adj_IV_Chg'], prev_straddle_df['IV_Chg'])
    denom_col = 'Prev2_IV'
    prev_straddle_df['Target_Base'] = np.where(gap_cond, prev_straddle_df['Prev2_Adj_IV'], prev_straddle_df[denom_col])
    prev_straddle_df['Target_Base'] = prev_straddle_df['Target_Base'].replace(0, np.nan)
    prev_straddle_df['Normalized_Adj_Chg'] = prev_straddle_df['Target_Chg'] * 0.1 / prev_straddle_df['Target_Base']
    yesterday_iv_col = 'Adj_IV'
    prev_straddle_df['Yesterday_IV'] = prev_straddle_df[yesterday_iv_col].replace(0, np.nan)
    master_prev_lookup = {}
    for idx, row in prev_straddle_df.iterrows():
        try:
            date_str_key = row['Date'].strftime('%Y-%m-%d')
            fut_val = row.get('Future')
            if pd.isna(fut_val) or fut_val is None:
                fut_val = row.get('Underlying')
            nw_iv = row.get('NW_Adj_IV', np.nan)
            nw_exp = row.get('NW_Expiry', None)
            master_prev_lookup[date_str_key] = {'Straddle': float(row['Straddle']), 'Normalized_Adj_Chg': float(row['Normalized_Adj_Chg']), 'Yesterday_IV': float(row['Yesterday_IV']), 'Future': float(fut_val), 'NW_Adj_IV': float(nw_iv) if pd.notna(nw_iv) else float('nan'), 'NW_Expiry': str(nw_exp) if pd.notna(nw_exp) else None}
        except Exception as e:
            pass
    all_files = sorted(glob.glob(os.path.join(INPUT_DIR, '*', 'NIFTY.parquet')) + glob.glob(os.path.join(INPUT_DIR, '*', 'NIFTY.feather')))

    def _file_date(f):
        try:
            ds = os.path.basename(os.path.dirname(f))
            return pd.Timestamp(year=int(ds[4:]), month=int(ds[2:4]), day=int(ds[:2]))
        except:
            return pd.Timestamp.min
    by_day = {}
    for f in all_files:
        k = os.path.dirname(f)
        if k not in by_day or f.endswith('.parquet'):
            by_day[k] = f
    all_files = list(by_day.values())
    prev_day_cache = {}
    if os.path.exists(MASTER_CACHE_PATH):
        try:
            with open(MASTER_CACHE_PATH, 'rb') as f:
                prev_day_cache = pickle.load(f)
        except Exception as e:
            print(f'  [WARN] Could not load existing master cache: {e}')
            prev_day_cache = {}
    prev_log_df = None
    if os.path.exists(DAILY_LOG_PATH):
        try:
            prev_log_df = pd.read_csv(DAILY_LOG_PATH)
        except Exception as e:
            print(f'  [WARN] Could not load existing daily log: {e}')
            prev_log_df = None
    old_logic_days = sorted((k for k, v in prev_day_cache.items() if not isinstance(v, dict) or v.get('price_logic') != PRICE_LOGIC_TAG))
    if old_logic_days:
        print(f'  [STOP] {len(old_logic_days)} cached day(s) ({old_logic_days[0]} .. {old_logic_days[-1]}) were built with a DIFFERENT rule set / LUT grid and cannot be mixed with the production book.\n         Point OUTPUT_BASE to a new folder (or move\n         {MASTER_CACHE_PATH}\n         and {DAILY_LOG_PATH} out of it) and run again.')
        return
    cutoff_date, exclusive = get_extraction_resume_date(prev_day_cache, prev_log_df)
    if exclusive:
        print(f'Existing output found. Resuming extraction for dates > {cutoff_date.date()}.')
        all_files = [f for f in all_files if _file_date(f) > cutoff_date and _file_date(f) not in SKIP_DATES]
    else:
        print(f'No existing output found. Building from START_DATE >= {cutoff_date.date()}.')
        all_files = [f for f in all_files if _file_date(f) >= cutoff_date and _file_date(f) not in SKIP_DATES]
    all_files.sort(key=_file_date)
    print(f'Total trading days to evaluate: {len(all_files)}\n')
    max_workers = max(1, min((os.cpu_count() or 2) - 1, 30))
    chunk_size = math.ceil(len(all_files) / max_workers) if all_files else 0
    tasks = []
    print(f'Distributing extraction across {max_workers} cores...')
    for i in range(max_workers):
        chunk = all_files[i * chunk_size:(i + 1) * chunk_size] if chunk_size else []
        if not chunk:
            continue
        tasks.append((chunk, idv_history, master_prev_lookup, event_dates, i, OUTPUT_BASE))
    new_day_cache = {}
    if tasks:
        with concurrent.futures.ProcessPoolExecutor(max_workers=max_workers) as executor:
            futures = {executor.submit(worker_v_curve_evaluator, task): task[4] for task in tasks}
            for future in concurrent.futures.as_completed(futures):
                worker_id = futures[future]
                try:
                    res_worker_id, error = future.result()
                    if error:
                        print(f'  [ERROR] Core {res_worker_id} failed:\n{error}')
                        continue
                    temp_path = os.path.join(OUTPUT_BASE, f'temp_cache_{res_worker_id}.pkl')
                    if os.path.exists(temp_path):
                        with open(temp_path, 'rb') as f:
                            worker_cache = pickle.load(f)
                        new_day_cache.update(worker_cache)
                        os.remove(temp_path)
                except Exception as e:
                    print(f'  [ERROR] Core {worker_id}: {e}')
    else:
        print('No new files to extract; reusing existing cache.')
    master_day_cache = {**prev_day_cache, **new_day_cache}
    try:
        with open(MASTER_CACHE_PATH, 'wb') as f:
            pickle.dump(master_day_cache, f)
    except Exception as e:
        print(f'  [WARN] Could not persist master cache: {e}')
    print(f'\n[SUCCESS] Cache Built! {len(new_day_cache)} new day(s), {len(master_day_cache)} total valid continuous days.')
    cache_excel_path = os.path.join(OUTPUT_BASE, 'day_cache.xlsx')
    day_rows = []
    scan_rows = []
    pnl_rows = []
    nw_rows = []
    for date_str, day_data in sorted(master_day_cache.items()):
        day_rows.append({'Date': date_str, 'FY': day_data.get('fy_label'), 'Is_Event': day_data.get('is_event'), 'DTE': day_data.get('dte_idx') + 1 if day_data.get('dte_idx') is not None else None, 'Scan_Count': len(day_data.get('scans', [])), 'Cached_Entry_Count': len(day_data.get('pnl_cache', {})), 'NW_Evaluated': day_data.get('nw') is not None})
        for scan in day_data.get('scans', []):
            scan_rows.append({'Date': date_str, 'Coord': str(scan.get('coord')), 'Stage_Index': scan.get('t_idx'), 'PnL': scan.get('pnl'), 'Trades': scan.get('trades'), 'Time': scan.get('time_str'), 'Time_HHMM': scan.get('time_hhmm')})
        for entry_idx, pnl_data in day_data.get('pnl_cache', {}).items():
            pnl_rows.append({'Date': date_str, 'Entry_Index': entry_idx, **pnl_data})
        nw = day_data.get('nw')
        if nw is not None:
            nw_rows.append({'Date': date_str, **{k: str(v) if k == 'cell' else v for k, v in nw.items()}})
    with pd.ExcelWriter(cache_excel_path) as writer:
        pd.DataFrame(day_rows).to_excel(writer, sheet_name='Days', index=False)
        pd.DataFrame(scan_rows).to_excel(writer, sheet_name='Scans', index=False)
        pd.DataFrame(pnl_rows).to_excel(writer, sheet_name='PnL_Cache', index=False)
        pd.DataFrame(nw_rows).to_excel(writer, sheet_name='NW', index=False)
    print(f'[SUCCESS] Day cache saved to: {cache_excel_path}')
    print(f'\n[PHASE 3] Executing Chronological Trade Sweep...')
    total_pnl = 0.0
    total_trades = 0.0
    trades_per_stage = [0, 0, 0, 0, 0, 0]
    fy_pnl = defaultdict(float)
    temp_log = []
    sorted_dates = sorted(master_day_cache.keys())
    for date_str in sorted_dates:
        data = master_day_cache[date_str]
        fy_label = data['fy_label']
        comfort = data.get('dte_idx') is not None and data['dte_idx'] <= 1
        hit = None
        for scan in data['scans']:
            if scan['time_hhmm'] > NW_AFTER_HHMM:
                break
            if lut_dict[scan['t_idx']][scan['coord']]:
                hit = ('CW', scan)
                break
        if hit is None and NW_ENABLED and comfort:
            nw = data.get('nw')
            if nw and nw.get('cell') is not None and NWL[nw['cell']] and np.isfinite(nw.get('pnl', np.nan)):
                hit = ('NW', nw)
        if hit is None:
            for scan in data['scans']:
                if scan['time_hhmm'] <= NW_AFTER_HHMM:
                    continue
                if lut_dict[scan['t_idx']][scan['coord']]:
                    hit = ('CW', scan)
                    break
        if hit is None:
            continue
        leg, obj = hit
        if leg == 'CW':
            t_idx = obj['t_idx']
            pc = data['pnl_cache'][obj['entry_idx']]
            if t_idx == 0:
                profiler = '09:16'
            elif t_idx == 1:
                profiler = '09:17'
            elif t_idx == 2:
                profiler = '09:18'
            elif t_idx == 3:
                profiler = '09:19'
            else:
                profiler = '09:20+'
            stage_bin = t_idx
            row = {'Date': date_str, 'PnL': obj['pnl'], 'FY': fy_label, 'Entry_Time': obj['time_str'], 'Profiler': profiler, 'Leg': 'CW', 'Trades': obj['trades'], 'Gross_PnL': pc['gross'], 'Entry_Cost': pc['entry_cost'], 'Hedge_Cost': pc['hedge_cost'], 'Hedge_Notional': pc['hedge_notional'], 'Strike': pc['strike'], 'Entry_U': pc['entry_u'], 'Exit_Time': pc['exit_time'], 'Exit_Reason': pc['exit_reason'], 'Min_Hedges': pc['min_hedges'], 'Intra_Hedges': pc['intra_hedges'], 'Coord': str(obj['coord'])}
        else:
            stage_bin = 5
            row = {'Date': date_str, 'PnL': obj['pnl'], 'FY': fy_label, 'Entry_Time': '09:21', 'Profiler': 'NW', 'Leg': 'NW', 'Trades': 1.0, 'Gross_PnL': obj['pnl'], 'Entry_Cost': 0.0, 'Hedge_Cost': 0.0, 'Hedge_Notional': np.nan, 'Strike': obj.get('strike'), 'Entry_U': obj.get('entry_u'), 'Exit_Time': obj.get('exit_time'), 'Exit_Reason': obj.get('exit_reason'), 'Min_Hedges': np.nan, 'Intra_Hedges': np.nan, 'Coord': str(obj['cell'])}
        total_pnl += row['PnL']
        total_trades += row['Trades']
        fy_pnl[fy_label] += row['PnL']
        trades_per_stage[stage_bin] += row['Trades']
        temp_log.append(row)
    print('\n==================================================')
    print('🏆 EXECUTION COMPLETE')
    print('==================================================')
    print(f'   Total Trades: {total_trades:.1f}')
    print(f'   Total PnL:    {total_pnl:.2f}  (normalised Rs, net of costs)\n')
    print('   Breakdown by Stage:')
    print(f'   09:16:  {trades_per_stage[0]:.1f} trades')
    print(f'   09:17:  {trades_per_stage[1]:.1f} trades')
    print(f'   09:18:  {trades_per_stage[2]:.1f} trades')
    print(f'   09:19:  {trades_per_stage[3]:.1f} trades')
    print(f'   09:20+: {trades_per_stage[4]:.1f} trades')
    print(f'   NW:     {trades_per_stage[5]:.1f} trades\n')
    print('   Breakdown by FY:')
    for fy in sorted(fy_pnl.keys()):
        print(f'   {fy}: {fy_pnl[fy]:.2f}')
    new_log_df = pd.DataFrame(temp_log, columns=LOG_COLS)
    if prev_log_df is not None and (not prev_log_df.empty):
        prev_log_aligned = prev_log_df.copy()
        for col in LOG_COLS:
            if col not in prev_log_aligned.columns:
                prev_log_aligned[col] = np.nan
        prev_log_aligned = prev_log_aligned[LOG_COLS]
        combined_log_df = pd.concat([prev_log_aligned, new_log_df], ignore_index=True)
    else:
        combined_log_df = new_log_df
    if not combined_log_df.empty:
        combined_log_df['Date'] = _to_dates(combined_log_df['Date']).values
        combined_log_df = combined_log_df.sort_values('Date')
        combined_log_df = combined_log_df.drop_duplicates(subset='Date', keep='last').reset_index(drop=True)
        combined_log_df['Date'] = combined_log_df['Date'].dt.strftime('%Y-%m-%d')
        combined_log_df['Cum_PnL'] = combined_log_df['PnL'].cumsum()
        combined_log_df['High_Water'] = combined_log_df['Cum_PnL'].cummax()
        combined_log_df['Drawdown'] = combined_log_df['Cum_PnL'] - combined_log_df['High_Water']
        mdd = combined_log_df['Drawdown'].min()
        print(f'\n   Overall Max Drawdown (full history): {mdd:.2f}')
        log_path = DAILY_LOG_PATH
        combined_log_df.to_csv(log_path, index=False)
        print(f'\n[SUCCESS] Daily Execution Log saved to: {log_path} ({len(new_log_df)} new row(s), {len(combined_log_df)} total)')
if __name__ == '__main__':
    main()

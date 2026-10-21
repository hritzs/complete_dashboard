import os
# CRITICAL FIX: Prevent massive CPU Thread Thrashing on Windows
os.environ['NUMBA_NUM_THREADS'] = '1'
os.environ['OMP_NUM_THREADS'] = '1'
os.environ['OPENBLAS_NUM_THREADS'] = '1'
os.environ['MKL_NUM_THREADS'] = '1'

import pandas as pd
import numpy as np
import pickle
import math
import glob
import warnings
import concurrent.futures
import gc
from datetime import timedelta
from collections import defaultdict
import traceback

# CRITICAL FIX: NumPy 2.0+ vs Numba Compatibility Patch
import numpy.polynomial.polynomial
if not hasattr(np.polynomial, 'polynomial'):
    np.polynomial.polynomial = numpy.polynomial.polynomial

import numba as nb

warnings.filterwarnings("ignore")

# ============================================================
# PATHS & GLOBAL STRATEGY PARAMETERS
# ============================================================
INPUT_DIR         = r"\\172.16.1.85\Shared\TickDataIV"
IDV_PATH          = r"\\172.16.1.85\Shared\Hardik\Project_Codes\GammaShortDailyProcess\GammaShortDailydata.csv"
PREV_STR_PATH     = r"\\172.16.1.85\Shared\Hardik\Project_Codes\GammaShortDailyProcess\GammaShortDailydata.csv"
OUTPUT_BASE       = r"C:\Users\Administrator\Documents\ScoreOptimizerV6_NIFTY\Custom_CSV_Execution_NewFull"
os.makedirs(OUTPUT_BASE, exist_ok=True)

# 🚨 POINT THESE TO YOUR 5 PRE-COMPUTED PROFILER CSVs 🚨
LUT_916_PATH = r"\\172.16.1.85\Shared\Hardik\Profilers\LUT_0916.csv"
LUT_917_PATH = r"\\172.16.1.85\Shared\Hardik\Profilers\LUT_0917.csv"
LUT_918_PATH = r"\\172.16.1.85\Shared\Hardik\Profilers\LUT_0918.csv"
LUT_919_PATH = r"\\172.16.1.85\Shared\Hardik\Profilers\LUT_0919.csv"
LUT_920_PATH = r"\\172.16.1.85\Shared\Hardik\Profilers\LUT_0920_Onwards.csv"

START_DATE        = pd.Timestamp('2026-10-07')

# RESUME-STATE PATHS
MASTER_CACHE_PATH = os.path.join(OUTPUT_BASE, "master_day_cache.pkl")
DAILY_LOG_PATH    = os.path.join(OUTPUT_BASE, "Loaded_LUTs_Daily_Log_01012021.csv")

# LUT ALIGNMENT PARAMS
ADJ_LOWER         = -0.008
ADJ_HIGHER        =  0.004
WEIGHTS           = (0.2, 0.5, 0.3) # (Pure, Prev, Target)
WKND_ADJ          = 1.0

# PRODUCTION RUN SETTINGS
SL_BPS            = 14.0
PARAM_HEDGE_DIV   = 57
PARAM_HEDGE_FRAC  = 1.0
PARAM_STR_D       = 4
N_OPT_SECONDS     = 30
N_FUT_SECONDS     = 3

# ============================================================
# ROBUST CSV LOADER
# ============================================================
def load_lut_from_csv(csv_path):
    print(f"Loading LUT: {os.path.basename(csv_path)}...")
    try:
        df = pd.read_csv(csv_path, sep=None, engine='python', encoding='utf-8-sig', dtype=str)
    except FileNotFoundError:
        print(f"  [ERROR] File not found: {csv_path}")
        return np.zeros((7, 10, 11, 5, 3, 3), dtype=bool)

    df.columns = df.columns.str.strip()
    
    # Mappings
    DTE_MAP = {'1':0, '2':1, '3':2, '4':3, '5':4, '6':5, '7':6}
    IV_MAP = {'<=0.65':0, '0.66-0.80':1, '0.81-0.90':2, '0.91-0.95':3, '0.96-1.00':4, '1.01-1.05':5, '1.06-1.10':6, '1.11-1.20':7, '1.21-1.30':8, '>1.30':9}
    STR_MAP = {'<=0.95':0, '0.96-1.00':1, '1.01-1.05':2, '1.06-1.10':3, '1.11-1.15':4, '1.16-1.20':5, '1.21-1.30':6, '1.31-1.50':7, '1.51-1.75':8, '1.76-2.00':9, '>2.00':10}
    BLD_MAP = {'<0.08':0, '0.08-0.12':1, '0.12-0.16':2, '0.16-0.20':3, '>=0.20':4}
    
    LUT = np.zeros((7, 10, 11, 5, 3, 3), dtype=bool)
    
    for idx, row in df.iterrows():
        trade_val = str(row.get('Trade', '')).strip().upper()
        if trade_val in ['YES', 'TRUE', '1']:
            try:
                raw_d = str(row.get('DTE', '')).replace('.0', '').strip()
                d = DTE_MAP[raw_d]
                i = IV_MAP[str(row.get('IV_Ratio', '')).replace(' ', '')]
                s = STR_MAP[str(row.get('Straddle_Ratio', '')).replace(' ', '')]
                b = BLD_MAP[str(row.get('Build_IV', '')).replace(' ', '')]
                
                # Robustly map OG Gap
                og_str = str(row.get('Norm_OG_Gap', '')).replace(' ', '')
                if '<' in og_str and 'Between' not in og_str: og = 0
                elif '>' in og_str and 'Between' not in og_str: og = 2
                else: og = 1
                
                # Robustly map Adj IV Chg
                a_str = str(row.get('Adj_IV_Chg', '')).replace(' ', '')
                if '<' in a_str and 'to' not in a_str: a = 0
                elif '>' in a_str and 'to' not in a_str: a = 2
                else: a = 1
                
                LUT[d, i, s, b, og, a] = True
            except KeyError as e:
                pass
                
    return LUT

# ============================================================
# HIGH PERFORMANCE BS / IV KERNELS (NUMBA JIT)
# ============================================================
@nb.njit(fastmath=True)
def _norm_cdf(x): return 0.5 * (1.0 + math.erf(x / 1.4142135623730951))

@nb.njit(fastmath=True)
def _norm_pdf(x): return 0.3989422804014327 * math.exp(-0.5 * x * x)

@nb.njit(fastmath=True)
def _bs_price_kernel(is_call, S, K, T, r, sig):
    if T <= 0 or sig <= 0: return 0.0
    sq = math.sqrt(T); d1 = (math.log(S / K) + (r + 0.5 * sig ** 2) * T) / (sig * sq); d2 = d1 - sig * sq
    if is_call: return S * _norm_cdf(d1) - K * math.exp(-r * T) * _norm_cdf(d2)
    else:       return K * math.exp(-r * T) * _norm_cdf(-d2) - S * _norm_cdf(-d1)

@nb.njit(fastmath=True)
def _iv_solver_kernel(is_call, price, S, K, T):
    if price <= 0 or T <= 0: return np.nan
    low, high = 1e-6, 5.0
    pl = _bs_price_kernel(is_call, S, K, T, 0, low); ph = _bs_price_kernel(is_call, S, K, T, 0, high)
    if pl > price or ph < price: return np.nan
    for _ in range(30):
        mid = 0.5 * (low + high); pm = _bs_price_kernel(is_call, S, K, T, 0, mid)
        if abs(pm - price) < 1e-5: return mid
        if pm < price: low = mid
        else:          high = mid
    return 0.5 * (low + high)

@nb.njit(fastmath=True)
def compute_iv_array(S_arr, K, T_arr, CE_arr, PE_arr):
    n = len(S_arr)
    out = np.empty(n, dtype=np.float64)
    for i in range(n):
        t = max(T_arr[i], 1e-5)
        if S_arr[i] < K:
            p = CE_arr[i]
            if math.isnan(p) or p <= 0.1: out[i] = np.nan
            else: out[i] = _iv_solver_kernel(True, p, S_arr[i], K, t)
        else:
            p = PE_arr[i]
            if math.isnan(p) or p <= 0.1: out[i] = np.nan
            else: out[i] = _iv_solver_kernel(False, p, S_arr[i], K, t)
    return out

@nb.njit
def distance_weighted_iv_numba(iv_values, time_seconds):
    n = len(iv_values); out = iv_values.copy(); valid = ~np.isnan(out)
    vi = np.where(valid)[0]
    if len(vi) == 0: return out
    for i in range(n):
        if valid[i]: continue
        ip = np.searchsorted(vi, i)
        if ip == 0 or ip == len(vi): continue
        p = vi[ip - 1]; nx = vi[ip]
        d1 = abs(time_seconds[i] - time_seconds[p]); d2 = abs(time_seconds[nx] - time_seconds[i])
        if d1 + d2 > 0: out[i] = (out[p] * d2 + out[nx] * d1) / (d1 + d2)
    return out

def bs_price(opt, S, K, T, r, sig):
    return _bs_price_kernel(opt == 'call', float(S), float(K), float(T), float(r), float(sig))
def bs_delta(opt, S, K, T, r, sig):
    if T <= 0 or sig <= 0: return np.nan
    d1 = (np.log(S / K) + (r + 0.5 * sig ** 2) * T) / (sig * np.sqrt(T))
    return _norm_cdf(d1) if opt == 'call' else _norm_cdf(d1) - 1.0
def bs_gamma(S, K, T, r, sig):
    if T <= 0 or sig <= 0: return np.nan
    d1 = (np.log(S / K) + (r + 0.5 * sig ** 2) * T) / (sig * np.sqrt(T))
    return _norm_pdf(d1) / (S * sig * np.sqrt(T))

# ============================================================
# C-SPEED NUMBA EXIT EVALUATOR
# ============================================================
# @nb.njit(fastmath=True)
def apply_exits_fast_precomp(tot_pnl, otm_trade, otm_ref, underlying, time_min, otm_iv, dte, chunk_id,
                             ce_exec_bid, pe_exec_bid, ce_exec_ask, pe_exec_ask, opt_pnl, fut_pnl,
                             sl_bps, tp_bps, tv_thresh, tv4_thresh, iv_drop_thresh, entry_underlying):
    n = len(tot_pnl)
    max_loss_pts  = -(entry_underlying * (sl_bps / 10000.0))
    max_prof_pts  = entry_underlying * (tp_bps / 10000.0)

    exit_local_idx = -1
    hit_tp = 0.0  
    
    for i in range(n):
        sl_cond = (tot_pnl[i] <= max_loss_pts)
        tp_cond = (tot_pnl[i] >= max_prof_pts)
        tv_cond = ((2.0 * otm_trade[i]) < (tv_thresh * underlying[i]))
        tv4_cond = ((2.0 * otm_ref[i]) < (tv4_thresh * underlying[i]))
        iv_cond = (time_min[i] >= 897) and (otm_iv[i] < iv_drop_thresh) and (dte[i] <= 1.0)

        if sl_cond or tp_cond or tv_cond or tv4_cond or iv_cond:
            exit_local_idx = i
            if tp_cond and not sl_cond:
                hit_tp = 1.0
            break

    if exit_local_idx != -1:
        c = chunk_id[exit_local_idx]
        first_idx = 0
        for i in range(n):
            if chunk_id[i] == c:
                first_idx = i
                break

        ev = ce_exec_bid[first_idx] + pe_exec_bid[first_idx]
        forced_ex_cost = ce_exec_ask[exit_local_idx] + pe_exec_ask[exit_local_idx]
        base = 0.0
        if c > 1:
            for i in range(n):
                if chunk_id[i] == c - 1:
                    base = opt_pnl[i]

        opt_pnl_val = ev - forced_ex_cost + base
        tot_pnl_val = opt_pnl_val + fut_pnl[exit_local_idx]
        raw_pnl = tot_pnl_val
    else:
        raw_pnl = tot_pnl[n - 1]

    return (raw_pnl), hit_tp

# ============================================================
# INSTANT SIMULATION ENGINE
# ============================================================
def get_exact_iv_and_prices_inner_cache(strike, full_idx, df, strike_col):
    temp = df[pd.to_numeric(df[strike_col], errors='coerce') == float(strike)]
    def _map_col(c):
        d = temp[temp['Type'] == 'CE'].set_index('Second')[c].to_dict()
        p = temp[temp['Type'] == 'PE'].set_index('Second')[c].to_dict()
        return full_idx['Second'].map(d).ffill().bfill(), full_idx['Second'].map(p).ffill().bfill()
        
    ce_ltp, pe_ltp = _map_col('LTP'); ce_bid, pe_bid = _map_col('Bid'); ce_ask, pe_ask = _map_col('Ask')
    
    raw_iv = compute_iv_array(full_idx['Underlying'].values, float(strike), (full_idx['DTE'].values / 365.0), ce_ltp.values, pe_ltp.values)
    tsec = full_idx['Time'].dt.hour * 3600 + full_idx['Time'].dt.minute * 60 + full_idx['Time'].dt.second
    iv = distance_weighted_iv_numba(raw_iv, tsec.values)
    bad = (iv > 2.0) | (iv < 0.01); iv[bad] = np.nan; iv = pd.Series(iv).ffill().bfill().values
    
    S = full_idx['Underlying'].values; T = np.maximum(full_idx['DTE'].values / 365.0, 1e-5)
    ct = np.array([bs_price('call', s, strike, t, 0, v) for s, t, v in zip(S, T, iv)])
    pt = np.array([bs_price('put',  s, strike, t, 0, v) for s, t, v in zip(S, T, iv)])
    
    ce_ltp = ce_ltp.fillna(pd.Series(ct)); pe_ltp = pe_ltp.fillna(pd.Series(pt))
    ce_bid = ce_bid.fillna(pd.Series(ct)); pe_bid = pe_bid.fillna(pd.Series(ct))
    ce_ask = ce_ask.fillna(pd.Series(ct)); pe_ask = pe_ask.fillna(pd.Series(pt))
    
    def _exec(s): return s.rolling(N_OPT_SECONDS, min_periods=1).mean().shift(-(N_OPT_SECONDS - 1)).fillna(s)
    cxb = _exec(ce_bid); pxb = _exec(pe_bid); cxa = _exec(ce_ask); pxa = _exec(pe_ask)
    return (ce_ltp.values, pe_ltp.values, iv, ce_bid.values, pe_bid.values, ce_ask.values, pe_ask.values, cxb.values, pxb.values, cxa.values, pxa.values)

def simulate_trade(idx_base, entry_idx, exit_idx, strike_data_cache, sl_bps, tp_bps, tv_thresh, tv4_thresh, iv_drop_thresh,date_str):
    sim_base = idx_base.iloc[entry_idx:exit_idx + 1].copy().reset_index(drop=True)
    if sim_base.empty: return 0.0, 0.0

    real_start = sim_base['Time'].iloc[0]
    sim_base['safe_idx'] = np.arange(len(sim_base))
    mins = pd.date_range(real_start, sim_base['Time'].iloc[-1], freq='1min')
    tgt_mins = pd.Index([real_start]).union(mins)
    tgt_df = pd.DataFrame({'Time': tgt_mins})
    merged = pd.merge_asof(tgt_df, sim_base[['Time', 'safe_idx']], on='Time', direction='forward')
    tgt_idxs = merged['safe_idx'].dropna().astype(int).values
    tgt_idxs = np.unique(tgt_idxs[tgt_idxs < len(sim_base)])
    sim = sim_base.iloc[tgt_idxs].copy().reset_index(drop=True)

    curr_strike = sim['ATMRef'].iloc[0]
    sim['ATMStrike'] = curr_strike

    abs_idxs = sim['safe_idx'].values + entry_idx
    arrays = strike_data_cache.get(curr_strike)
    if arrays is None: return 0.0, 0.0
    
    sim['CEPRICE'] = arrays[0][abs_idxs];  sim['PEPRICE'] = arrays[1][abs_idxs];  sim['OTMIV'] = arrays[2][abs_idxs]
    sim['CEBID'] = arrays[3][abs_idxs];    sim['PEBID'] = arrays[4][abs_idxs];    sim['CEASK'] = arrays[5][abs_idxs]; sim['PEASK'] = arrays[6][abs_idxs]
    sim['CEExecBid'] = arrays[7][abs_idxs];sim['PEExecBid'] = arrays[8][abs_idxs];sim['CEExecAsk'] = arrays[9][abs_idxs];sim['PEExecAsk'] = arrays[10][abs_idxs]

    sim['Dist'] = abs(sim['Underlying'] - sim['ATMStrike'])
    sim['RollThreshold'] = sim['Straddle'] / 0.0001
    sim['Flag'] = (sim['Dist'] > sim['RollThreshold'])
    sim['Flag'] = sim['Flag'] & sim['Flag'].shift(1).fillna(False).astype(bool)
    sim['Flag'] = sim['Flag'] & (sim['Time'].dt.hour >= 13)
    sim['LegacyExitCost'] = np.nan

    for _ in range(30):
        if not sim['Flag'].any(): break
        i = sim['Flag'].idxmax()
        new_st = sim.loc[i, 'ATMRef']
        exit_cost = sim.iloc[i]['CEExecAsk'] + sim.iloc[i]['PEExecAsk']
        
        hist = sim.iloc[:i + 1].copy()
        hist.iloc[-1, hist.columns.get_loc('Flag')] = False
        hist.loc[hist.index[-1], 'LegacyExitCost'] = exit_cost
        
        fwd = sim.iloc[i:].copy()
        fwd['ATMStrike'] = new_st
        
        fwd_abs_idxs = fwd['safe_idx'].values + entry_idx
        fwd_arrays = strike_data_cache.get(new_st)
        if fwd_arrays is None: break
            
        fwd['CEPRICE'] = fwd_arrays[0][fwd_abs_idxs];  fwd['PEPRICE'] = fwd_arrays[1][fwd_abs_idxs];  fwd['OTMIV'] = fwd_arrays[2][fwd_abs_idxs]
        fwd['CEBID'] = fwd_arrays[3][fwd_abs_idxs];    fwd['PEBID'] = fwd_arrays[4][fwd_abs_idxs];    fwd['CEASK'] = fwd_arrays[5][fwd_abs_idxs]; fwd['PEASK'] = fwd_arrays[6][fwd_abs_idxs]
        fwd['CEExecBid'] = fwd_arrays[7][fwd_abs_idxs];fwd['PEExecBid'] = fwd_arrays[8][fwd_abs_idxs];fwd['CEExecAsk'] = fwd_arrays[9][fwd_abs_idxs];fwd['PEExecAsk'] = fwd_arrays[10][fwd_abs_idxs]
        
        fwd['Dist'] = abs(fwd['Underlying'] - fwd['ATMStrike'])
        fwd['RollThreshold'] = fwd['Straddle'] / 0.0001
        fwd['Flag'] = (fwd['Dist'] > fwd['RollThreshold'])
        fwd['Flag'] = fwd['Flag'] & fwd['Flag'].shift(1).fillna(False).astype(bool)
        fwd['Flag'] = fwd['Flag'] & (fwd['Time'].dt.hour >= 13)
        sim = pd.concat([hist, fwd], ignore_index=True)

    idx_res = sim
    ts_arr = np.maximum(idx_res['DTE'].values / 365.0, 1e-5)
    
    d_c = [bs_delta('call', s, k, t, 0, v) for s, k, t, v in zip(idx_res['Underlying'], idx_res['ATMStrike'], ts_arr, idx_res['OTMIV'])]
    d_p = [bs_delta('put',  s, k, t, 0, v) for s, k, t, v in zip(idx_res['Underlying'], idx_res['ATMStrike'], ts_arr, idx_res['OTMIV'])]
    idx_res['OptDelta'] = -np.array(d_c) - np.array(d_p)
    
    g_c = [bs_gamma(s, k, t, 0, v) for s, k, t, v in zip(idx_res['Underlying'], idx_res['ATMStrike'], ts_arr, idx_res['OTMIV'])]
    idx_res['OptGamma'] = -2 * np.array(g_c)

    idx_res['FutHedge'] = 0.0
    idx_res.loc[0, 'FutHedge'] = -idx_res.loc[0, 'OptDelta']
    chg = idx_res['ATMStrike'] != idx_res['ATMStrike'].shift(); chg.iloc[0] = False
    idx_res.loc[chg, 'FutHedge'] = -idx_res.loc[chg, 'OptDelta']

    raw_pts = np.minimum((idx_res['OTMIVRef'] / PARAM_HEDGE_DIV) * idx_res['Underlying'], (idx_res['CERef'] + idx_res['PERef']) / PARAM_STR_D)
    idx_res['PtsAllowed'] = np.maximum(raw_pts, 0.0008 * idx_res['Underlying'])

    df_h = idx_res.copy()
    df_h['chunk_id'] = df_h['ATMStrike'].ne(df_h['ATMStrike'].shift()).cumsum()
    df_h['FutTot'] = np.nan
    for c in df_h['chunk_id'].unique():
        m = df_h['chunk_id'] == c
        df_h.loc[m, 'FutTot'] = df_h.loc[m, 'FutHedge'].iloc[0]
        
    df_h['NetDelta'] = df_h['OptDelta'] + df_h['FutTot']
    df_h['PtsOut'] = np.where(df_h['OptGamma'].fillna(0) != 0, abs(df_h['NetDelta'] / df_h['OptGamma']), np.nan)

    prev_net = 0
    for c in df_h['chunk_id'].unique():
        m = df_h['chunk_id'] == c; idxs = df_h.index[m]
        if idxs[0] != df_h.index[0]:
            df_h.loc[idxs, 'FutTot']      += prev_net
            df_h.loc[idxs[0], 'FutHedge'] += prev_net
            df_h.loc[idxs, 'NetDelta']     = df_h.loc[idxs, 'OptDelta'] + df_h.loc[idxs, 'FutTot']
            df_h.loc[idxs, 'PtsOut']       = abs(df_h.loc[idxs, 'NetDelta'] / df_h.loc[idxs, 'OptGamma'])
            
        it = 0
        while (df_h.loc[idxs, 'PtsOut'] > df_h.loc[idxs, 'PtsAllowed']).any():
            it += 1
            if it > 100: break
            bad = df_h.loc[idxs][df_h.loc[idxs, 'PtsOut'] > df_h.loc[idxs, 'PtsAllowed']]
            i = bad.index[0]; nd = df_h.loc[i, 'NetDelta']; pa = df_h.loc[i, 'PtsAllowed']; gm = df_h.loc[i, 'OptGamma']
            adj = (-nd + (1 - PARAM_HEDGE_FRAC) * pa * gm) if nd < 0 else (-nd - (1 - PARAM_HEDGE_FRAC) * pa * gm)
            
            df_h.loc[i, 'FutHedge'] = adj; app = (df_h.index >= i) & m
            df_h.loc[app, 'FutTot']  += adj
            df_h.loc[app, 'NetDelta'] = df_h.loc[app, 'OptDelta'] + df_h.loc[app, 'FutTot']
            df_h.loc[app, 'PtsOut']   = abs(df_h.loc[app, 'NetDelta'] / df_h.loc[app, 'OptGamma'])
            
        prev_net = df_h.loc[idxs[-1], 'NetDelta']

    df_h['OptPnL'] = np.nan
    for c in df_h['chunk_id'].unique():
        m = df_h['chunk_id'] == c; sub = df_h.loc[m]
        ev = sub.iloc[0]['CEExecBid'] + sub.iloc[0]['PEExecBid']
        final = sub.index[-1]; lexit = sub.iloc[-1]['LegacyExitCost']
        ex_cost = lexit if pd.notna(lexit) else (sub.iloc[-1]['CEExecAsk'] + sub.iloc[-1]['PEExecAsk'])
        pnl = ev - (sub['CEASK'] + sub['PEASK']); pnl.loc[final] = ev - ex_cost
        base = df_h.loc[df_h['chunk_id'] == (c - 1), 'OptPnL'].iloc[-1] if c > 1 else 0.0
        df_h.loc[m, 'OptPnL'] = pnl + base

    df_h['TradeQty'] = df_h['FutTot'] - df_h['FutTot'].shift(1).fillna(0.0)
    df_h['FlowCash'] = np.where(df_h['TradeQty']>0, -df_h['TradeQty'] * df_h['FutExecAsk'],
                                np.where(df_h['TradeQty']<0, -df_h['TradeQty'] * df_h['FutExecBid'], 0.0))
    df_h['CumCash'] = df_h['FlowCash'].cumsum()
    
    liq = np.where(df_h['FutTot'] > 0, df_h['FutTot'] * df_h['UnderlyingBid'],
                   np.where(df_h['FutTot'] < 0, df_h['FutTot'] * df_h['UnderlyingAsk'], 0.0))
                   
    df_h['FutPnL'] = df_h['CumCash'] + liq
    df_h['TotPnL'] = df_h['OptPnL'] + df_h['FutPnL']

    df_h['OTM_Price_Trade'] = np.where(df_h['Underlying'] > df_h['ATMStrike'], df_h['PEPRICE'], df_h['CEPRICE'])
    df_h['OTM_Price_Ref'] = np.where(df_h['Underlying'] > df_h['ATMRef'], df_h['PERef'], df_h['CERef'])
    df_h['time_in_minutes'] = df_h['Time'].dt.hour * 60 + df_h['Time'].dt.minute
    
    if not os.path.exists(os.path.join(OUTPUT_BASE, fr"DailyFiles\indexdf{date_str}_{idx_base.iloc[entry_idx]['Time_hhmm']}.parquet")):
        df_h.to_parquet(os.path.join(OUTPUT_BASE, fr"DailyFiles\indexdf{date_str}_{idx_base.iloc[entry_idx]['Time_hhmm']}.parquet"), index=False)
    
    entry_underlying = idx_base.iloc[entry_idx]['Underlying']
    
    arrs = (
        df_h['TotPnL'].values.astype(np.float64),
        df_h['OTM_Price_Trade'].values.astype(np.float64),
        df_h['OTM_Price_Ref'].values.astype(np.float64),
        df_h['Underlying'].values.astype(np.float64),
        df_h['time_in_minutes'].values.astype(np.float64),
        df_h['OTMIVRef'].values.astype(np.float64), 
        df_h['DTE'].values.astype(np.float64),
        df_h['chunk_id'].values.astype(np.int64),
        df_h['CEExecBid'].values.astype(np.float64),
        df_h['PEExecBid'].values.astype(np.float64),
        df_h['CEExecAsk'].values.astype(np.float64),
        df_h['PEExecAsk'].values.astype(np.float64),
        df_h['OptPnL'].values.astype(np.float64),
        df_h['FutPnL'].values.astype(np.float64)
    )
    
    raw_pnl, hit_tp = apply_exits_fast_precomp(*arrs, sl_bps, tp_bps, tv_thresh, tv4_thresh, iv_drop_thresh, entry_underlying)
    return raw_pnl, hit_tp

# ============================================================
# ACTOR WORKER - EXTRACT 5 TIME-WINDOW SCANS
# ============================================================
def get_prev_idvs_local(current_date, idv_df):
    check = current_date - timedelta(days=0)
    for _ in range(30):
        m = idv_df[idv_df['Date'] == check]
        if not m.empty: 
            return float(m.iloc[-1]['IDV_Pure']), float(m.iloc[-1]['IDV_With_Prev'])
        check -= timedelta(days=1)
    raise ValueError("No IDV")

def worker_v_curve_evaluator(args):
    """
    Extracts 5 specific minute-by-minute profiles: 916, 917, 918, 919, and 920+.
    Saves to a temp cache file.
    """
    chunk_files, idv_history, master_prev_lookup, event_dates, worker_id, output_base = args
    
    try:
        iv_thresholds  = np.array([0.6501, 0.8001, 0.9001, 0.9501, 1.0001, 1.0501, 1.1001, 1.2001, 1.3001])
        str_thresholds = np.array([0.9501, 1.0001, 1.0501, 1.1001, 1.1501, 1.2001, 1.3001, 1.5001, 1.7501, 2.0001])
        bld_thresholds = np.array([0.0801, 0.1201, 0.1601, 0.2001])
        og_thresholds  = np.array([-0.7501, 0.7501]) 
        adj_thresholds = np.array([ADJ_LOWER, ADJ_HIGHER])
        
        tp_high_arr = np.array([30.0, 25.0, 25.0, 20.0, 15.0, 15.0, 15.0], dtype=np.float64)
        tp_low_arr  = np.array([16.0, 13.0, 13.0, 10.0,  8.0,  8.0,  8.0], dtype=np.float64)

        day_cache = {}
        total_files = len(chunk_files)
        
        print(f"[INFO] Core {worker_id} compiled math, starting {total_files} files...")

        for i, file_path in enumerate(chunk_files):
            try:
                date_str  = os.path.basename(os.path.dirname(file_path))
                day = int(date_str[:2]); month = int(date_str[2:4]); year = int(date_str[4:])
                base_date = pd.Timestamp(year=year, month=month, day=day)

                if month >= 4: fy_label = f"FY{str(year)[-2:]}-{str(year+1)[-2:]}"
                else:          fy_label = f"FY{str(year-1)[-2:]}-{str(year)[-2:]}"

                is_event = base_date.date() in event_dates

                df = pd.read_parquet(file_path)
                df = df[df['DTE'] <= 7].copy()
                if df.empty: continue
                
                # ROBUST TIME DETECTOR
                time_col = next((c for c in ['Time_dt', 'Time', 'timestamp', 'datetime', 'Date'] if c in df.columns), None)
                if time_col is None: continue
                
                if not pd.api.types.is_datetime64_any_dtype(df[time_col]): 
                    df[time_col] = pd.to_datetime(df[time_col])
                if df[time_col].dt.tz is not None:
                    df[time_col] = df[time_col].dt.tz_localize(None)
                    
                df['Second'] = df[time_col].dt.time
                df['Time'] = df[time_col]

                date_str_key = base_date.strftime('%Y-%m-%d')
                prev_straddle_row = master_prev_lookup.get(date_str_key)
                if not prev_straddle_row: continue
                
                prev_straddle = float(prev_straddle_row['Straddle'])
                adj_iv_chg = float(prev_straddle_row['Normalized_Adj_Chg'])
                yesterday_iv = float(prev_straddle_row['Yesterday_IV'])
                prev_future = float(prev_straddle_row['Future'])
                
                if pd.isna(adj_iv_chg): continue

                try: prev_pure, prev_with_prev = get_prev_idvs_local(base_date, idv_history)
                except: continue
                if prev_pure <= 0 or prev_with_prev <= 0: continue
                
                weighted_idv = WEIGHTS[0]*prev_pure + WEIGHTS[1]*prev_with_prev + WEIGHTS[2]*yesterday_iv
                if weighted_idv <= 0: continue
                # print(f"[INFO] Core {worker_id} processing {file_path} | Weighted IDV: {weighted_idv:.4f} | Prev Pure: {prev_pure:.4f} | Prev With Prev: {prev_with_prev:.4f} | Yesterday IV: {yesterday_iv:.4f}")
                start_ts = base_date.replace(hour=9, minute=15, second=0)
                if base_date < pd.Timestamp(2026, 6, 3):
                    end_ts   = base_date.replace(hour=15, minute=27, second=0)
                else:
                    end_ts   = base_date.replace(hour=15, minute=37, second=0)
                
                idx_base = pd.DataFrame({'Time': pd.date_range(start_ts, end_ts, freq='1s')})
                idx_base['Second'] = idx_base['Time'].dt.time
                
                grp = df.groupby('Second')
                idx_base['Underlying']    = idx_base['Second'].map(grp['UnderlyingLTP'].first())
                idx_base['UnderlyingBid'] = idx_base['Second'].map(grp['UnderlyingBid'].first())
                idx_base['UnderlyingAsk'] = idx_base['Second'].map(grp['UnderlyingAsk'].first())
                idx_base['DTE']           = idx_base['Second'].map(grp['DTE'].first())
                idx_base.dropna(subset=['Underlying'], inplace=True)
                idx_base.reset_index(drop=True, inplace=True)
                if idx_base.empty: continue
                
                idx_base['Time_hhmm'] = idx_base['Time'].dt.hour * 100 + idx_base['Time'].dt.minute
                
                # OG CALCULATED STRICTLY OFF 09:16 UNDERLYING
                underlying_0916_series = idx_base[idx_base['Time_hhmm'] >= 916]['Underlying']
                if underlying_0916_series.empty: continue
                underlying_0916 = float(underlying_0916_series.values[0])
                
                og_pct = ((underlying_0916 - prev_future) / prev_future) * 100.0
                norm_og = og_pct * (0.19 / yesterday_iv)
                
                if pd.isna(norm_og): continue
                
                idx_base['UnderlyingBid'] = idx_base['UnderlyingBid'].fillna(idx_base['Underlying'])
                idx_base['UnderlyingAsk'] = idx_base['UnderlyingAsk'].fillna(idx_base['Underlying'])
                idx_base['ATMStrike']     = round(idx_base.iloc[0]['Underlying'] / 50) * 50

                def _fut_exec(s): return s.rolling(N_FUT_SECONDS, min_periods=1).mean().shift(-(N_FUT_SECONDS - 1)).fillna(s)
                idx_base['FutExecBid'] = _fut_exec(idx_base['UnderlyingBid'])
                idx_base['FutExecAsk'] = _fut_exec(idx_base['UnderlyingAsk'])
                idx_base['ATMRef'] = (idx_base['Underlying'] / 50).round() * 50
                
                unique_refs = idx_base['ATMRef'].unique()
                
                # ROBUST STRIKE DETECTOR
                strike_col = 'Strike' if 'Strike' in df.columns else 'StrikePrice'
                strike_data_cache = {st: get_exact_iv_and_prices_inner_cache(st, idx_base, df, strike_col) for st in unique_refs}

                idx_base['OTMIVRef'] = np.nan
                for st, arrays in strike_data_cache.items():
                    m = idx_base['ATMRef'] == st
                    idx_base.loc[m, 'OTMIVRef'] = arrays[2][m]

                ts_arr = np.maximum(idx_base['DTE'].values / 365.0, 1e-5)
                idx_base['CERef'] = [bs_price('call', s, k, t, 0, v) for s, k, t, v in zip(idx_base['Underlying'], idx_base['ATMRef'], ts_arr, idx_base['OTMIVRef'])]
                idx_base['PERef'] = [bs_price('put',  s, k, t, 0, v) for s, k, t, v in zip(idx_base['Underlying'], idx_base['ATMRef'], ts_arr, idx_base['OTMIVRef'])]
                idx_base['Straddle'] = idx_base['CERef'] + idx_base['PERef']

                idx_base['Minute'] = idx_base['Time'].dt.floor('1min')

                minute_bars  = idx_base.groupby('Minute').first().reset_index()
                minute_bars['current_straddle'] = minute_bars['CERef'] + minute_bars['PERef']
                minute_bars['str_rat']  = prev_straddle / minute_bars['current_straddle'].replace(0, np.nan)
                minute_bars['build_iv'] = minute_bars['OTMIVRef']
                
                minute_bars = minute_bars.dropna(subset=['str_rat', 'build_iv', 'DTE'])
                if minute_bars.empty: continue
                minute_bars = minute_bars[minute_bars['DTE'] > 0].reset_index(drop=True)

                minute_bars['time_hhmm'] = minute_bars['Minute'].dt.hour * 100 + minute_bars['Minute'].dt.minute
                
                og_idx = np.searchsorted(og_thresholds, norm_og, side='right')
                a_idx = np.searchsorted(adj_thresholds, adj_iv_chg, side='right')
                
                dates_np = np.array([base_date.date()], dtype='datetime64[D]')
                exp_offset = int(np.ceil(minute_bars['DTE'].iloc[0]))
                exp_dates_np = dates_np + np.timedelta64(exp_offset, 'D')
                bus_days = np.busday_count(dates_np, exp_dates_np)[0]
                
                min_to_idx = {mn: grp.index[0] for mn, grp in idx_base.groupby('Minute')}
                
                day_data = {
                    'fy_label': fy_label,
                    'is_event': is_event,
                    'scans': [],
                    'pnl_cache': {}
                }
                
                unique_916 = set()
                unique_917 = set()
                unique_918 = set()
                unique_919 = set()
                unique_920 = set()
                
                # FULL DAY SCAN WINDOW
                scan_window = minute_bars[(minute_bars['time_hhmm'] >= 916) & (minute_bars['time_hhmm'] <= 1330)]
                
                for _, row in scan_window.iterrows():
                    target_time = row['time_hhmm']
                    
                    # 🚨 Event rule: skip EVERYTHING before 09:18 on event dates
                    if is_event and target_time < 918:
                        continue
                        
                    raw_dte = row['DTE']
                    weekend_days = int(np.ceil(raw_dte)) - bus_days
                    trading_dte = max(0.0, raw_dte - WKND_ADJ) if weekend_days > 0 else raw_dte
                    adj_factor = math.sqrt(raw_dte / trading_dte) if raw_dte > 0 and trading_dte > 0 and raw_dte != trading_dte else 1.0
                    
                    dte_idx = np.clip(np.ceil(trading_dte) - 1, 0, 6).astype(int)
                    
                    # 🚨 1 DTE Rule: Stop evaluating after 09:59
                    if dte_idx == 0 and target_time > 959:
                        break
                        
                    adjusted_build_iv = row['build_iv'] * adj_factor
                    adj_iv_idv = adjusted_build_iv / weighted_idv
                    
                    iv_idx = np.searchsorted(iv_thresholds, adj_iv_idv, side='right')
                    str_idx = np.searchsorted(str_thresholds, row['str_rat'], side='right')
                    bld_idx = np.searchsorted(bld_thresholds, adjusted_build_iv, side='right')
                    
                    coord = (dte_idx, iv_idx, str_idx, bld_idx, og_idx, a_idx)
                    
                    target_minute = pd.Timestamp(row['Minute'])
                    entry_idx = min_to_idx.get(target_minute)
                    
                    if entry_idx is not None:
                        # 🚨 THE 5 TIME WINDOWS
                        t_idx = 0 if target_time == 916 else (1 if target_time == 917 else (2 if target_time == 918 else (3 if target_time == 919 else 4)))
                        
                        is_new = False
                        if t_idx == 0 and coord not in unique_916: unique_916.add(coord); is_new = True
                        elif t_idx == 1 and coord not in unique_917: unique_917.add(coord); is_new = True
                        elif t_idx == 2 and coord not in unique_918: unique_918.add(coord); is_new = True
                        elif t_idx == 3 and coord not in unique_919: unique_919.add(coord); is_new = True
                        elif t_idx == 4 and coord not in unique_920: unique_920.add(coord); is_new = True
                        
                        # FIRST-OCCURRENCE THEOREM 
                        if is_new:
                            if entry_idx not in day_data['pnl_cache']:
                                rounded_bld = round(adjusted_build_iv, 2)
                                fraction = (rounded_bld - 0.08) / 0.12
                                tp_high = tp_high_arr[dte_idx]
                                tp_low  = tp_low_arr[dte_idx]
                                base_tp_bps = tp_low + (fraction * (tp_high - tp_low))
                                tp_bps = float(max(1.0, round(base_tp_bps)))
                                
                                exit_idx = len(idx_base) - 1
                                raw_pnl, _ = simulate_trade(
                                    idx_base, entry_idx, exit_idx, strike_data_cache, 
                                    SL_BPS, tp_bps, 0.00015, 0.0006, 0.1,date_str
                                )
                                
                                pnl = raw_pnl / 3.0 if is_event else raw_pnl
                                trades = 0.333333 if is_event else 1.0
                                
                                day_data['pnl_cache'][entry_idx] = {'pnl': pnl, 'trades': trades}
                        
                        if entry_idx in day_data['pnl_cache']:
                            pnl_data = day_data['pnl_cache'][entry_idx]
                            
                            data_point = {
                                'coord': coord,
                                't_idx': t_idx,
                                'pnl': pnl_data['pnl'],
                                'trades': pnl_data['trades'],
                                'time_str': target_minute.strftime('%H:%M'),
                                'time_hhmm': target_time
                            }
                            
                            # 1. Execution Scan Timeline
                            day_data['scans'].append(data_point)

                if day_data['scans']:
                    day_cache[base_date.strftime('%Y-%m-%d')] = day_data

                del df, idx_base, strike_data_cache, minute_bars, scan_window
                gc.collect()

                if (i+1) % 25 == 0:
                    print(f"   [Core {worker_id}] Processed {i+1}/{total_files} files...")

            except Exception as e:
                pass 

        print(f"[SUCCESS] Core {worker_id} completely finished. Found {len(day_cache)} valid days.")
        
        temp_file_path = os.path.join(output_base, f"temp_cache_{worker_id}.pkl")
        with open(temp_file_path, 'wb') as f:
            pickle.dump(day_cache, f)
        
            
        return worker_id, None
        
    except Exception as e:
        error_trace = traceback.format_exc()
        return worker_id, error_trace


# ============================================================
# MASTER COORDINATOR
# ============================================================
def get_extraction_resume_date(prev_day_cache, prev_log_df):
    """
    Determines where Phase 2 extraction should resume from by checking two
    existing outputs:
      1. The merged extraction cache from the last run (master_day_cache.pkl,
         already loaded as prev_day_cache) -- via its date keys.
      2. The final trade-sweep summary log (Loaded_LUTs_Daily_Log_...csv,
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
    if prev_log_df is not None and not prev_log_df.empty:
        max_log_date = pd.to_datetime(
            prev_log_df['Date'], format='mixed', dayfirst=True, errors="coerce"
        ).max()

    candidates = [d for d in [max_cache_date, max_log_date] if d is not None]
    if not candidates:
        return START_DATE, False

    return min(candidates), True


def main():
    print("==================================================")
    print(">>> FULL CSV EXECUTION ENGINE (DATA MINE + TRADE) <<<")
    print("==================================================")
    
    # 1. Load the 5 Profilers
    print("\n[PHASE 1] Loading Pre-Optimized CSVs...")
    L16 = load_lut_from_csv(LUT_916_PATH)
    L17 = load_lut_from_csv(LUT_917_PATH)
    L18 = load_lut_from_csv(LUT_918_PATH)
    L19 = load_lut_from_csv(LUT_919_PATH)
    L20 = load_lut_from_csv(LUT_920_PATH)
    
    lut_dict = {
        0: L16,
        1: L17,
        2: L18,
        3: L19,
        4: L20
    }
    
    # 2. Extract Data and Build Cache
    print("\n[PHASE 2] Extracting Tick Data & Building RAM Cache...")
    try:
        _ = bs_price('call', 100.0, 100.0, 30.0 / 365.0, 0.0, 0.2)
        _ = bs_delta('call', 100.0, 100.0, 30.0 / 365.0, 0.0, 0.2)
        _ = bs_gamma(100.0, 100.0, 30.0 / 365.0, 0.0, 0.2)
        _ = compute_iv_array(np.array([100.0]), 100.0, np.array([30.0/365.0]), np.array([5.0]), np.array([5.0]))
        dummy_f = np.array([100.0], dtype=np.float64); dummy_i = np.array([1], dtype=np.int64)
        _, _ = apply_exits_fast_precomp(dummy_f, dummy_f, dummy_f, dummy_f, dummy_f, dummy_f, dummy_f, dummy_i, 
                                        dummy_f, dummy_f, dummy_f, dummy_f, dummy_f, dummy_f, 
                                        14.0, 30.0, 0.0001, 0.0002, 0.05, 20000.0)
    except Exception as e:
        pass

    try:
        event_dates_df = pd.read_excel(r"\\172.16.1.85\Shared\EventDates.xlsx", header=None)
        event_dates = set(pd.to_datetime(event_dates_df[0], dayfirst=True).dt.date)
    except:
        event_dates = set()

    idv_history = pd.read_csv(IDV_PATH)
    idv_history['Date'] = pd.to_datetime(idv_history['Date'],errors='coerce')
    # idv_history['Date'] = idv_history['Date'].dt.normalize()
    idv_history = idv_history.sort_values('Date').reset_index(drop=True)

    prev_straddle_df = pd.read_csv(PREV_STR_PATH)
    prev_straddle_df['Date'] = pd.to_datetime(prev_straddle_df['Date'], errors='coerce')
    # prev_straddle_df['Date'] = prev_straddle_df['Date'].dt.normalize()
    prev_straddle_df['PrevDate'] = pd.to_datetime(prev_straddle_df['PrevDate'], errors='coerce')
    prev_straddle_df['Prev2Date'] = pd.to_datetime(prev_straddle_df['Prev2Date'], errors='coerce')

    prev_straddle_df['Days_Diff'] = (prev_straddle_df['PrevDate'] - prev_straddle_df['Prev2Date']).dt.days
    gap_cond = prev_straddle_df['Days_Diff'] > 1
    
    prev_straddle_df['Target_Chg'] = np.where(gap_cond, prev_straddle_df['Adj_IV_Chg'], prev_straddle_df['IV_Chg'])
    denom_col = 'Prev2_IV'
    prev_straddle_df['Target_Base'] = np.where(gap_cond, prev_straddle_df['Prev2_Adj_IV'], prev_straddle_df[denom_col])
    prev_straddle_df['Target_Base'] = prev_straddle_df['Target_Base'].replace(0, np.nan)
    prev_straddle_df['Normalized_Adj_Chg'] = (prev_straddle_df['Target_Chg'] * 0.1) / prev_straddle_df['Target_Base']

    yesterday_iv_col = 'Adj_IV'
    prev_straddle_df['Yesterday_IV'] = prev_straddle_df[yesterday_iv_col].replace(0, np.nan)
    
    master_prev_lookup = {}
    for idx, row in prev_straddle_df.iterrows():
        try:
            date_str_key = row['Date'].strftime('%Y-%m-%d')
            fut_val = row.get('Future')
            if pd.isna(fut_val) or fut_val is None:
                fut_val = row.get('Underlying')
                
            master_prev_lookup[date_str_key] = {
                'Straddle': float(row['Straddle']),
                'Normalized_Adj_Chg': float(row['Normalized_Adj_Chg']),
                'Yesterday_IV': float(row['Yesterday_IV']),
                'Future': float(fut_val) 
            }
        except Exception as e: 
            pass

    all_files = sorted(glob.glob(os.path.join(INPUT_DIR, "*", "NIFTY.parquet")))

    def _file_date(f):
        try:
            ds = os.path.basename(os.path.dirname(f))
            return pd.Timestamp(year=int(ds[4:]), month=int(ds[2:4]), day=int(ds[:2]))
        except: return pd.Timestamp.min

    # --- RESUME: load any existing extraction cache and summary log ---
    prev_day_cache = {}
    if os.path.exists(MASTER_CACHE_PATH):
        try:
            with open(MASTER_CACHE_PATH, 'rb') as f:
                prev_day_cache = pickle.load(f)
        except Exception as e:
            print(f"  [WARN] Could not load existing master cache: {e}")
            prev_day_cache = {}

    prev_log_df = None
    if os.path.exists(DAILY_LOG_PATH):
        try:
            prev_log_df = pd.read_csv(DAILY_LOG_PATH)
        except Exception as e:
            print(f"  [WARN] Could not load existing daily log: {e}")
            prev_log_df = None

    cutoff_date, exclusive = get_extraction_resume_date(prev_day_cache, prev_log_df)
    if exclusive:
        print(f"Existing output found. Resuming extraction for dates > {cutoff_date.date()}.")
        all_files = [f for f in all_files if _file_date(f) > cutoff_date and _file_date(f) != pd.Timestamp('2024-04-18')]
    else:
        print(f"No existing output found. Building from START_DATE >= {cutoff_date.date()}.")
        all_files = [f for f in all_files if _file_date(f) >= cutoff_date and _file_date(f) != pd.Timestamp('2024-04-18')]
    all_files.sort(key=_file_date)
    
    print(f"Total trading days to evaluate: {len(all_files)}\n")

    max_workers = min(os.cpu_count() - 1, 30) 
    chunk_size = math.ceil(len(all_files) / max_workers) if all_files else 0
    
    tasks = []
    print(f"Distributing extraction across {max_workers} cores...")
    for i in range(max_workers):
        chunk = all_files[i*chunk_size : (i+1)*chunk_size] if chunk_size else []
        if not chunk: continue
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
                        continue
                        
                    temp_path = os.path.join(OUTPUT_BASE, f"temp_cache_{res_worker_id}.pkl")
                    if os.path.exists(temp_path):
                        with open(temp_path, 'rb') as f:
                            worker_cache = pickle.load(f)
                        new_day_cache.update(worker_cache)
                        os.remove(temp_path)
                except Exception as e:
                    pass
    else:
        print("No new files to extract; reusing existing cache.")

    # Merge newly-extracted days into the resumed cache and persist for next run
    master_day_cache = {**prev_day_cache, **new_day_cache}
    try:
        with open(MASTER_CACHE_PATH, 'wb') as f:
            pickle.dump(master_day_cache, f)
    except Exception as e:
        print(f"  [WARN] Could not persist master cache: {e}")

    print(f"\n[SUCCESS] Cache Built! {len(new_day_cache)} new day(s), {len(master_day_cache)} total valid continuous days.")

    # Export the merged cache in flat tables so it can be inspected in Excel.
    cache_excel_path = os.path.join(OUTPUT_BASE, "day_cache.xlsx")
    day_rows = []
    scan_rows = []
    pnl_rows = []
    for date_str, day_data in sorted(master_day_cache.items()):
        day_rows.append({
            'Date': date_str,
            'FY': day_data.get('fy_label'),
            'Is_Event': day_data.get('is_event'),
            'Scan_Count': len(day_data.get('scans', [])),
            'Cached_Entry_Count': len(day_data.get('pnl_cache', {}))
        })

        for scan in day_data.get('scans', []):
            scan_rows.append({
                'Date': date_str,
                'Coord': str(scan.get('coord')),
                'Stage_Index': scan.get('t_idx'),
                'PnL': scan.get('pnl'),
                'Trades': scan.get('trades'),
                'Time': scan.get('time_str'),
                'Time_HHMM': scan.get('time_hhmm')
            })

        for entry_idx, pnl_data in day_data.get('pnl_cache', {}).items():
            pnl_rows.append({
                'Date': date_str,
                'Entry_Index': entry_idx,
                'PnL': pnl_data.get('pnl'),
                'Trades': pnl_data.get('trades')
            })

    with pd.ExcelWriter(cache_excel_path) as writer:
        pd.DataFrame(day_rows).to_excel(writer, sheet_name='Days', index=False)
        pd.DataFrame(scan_rows).to_excel(writer, sheet_name='Scans', index=False)
        pd.DataFrame(pnl_rows).to_excel(writer, sheet_name='PnL_Cache', index=False)
    print(f"[SUCCESS] Day cache saved to: {cache_excel_path}")

    # 3. Execute the Sweep
    print(f"\n[PHASE 3] Executing Chronological Trade Sweep...")

    total_pnl = 0.0
    total_trades = 0.0
    trades_per_stage = [0, 0, 0, 0, 0]
    
    fy_pnl = defaultdict(float)
    temp_log = []
    
    sorted_dates = sorted(master_day_cache.keys())
    
    for date_str in sorted_dates:
        data = master_day_cache[date_str]
        fy_label = data['fy_label']
        
        trade_taken = False
        pnl = 0.0
        entry_time = 'NONE'
        profiler = 'NONE'
        trades_ct = 0.0
        
        for scan in data['scans']:
            t_idx = scan['t_idx'] # 0=916, 1=917, 2=918, 3=919, 4=920+
            active_lut = lut_dict[t_idx]
            
            if active_lut[scan['coord']]:
                pnl = scan['pnl']
                entry_time = scan['time_str']
                trades_ct = scan['trades']
                
                if t_idx == 0: profiler = "09:16"; stage_bin = 0
                elif t_idx == 1: profiler = "09:17"; stage_bin = 1
                elif t_idx == 2: profiler = "09:18"; stage_bin = 2
                elif t_idx == 3: profiler = "09:19"; stage_bin = 3
                else: profiler = "09:20+"; stage_bin = 4
                
                total_pnl += pnl
                total_trades += scan['trades']
                fy_pnl[fy_label] += pnl
                trade_taken = True
                
                trades_per_stage[stage_bin] += scan['trades']
                break 
            
        if trade_taken:
            temp_log.append({
                'Date': date_str, 'PnL': pnl, 'FY': fy_label, 
                'Entry_Time': entry_time, 'Profiler': profiler, 'Trades': trades_ct
            })

    print("\n==================================================")
    print("🏆 EXECUTION COMPLETE")
    print("==================================================")
    
    print(f"   Total Trades: {total_trades:.1f}")
    print(f"   Total PnL:    {total_pnl:.2f}\n")
    
    print("   Breakdown by Stage:")
    print(f"   09:16:  {trades_per_stage[0]:.1f} trades")
    print(f"   09:17:  {trades_per_stage[1]:.1f} trades")
    print(f"   09:18:  {trades_per_stage[2]:.1f} trades")
    print(f"   09:19:  {trades_per_stage[3]:.1f} trades")
    print(f"   09:20+: {trades_per_stage[4]:.1f} trades\n")
    
    print("   Breakdown by FY:")
    for fy in sorted(fy_pnl.keys()):
        print(f"   {fy}: {fy_pnl[fy]:.2f}")

    # --- CONCAT WITH EXISTING DAILY LOG (don't just overwrite with new days) ---
    new_log_df = pd.DataFrame(temp_log, columns=['Date', 'PnL', 'FY', 'Entry_Time', 'Profiler'])

    if prev_log_df is not None and not prev_log_df.empty:
        prev_log_aligned = prev_log_df.copy()
        for col in ['Date', 'PnL', 'FY', 'Entry_Time', 'Profiler']:
            if col not in prev_log_aligned.columns:
                prev_log_aligned[col] = np.nan
        prev_log_aligned = prev_log_aligned[['Date', 'PnL', 'FY', 'Entry_Time', 'Profiler']]
        combined_log_df = pd.concat([prev_log_aligned, new_log_df], ignore_index=True)
    else:
        combined_log_df = new_log_df

    if not combined_log_df.empty:
        combined_log_df['Date'] = pd.to_datetime(
            combined_log_df['Date'], format='mixed', dayfirst=True
        )
        combined_log_df = combined_log_df.sort_values('Date')
        combined_log_df = combined_log_df.drop_duplicates(subset='Date', keep='last').reset_index(drop=True)
        combined_log_df['Date'] = combined_log_df['Date'].dt.strftime('%Y-%m-%d')

        combined_log_df['Cum_PnL'] = combined_log_df['PnL'].cumsum()
        combined_log_df['High_Water'] = combined_log_df['Cum_PnL'].cummax()
        combined_log_df['Drawdown'] = combined_log_df['Cum_PnL'] - combined_log_df['High_Water']
        
        mdd = combined_log_df['Drawdown'].min()
        print(f"\n   Overall Max Drawdown (full history): {mdd:.2f}")
        
        log_path = DAILY_LOG_PATH
        combined_log_df.to_csv(log_path, index=False)
        print(f"\n[SUCCESS] Daily Execution Log saved to: {log_path} "
              f"({len(new_log_df)} new row(s), {len(combined_log_df)} total)")

if __name__ == "__main__":
    main()
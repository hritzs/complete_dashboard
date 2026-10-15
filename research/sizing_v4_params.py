"""
Sizing v4: size from the EXISTING ENTRY PARAMETERS with a real size spread, plus
entry rules that change WHICH trades are taken -- asked by the owner 2026-10-15
("perform the sizing properly based on the already existing params; how can I
vary sizes and make my entries better").

Same method as v2 / VRP / v3: 60 / 20 / 20 chronological split, TRAIN fits,
VALIDATION decides, TEST reported once. Inputs known at the entry second.

Parameters (the table's own inputs, at the entry minute):
  Table (which stage said YES: 09:16 / 09:17 / 09:18 / later), IV_Ratio,
  Str_Ratio, Build_IV, DTE, Norm_OG (opening gap), Adj_IV_Chg, Fixed_IDV.
CW trades only; NW trades keep size 1 (they have their own, different inputs).

CANDIDATE LIST (fixed before the first run; nothing below is tuned):

S  Sizing -- a TRAIN-fitted score, cut into TRAIN terciles low / mid / high:
   S1 score = sum over params of (bin average - overall average) x n/(n+20),
      bins = TRAIN quintiles (Table and DTE: their own levels)
   S2 score = ridge regression of profit on the 8 standardised params
      (+ Table as dummies), lambda = 10, fitted on TRAIN
   each mapped to sizes   [0.5, 1, 1.5]  spread
                          [0.5, 1, 1  ]  cut only
                          [1,   1, 1.5]  boost only
                          [0,   1, 1  ]  skip the low tercile (an entry filter)
   ("spread" and "boost only" scaled to TRAIN average size 1.)
E  Entry rules, from the backtest's fixed-time side output (PnL had we entered at
   09:16 / 09:17 / 09:18; verified equal to Net_PnL on the days actually entered then):
   E1 wait one minute: a 09:16 entry goes in at 09:17, a 09:17 entry at 09:18
   E2 confirmation: a 09:16 / 09:17 entry is taken (one minute later) only if the
      table still says YES the next minute; otherwise no trade
   E3 add VETO days: no-trade, non-event days whose 09:18 reading was VETO, entered at 09:18
   E4 add FLOOR days: same for FLOOR (Build IV < 0.12 on DTE 1-2)
   E5 add every no-trade, non-event day at 09:18 (what the table's NO is worth)
Decision rule, separately for S and for E (fixed in advance): best profit / max
drawdown on VALIDATION, only if it beats the current book there on BOTH profit
and profit / drawdown; otherwise keep the current book. A winner gets a shuffle
check on TEST (2000 random re-assignments; for E rules, random days of the same
count changed instead).

Caveat (as v2): the tables were built on the whole history, so table-related
readings are not fully out of sample in any period.

    ./.venv/bin/python sizing_v4_params.py
Writes research/output/sizing_v4_params_report.xlsx
"""
import os

import numpy as np
import pandas as pd

from sizing_vrp import HERE, load as load_vrp, stats

PARAMS = ["Table", "IV_Ratio", "Str_Ratio", "Build_IV", "DTE", "Norm_OG", "Adj_IV_Chg", "Fixed_IDV"]
DISCRETE = {"Table", "DTE"}
SHRINK, LAMBDA = 20.0, 10.0
MAPS = {"spread": [0.5, 1.0, 1.5], "cut only": [0.5, 1.0, 1.0], "boost only": [1.0, 1.0, 1.5], "skip low tercile": [0.0, 1.0, 1.0]}


def load():
    D = load_vrp()
    D["DTE"] = np.minimum(D["DTE_idx"] + 1, 5)
    D["Table"] = D["Stage"].map({"09:16": 0, "09:17": 1, "09:18": 2}).where(D["Leg"] == "CW").fillna(3)
    return D


def main():
    D = load()
    part = D["Part"].values
    cw = (D["Leg"] == "CW").values
    traded = D["Leg"].isin(["CW", "NW"]).values
    tr = cw & (part == "Train")
    T = D[tr]
    y = T["Net_PnL"].values
    mu = y.mean()
    pnl = D["Net_PnL"].values

    # ---- S1: bucket score
    s1 = np.zeros(len(D))
    eff = []
    for c in PARAMS:
        xt = T[c].astype(float).values
        edges = sorted(set(xt)) if c in DISCRETE else list(np.quantile(xt, [0.2, 0.4, 0.6, 0.8]))
        if c in DISCRETE:
            bt, ba = xt, D[c].astype(float).values
            levels = edges
        else:
            bt = np.searchsorted(edges, xt, side="right")
            ba = np.searchsorted(edges, D[c].astype(float).values, side="right")
            levels = range(5)
        for k in levels:
            m = bt == k
            n = int(m.sum())
            e = (y[m].mean() - mu) * n / (n + SHRINK) if n else 0.0
            s1[cw & (ba == k)] += e
            lab = str(int(k)) if c in DISCRETE else f"Q{k + 1} ({'' if k == 0 else f'{edges[k - 1]:.4f}'}..{'' if k == 4 else f'{edges[k]:.4f}'})"
            eff.append(dict(Param=c, Bin=lab, Train_trades=n, Avg_profit=y[m].mean() if n else np.nan,
                            Stopped_pct=100 * (T["Exit_Reason"].values[m] == "SL").mean() if n else np.nan, Effect=e))

    # ---- S2: ridge on standardised params (+ Table dummies)
    def design(df):
        X = [df[c].astype(float).values for c in PARAMS if c != "Table"]
        X += [(df["Table"].values == k).astype(float) for k in (1, 2, 3)]
        return np.column_stack(X)
    Xt = design(T)
    m_, s_ = Xt.mean(0), Xt.std(0)
    s_[s_ == 0] = 1
    Z = (Xt - m_) / s_
    beta = np.linalg.solve(Z.T @ Z + LAMBDA * np.eye(Z.shape[1]), Z.T @ (y - mu))
    Xa = design(D)
    s2 = np.where(cw, ((np.nan_to_num(Xa) - m_) / s_) @ beta + mu, 0.0)
    coef = pd.DataFrame({"Feature": [c for c in PARAMS if c != "Table"] + ["Table=09:17", "Table=09:18", "Table=later"],
                         "Coef_per_1sd": beta})

    S, tiers = {"Current book (size 1)": np.ones(len(D))}, {}
    for tag, sc in (("S1 bucket score", s1), ("S2 ridge score", s2)):
        q = np.quantile(sc[tr], [1 / 3, 2 / 3])
        b = np.searchsorted(q, sc, side="right")
        tiers[tag] = (q, b)
        for mname, lv in MAPS.items():
            raw = np.where(cw, np.array(lv)[b], 1.0)
            if mname in ("spread", "boost only"):
                raw = np.where(cw, raw / raw[tr].mean(), 1.0)
            S[f"{tag} [{mname}]"] = raw

    # ---- E: entry rules (alternative daily PnL series)
    p16, p17, p18 = (D[f"PnL_{t}"].values for t in ("0916", "0917", "0918"))
    st = D["Stage"].values
    noev = ~D["Event"].astype(bool).values
    notrade = D["Leg"].values == ""
    E = {"Current book": pnl.copy()}
    e = pnl.copy()
    a = cw & (st == "09:16") & np.isfinite(p17); e[a] = p17[a]
    a = cw & (st == "09:17") & np.isfinite(p18); e[a] = p18[a]
    E["E1 wait one minute"] = e
    e = pnl.copy()
    for s_now, nxt, pn in (("09:16", "Signal_0917", p17), ("09:17", "Signal_0918", p18)):
        a = cw & (st == s_now)
        ok_ = D[nxt].values == "LUT_ON"
        e[a & ok_ & np.isfinite(pn)] = pn[a & ok_ & np.isfinite(pn)]
        e[a & ~ok_] = 0.0
    E["E2 confirmation (next minute still YES)"] = e
    for tag, sig in (("E3 add VETO days at 09:18", ["VETO"]), ("E4 add FLOOR days at 09:18", ["FLOOR"]),
                     ("E5 add every no-trade day at 09:18", None)):
        a = notrade & noev & np.isfinite(p18) & (np.isin(D["Signal_0918"].values, sig) if sig else True)
        e = pnl.copy(); e[a] = p18[a]
        E[tag] = e

    def table(series, key):
        rows = []
        for name, p in series.items():
            for per in ("Train", "Validation", "Test"):
                r = stats(p[part == per]); r.update({key: name, "Period": per}); rows.append(r)
        return pd.DataFrame(rows)[[key, "Period", "Net", "MDD", "Net_per_MDD", "Worst_day", "Trades", "Per_trade"]]

    def decide(R, key, base):
        V = R[R.Period == "Validation"].set_index(key)
        f = V.loc[base]
        ok = V[(V["Net"] > f["Net"]) & (V["Net_per_MDD"] > f["Net_per_MDD"])]
        return ok["Net_per_MDD"].idxmax() if len(ok) else base

    RS = table({k: pnl * v for k, v in S.items()}, "Candidate")
    RE = table(E, "Rule")
    s_ch = decide(RS, "Candidate", "Current book (size 1)")
    e_ch = decide(RE, "Rule", "Current book")
    rng = np.random.default_rng(7)
    tm = part == "Test"
    shuf = {}
    if s_ch != "Current book (size 1)":
        base = stats((pnl * S[s_ch])[tm])["Net_per_MDD"]
        m = cw & tm
        cnt = 0
        for _ in range(2000):
            s2_ = S[s_ch].copy(); s2_[m] = rng.permutation(s2_[m]); cnt += stats((pnl * s2_)[tm])["Net_per_MDD"] < base
        shuf["sizing"] = cnt / 2000
    if e_ch != "Current book":
        base = stats(E[e_ch][tm])["Net_per_MDD"]
        changed = np.flatnonzero(tm & (E[e_ch] != pnl))
        cand = np.flatnonzero(tm & ((pnl != 0) | (np.isfinite(p18) & notrade & noev)))
        delta = E[e_ch][changed] - pnl[changed]
        cnt = 0
        for _ in range(2000):            # same changes applied to random days of the same kind
            e_ = pnl.copy(); pick = rng.choice(cand, len(changed), replace=False); e_[pick] += rng.permutation(delta)
            cnt += stats(e_[tm])["Net_per_MDD"] < base
        shuf["entry"] = cnt / 2000
    combo = None
    if s_ch != "Current book (size 1)" and e_ch != "Current book":
        combo = table({"Chosen entry x chosen size": E[e_ch] * S[s_ch]}, "Candidate")

    # Tier profile per period: does the score separate trades out of sample?
    prof = []
    for tag, (q, b) in tiers.items():
        for per in ("Train", "Validation", "Test"):
            m = cw & (part == per)
            row = dict(Score=tag, Period=per)
            for k, lab in enumerate(["low", "mid", "high"]):
                sel = m & (b == k)
                row[f"{lab}_n"], row[f"{lab}_avg"] = int(sel.sum()), pnl[sel].mean() if sel.any() else np.nan
                row[f"{lab}_stop%"] = 100 * (D["Exit_Reason"].values[sel] == "SL").mean() if sel.any() else np.nan
            prof.append(row)
    P = pd.DataFrame(prof)
    # Entry-minute profile: fixed-time PnL by what the table said (non-event days)
    sig = []
    for t in ("0916", "0917", "0918"):
        for per in ("Train", "Validation", "Test"):
            g = D[(part == per) & noev].groupby(f"Signal_{t}")[f"PnL_{t}"].agg(["count", "mean", "sum"])
            g["stop%"] = D[(part == per) & noev].groupby(f"Signal_{t}")[f"Exit_{t}"].apply(lambda s: 100 * s.astype(str).str.startswith("SL").mean())
            g = g.reset_index().rename(columns={f"Signal_{t}": "Signal"}); g.insert(0, "Period", per); g.insert(0, "Minute", t)
            sig.append(g)
    G = pd.concat(sig, ignore_index=True)

    pd.set_option("display.width", 250); pd.set_option("display.max_colwidth", 60)
    print(f"train CW trades {int(tr.sum())}, average {mu:.2f}\n")
    print("Score tiers by period (CW):"); print(P.round(2).to_string(index=False))
    print("\nSIZING, validation by profit / drawdown:")
    print(RS[RS.Period == "Validation"].sort_values("Net_per_MDD", ascending=False).round(2).to_string(index=False))
    print("\nENTRY RULES, all periods:"); print(RE.round(2).to_string(index=False))
    print("\nFixed-time PnL by table reading (non-event days):"); print(G.round(2).to_string(index=False))
    print("\nRidge coefficients:"); print(coef.round(3).to_string(index=False))
    print(f"\nSIZING DECISION: {s_ch}"); print(RS[RS.Candidate.isin(["Current book (size 1)", s_ch])].round(2).to_string(index=False))
    print(f"\nENTRY DECISION: {e_ch}"); print(RE[RE.Rule.isin(["Current book", e_ch])].round(2).to_string(index=False))
    if combo is not None:
        print("\nCombined:"); print(combo.round(2).to_string(index=False))
    print(f"\nshuffle checks on TEST: {shuf}")

    out = os.path.join(HERE, "output", "sizing_v4_params_report.xlsx")
    with pd.ExcelWriter(out) as w:
        pd.DataFrame({"Read me": [
            "Sizing from the existing entry parameters (S) and entry rules (E); candidate list fixed before running (sizing_v4_params.py header).",
            "60 / 20 / 20 chronological split. TRAIN fits, VALIDATION decides (must beat the current book on profit AND profit / drawdown), TEST reported once.",
            f"Sizing decision: {s_ch}.   Entry decision: {e_ch}.   Shuffle checks on TEST: {shuf}.",
            "Entry rules use the backtest's fixed-time PnL (09:16 / 09:17 / 09:18), equal to Net_PnL on the days actually entered at that minute.",
            "Caveat: the tables were built on the whole history, so table-related readings are not fully out of sample.",
            "PnL in backtest units (one trade = Rs 25,000 notional).",
        ]}).to_excel(w, sheet_name="Read_me", index=False)
        RS.round(3).to_excel(w, sheet_name="Sizing_candidates", index=False)
        RE.round(3).to_excel(w, sheet_name="Entry_rules", index=False)
        P.round(3).to_excel(w, sheet_name="Score_tiers", index=False)
        G.round(3).to_excel(w, sheet_name="Signal_x_fixed_time", index=False)
        pd.DataFrame(eff).round(4).to_excel(w, sheet_name="S1_bucket_effects", index=False)
        coef.round(4).to_excel(w, sheet_name="S2_ridge", index=False)
        d = D[["Date", "Part", "Leg", "Stage"] + PARAMS[1:] + ["Exit_Reason", "Net_PnL"]].copy()
        d["S1_score"], d["S2_score"] = s1.round(3), s2.round(3)
        d["Size_chosen"] = S[s_ch]
        d.to_excel(w, sheet_name="Daily", index=False)
    print(f"\nwrote {out}")


if __name__ == "__main__":
    main()

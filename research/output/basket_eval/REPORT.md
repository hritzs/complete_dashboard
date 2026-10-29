# Bucket / instrument basket

Run: /home/ubuntu/Desktop/api_gs/trading-platform-git-recover/research/output/backtest_basket/run_027bf0249b
Days: 2021-01-01 to 2026-10-28 (1439)
Fixed entries 09:16-09:18 only (live also checks 09:19 / 09:20+).

## Live tables (baseline)

```
 train_net  train_trades  train_win  train_mdd  train_net_mdd  val_net  val_trades  val_win  val_mdd  val_net_mdd  test_net  test_trades  test_win  test_mdd  test_net_mdd
    6776.3           445       74.2      107.5          63.03   2142.5         133     69.9    116.9        18.33     919.1         77.0      75.3     138.1          6.65
```

## Variants beating the live tables on VALIDATION (net and net/MDD): 0 of 864

```
Empty DataFrame
Columns: [variant, cells, train_net, train_trades, train_win, train_mdd, train_net_mdd, val_net, val_trades, val_win, val_mdd, val_net_mdd]
Index: []
```

## Choice: LIVE tables (ATM straddle)

TEST (reported once): {'net': 919.1, 'trades': 77, 'win': 75.3, 'mdd': 138.1, 'net_mdd': 6.65}

## Recent days (forced PnL at 09:16; live and chosen decisions)

```
           live_T  live_PnL choice_T  choice_PnL  PnL_0916  D15_0916_PnL  D20_0916_PnL  D25_0916_PnL  D30_0916_PnL  D35_0916_PnL
Date                                                                                                                            
2026-09-01    NaN      0.00      NaN        0.00      9.46        -12.04         -8.87         -8.87          6.43          6.43
2026-09-02    NaN      0.00      NaN        0.00     23.44         14.19         18.45         14.00         16.07         20.84
2026-09-03    NaN      0.00      NaN        0.00     -9.83         -5.85         -6.26         -8.52         -9.83        -12.21
2026-09-04    NaN      0.00      NaN        0.00     29.53         18.63         11.51         15.52         18.21         23.56
2026-09-07    NaN      0.00      NaN        0.00     -5.34          2.85          1.98          2.89         -9.88         -5.47
2026-09-08    NaN      0.00      NaN        0.00     -7.82         16.73         16.73         23.44         16.23        -11.19
2026-09-09    NaN      0.00      NaN        0.00     -4.31          1.06          2.38          4.39          5.88          6.16
2026-09-10    NaN      0.00      NaN        0.00     16.39         10.74         14.70          8.73         13.71         19.29
2026-09-11    NaN      0.00      NaN        0.00    -39.17         -3.47        -10.15        -15.22        -17.81        -19.92
2026-09-15    NaN      0.00      NaN        0.00    -46.47        -38.29        -33.94        -43.29        -49.70        -41.71
2026-09-16    NaN      0.00      NaN        0.00     -3.41          1.49          3.69          6.98          7.59         -4.79
2026-09-17    NaN      0.00      NaN        0.00     12.53          0.85        -13.93          8.77          2.05          3.06
2026-09-18    NaN      0.00      NaN        0.00     28.91         17.23         23.51         30.42         30.41         25.94
2026-09-21    NaN      0.00      NaN        0.00     -0.26         -2.70          4.76          8.26         10.17         10.17
2026-09-22    NaN      0.00      NaN        0.00     -2.96         27.22          9.27          9.27          1.23          1.23
2026-09-23    NaN      0.00      NaN        0.00     19.08          9.00          8.90         13.79         16.75         20.50
2026-09-24    NaN      0.00      NaN        0.00    -34.57        -34.16        -40.62        -44.99        -36.15        -31.04
2026-09-25    NaN      0.00      NaN        0.00     20.45         18.30         11.12          1.95          8.23         17.48
2026-09-28    NaN      0.00      NaN        0.00     -6.00         -5.65         -4.66         -7.56         -3.05         -3.05
2026-09-29   0918      3.44     0918        3.44    -44.64         -6.32        -43.49        -34.94         27.37         27.37
2026-10-01    NaN      0.00      NaN        0.00    -39.62        -35.66        -36.07        -39.72        -43.35        -47.26
2026-10-05    NaN      0.00      NaN        0.00    -36.63        -35.25         -6.89          2.40        -38.00        -35.11
2026-10-06   0918     38.74     0918       38.74     30.12         36.62         24.45         24.45         31.02         31.02
2026-10-07    NaN      0.00      NaN        0.00     30.54         24.30         29.83         19.25         10.25         12.75
2026-10-08   0916    -33.70     0916      -33.70    -33.70        -35.54        -30.97        -32.15        -33.04        -34.42
2026-10-09    NaN      0.00      NaN        0.00     -2.16         12.46          8.35         12.87         -2.50         -9.87
2026-10-12    NaN      0.00      NaN        0.00    -36.46        -36.83        -44.07        -33.66        -37.00        -37.00
2026-10-13   0917     10.83     0917       10.83    -41.65        -10.55         -0.32         -0.32         -8.05         -8.05
2026-10-14    NaN      0.00      NaN        0.00      3.75         15.59         -1.25          2.25          6.47        -38.89
2026-10-15    NaN      0.00      NaN        0.00    -31.91        -35.40        -37.36        -38.49        -33.76        -32.20
2026-10-16   0917     16.94     0917       16.94      0.16        -35.48          5.63         13.22         20.52         19.80
2026-10-19   0918    -49.46     0918      -49.46    -38.38        -50.95        -36.21        -42.86        -39.69        -38.40
2026-10-21    NaN      0.00      NaN        0.00    -36.92        -34.31        -40.23        -38.20        -39.60        -41.69
2026-10-22    NaN      0.00      NaN        0.00    -21.78        -36.49        -37.02          8.25         -8.18        -35.66
2026-10-23   0916    -36.37     0916      -36.37    -36.37        -20.96        -33.21        -35.50        -35.18        -35.15
2026-10-26   0916    -46.37     0916      -46.37    -46.37        -35.52         20.83         18.34        -43.94        -27.96
2026-10-27   0916     35.56     0916       35.56     35.56          0.68        -11.90        -11.90         -9.07         29.86
2026-10-28    NaN      0.00      NaN        0.00     35.73         29.01         35.01         35.90         33.56         36.07
```

## Follow-up checks (2026-10-29)

- 28 Oct cell (5,7,0,5,0,0) and 29 Oct cell (4,2,2,5,1,1): no earlier day at 09:16 in 2021-2026-10-27.
  An empty cell is NO by construction; nothing in the history says these days had an edge.
- Neighbours of 29 Oct (IV ratio 0.80-0.90, Build IV 0.16-0.20, any DTE / straddle ratio): 26 days,
  54 % win, avg +3.03; last 12 months 5 days, avg -3.85.
- 28 Oct lead (straddle ratio <= 0.95, today's straddle richer than yesterday's): the 57 days average
  +9.9, but the LUT already says YES on 21 of them. The 26 days a rule would ADD: train 19 days,
  37 % win, avg -12.56 (sum -238.7); val 3 days +70.3; test 4 days +11.3. Rejected.
- Conclusion: keep the live tables. No bucket edges, threshold or delta strangle in the basket
  beat them on validation (0 of 864).

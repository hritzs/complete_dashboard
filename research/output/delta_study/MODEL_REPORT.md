# Combined-signal regime delta model (walk-forward)

Configurations tried: 1056 (11 times x 8 outcomes x 3 ridge strengths x 4 thresholds). Positive on VALIDATION with >= 30 trades: 240 (23 %). All predictions out-of-sample (yearly expanding refit). Units: bps of the underlying per trade, net.

'train' = the out-of-sample predictions of 2022 - Jun 2024 (each year from a model fitted on the years before).

## Top on VALIDATION (chosen = first row; TEST shown for all 12 so luck can be judged)

```
 time    outcome  alpha  thr  train_net  train_trades  val_net  val_trades  val_win  val_mdd  val_net_mdd  test_net  test_trades  test_win  test_avg  test_mdd
11:00 F_x_nextam    1.0  2.0      363.6           553   4406.4         256     58.6    419.3        10.51    -219.0          266      50.8     -0.82    1433.3
11:00 F_x_nextam    1.0  0.0     1161.0           606   4203.9         283     57.6    419.3        10.03    -320.0          291      49.8     -1.10    1470.6
11:00 F_x_nextam    1.0 10.0     1094.8           378   3162.6         164     60.4    329.8         9.59     477.6          149      56.4      3.21     812.6
11:00 F_x_nextam    1.0  5.0      309.9           489   3710.9         219     59.4    419.3         8.85      -7.7          219      52.1     -0.04    1318.5
11:00 F_x_nextpm    1.0  5.0     4167.2           532   4112.3         245     56.3    612.8         6.71    2546.6          244      52.5     10.44     730.2
13:00    F_x_day    1.0 10.0     -243.8           255    760.0          75     53.3    118.4         6.42    -298.1           61      39.3     -4.89     506.8
13:00 F_x_nextam   10.0  5.0     2941.4           395   1434.3         152     53.9    231.0         6.21    -800.8          128      50.0     -6.26    1515.8
13:00 F_x_nextam   10.0  2.0     4545.5           529   2398.7         230     54.3    394.1         6.09     271.7          223      52.5      1.22     929.7
11:00 O_x_nextam    1.0  0.0      269.6           606   1429.9         283     43.8    244.6         5.85     -69.5          289      41.9     -0.24     531.3
12:00 F_x_nextam   10.0 10.0     2117.0           243    836.7          59     59.3    144.1         5.80    1248.8           52      61.5     24.02     249.5
13:00 F_x_nextam   10.0  0.0     5137.9           606   2555.4         283     55.1    443.3         5.77    -331.3          291      51.2     -1.14    1244.1
13:00 F_x_nextam  100.0  5.0     1728.1           330   1481.1         156     56.4    267.4         5.54    -583.5          106      50.9     -5.50     899.8
```

All 240 validation-positive configurations on TEST: 121 positive, median test net +6.8, median test avg +0.04 bps.

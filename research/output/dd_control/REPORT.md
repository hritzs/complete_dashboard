# Drawdown control on the straddle book

Trades: 804 (2021-01-01 to 2026-10-27); each period replayed from a clean start. Units: backtest Rs per 25k notional.

```
                         policy  train_net  train_mdd  train_net_mdd  val_net  val_mdd  val_net_mdd  val_trades  val_size
                           flat     7093.1      144.4          49.13   2106.4    126.4        16.67         176      1.00
       pause 2 losses -> skip 1     6662.3      135.5          49.18   1474.8    236.0         6.25         157      0.89
       pause 2 losses -> skip 2     5713.2      126.6          45.12   1373.5    170.9         8.04         138      0.78
       pause 2 losses -> skip 3     5365.6      127.4          42.12   1176.7    238.3         4.94         119      0.68
       pause 2 losses -> skip 5     4281.3      168.4          25.42   1078.9    309.5         3.49         106      0.60
       pause 3 losses -> skip 1     7076.7      154.2          45.90   2028.9    125.8        16.13         174      0.99
       pause 3 losses -> skip 2     6939.4      116.0          59.82   2022.2    125.8        16.08         172      0.98
       pause 3 losses -> skip 3     6671.0      116.0          57.50   1924.1    125.8        15.30         170      0.97
       pause 3 losses -> skip 5     6161.9      116.0          53.11   1789.7    154.2        11.61         166      0.94
       equity >= avg of last 10     6187.2      126.5          48.92   1578.0    207.0         7.62         147      0.84
       equity >= avg of last 20     6768.8      124.1          54.55   1708.5    152.3        11.22         164      0.93
       equity >= avg of last 40     7012.5      144.4          48.57   2106.4    126.4        16.67         176      1.00
             halve after a loss     6256.4      111.8          55.94   1659.3    116.1        14.29         176      0.82
   live DD schedule 100/135/170     7033.9      137.3          51.24   1988.8    134.8        14.76         176      0.98
gentler DD schedule (floor 0.5)     7061.4      144.4          48.91   2027.5    139.4        14.54         176      0.99
        one cut: 0.5 beyond 150     7093.1      144.4          49.13   2106.4    126.4        16.67         176      1.00
```

Beat flat on VALIDATION net and net/MDD: 0 of 15.

Choice: **flat**.

TEST (reported once): flat: net 1242.8, MDD 176.4, net/MDD 7.04, trades 102; flat: net 1242.8, MDD 176.4, net/MDD 7.04.

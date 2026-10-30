# Opportunity-based delta rules

Decision rows: 15716 over 1430 days (2021-01-01 to 2026-10-28).
Candidates tried: 160608. Passed TRAIN (n, t >= 2.5): 1452. Survived VALIDATION: 70.
Units: bps of the underlying per trade, net of spread and costs.

## Survivors (TEST reported once)

```
 time    feature  bin      lo      hi       regime      outcome side  train_n  train_avg  train_t  val_n  val_avg  val_t  test_n  test_avg  test_t  test_win
11:00       skew q4/5   0.037   0.053     vrp=rich   F_x_nextpm    L      119      36.34     3.61     28    63.23   3.64    10.0     26.77    0.76      70.0
10:00        r30 q2/5 -17.733  -3.486     expw=yes F_x_sl25tp50    S       73      13.05     3.31     20    20.76   3.48    32.0      9.47    1.57      56.2
09:45  pos_range q2/5   0.180   0.404     expw=yes F_x_sl25tp50    S       68      12.72     2.95     25    19.44   3.00    20.0     -1.73   -0.23      35.0
12:00       skew q5/5   0.053     NaN     vrp=rich   F_x_nextam    L       94      27.23     2.61     18    73.77   2.89    11.0      9.92    0.17      63.6
10:00   prev_ret q2/5 -60.226 -10.724     expw=yes F_x_sl25tp50    S       80      12.98     3.30     34    14.95   2.82    27.0     -0.57   -0.10      48.1
13:00        r60 q5/5  14.948     NaN  comp=normal      F_x_day    L      111      13.07     3.13     29    17.03   2.79    40.0    -11.57   -1.68      30.0
10:00 adj_iv_chg q4/5   0.006   0.021     expw=yes F_x_sl25tp50    S       73      13.26     3.26     23    19.01   2.73    16.0     -1.58   -0.20      37.5
13:00       skew q4/5   0.038   0.054     vrp=rich   F_x_nextam    L      119      21.44     3.64     21    32.56   2.68    15.0      1.91    0.11      53.3
10:00       skew q1/5     NaN   0.015     expw=yes F_x_sl40tp80    S      112      13.22     2.91     59    11.98   2.51    73.0      0.58    0.13      50.7
12:00     st_chg q4/5  -0.046  -0.010     expw=yes   F_x_nextam    L       63      25.15     3.07     19    24.57   2.50    16.0    -14.79   -1.14      43.8
12:00     r_open q4/5   9.249  34.809      ivp=low   F_x_nextpm    L       70      21.08     2.52     17    43.36   2.50    33.0     -0.17   -0.02      63.6
10:30        orb q4/4   1.000     NaN  trend=range   F_x_nextam    L      170      18.94     3.15     58    19.85   2.48    71.0     -3.17   -0.43      57.7
10:00   prev_ret q2/5 -60.226 -10.724          all F_x_sl25tp50    S      170       7.35     2.75     75     9.17   2.43    70.0     -6.09   -1.82      34.3
11:00       skew q4/5   0.037   0.053     vrp=rich   O_x_nextpm    L      119      17.01     3.00     28    29.85   2.36    10.0     11.12    0.56      50.0
12:00  pos_range q4/5   0.693   0.867      ivp=low   F_x_nextpm    L       58      23.47     2.60     15    38.31   2.33    26.0      6.00    0.50      57.7
10:00        orb q2/4  -1.000   0.000     expw=yes F_x_sl25tp50    S       96      13.13     3.59     24    14.88   2.30    36.0      7.13    1.22      50.0
13:00       skew q4/5   0.038   0.054     vrp=rich   F_x_nextpm    L      119      29.55     3.18     21    49.78   2.26    15.0      5.78    0.18      53.3
10:30        orb q4/4   1.000     NaN idvc=falling   F_x_nextam    L      139      16.44     2.69     48    22.16   2.22    40.0    -19.04   -1.76      45.0
11:00        r60 q4/5   5.200  16.811     vrp=rich   F_x_nextpm    L      133      20.74     2.63     34    38.14   2.15    28.0    -30.54   -2.03      32.1
10:30  pos_range q5/5   0.870     NaN     vrp=rich   F_x_nextam    L      121      17.97     2.88     36    20.99   2.13    26.0      0.34    0.02      50.0
12:00     r_open q5/5  34.809     NaN     vrp=rich   F_x_nextam    L      113      31.47     4.03     43    24.85   2.12    16.0     53.37    1.61      56.2
10:30  range_bps q2/5  35.567  47.141      ivp=low   F_x_nextpm    L       74      25.12     3.01     24    35.73   2.10    35.0    -19.26   -2.04      31.4
10:00   prev_ret q2/5 -60.226 -10.724     expw=yes F_x_sl40tp80    S       80      13.82     2.55     34    11.70   2.03    27.0     -2.44   -0.37      51.9
11:00        r60 q4/5   5.200  16.811     vrp=rich   F_x_nextam    L      133      20.39     3.56     34    27.90   2.02    28.0    -14.68   -1.50      28.6
11:00        r60 q4/5   5.200  16.811 idvc=falling   F_x_nextam    L       92      17.89     2.57     23    30.95   2.02    27.0     -1.68   -0.16      40.7
10:30        orb q4/4   1.000     NaN          all   F_x_nextam    L      273      18.92     4.17     93    14.13   1.95    89.0     -4.07   -0.49      52.8
10:30        r60 q5/5  21.229     NaN  trend=range   F_x_nextam    L      111      22.82     2.68     38    22.26   1.94    42.0      5.48    0.50      61.9
13:00        orb q4/4   1.000     NaN     trend=up   F_x_nextam    L       87      24.69     5.30     20    19.84   1.91     4.0    -43.45   -4.35       0.0
11:00        orb q3/4   0.000   1.000      ivp=low   F_x_nextam    L      117      15.83     3.26     26    19.92   1.91    40.0      1.25    0.15      40.0
10:30        orb q4/4   1.000     NaN          all   F_x_nextpm    L      273      22.74     3.83     93    18.77   1.90    89.0     -3.97   -0.40      48.3
14:00       skew q5/5   0.053     NaN  idvc=rising   F_x_nextam    L       85      24.81     3.00     15    40.00   1.86    15.0    -43.05   -1.27      53.3
11:00       skew q4/5   0.037   0.053     vrp=rich   F_x_nextam    L      119      21.91     2.82     28    29.36   1.85    10.0     11.33    0.43      60.0
11:00        orb q4/4   1.000     NaN idvc=falling   F_x_nextam    L      141      15.02     2.59     45    14.16   1.85    42.0    -21.54   -1.83      42.9
11:30        r60 q4/5   5.205  15.039     vrp=rich   F_x_nextpm    L      122      22.94     3.03     26    28.73   1.84    30.0     -7.50   -0.46      46.7
11:00        orb q4/4   1.000     NaN     expw=yes   F_x_nextam    L      115      22.91     3.11     36    19.51   1.83    36.0     -4.21   -0.40      50.0
12:00        orb q3/4   0.000   1.000      ivp=low   F_x_nextam    L      107      14.47     2.72     23    18.73   1.83    33.0     15.24    1.69      54.5
14:00     r_open q5/5  42.738     NaN  idvc=rising   F_x_nextpm    L       85      30.90     3.02     26    33.50   1.82    26.0      7.71    0.36      53.8
10:30     r_open q5/5  30.530     NaN idvc=falling   F_x_nextam    L       86      27.05     3.14     24    34.96   1.81    20.0    -20.29   -1.36      35.0
09:20  range_bps q4/5  27.865  39.558      expw=no   F_x_nextam    L      108      26.54     3.35     34    29.56   1.79    50.0    -15.13   -1.07      48.0
10:00        r30 q2/5 -17.733  -3.486     expw=yes F_x_sl40tp80    S       73      14.54     2.65     20    13.45   1.78    32.0      5.84    0.80      59.4
```

## Straddle book (production) by regime, average net PnL per traded day

```
regime      state  train_n  train_avg  val_n  val_avg  test_n  test_avg
   ivp        low      119       9.41     29    12.25      20     12.19
   ivp        mid      131       9.92     73     7.90      26      9.21
   ivp       high      175      14.82     74    15.87      56     13.56
   vrp      cheap      142      15.63     47     8.48      37      9.88
   vrp       rich      373      13.06    129    13.24      64     12.09
 trend       down       33      14.29     32    13.07      30     18.52
 trend      range      343      15.19    109    12.85      62      9.41
 trend         up      130       9.84     35     8.23      10     10.40
  comp compressed       78      10.33     31     9.02      16     17.32
  comp     normal      333      14.96    123    13.84      60      8.93
  comp   expanded       96      12.55     22     5.66      26     16.53
  idvc    falling      266      12.87    100    12.25      46     10.14
  idvc     rising      259      14.16     76    11.59      56     13.87
```

# Explain the sizing rerun from the basics

This is a sizing overlay on the existing GammaShort entries. It does not
generate a new entry, build a directional straddle or use a neural network.
The two new policies rerun the maximum-profit ideas from iterations10/11.
Their ledger IDs are15/16 so the old in-sample results remain preserved.

## 1. What changes on a trade day?

Size1 means the original recorded trade. Size0.85 scales its saved net result
by0.85; size1.10 scales it by1.10. All existing entries remain taken: the
allowed range is0.75–1.15. Existing event reductions are already inside the
recorded PnL. Fractional multipliers are research sizes, without a fresh replay
of lot rounding, fixed fees or quantity-dependent fills.

CW means the current weekly expiry; NW means the next weekly expiry.
“Non-09:16 CW” means a CW entry at09:17,09:18,09:19 or the09:20+ stage. It
does not mean a different strategy. This rerun retains the original10/11
09:16 CW cap1. It does not add the separate iteration13 higher-tax boost rule.
The original LUT construction hurdles13/5/2/2/6 remain inside the frozen
entry book. They are not deducted again from each trade's PnL.

## 2. What information is allowed?

For a2024 trade, fit the model using completed2021–2023 trades. Save it before
scoring2024. Features for an entry at09:17 must use ticks strictly before
09:17. A completed2023 trade's eventual PnL is a valid training label for2024;
the2024 trade's eventual PnL is not a valid input to its own prediction.

The sequence is2021 warmup at size1, then five annual folds2022–2026. Each
fold refits on all earlier years. Medians, scales, score buckets, regression
weights, percentile samples, risk cutoff, allocation weights and balancing
offset all come from the training period. None are fitted on the scoring year.

For intraday measurements, choose ATM using the latest prior underlying,
hold that strike during the60-second prefix and align CE/PE quotes backward
in time. Quotes must be at most3 seconds old; at least20 snapshots and the
final snapshot must be valid. Missing readings are explicit. Previous daily
reference dates must precede the trade date. Reference-file availability and
legacy source IV calculations are assumed as timestamped, not independently
certified against the original live feed.

## 3. Loss probability

For each completed training trade create a label:

    y = 1 if net PnL < 0, otherwise 0

Fit a regularized logistic regression using its entry inputs X:

    z = beta0 + sum(X_j * beta_j)
    predicted probability = 1 / (1 + exp(-clip(z,-25,25)))

The training objective is summed logistic loss plus
`20/2 * sum(beta_j²)`; the intercept is unpenalized. This discourages large
weights supported by little evidence. For illustration, z≈-1.386 gives an
estimated probability≈0.20. That is a model estimate, not a guarantee or
independently established calibration. The observed outcome is used later to
evaluate the prediction, never to create that trade's prediction.

## 4. Expected downside

Make a different training target:

    d = max(-net PnL,0)

Hypothetical outcomes-20,+10,-5,+12 give targets20,0,5,0. The average target
is6.25. Profitable trades contribute zero because this model predicts the
unconditional negative part of PnL, rather than loss size conditional on losing.

Fit ridge regression with penalty50, using an unpenalized intercept:

    Xc = X - training_mean(X)
    gamma = inverse(Xc.T*Xc + 50I) * Xc.T * (d-training_mean(d))
    gamma0 = training_mean(d) - training_mean(X)*gamma
    expected downside = max(0,gamma0 + X*gamma)

It is an average downside estimate, not a worst-case loss, stop price or VaR.

## 5. Instability and percentile ranks

Measure the prefix underlying range, straddle LTP range and bid/ask spread.
Convert each to a percentile against valid prior-training CW observations,
then average their three ranks. Missing/NW components receive neutral0.5;
missing CW instability separately forces the dicey flag.

A percentile compares an estimate with the reference estimates. A loss-model
output at its80th percentile is relatively high; it is not an80% probability
of losing. With N reference values, L strictly below the new value and E
exactly equal to it, the saved rule is `(L+(E+1)/2)/N` when E>0, else L/N.
For training rows this reproduces average-rank percentile conventions.

## 6. The dicey flag

    R = 0.4*rank(loss probability)
      + 0.4*rank(expected downside)
      + 0.2*instability

For a hypothetical set of ranks0.8,0.7,0.6, R=0.72. Compare it with the
training sample's75th-percentile R cutoff, saved before scoring. Dicey means
R>=that cutoff OR missing required CW instability. The weights40/40/20 and
75th-percentile rule are fixed research choices, not fitted probabilities.

Approximately a quarter of the training scores lie above the cutoff; ties and
missing inputs can change this. The fraction in the following year can be
very different. The old full-fit count201/799 is not a prescribed count in
these annual models.

## 7. Where does the continuous sizing score come from?

S1 adds shrunk historical bucket effects:

    effect = (bucket mean PnL - overall CW mean PnL) * n/(n+20)
    S1 = sum of effects across the eight entry parameters

Continuous parameters use training quintiles; Table and DTE use their observed
discrete levels. For example, a bucket mean20 versus overall10 with50
observations contributes10*50/70≈7.14. The20 is a shrinkage count, not a
minimum required sample. Correlated parameter effects may overlap, so S1 is a
relative score rather than a calibrated profit prediction.

S2 is a centered linear profit score, fitted on standardized entry parameters
and table indicators with ridge penalty10. Both S1 and S2 are learned only on
prior CW trades. The broader allocation design also contains volatility,
calendar/stage, event/expiry and instability inputs. Numeric inputs use prior
training medians, means and standard deviations, clipped to[-3,3].

The allocator then uses a different coefficient vector b:

    raw size = 1 + sum(X_j*b_j)

A fixed-seed multi-start coordinate search chooses b within[-1,1] to maximize
prior-training sized PnL, with a tiny coefficient penalty. It tries declared
starts and decreasing positive/negative coordinate steps, retaining the best
qualifying point encountered. This is a local numerical search, not a proven
global optimum. Allocation b is different from logistic beta and ridge gamma.

WF10 uses34 inputs. WF11 adds prior realized flat-book DD/loss streak and the
loss/downside predictions, giving38. Prior DD/streak may update within a
scoring year because earlier completed outcomes are available by the next day.

## 8. How does normalization remain causal?

On training trades only, solve one offset c so bounded sizes average1:

    training size_i = clip(raw_i+c,lower_i,upper_i)
    sum(training sizes) = training entry count

Save c. For a scoring trade apply the same c without solving again:

    future size = clip(1+X*b+saved_c,lower,upper)

This preserves bounds but cannot guarantee future average1. Normalizing an
entire scoring year back to1 would use future trades. The reports give the
actual mean and an ex-post same-mean flat diagnostic; that diagnostic is not
a live rule or proof of identical cash exposure.

For example, hypothetical raw1.08 plus saved offset0.03 gives1.11. At09:16 CW
it clips to1; on a WF11 dicey entry it clips to0.85. The risk cap overrides
the optimizer's desired boost. These examples are illustrative, not run results.

WF10 only maximizes training profit within its bounds. WF11 also requires
training profit>=flat, DD<=flat and worst day>=flat while applying dicey<=0.85.
If training mean1 is infeasible, or no qualifying fit is found, WF11 reports
a risk-only fallback: dicey0.85, otherwise1. It does not weaken the risk cap.
Training constraints cannot guarantee the same realized limits in a new year.

An optimized boost here is an allocation decision; these reruns do not add the
separate iteration12 high-conviction gate or iteration13 higher-tax gate.

## 9. How to judge the results

    daily sized PnL = saved net PnL * size
    equity = cumulative daily sized PnL
    DD = running peak equity, including starting zero, minus current equity
    maximum DD = largest DD

Compare the same2022+ dates with flat1, excluding2021 warmup. Higher PnL,
lower DD and a better worst day are separate outcomes; optimizing historical
profit need not improve all three. The declared qualification requires higher
net, higher net/DD and no worse worst day. The user's stronger preference for
both higher net and lower DD must also be read directly from the table.
Per-period DD resets at each period start; full-book DD does not. Annual DDs
must not be added to obtain full-book DD.

See `output/walkforward_sizing/REPORT.md`, its comparison workbook and the
master ledger15_/16_ sheets for measured results. All trades, quarterly,
yearly, monthly and Indian financial-year reports remain stored. JSON fold
models and independent replay verification document how sizes were produced.

The correction is conditional on the existing entry book: base LUTs were
built using broader history and saved fills remain unchanged. Previously
researched years are not an untouched holdout. A successful overlay result
would still require rebuilding and testing the full entry process using only
past data before an end-to-end no-lookahead claim or a live change.

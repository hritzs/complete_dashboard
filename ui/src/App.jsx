  import { createSignal, createMemo, createEffect, onMount, onCleanup, Index, Show, For } from 'solid-js';
  import LutBuildTab from './LutBuildTab.jsx';
  import StraddleBuildTab from './StraddleBuildTab.jsx';
  import PaperSimTab from './PaperSimTab.jsx';
  import SBPortfolioRows, { sbStore } from './SBPortfolioRows.jsx';
  import ManualLegPanel, { closeManualLeg } from './ManualLegPanel.jsx';
  import PortfolioMTMPanel from './PortfolioMTMPanel.jsx';
  import './App.css';
  import LatencyDashboard from './LatencyDashboard.jsx';


  const normalize = (s) => (s || '').toString().trim().toUpperCase();

  const toNum = (v) => {
    const n = Number(v);
    return Number.isFinite(n) ? n : 0;
  };

  const hasValue = (v) => {
    return v !== null && v !== undefined && v !== '' && Number.isFinite(Number(v));
  };

  const fmt = (v, d = 2) => {
    return hasValue(v) ? Number(v).toFixed(d) : '—';
  };

  // Compact action buttons on a Portfolio row.
  const rowBtn = { padding: "3px 7px", "font-size": "11px", "min-width": "0" };

  // HH:MM:SS (IST) of a broker rejection.
  const rejectTime = (r) => {
    const t = new Date(r?.time || 0);
    return Number.isFinite(t.getTime()) && t.getTime() > 0
      ? t.toLocaleTimeString('en-IN', { hour12: false, timeZone: 'Asia/Kolkata' })
      : '—';
  };

  async function safeFetchJson(url, options = {}) {
    const res = await fetch(url, options);
    const text = await res.text();

    let data;
    try {
      data = text ? JSON.parse(text) : {};
    } catch {
      throw new Error(`Invalid backend response from ${url}`);
    }

    if (!res.ok) {
      throw new Error(data?.error || data?.message || `HTTP ${res.status}`);
    }

    return data;
  }

  function App() {
    const [activeTab, setActiveTab] = createSignal('terminal');
    const [wsStatus, setWsStatus] = createSignal('Connecting...');
    const [dataSource, setDataSource] = createSignal('Waiting...');
    const [selectedSymbol, setSelectedSymbol] = createSignal('NIFTY');
    const [selectedExpiry, setSelectedExpiry] = createSignal('');
    const [visibleStrikes, setVisibleStrikes] = createSignal(14);
    const [sellStatus, setSellStatus] = createSignal('');
    const [eventLogs, setEventLogs] = createSignal([]);
    const [portfolioItems, setPortfolioItems] = createSignal([]);
    const [positions, setPositions] = createSignal([]);
    const [positionsLoading, setPositionsLoading] = createSignal(false);
    const [directOrderBusy, setDirectOrderBusy] = createSignal(false);
    const [directOrderStatus, setDirectOrderStatus] = createSignal('');
    const [directOrderResult, setDirectOrderResult] = createSignal(null);
    const [directOrderLeg, setDirectOrderLeg] = createSignal('CE');
    const [manualTotalLots, setManualTotalLots] = createSignal(2);
    const [manualLotsPerOrder, setManualLotsPerOrder] = createSignal(2);
    const [manualOrderType, setManualOrderType] = createSignal('1');
    const [manualProduct, setManualProduct] = createSignal('1');
    const [modifyOrderId, setModifyOrderId] = createSignal('');
    const [modifyOrderPrice, setModifyOrderPrice] = createSignal('');
    const [modifyOrderBusy, setModifyOrderBusy] = createSignal(false);
    const [modifyOrderStatus, setModifyOrderStatus] = createSignal('');



  async function handleDirectTwoLotSingleOrder() {
    const row = atmRow();
    const leg = directOrderLeg();
    const oc = optionChain();
    const totalLots = Math.max(1, Math.floor(toNum(manualTotalLots())));
    const lotsPerOrder = Math.max(1, Math.floor(toNum(manualLotsPerOrder())));

    if (!row) {
      setDirectOrderStatus('Failed: live ATM option-chain data is not available.');
      return;
    }

    // oc.lot_size is never actually populated by the live chain payload
    // (confirmed by tracing feed-decoder -> snapshot-service -> here); use
    // the live-fetched, per-symbol-correct lotSize() instead -- this value
    // sizes a REAL order. lotSize() has no hardcoded fallback (see its
    // definition), so 0/unset here means the real per-symbol lot size
    // genuinely hasn't loaded yet -- refuse rather than guess, since a
    // wrong guess would size a real order wrong.
    const exchangeLotSize = lotSize();
    if (!(exchangeLotSize > 0)) {
      setDirectOrderStatus(`Failed: live lot size for ${selectedSymbol() || 'this symbol'} has not loaded yet -- refusing to guess. Wait a moment and retry.`);
      return;
    }

    const shortToken = leg === 'CE' ? toNum(row.ce_token) : toNum(row.pe_token);
    const ltp = leg === 'CE' ? toNum(row.ce_ltp) : toNum(row.pe_ltp);
    const symbol = normalize(selectedSymbol() || oc.symbol || 'NIFTY');
    const expiry = selectedExpiry() || oc.expiry || '';
    const strike = Math.round(toNum(row.strike));
    const quantity = totalLots * exchangeLotSize;
    const orderCount = Math.ceil(totalLots / lotsPerOrder);

    if (!shortToken || !ltp || !expiry || !strike) {
      setDirectOrderStatus(
        `Failed: incomplete live ATM data. token=${shortToken}, ltp=${ltp}, expiry=${expiry}, strike=${strike}`
      );
      return;
    }

    const confirmed = window.confirm(
      `LIVE SELL ORDER\n\n` +
      `${symbol} ${expiry} ${leg} ${strike}\n` +
      `Live token: ${shortToken}\n` +
      `Live LTP: ${ltp.toFixed(2)}\n\n` +
      `Total lots: ${totalLots}\n` +
      `Lots per broker order: ${lotsPerOrder}\n` +
      `Exchange lot size: ${exchangeLotSize}\n` +
      `Total quantity: ${quantity}\n` +
      `Expected broker orders: ${orderCount}\n\n` +
      `Continue?`
    );
    if (!confirmed) return;

    setDirectOrderBusy(true);
    setDirectOrderStatus(
      `Submitting ${symbol} ${expiry} ${leg} ${strike}: ${totalLots} lots, ${quantity} qty, ${orderCount} broker order(s)...`
    );
    setDirectOrderResult(null);

    try {
      // Field names/types must match ManualOrderRequest exactly (Go's json
      // decoder silently drops anything it doesn't recognize instead of
      // erroring) -- this previously sent short_token/side='2'/ordertype
      // etc., none of which matched the backend's gtoken/side/ordertype
      // fields, so token in particular always decoded to empty regardless
      // of what was shown in the confirmation dialog above.
      const result = await safeFetchJson('/api/manual/order', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          user_id: executionPrefs().user_id || 'U001',
          broker_name: executionPrefs().broker_name || 'greeksoft',
          account_id: executionPrefs().account_id || '147',
          exchange_segment: executionPrefs().exchange_segment || 'NSEFO',
          product_type: manualProduct() === '0' ? 'MIS' : 'NRML',
          symbol,
          token: shortToken,
          side: 'SELL',
          order_type: manualOrderType() === '2' ? 'MARKET' : 'LIMIT',
          price: Number(ltp.toFixed(2)),
          total_lots: totalLots,
          lots_per_order: lotsPerOrder,
          lot_size: exchangeLotSize
        })
      });

      setDirectOrderResult(result);
      const legs = Array.isArray(result.legs) ? result.legs : [];
      const filled = legs.filter((l) => l.verified_qty > 0).length;
      const failed = legs.filter((l) => l.error).length;
      if (result.success) {
        setDirectOrderStatus(
          `Placed: ${symbol} ${expiry} ${leg} ${strike} | ${legs.length} order(s), ${filled} verified filled | total lots=${totalLots} qty=${quantity}.`
        );
        appendEventLog('success', `Manual order placed: ${symbol} ${expiry} ${leg} ${strike}, ${legs.length} order(s), ${filled} filled`);
      } else {
        setDirectOrderStatus(`Failed: ${symbol} ${expiry} ${leg} ${strike} | ${failed}/${legs.length} order(s) failed — see details below.`);
        appendEventLog('error', `Manual order partially/fully failed: ${symbol} ${expiry} ${leg} ${strike}`);
      }
    } catch (error) {
      setDirectOrderStatus(`Failed: ${error.message}`);
      appendEventLog('error', `Manual sell failed: ${error.message}`);
    } finally {
      setDirectOrderBusy(false);
    }
  }

  async function handleModifyOrder() {
    const brokerOrderId = modifyOrderId().trim();
    const price = toNum(modifyOrderPrice());
    // See handleDirectTwoLotSingleOrder: oc.lot_size is never populated by
    // the live chain payload, so this must use lotSize() (live-fetched,
    // per-symbol) rather than fall back to a hardcoded 65 for a real order.
    const exchangeLotSize = lotSize();
    const quantity = Math.max(1, Math.floor(toNum(manualTotalLots()))) * exchangeLotSize;

    if (!brokerOrderId) {
      setModifyOrderStatus('Failed: enter the broker order id to modify (from a previous Direct manual order result).');
      return;
    }
    if (!price || price <= 0) {
      setModifyOrderStatus('Failed: enter a new price.');
      return;
    }

    const confirmed = window.confirm(
      `MODIFY LIVE ORDER\n\norder ${brokerOrderId}\nnew price: ${price}\nquantity: ${quantity}\n\nContinue?`
    );
    if (!confirmed) return;

    setModifyOrderBusy(true);
    setModifyOrderStatus(`Modifying order ${brokerOrderId} to price ${price}...`);
    try {
      const result = await safeFetchJson('/api/manual/order/modify', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          broker_name: executionPrefs().broker_name || 'greeksoft',
          account_id: executionPrefs().account_id || '147',
          broker_order_id: brokerOrderId,
          price,
          quantity,
          lot_size: exchangeLotSize
        })
      });
      setModifyOrderStatus(`Modified: order ${brokerOrderId} re-priced to ${result.price ?? price}.`);
      appendEventLog('success', `Order ${brokerOrderId} modified to price ${price}`);
    } catch (error) {
      setModifyOrderStatus(`Failed: ${error.message}`);
      appendEventLog('error', `Modify order failed for ${brokerOrderId}: ${error.message}`);
    } finally {
      setModifyOrderBusy(false);
    }
  }



  async function fetchBrokerPositions() {
    setPositionsLoading(true);
    setPositionsError("");
    try {
      const response = await fetch("/api/positions", {
        method: "GET",
        headers: { Accept: "application/json" },
      });
      const res = await response.json();
      if (res && res.success && Array.isArray(res.positions)) {
        setPositions(res.positions);
      } else {
        setPositions([]);
        setPositionsError(res?.error || "Positions fetch failed");
      }
    } catch (err) {
      console.error("positions fetch error", err);
      setPositions([]);
      setPositionsError(err?.message || "Network error");
    } finally {
      setPositionsLoading(false);
    }
  }

    const [positionsError, setPositionsError] = createSignal("");
    const [straddleMetrics, setStraddleMetrics] = createSignal({});
    // Plain (non-reactive) tracking set for refreshLiveMetrics's
    // failure-transition logging -- not rendered, so it doesn't need to
    // be a signal.
    const snapshotFailureUids = new Set();
    const [expandedTrade, setExpandedTrade] = createSignal(null);
  const [modifyTradeModal, setModifyTradeModal] = createSignal({ open: false, trade: null });
  const [modifyTradeForm, setModifyTradeForm] = createSignal({
    sl_points_per_lot: "30",
    sl_pnl_bps_of_spot: "14",
    tp_pnl_bps_of_spot: "0",
    mtm_exit_level: "",
    mtm_exit_unit: "rs",
    mtm_exit_pct: "100",
    straddle_exit_below: "",
    straddle_exit_pct: "100",
    square_off_time: "15:37:00",
    square_off_hard_time: "",
    straddle_div: "4",
    hedge_div: "57",
    hedge_threshold_delta: "",
    hedge_min_threshold_bps: "8"
  });
  const [modifyTradeSaving, setModifyTradeSaving] = createSignal(false);
  const [modifyTradeError, setModifyTradeError] = createSignal("");

    const PORTFOLIO_STORAGE_KEY = 'trading-platform-portfolio-items-v1';

    const readStoredPortfolioItems = () => {
      try {
        const raw = window.localStorage.getItem(PORTFOLIO_STORAGE_KEY);
        const parsed = raw ? JSON.parse(raw) : [];
        return Array.isArray(parsed) ? parsed : [];
      } catch (_) {
        return [];
      }
    };

    const writeStoredPortfolioItems = (items) => {
      try {
        window.localStorage.setItem(PORTFOLIO_STORAGE_KEY, JSON.stringify(items || []));
      } catch (_) {}
    };

    const setPortfolioItemsPersisted = (updater) => {
      setPortfolioItems((prev) => {
        const next = typeof updater === 'function' ? updater(prev) : updater;
        writeStoredPortfolioItems(next);
        return next;
      });
    };

    const [customCeStrike, setCustomCeStrike] = createSignal('');
    const [customPeStrike, setCustomPeStrike] = createSignal('');

    const [executionPrefs, setExecutionPrefs] = createSignal({
      user_id: 'U001',
      broker_name: 'greeksoft',
      account_id: '147',
      product_type: 'NRML',
      exchange_segment: 'NSEFO',
      order_lots_per_call: 1,
      delta_neutral: true
    });

    const [automationSizeText, setAutomationSizeText] = createSignal('65');
    const [automationConfig, setAutomationConfig] = createSignal({
      symbol: 'NIFTY',
      size: 1,
      entry_time: '',
      exit_time: '15:37:00',
      hedge_div: 57,
      straddle_div: 4,
      roll_straddle_div: 0.2,
      hedge_frac: 1.0,
      sl_bps: 14,
      tp_bps: 14,
      wing_pct: 0,
      buy_buffer: 2,
      sell_buffer: 2,
      order_lots_per_call: 1,
      idv: 11.4,
      idv_divisor: 1.5,
      straddle_filter: 250,
      sl_monitor_interval: 60,
      hedge_monitor_interval: 60,
      roll_monitor_interval: 60,
      hedge_start_time: '',
      sl_start_time: '',
      roll_start_time: ''
    });

    const [manualHedgeConfig, setManualHedgeConfig] = createSignal({
      net_delta: 0,
      lot_size: 0
    });
    const [manualHedgePreview, setManualHedgePreview] = createSignal(null);
    const [manualHedgeExecutions, setManualHedgeExecutions] = createSignal([]);
    const [terminalSellQty, setTerminalSellQty] = createSignal(1);
    // What's shown in the Sell Quantity box while typing -- kept separate
    // from terminalSellQty (the committed, lot-rounded value) so typing
    // "2500" isn't stomped mid-keystroke by rounding to the nearest lot on
    // every character. Rounding happens once, on blur.
    const [terminalSellQtyText, setTerminalSellQtyText] = createSignal('65');
    const [manualRiskConfig, setManualRiskConfig] = createSignal({
      exit_time: '15:37:00',
      sl_bps: 14,
      tp_bps: 14,
      wing_pct: 0
    });
    const [manualHedgeBusy, setManualHedgeBusy] = createSignal(false);
    const [scheduledBuilds, setScheduledBuilds] = createSignal([]);
    // Deliberately NOT initialised from selectedExpiry(): an automated build must
    // never inherit an expiry, it has to be chosen here every time.
    const [automationExpiry, setAutomationExpiry] = createSignal('');
    const [portfolioTotals, setPortfolioTotals] = createSignal(null);
    // Broker vs platform position check (GET /api/positions/check, every 30 s).
    const [posCheck, setPosCheck] = createSignal(null);
    {
      const pullCheck = async () => {
        try {
          const d = await (await fetch("/api/positions/check")).json();
          if (d.success) setPosCheck(d.check);
        } catch (_) { /* gateway restarting */ }
      };
      pullCheck();
      const t = setInterval(pullCheck, 30000);
      onCleanup(() => clearInterval(t));
    }
    const [manualHedgeError, setManualHedgeError] = createSignal('');

    const [optionChain, setOptionChain] = createSignal({
      symbol: 'NIFTY',
      synthetic_future: 0,
      future_ltp: 0,
      atm: 0,
      expiry: '',
      available_expiries: [],
      chain: []
    });

    const availableSymbols = [
      'NIFTY',
      'BANKNIFTY',
      'FINNIFTY',
      'MIDCPNIFTY',
      'SENSEX',
      'BANKEX'
    ];

    const brokerOptions = ['xts', 'greeksoft', 'mock'];
    const greeksoftAccounts = ['147', 'HRITIK', 'HRITIK1'];

    let ws = null;
    let reconnectTimer = null;
    let heartbeatTimer = null;

    const appendEventLog = (level, message) => {
      const entry = {
        ts: new Date().toLocaleTimeString(),
        level: level.toUpperCase(),
        message
      };
      setEventLogs((prev) => [entry, ...prev].slice(0, 300));
    };

    const underlying = createMemo(() => {
      const oc = optionChain();
      if (toNum(oc.synthetic_future) > 0) return toNum(oc.synthetic_future);
      if (toNum(oc.future_ltp) > 0) return toNum(oc.future_ltp);
      return 0;
    });

    const normalizePayload = (rawSymbol, d) => {
      const chainRows = Array.isArray(d?.chain) ? d.chain : [];
      const currentExpiry = d?.expiry || '';

      const availableExpiries =
        Array.isArray(d?.available_expiries) && d.available_expiries.length > 0
          ? d.available_expiries
          : currentExpiry
            ? [currentExpiry]
            : [];

      return {
        symbol: normalize(rawSymbol || d?.symbol),
        synthetic_future: toNum(d?.synthetic_future ?? d?.synthetic_spot),
        future_ltp: toNum(d?.future_ltp ?? d?.fut_ltp),
        atm: toNum(d?.atm),
        expiry: currentExpiry,
        available_expiries: availableExpiries,
        chain: chainRows.map((row) => ({
          strike: toNum(row?.strike),
          ce_token: toNum(row?.ce_token),
          pe_token: toNum(row?.pe_token),
          ce_ltp: toNum(row?.ce_ltp),
          pe_ltp: toNum(row?.pe_ltp),
          ce_iv: hasValue(row?.ce_iv) ? Number(row.ce_iv) : null,
          pe_iv: hasValue(row?.pe_iv) ? Number(row.pe_iv) : null,
          ce_delta: hasValue(row?.ce_delta) ? Number(row.ce_delta) : null,
          pe_delta: hasValue(row?.pe_delta) ? Number(row.pe_delta) : null,
          ce_gamma: hasValue(row?.ce_gamma) ? Number(row.ce_gamma) : null,
          pe_gamma: hasValue(row?.pe_gamma) ? Number(row.pe_gamma) : null,
          ce_theta: hasValue(row?.ce_theta) ? Number(row.ce_theta) : null,
          pe_theta: hasValue(row?.pe_theta) ? Number(row.pe_theta) : null,
          ce_vega: hasValue(row?.ce_vega) ? Number(row.ce_vega) : null,
          pe_vega: hasValue(row?.pe_vega) ? Number(row.pe_vega) : null,
          is_atm: !!row?.is_atm
        }))
      };
    };

    const connectSocket = () => {
      const proto = window.location.protocol === 'https:' ? 'wss' : 'ws';
      const url = `${proto}://${window.location.hostname}:8003/ws/snapshots`;

      if (ws) {
        try {
          ws.close();
        } catch (_) {}
      }

      setWsStatus('Connecting...');
      ws = new WebSocket(url);

      ws.onopen = () => {
        setWsStatus('LIVE');
        appendEventLog('info', 'Snapshot websocket connected');
      };

      ws.onmessage = (event) => {
      try {
        const raw = JSON.parse(event.data);

        // LUT entry check (paper): handed to the LUT tab as-is.
        if (raw.type === 'lut_update') {
          window.dispatchEvent(new CustomEvent('lut_update', { detail: raw.data }));
          return;
        }
        if (raw.type === 'sbuild_update') {
          window.dispatchEvent(new CustomEvent('sbuild_update', { detail: raw.data }));
          return;
        }

        if (raw.type === 'straddle_update') {
          const data = raw.data || {};
          setStraddleMetrics((prev) => {
            const uid = data.trade_uid || data.id || 'default';
            return {
              ...prev,
              [uid]: {
                ...(prev[uid] || {}),
                ...data
              }
            };
          });
          return;
        }

        if (raw.type !== 'option_chain_update' && raw.type !== 'option_chain') {
          return;
        }

        const payload =
          raw?.data && typeof raw.data === 'object'
            ? raw.data
            : raw;

        const incomingSymbol = normalize(raw?.symbol || payload?.symbol);
        if (incomingSymbol !== normalize(selectedSymbol())) return;

        const next = payload || {};

        if (!next.symbol || !next.expiry) return;

        if (!selectedSymbol() || !selectedExpiry()) {
          setSelectedSymbol(next.symbol || 'NIFTY');
          setSelectedExpiry(next.expiry || '');
        }

        if (selectedExpiry() && next.expiry !== selectedExpiry()) return;

        if (next.chain?.length > 0) {
          setOptionChain(next);

          if (!selectedExpiry()) {
            setSelectedExpiry(next.expiry || '');
          }

          setDataSource('C++ Feed');
        }
      } catch (err) {
        console.error('WS parse error', err);
      }
    };

    ws.onerror = () => {
        appendEventLog('error', 'WebSocket error');
        try {
          ws.close();
        } catch (_) {}
      };

      ws.onclose = () => {
        setWsStatus('Disconnected');
        appendEventLog('warn', 'WebSocket disconnected, retrying...');
        reconnectTimer = setTimeout(connectSocket, 2000);
      };
    };

    let portfolioSnapshotTimer;
    let portfolioResyncTimer;
    let scheduledBuildsTimer;

    onMount(() => {
      connectSocket();
      setUiDefaults();
      loadPortfolioFromBackend();
      refreshLiveMetrics();

      portfolioSnapshotTimer = setInterval(() => {
        if (activeTab() === "portfolio") {
          refreshLiveMetrics();
        }
      }, 2000);

      // refreshLiveMetrics (above) only re-fetches PnL/greeks for trades
      // already sitting in the cached portfolio list -- it never
      // re-validates the list itself against the backend, so a trade
      // cleared server-side while this tab stays open would otherwise
      // keep showing until the next full page reload. Periodically
      // re-running loadPortfolioFromBackend (which now correctly clears
      // stale cache on a confirmed-empty response) closes that gap so
      // this self-corrects without ever needing a manual
      // localStorage.removeItem or a reload.
      portfolioResyncTimer = setInterval(() => {
        loadPortfolioFromBackend();
      }, 20000);

      loadScheduledBuilds();
      scheduledBuildsTimer = setInterval(loadScheduledBuilds, 5000);

      heartbeatTimer = setInterval(() => {
        appendEventLog('info', `Heartbeat • ${selectedSymbol()} • ${selectedExpiry() || 'no-expiry'}`);
      }, 10000);
    });

    onCleanup(() => {
      if (ws) ws.close();
      if (reconnectTimer) clearTimeout(reconnectTimer);
      if (heartbeatTimer) clearInterval(heartbeatTimer);
      if (portfolioSnapshotTimer) clearInterval(portfolioSnapshotTimer);
      if (portfolioResyncTimer) clearInterval(portfolioResyncTimer);
      if (scheduledBuildsTimer) clearInterval(scheduledBuildsTimer);
    });

    const handleSymbolChange = (e) => {
      const sym = e.target.value.toUpperCase();
      setSelectedSymbol(sym);
      setSelectedExpiry('');
      setAutomationExpiry('');
      setSellStatus('');
      appendEventLog('info', `Symbol changed to ${sym}`);

      const isSensex = sym.includes('SENSEX');
      const defaultBuffer = isSensex ? 6 : 2;

      setAutomationConfig((prev) => ({
        ...prev,
        symbol: sym,
        buy_buffer: defaultBuffer,
        sell_buffer: defaultBuffer
      }));

      setOptionChain({
        symbol: sym,
        synthetic_future: 0,
        future_ltp: 0,
        atm: 0,
        expiry: '',
        available_expiries: [],
        chain: []
      });
    };

    const handleExpiryChange = (e) => {
      setSelectedExpiry(e.target.value);
      setSellStatus('');
      appendEventLog('info', `Expiry changed to ${e.target.value}`);
    };

    const filteredChain = createMemo(() => {
      const oc = optionChain();
      const chain = oc.chain || [];

      if (!chain.length) return [];

      const atm = oc.atm;
      const idx = chain.findIndex((r) => r.strike === atm || r.is_atm);
      const span = visibleStrikes();

      if (idx === -1) {
        const mid = Math.floor(chain.length / 2);
        return chain.slice(
          Math.max(0, mid - span),
          Math.min(chain.length, mid + span + 1)
        );
      }

      return chain.slice(
        Math.max(0, idx - span),
        Math.min(chain.length, idx + span + 1)
      );
    });

    // Single source of truth for lot size everywhere it's needed (quantity
    // rounding, display, order sizing). The live chain payload (feed-decoder
    // -> snapshot-service -> this WS) never actually carries a `lot_size`
    // field -- confirmed by grepping both services, so `optionChain().lot_size`
    // was silently always falling back to 65 for EVERY symbol, not just as a
    // brief startup gap. Real lot sizes differ a lot (BANKNIFTY=15, SENSEX=20,
    // FINNIFTY=40 as of 2026-09-23) and only NIFTY happened to also be 65,
    // which is why the bug wasn't visible there. Fetched directly from
    // contract-master's own DB-backed /api/lot-size, the same authoritative
    // source the Go execution-gateway resolves from -- not hardcoded.
    const [liveLotSize, setLiveLotSize] = createSignal(0);
    createEffect(() => {
      const symbol = selectedSymbol();
      const expiry = selectedExpiry();
      setLiveLotSize(0); // avoid showing the PREVIOUS symbol's lot size while this fetch is in flight
      if (!symbol || !expiry) return;
      const host = window.location.hostname;
      fetch(`http://${host}:8010/api/lot-size?symbol=${encodeURIComponent(symbol)}&expiry=${encodeURIComponent(expiry)}`)
        .then((r) => r.json())
        .then((data) => {
          // Guard against a slow response for a symbol/expiry the user has
          // since switched away from landing late and overwriting the
          // current selection's lot size.
          if (symbol !== selectedSymbol() || expiry !== selectedExpiry()) return;
          if (data?.success && Number(data.lot_size) > 0) {
            setLiveLotSize(Number(data.lot_size));
          }
        })
        .catch(() => {});
    });
    // No hardcoded numeric fallback here on purpose (this used to fall back
    // to 65, which is only NIFTY's current lot size): if BANKNIFTY/FINNIFTY/
    // etc. is selected before the live per-symbol lot size has loaded,
    // silently guessing 65 would size a REAL order 2x/1.5x/2x too large or
    // small. 0 means "not known yet" -- callers must treat that as blocking,
    // never as a value to multiply a real order quantity by.
    const lotSize = createMemo(() => liveLotSize() || toNum(optionChain().lot_size) || 0);

    // Whole-portfolio live totals: every trade's own monitor snapshot summed
    // (greeks and unrealized from OPEN trades, realized from all of today's
    // trades). Wings are shown separately -- they're RM only and are already
    // excluded from each trade's greeks/PnL by the monitor.
    const portfolioLive = createMemo(() => {
      const t = { open: 0, delta: 0, gamma: 0, theta: 0, vega: 0, unrealized: 0, realized: 0, wing: 0 };
      const metricsByUid = straddleMetrics() || {};
      for (const item of portfolioItems() || []) {
        const uid = item.tradeUid || item.id;
        const m = metricsByUid[uid] || {};
        const st = String(m.status ?? item.status ?? "").toUpperCase();
        const closed = st.replace(/^SBUILD_/, "").startsWith("CLOSED") || st === "FAILED";
        // Realized from exchange fills (the summary): the live monitor reports
        // 0 for an open trade, which would drop a partial exit's booked PnL.
        t.realized += Number(item.realizedPnl ?? m.realized_pnl ?? 0) || 0;
        t.wing += Number(item.wingRealizedPnl) || 0;
        if (closed || st === "RECONCILIATION_REQUIRED") continue;
        t.open += 1;
        t.delta += Number(m.net_delta ?? item.netDelta ?? 0) || 0;
        t.gamma += Number(m.net_gamma ?? item.netGamma ?? 0) || 0;
        t.theta += Number(m.net_theta ?? item.netTheta ?? 0) || 0;
        t.vega += Number(m.net_vega ?? item.netVega ?? 0) || 0;
        t.unrealized += Number(m.unrealized_pnl ?? item.unrealizedPnl ?? 0) || 0;
        t.wing += Number(m.wing_pnl) || 0;
      }
      // Live straddle builds (their open trade row is shown from the build's PMS).
      for (const run of sbStore.holding()) {
        if (run.mode !== "LIVE") continue;
        const p = run.position || {};
        const legRealized = (p.legs || []).reduce((a, l) => a + (Number(l.realized) || 0), 0);
        t.open += 1;
        t.delta += Number(p.net_delta) || 0;
        t.gamma += Number(p.net_gamma) || 0;
        t.realized += legRealized;
        t.unrealized += (Number(p.pnl) || 0) - legRealized;
      }
      t.total = t.realized + t.unrealized;
      return t;
    });

    // Keep the Sell Quantity / Automation size text boxes in sync with the
    // committed, lot-rounded values -- but only the committed values, never
    // on every keystroke (see the onInput handlers below), so free typing
    // of e.g. "2500" is never interrupted mid-entry.
    createEffect(() => {
      setTerminalSellQtyText(String(terminalSellQty() * lotSize()));
    });
    createEffect(() => {
      setAutomationSizeText(String(automationConfig().size * lotSize()));
    });

    const atmRow = createMemo(() => {
      const chain = optionChain().chain || [];
      return chain.find((r) => r.is_atm || r.strike === optionChain().atm) || null;
    });

    const atmStraddle = createMemo(() => {
      const row = atmRow();
      return (row?.ce_ltp || 0) + (row?.pe_ltp || 0);
    });

    const stats = createMemo(() => {
      const chain = optionChain().chain || [];
      return {
        ceActive: chain.filter((r) => (r.ce_ltp || 0) > 0).length,
        peActive: chain.filter((r) => (r.pe_ltp || 0) > 0).length
      };
    });

    const cellClass = (v, side) => {
      if (!hasValue(v) || Number(v) === 0) return 'cell-zero';
      return side === 'ce' ? 'cell-ce' : 'cell-pe';
    };

    const dedupePortfolio = (items) => {
      const seen = new Set();
      return items.filter((item) => {
        const key = item.tradeUid || item.id;
        if (!key || seen.has(key)) return false;
        seen.add(key);
        return true;
      });
    };

    const storePortfolioEntry = (payload, responseData) => {
      const tradeData = responseData?.data?.straddleData || responseData?.data || responseData;

      const item = {
        id: tradeData.trade_uid || `LOCAL-${Date.now()}`,
        symbol: tradeData.symbol || payload.symbol,
        expiry: tradeData.expiry || payload.targetExpiry || optionChain().expiry || "",
        strike: tradeData.strike || payload.strike || optionChain().atm || 0,
        lots: tradeData.lots || payload.lots || 0,
        status: tradeData.status || responseData.status || "ACTIVE",
        createdAt: tradeData.created_at
          ? new Date(tradeData.created_at).toLocaleTimeString()
          : new Date().toLocaleTimeString(),
        tradeUid: tradeData.trade_uid || "",
        brokerName: payload.broker_name || tradeData.broker_name || executionPrefs().broker_name,
        accountId: payload.account_id || payload.accountID || tradeData.account_id || executionPrefs().account_id,
        exchangeSegment:
          tradeData.exchange_segment || payload.exchange_segment || payload.exchangeSegment || executionPrefs().exchange_segment,
        lotSize: tradeData.lot_size || payload.lot_size || payload.lotSize || 0,
        ceToken: tradeData.ce_token,
        peToken: tradeData.pe_token,
        ceQty: tradeData.ce_quantity || tradeData.ceqty,
        peQty: tradeData.pe_quantity || tradeData.peqty,
        ceEntry: tradeData.ce_entry_price ?? 0,
        ceLtp: tradeData.ce_ltp ?? 0,
        peEntry: tradeData.pe_entry_price ?? 0,
        peLtp: tradeData.pe_ltp ?? 0,
        netDelta: tradeData.net_delta ?? 0
      };

      setPortfolioItemsPersisted((prev) => dedupePortfolio([item, ...prev]));
    };

    const buildBasePayload = () => {
      const prefs = executionPrefs();

      return {
        user_id: prefs.user_id || 'U001',
        broker_name: normalize(prefs.broker_name || 'greeksoft').toLowerCase(),
        account_id: prefs.account_id || '147',
        symbol: selectedSymbol(),
        delta_neutral: !!prefs.delta_neutral,
        product_type: prefs.product_type || 'NRML',
        target_expiry: selectedExpiry() || optionChain().expiry,
        order_lots_per_call: toNum(prefs.order_lots_per_call) || 1,
        exchange_segment: prefs.exchange_segment || 'NSEFO',
        // Same defaults as the Automation tab (SL/TP 14 bps of spot, exit
        // 15:37:00) so a manual build is armed the same way an automated
        // one is, unless explicitly changed here.
        exit_time: manualRiskConfig().exit_time,
        sl_bps: manualRiskConfig().sl_bps,
        tp_bps: manualRiskConfig().tp_bps,
        wing_pct: manualRiskConfig().wing_pct
      };
    };

    const handleSellStraddle = async () => {
      const row = atmRow();
      if (!row) {
        setSellStatus("ATM row not available yet");
        appendEventLog("error", "Sell failed: ATM row not available");
        return;
      }

      if (!executionPrefs().account_id && executionPrefs().broker_name === "xts") {
        setSellStatus("XTS accountID is required");
        appendEventLog("error", "Sell failed: missing XTS accountID");
        return;
      }

      const payload = {
        ...buildBasePayload(),
        lots: toNum(terminalSellQty()) || 1,
        strike: Math.round(row.strike),
        ce_token: row.ce_token,
        pe_token: row.pe_token,
        lot_size: 0,
        ce_strike_price: Math.round(row.strike),
        pe_strike_price: Math.round(row.strike)
      };

      setSellStatus(
        `Sending ${payload.broker_name.toUpperCase()} order for ${payload.symbol} ${payload.strike}...`
      );

      appendEventLog(
        "info",
        `Sell request sent via ${payload.broker_name} for ${payload.symbol} ATM ${payload.strike}`
      );

      try {
        const data = await safeFetchJson("/api/trade/straddle", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(payload)
        });

        if (data.success) {
          setSellStatus(
            `✅ Success via ${payload.broker_name.toUpperCase()}! Trade ID: ${data.trade_uid || "Created"}`
          );
          appendEventLog(
            "success",
            `Trade created via ${payload.broker_name}: ${data.trade_uid || "Created"}`
          );
          storePortfolioEntry(payload, data);
          setActiveTab("portfolio");
        } else {
          setSellStatus(`Failed: ${data.error || "Unknown error"}`);
          appendEventLog("error", `Trade failed: ${data.error || "Unknown error"}`);
        }
      } catch (err) {
        setSellStatus(`Network Error: ${err.message}`);
        appendEventLog("error", `Sell network error: ${err.message}`);
      }
    };


    const handleSellCustomStraddle = async () => {
      const ceStrike = toNum(customCeStrike());
      const peStrike = toNum(customPeStrike());

      if (!ceStrike || !peStrike) {
        setSellStatus("Both CE and PE strikes are required");
        appendEventLog("error", "Custom sell failed: Both strikes required");
        return;
      }

      if (!executionPrefs().account_id && executionPrefs().broker_name === "xts") {
        setSellStatus("XTS accountID is required");
        appendEventLog("error", "Custom sell failed: missing XTS accountID");
        return;
      }

      const payload = {
        ...buildBasePayload(),
        lots: toNum(terminalSellQty()) || 1,
        ce_strike_price: Math.round(ceStrike),
        pe_strike_price: Math.round(peStrike)
      };

      setSellStatus(
        `Sending custom ${payload.broker_name.toUpperCase()} order for ${payload.symbol}...`
      );
      appendEventLog(
        "info",
        `Custom sell request sent via ${payload.broker_name} for ${payload.symbol} CE ${ceStrike}, PE ${peStrike}`
      );

      try {
        const data = await safeFetchJson("/api/trade/straddle", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(payload)
        });

        if (data.success) {
          setSellStatus(
            `✅ Success via ${payload.broker_name.toUpperCase()}! Trade ID: ${data.trade_uid || "Created"}`
          );
          appendEventLog(
            "success",
            `Trade created via ${payload.broker_name}: ${data.trade_uid || "Created"}`
          );
          storePortfolioEntry(payload, data);
          setActiveTab("portfolio");
        } else {
          setSellStatus(`Failed: ${data.error || "Unknown error"}`);
          appendEventLog("error", `Trade failed: ${data.error || "Unknown error"}`);
        }
      } catch (err) {
        setSellStatus(`Network Error: ${err.message}`);
        appendEventLog("error", `Sell network error: ${err.message}`);
      }
    };


    const loadPortfolioFromBackend = async () => {
      try {
        const cached = readStoredPortfolioItems();
        if (cached.length > 0) {
          setPortfolioItems((prev) => dedupePortfolio([...prev, ...cached]));
        }

        const data = await safeFetchJson("/api/straddles");

        const allRows = Array.isArray(data)
          ? data
          : Array.isArray(data?.straddles)
            ? data.straddles
            : Array.isArray(data?.data)
              ? data.data
              : Array.isArray(data?.trades)
                ? data.trades
                : [];

        const prefs = executionPrefs();
        const currentAccount = String(prefs.account_id || "").trim().toUpperCase();
        const currentBroker = String(prefs.broker_name || "").trim().toUpperCase();

        const rows = allRows
          .filter((tr) => {
            const status = normalize(tr.status);
            const account = String(tr.account_id || tr.accountId || "").trim().toUpperCase();
            const broker = String(tr.broker_name || tr.brokerName || "").trim().toUpperCase();

            const sameAccount = !currentAccount || account === currentAccount;
            const sameBroker = !currentBroker || broker === currentBroker;

            const hasLegs =
              toNum(tr.ce_token) > 0 ||
              toNum(tr.pe_token) > 0 ||
              toNum(tr.ce_quantity) > 0 ||
              toNum(tr.pe_quantity) > 0 ||
              toNum(tr.lots) > 0;

            const usefulStatus =
              status === "ACTIVE" ||
              status === "FILLED" ||
              status === "PENDING_FILL" ||
              status === "BUILDING" ||
              status === "PARTIAL" ||
              status === "RECONCILIATION_REQUIRED" ||
              status === "PARTIAL-SQF" ||
              status === "HEDGING" ||
              status === "SQUARING-OFF";

            // Straddle-build trades are shown by their own panel (live PMS view).
            if (status === "SBUILD") return false;

            return sameAccount && sameBroker && status !== "FAILED" && (hasLegs || usefulStatus);
          })
          .sort((a, b) => new Date(b.created_at || 0) - new Date(a.created_at || 0));

        // Today's trades with what actually executed and their realized PnL.
        // /api/straddles returns only sparse rows for closed trades (no
        // legs, no lots), so the filter above dropped every trade the moment
        // it was squared off. This endpoint is the source for closed trades.
        let todaySummaries = [];
        try {
          const today = await safeFetchJson("/api/portfolio/today");
          todaySummaries = (Array.isArray(today?.trades) ? today.trades : []).filter((t) => {
            const account = String(t.account_id || "").trim().toUpperCase();
            const broker = String(t.broker_name || "").trim().toUpperCase();
            return (!currentAccount || account === currentAccount) && (!currentBroker || broker === currentBroker);
          });
          setPortfolioTotals(today?.totals ? { ...today.totals, date: today.date } : null);
        } catch (err) {
          appendEventLog("warn", `Could not load today's executed trades: ${err.message}`);
        }
        const summaryByUid = Object.fromEntries(todaySummaries.map((t) => [t.trade_uid, t]));
        const isClosedStatus = (status) => String(status || "").trim().toUpperCase().replace(/^SBUILD_/, "").startsWith("CLOSED");

        const closedItems = todaySummaries
          .filter((t) => isClosedStatus(t.status))
          .map((t) => ({
            id: t.trade_uid,
            symbol: t.symbol || "—",
            expiry: t.expiry || "",
            strike: t.strike || 0,
            lots: t.lots || 0,
            status: t.status,
            createdAt: t.created_at ? new Date(t.created_at).toLocaleTimeString() : "",
            tradeUid: t.trade_uid,
            brokerName: t.broker_name || "—",
            accountId: t.account_id || "—",
            exchangeSegment: "",
            lotSize: t.lot_size || 0,
            ceToken: 0,
            peToken: 0,
            ceQty: 0,
            peQty: 0,
            // For a closed trade the "LTP" columns show the average exit price.
            ceEntry: t.ce?.sold_avg ?? 0,
            ceLtp: t.ce?.bought_avg ?? 0,
            peEntry: t.pe?.sold_avg ?? 0,
            peLtp: t.pe?.bought_avg ?? 0,
            netDelta: 0,
            realizedPnl: t.realized_pnl ?? 0,
            wingRealizedPnl: t.wing_realized_pnl ?? 0,
            pnlPerStraddle: t.pnl_per_straddle ?? 0,
            unrealizedPnl: 0,
            totalPnl: t.realized_pnl ?? 0,
            config: t.config || {},
            lastReject: t.last_reject || null,
            rejectCount: t.reject_count || 0,
            points_allowed: 0,
            closeReason: t.close_reason || "",
            closedAt: t.closed_at || "",
            executions: t.executions || [],
            ceLeg: t.ce || null,
            peLeg: t.pe || null
          }));

        if (!rows.length && !closedItems.length) {
          // A successful fetch that confirms zero trades is authoritative,
          // not the same as a failed fetch (handled in the catch block
          // below, which intentionally keeps showing cached data as a
          // resilience fallback). Previously this returned early without
          // clearing anything, so a trade cached once in localStorage
          // would keep displaying forever even after the backend's data
          // was cleared -- there was no way to make it go away short of
          // manually clearing browser storage.
          appendEventLog("info", "Backend confirms no active trades; clearing any stale cached portfolio entries");
          setPortfolioItemsPersisted(() => []);
          return;
        }

        const mapped = rows.map((tr) => ({
          id: tr.trade_uid || tr.id || `DB-${Date.now()}`,
          symbol: tr.symbol || "—",
          expiry: tr.expiry || "",
          strike: tr.strike || 0,
          lots: tr.lots || 0,
          status: tr.status || "ACTIVE",
          createdAt: tr.created_at
            ? new Date(tr.created_at).toLocaleTimeString()
            : "",
          tradeUid: tr.trade_uid || "",
          brokerName: tr.broker_name || tr.brokerName || "—",
          accountId: tr.account_id || tr.accountId || "—",
          exchangeSegment: tr.exchange_segment || tr.exchangeSegment || "",
          lotSize: tr.lot_size || tr.lotSize || 0,
          ceToken: tr.ce_token,
          peToken: tr.pe_token,
          ceQty: tr.ce_quantity || tr.ceQty,
          peQty: tr.pe_quantity || tr.peQty,
          ceEntry: tr.ce_entry_price ?? 0,
          ceLtp: tr.ce_ltp ?? 0,
          peEntry: tr.pe_entry_price ?? 0,
          peLtp: tr.pe_ltp ?? 0,
          netDelta: tr.net_delta ?? 0,
          realizedPnl: summaryByUid[tr.trade_uid]?.realized_pnl ?? tr.realized_pnl ?? 0,
          wingRealizedPnl: summaryByUid[tr.trade_uid]?.wing_realized_pnl ?? 0,
          executions: summaryByUid[tr.trade_uid]?.executions || [],
          lastReject: summaryByUid[tr.trade_uid]?.last_reject || null,
          rejectCount: summaryByUid[tr.trade_uid]?.reject_count || 0,
          unrealizedPnl: tr.unrealized_pnl ?? 0,
          totalPnl: tr.total_pnl ?? tr.live_pnl ?? 0,
          config: tr.config || {},
          netGamma: tr.net_gamma ?? 0,
          netTheta: tr.net_theta ?? 0,
          netVega: tr.net_vega ?? 0,
          points_allowed: tr.points_allowed ?? 0
        }));

        const activeMapped = mapped.filter((it) => !isClosedStatus(it.status));
        const merged = [...activeMapped, ...closedItems].sort(
          (a, b) => (summaryByUid[b.tradeUid]?.created_at || "").localeCompare(summaryByUid[a.tradeUid]?.created_at || "")
        );
        setPortfolioItemsPersisted(() => dedupePortfolio(merged));
        appendEventLog("info", `Loaded ${activeMapped.length} open and ${closedItems.length} closed trades from backend`);
      } catch (err) {
        appendEventLog("warn", `Could not load saved trades: ${err.message}`);
      }
    };


    const postJsonFallback = async (urls, body = {}) => {
      let lastErr = null;

      for (const url of urls) {
        try {
          return await safeFetchJson(url, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(body)
          });
        } catch (err) {
          lastErr = err;
        }
      }

      throw lastErr || new Error("all action endpoints failed");
    };

    const refreshLiveMetrics = async () => {
      const items = portfolioItems();

      const requests = items
        .filter((item) => {
          const status = String(item.status || "").trim().toUpperCase();
          return ![
            "CLOSED",
            "CLOSEDSQF",
            "CLOSED_SQF",
            "CLOSED_MANUAL",
            "CLOSED_SL",
            "CLOSED_TP",
            "CLOSED_TIME",
            "CLOSED_MTM",
            "CLOSED_STRADDLE",
            "CLOSED_PORTFOLIO",
            "FAILED"
          ].includes(status);
        })
        .map((item) => item.tradeUid || item.id)
        .filter(Boolean)
        .map(async (tradeUid) => {
          try {
            const payload = await safeFetchJson(
              `/api/snapshots/${encodeURIComponent(tradeUid)}`
            );
            const snapshot = payload?.data ?? payload;
            if (!snapshot || snapshot.success === false) return { tradeUid, snapshot: null };
            return { tradeUid, snapshot };
          } catch (err) {
            return { tradeUid, snapshot: null, error: err?.message || "snapshot fetch failed" };
          }
        });

      const results = await Promise.all(requests);
      const fresh = {};

      for (const result of results) {
        if (!result?.tradeUid) continue;

        // Log only on the first failure for a trade and once more on
        // recovery, not once per poll (this runs on a timer) -- a
        // persistently-broken snapshot feed used to fail completely
        // silently forever.
        const wasFailing = snapshotFailureUids.has(result.tradeUid);
        if (result.error || !result.snapshot) {
          if (!wasFailing) {
            snapshotFailureUids.add(result.tradeUid);
            appendEventLog("warn", `Live metrics unavailable for ${result.tradeUid}: ${result.error || "no snapshot data"}`);
          }
          continue;
        }
        if (wasFailing) {
          snapshotFailureUids.delete(result.tradeUid);
          appendEventLog("info", `Live metrics recovered for ${result.tradeUid}`);
        }
        fresh[result.tradeUid] = result.snapshot;
      }

      if (Object.keys(fresh).length > 0) {
        setStraddleMetrics((previous) => ({ ...previous, ...fresh }));
      }
    };

    const refreshPortfolio = async () => {
      appendEventLog("info", "Refreshing portfolio from backend");
      await loadPortfolioFromBackend();
      await refreshLiveMetrics();
    };

    const portfolioActionPayload = (item, extra = {}) => ({
      user_id: executionPrefs().user_id || "U001",
      broker_name: item.brokerName || executionPrefs().broker_name || "greeksoft",
      account_id: item.accountId || executionPrefs().account_id || "147",
      exchange_segment: item.exchangeSegment || executionPrefs().exchange_segment || "NSEFO",
      product_type: executionPrefs().product_type || "NRML",
      symbol: item.symbol || selectedSymbol(),
      expiry: item.expiry || selectedExpiry() || optionChain().expiry,
      target_expiry: item.expiry || selectedExpiry() || optionChain().expiry,
      trade_uid: item.tradeUid || item.id,
      net_delta: Number(item.netDelta) || 0,
      lot_size: Number(item.lotSize) || 0,
      ...extra
    });

    const updatePortfolioStatus = (tradeUid, status) => {
      setPortfolioItemsPersisted((prev) =>
        dedupePortfolio(prev.map((item) =>
          (item.tradeUid || item.id) === tradeUid ? { ...item, status } : item
        ))
      );
    };


    const CLOSED_TRADE_STATUSES = new Set([
      "CLOSED",
      "CLOSED_SQF",
      "CLOSED_MANUAL",
      "CLOSEDSQF",
      "CLOSED_SL",
      "CLOSED_TP",
      "CLOSED_TIME",
      "CLOSED_MTM",
      "CLOSED_STRADDLE",
      "CLOSED_PORTFOLIO"
    ]);

    const isTradeClosed = (item) =>
      CLOSED_TRADE_STATUSES.has(String(item.status || "").trim().toUpperCase());

    // Stop building (OMS only): no more build orders; resting ones are
    // cancelled and what filled becomes an ACTIVE (partial) trade that the
    // monitor (hedge / SL / TP / exit time) keeps running on.
    const handlePortfolioStopBuild = async (item) => {
      const tradeUid = item.tradeUid || item.id;
      if (!tradeUid) return;
      if (!window.confirm(`Stop building ${tradeUid}?\n\nNo more build orders are sent (resting ones are cancelled). What has filled stays open as a PARTIAL position and stays monitored (hedge / SL / TP / exit time).`)) return;
      try {
        const res = await fetch("/api/build/stop", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ trade_uid: tradeUid }) });
        const data = await res.json();
        appendEventLog(data.success ? "success" : "error", data.success ? `Stop building sent for ${tradeUid}: filled part will be monitored as partial` : `Stop building ${tradeUid}: ${data.error}`);
      } catch (err) {
        appendEventLog("error", `Stop building ${tradeUid}: ${err.message}`);
      }
    };

    // Broker sync: match every order of this trade against the broker's own
    // order book (broker order id AND our order tag), preview, then record
    // any broker fill we missed under the trade. Never sends an order.
    const handlePortfolioBrokerSync = async (item) => {
      const tradeUid = item.tradeUid || item.id;
      if (!tradeUid) return;
      const call = async (apply) => {
        const res = await fetch("/api/trade/broker-sync", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ trade_uid: tradeUid, apply }) });
        return res.json();
      };
      try {
        const pv = await call(false);
        if (!pv.success) { window.alert(`Broker sync ${tradeUid} failed: ${pv.error}`); return; }
        const r = pv.result || {};
        const lines = (r.rows || []).map((x) =>
          `${x.side} ${x.leg} ${x.strike || ""} x${x.qty}  ours ${x.intent_id} / broker ${x.broker_order_id || "—"}  ` +
          `recorded ${x.our_filled} vs broker ${x.broker_filled}${x.broker_price ? " @" + x.broker_price : ""}  [${x.match}] ${x.action}`);
        const open = (r.open_legs || []).map((l) => `${l.OptionType || l.option_type} token ${l.Token || l.token} qty ${l.Qty ?? l.qty}`).join(", ") || "none";
        const head = `Broker sync for ${tradeUid}\n(matched on broker order id AND our order tag)\n\n`;
        if (!r.to_add) {
          window.alert(head + lines.join("\n") + `\n\nEverything matches the broker -- nothing to sync. Open legs: ${open}`);
          return;
        }
        if (!window.confirm(head + lines.join("\n") + `\n\n${r.to_add} broker fill(s) are missing from this trade. Record them under the trade now?\n(No order is sent. If a leg is left open the trade reopens so you can square it off.)`)) return;
        const ap = await call(true);
        if (!ap.success) { window.alert(`Broker sync apply failed: ${ap.error}`); return; }
        const a = ap.result || {};
        const openAfter = (a.open_legs || []).map((l) => `${l.OptionType || l.option_type} token ${l.Token || l.token} qty ${l.Qty ?? l.qty}`).join(", ") || "none";
        appendEventLog("success", `Broker sync ${tradeUid}: recorded ${a.to_add} missing fill(s); status ${a.status_from} -> ${a.status_to}; open legs: ${openAfter}`);
        window.alert(`Synced. Status ${a.status_from} -> ${a.status_to}. Open legs now: ${openAfter}`);
        await refreshPortfolio();
      } catch (err) {
        appendEventLog("error", `Broker sync ${tradeUid}: ${err.message}`);
      }
    };

    const handlePortfolioSync = async (item) => {
      const tradeUid = item.tradeUid || item.id;
      if (!tradeUid) return;

      appendEventLog("info", `Sync requested for ${tradeUid}`);
      try {
        const res = await fetch(`/api/trade/${encodeURIComponent(tradeUid)}/sync-preview`);
        const data = await res.json();
        if (data && data.success) {
          appendEventLog(
            "success",
            `Sync preview for ${tradeUid}: CE open=${data.ce?.open_qty ?? "—"} PE open=${data.pe?.open_qty ?? "—"} proposed_status=${data.proposed_trade_status ?? "—"}`
          );
        } else {
          appendEventLog("warn", `Sync preview for ${tradeUid}: ${data?.error || "no verified broker fills matched"}`);
        }
      } catch (err) {
        appendEventLog("error", `Sync preview failed for ${tradeUid}: ${err.message}`);
      }

      await refreshPortfolio();
    };

    const handlePortfolioHedge = async (item) => {
      if (isTradeClosed(item)) {
        appendEventLog("warn", `Hedge ignored for closed trade ${item.tradeUid || item.id}`);
        return;
      }

      const tradeUid = item.tradeUid || item.id;
      if (!tradeUid) return;

      if (!confirm(`Hedge trade ${tradeUid}?`)) return;

      try {
        appendEventLog("info", `Manual hedge requested for ${tradeUid}`);
        const data = await postJsonFallback(
          ["/api/trade/manual-execute"],
          portfolioActionPayload(item)
        );

        if (data.success === false) {
          throw new Error(data.error || "manual hedge failed");
        }

        // The server sizes Hedge Now from its own live delta and may place
        // nothing (|delta| under half a lot) -- show what it actually did
        // instead of always claiming a hedge went out.
        if (!(Number(data.quantity) > 0)) {
          const msg = data.message || "nothing to hedge";
          appendEventLog("info", `Hedge for ${tradeUid}: ${msg}`);
          alert(`No hedge placed: ${msg}`);
          return;
        }

        updatePortfolioStatus(tradeUid, "HEDGING");
        appendEventLog("success", `Hedge for ${tradeUid}: ${data.message || `${data.quantity} qty submitted`}`);
        await refreshPortfolio();
      } catch (err) {
        appendEventLog("error", `Hedge failed for ${tradeUid}: ${err.message}`);
        alert(`Hedge failed: ${err.message}`);
      }
    };

    const handlePortfolioPartialSquareOff = async (item) => {
      const tradeUid = item.tradeUid || item.id;
      if (!tradeUid) return;

      const raw = prompt(`Partial square off % for ${tradeUid}`, "50");
      if (raw === null) return;

      const percentage = Number(raw);
      if (!Number.isFinite(percentage) || percentage <= 0 || percentage > 100) {
        alert("Enter a percentage between 1 and 100");
        return;
      }

      if (!confirm(`Partially square off ${percentage}% of ${tradeUid}?`)) return;

      try {
        appendEventLog("info", `Partial square-off requested for ${tradeUid}`);
        const encoded = encodeURIComponent(tradeUid);
        const data = await postJsonFallback(
          [
            `/api/trade/${encoded}/partial-square-off`,
            `/api/straddle/partial-square-off/${encoded}`
          ],
          portfolioActionPayload(item, { percentage })
        );

        if (data.success === false) {
          throw new Error(data.error || data.detail || "partial square-off failed");
        }

        updatePortfolioStatus(tradeUid, "PARTIAL-SQF");
        appendEventLog("success", `Partial square-off submitted for ${tradeUid}`);
        await refreshPortfolio();
      } catch (err) {
        appendEventLog("error", `Partial SQF failed for ${tradeUid}: ${err.message}`);
        alert(`Partial SQF failed: ${err.message}`);
      }
    };

    const openModifyTradeModal = (item) => {

      const cfg = item.config || {};

      const rawSquareOffTime = cfg.square_off_time ?? cfg.squareOffTime ?? "";

      const squareOffTime = String(rawSquareOffTime || "");

      const rawSquareOffHardTime = cfg.square_off_hard_time ?? cfg.squareOffHardTime ?? "";

      const squareOffHardTime = String(rawSquareOffHardTime || "");


      setModifyTradeError("");

      setModifyTradeForm({

        // Prefilled from the trade's actual persisted config -- falling
        // back to the same conservative defaults used for a brand-new
        // one-lot NIFTY straddle only when a value was never set.

        sl_points_per_lot: cfg.sl_points_per_lot ?? cfg.slPointsPerLot ?? "30",

        sl_pnl_bps_of_spot: cfg.sl_pnl_bps_of_spot ?? cfg.slPnlBpsOfSpot ?? "14",

        tp_pnl_bps_of_spot: cfg.tp_pnl_bps_of_spot ?? cfg.tpPnlBpsOfSpot ?? "0",

        // MTM exit: blank = infinity (never fires).
        mtm_exit_level: cfg.mtm_exit_level != null ? String(cfg.mtm_exit_level) : "",

        mtm_exit_unit: cfg.mtm_exit_unit || "rs",

        mtm_exit_pct: String(cfg.mtm_exit_pct || 100),

        // ATM-straddle exit: blank = off.
        straddle_exit_below: cfg.straddle_exit_below != null ? String(cfg.straddle_exit_below) : "",

        straddle_exit_pct: String(cfg.straddle_exit_pct || 100),


        straddle_div: cfg.straddle_div ?? cfg.straddleDiv ?? "4",

        hedge_div: cfg.hedge_div ?? cfg.hedgeDiv ?? "57",

        hedge_threshold_delta: cfg.hedge_threshold_delta ?? cfg.hedgeThresholdDelta ?? String(item.lotSize || ""),

        hedge_min_threshold_bps: cfg.hedge_min_threshold_bps ?? "8",

        square_off_time: /^\d{2}:\d{2}:\d{2}$/.test(squareOffTime)

          ? squareOffTime

          : "",

        square_off_hard_time: /^\d{2}:\d{2}(:\d{2})?$/.test(squareOffHardTime)

          ? squareOffHardTime

          : ""

      });


      setModifyTradeModal({ open: true, trade: item });

    };


    const handleModifyTrade = async () => {

      const item = modifyTradeModal().trade;

      const tradeUid = item?.tradeUid || item?.id;


      if (!tradeUid) {

        setModifyTradeError("Trade UID is missing.");

        return;

      }


      const form = modifyTradeForm();

      const payload = {};


      for (const field of [

        "sl_points_per_lot",

        "sl_pnl_bps_of_spot",

        "tp_pnl_bps_of_spot",

        "straddle_div",

        "hedge_div",

        "hedge_threshold_delta",

        "hedge_min_threshold_bps"

      ]) {

        const raw = String(form[field] ?? "").trim();

        if (raw === "") continue;


        const value = Number(raw);

        if (!Number.isFinite(value)) {

          setModifyTradeError(`Invalid number for ${field}.`);

          return;

        }

        payload[field] = value;

      }

      // MTM exit: a number sets the level (rupees / pts / bps), blank = infinity.
      const mtmRaw = String(form.mtm_exit_level ?? "").trim();
      if (mtmRaw === "") {
        payload.mtm_exit_off = true;
      } else {
        const mtm = Number(mtmRaw);
        if (!Number.isFinite(mtm)) {
          setModifyTradeError("Invalid number for MTM exit level.");
          return;
        }
        payload.mtm_exit_level = mtm;
        payload.mtm_exit_unit = form.mtm_exit_unit || "rs";
      }
      const pctOf = (raw, name) => {
        const v = Number(String(raw ?? "").trim() || "100");
        if (!Number.isFinite(v) || v <= 0 || v > 100) {
          setModifyTradeError(`${name} must be 1-100 (100 = complete).`);
          return null;
        }
        return v;
      };
      const mtmPct = pctOf(form.mtm_exit_pct, "MTM exit %");
      if (mtmPct == null) return;
      payload.mtm_exit_pct = mtmPct;
      // ATM-straddle exit: a number = close when the ATM straddle LTP falls below it; blank = off.
      const strRaw = String(form.straddle_exit_below ?? "").trim();
      if (strRaw === "") {
        payload.straddle_exit_off = true;
      } else {
        const lvl = Number(strRaw);
        if (!Number.isFinite(lvl) || lvl <= 0) {
          setModifyTradeError("ATM straddle exit level must be a positive number.");
          return;
        }
        payload.straddle_exit_below = lvl;
        const sp = pctOf(form.straddle_exit_pct, "ATM straddle exit %");
        if (sp == null) return;
        payload.straddle_exit_pct = sp;
      }




      // Only the visible Exit Time field is sent (square_off_hard_time
      // below). The old alert-only square_off_time is never sent: it looked
      // like an exit time but never exited (live 2026-09-30: 15:38 saved
      // there, trade still exited at 15:37).


      const squareOffHardTime = String(form.square_off_hard_time ?? "").trim();

      if (squareOffHardTime !== "") {

        if (!/^\d{2}:\d{2}(:\d{2})?$/.test(squareOffHardTime)) {

          setModifyTradeError("Hard square-off time must be HH:MM or HH:MM:SS, for example 15:15.");

          return;

        }

        payload.square_off_hard_time = squareOffHardTime;

      }


      if (Object.keys(payload).length === 0) {

        setModifyTradeError("Enter at least one value to modify.");

        return;

      }


      setModifyTradeSaving(true);

      setModifyTradeError("");


      try {

        const data = await postJsonFallback(

          [`/api/trade/${encodeURIComponent(tradeUid)}/modify`],

          payload

        );


        if (data.success === false) {

          throw new Error(data.error || data.detail || "trade modification failed");

        }


        const updated = data.trade || {};

        setPortfolioItemsPersisted((previous) =>

          dedupePortfolio(previous.map((entry) => {

            const uid = entry.tradeUid || entry.id;

            return uid === tradeUid

              ? { ...entry, config: updated.config || entry.config, status: updated.status || entry.status }

              : entry;

          }))

        );


        appendEventLog("success", `Trade configuration updated for ${tradeUid}`);

        setModifyTradeModal({ open: false, trade: null });

        await refreshPortfolio();

      } catch (err) {

        const message = err?.message || "trade modification failed";

        setModifyTradeError(message);

        appendEventLog("error", `Modify failed for ${tradeUid}: ${message}`);

      } finally {

        setModifyTradeSaving(false);

      }

    };


    const handlePortfolioSquareOff = async (item) => {
      if (isTradeClosed(item)) {
        appendEventLog("warn", `Square-off ignored for closed trade ${item.tradeUid || item.id}`);
        return;
      }

      const tradeUid = item.tradeUid || item.id;
      if (!tradeUid) return;

      if (!confirm(`FULL square off trade ${tradeUid}?\n\nThis should send opposite BUY orders for open short legs.`)) return;

      try {
        appendEventLog("info", `Full square-off requested for ${tradeUid}`);
        const encoded = encodeURIComponent(tradeUid);
        const data = await postJsonFallback(
          [
            `/api/trade/${encoded}/square-off`
          ],
          portfolioActionPayload(item)
        );

        if (data.success === false) {
          throw new Error(data.error || data.detail || "square-off failed");
        }

        updatePortfolioStatus(tradeUid, "SQUARING-OFF");
        appendEventLog("success", `Full square-off submitted for ${tradeUid}`);
        await refreshPortfolio();
      } catch (err) {
        appendEventLog("error", `Full SQF failed for ${tradeUid}: ${err.message}`);
        alert(`Full SQF failed: ${err.message}`);
      }
    };


    const setUiDefaults = () => {
      const now = new Date();
      now.setMinutes(now.getMinutes() + 1);
      now.setSeconds(0);
      now.setMilliseconds(0);

      const nextMinuteStr = now.toLocaleTimeString('en-GB', {
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
        hour12: false
      });

      let exitTimeStr = '15:37:00';  // Auto square-off 3 min before market close
      const marketExit = new Date();
      marketExit.setHours(15, 37, 0, 0);  // Square-off at 15:37

      if (now >= marketExit) {
        const testExit = new Date(now);
        testExit.setHours(testExit.getHours() + 1);
        exitTimeStr = testExit.toLocaleTimeString('en-GB', {
          hour: '2-digit',
          minute: '2-digit',
          second: '2-digit',
          hour12: false
        });
      }

      const isSensex = selectedSymbol().includes('SENSEX');
      const defaultBuffer = isSensex ? 6 : 2;

      setAutomationConfig((prev) => ({
        ...prev,
        symbol: selectedSymbol(),
        entry_time: nextMinuteStr,
        exit_time: exitTimeStr,
        sl_start_time: nextMinuteStr,
        hedge_start_time: nextMinuteStr,
        roll_start_time: nextMinuteStr,
        size: 1,
        hedge_div: 57,
        straddle_div: 4,
        roll_straddle_div: 0.2,
        buy_buffer: defaultBuffer,
        sell_buffer: defaultBuffer
      }));

      // Same past-market-close push-forward as the Automation defaults
      // above: a manual build can happen at any time of day, not just
      // before 15:37, and the default exit time must never be in the past
      // (DeployStraddle rejects an exit_time that isn't after entry_time).
      setManualRiskConfig((prev) => ({ ...prev, exit_time: exitTimeStr }));
    };

    const handleAutomationBuild = async () => {
      const cfg = automationConfig();

      if (!automationExpiry()) {
        appendEventLog("error", "Automation build not scheduled: choose the expiry first");
        return;
      }

      const payload = {
        user_id: executionPrefs().user_id || 'U001',
        broker_name: executionPrefs().broker_name,
        account_id: executionPrefs().account_id,
        exchange_segment: executionPrefs().exchange_segment,
        product_type: executionPrefs().product_type || 'NRML',
        symbol: cfg.symbol,
        size: cfg.size,
        lots: cfg.size,
        entry_time: cfg.entry_time,
        exit_time: cfg.exit_time,
        idv: cfg.idv,
        idv_divisor: cfg.idv_divisor,
        straddle_filter: cfg.straddle_filter,
        sl_bps: cfg.sl_bps,
        tp_bps: cfg.tp_bps,
        wing_pct: cfg.wing_pct,
        buy_buffer: cfg.buy_buffer,
        sell_buffer: cfg.sell_buffer,
        hedge_div: cfg.hedge_div,
        straddle_div: cfg.straddle_div,
        roll_straddle_div: cfg.roll_straddle_div,
        sl_start_time: cfg.sl_start_time,
        hedge_start_time: cfg.hedge_start_time,
        roll_start_time: cfg.roll_start_time,
        order_lots_per_call: cfg.order_lots_per_call,
        target_expiry: automationExpiry()
      };

      appendEventLog(
        "info",
        `Automation build trigger via ${payload.broker_name} for ${payload.symbol} ${payload.target_expiry}`
      );

      try {
        const data = await safeFetchJson("/api/trade/straddle/automated", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(payload)
        });

        if (data.success) {
          appendEventLog(
            "success",
            `Automation build scheduled for ${data.entry_time}: ${data.symbol} ${data.expiry}, exit ${data.exit_time || "none"}, SL ${data.sl_bps || 0} bps, TP ${data.tp_bps || 0} bps. ${data.message}`
          );
          if (Array.isArray(data.not_applied) && data.not_applied.length > 0) {
            appendEventLog(
              "warn",
              `Accepted but NOT implemented yet (no effect): ${data.not_applied.join(", ")}`
            );
          }
          loadScheduledBuilds();
        } else {
          appendEventLog("error", `Automation build failed: ${data.error || "Unknown error"}`);
        }
      } catch (err) {
        appendEventLog("error", `Automation build error: ${err.message}`);
      }
    };


    const loadScheduledBuilds = async () => {
      try {
        const data = await safeFetchJson("/api/trade/straddle/scheduled");
        setScheduledBuilds(Array.isArray(data?.jobs) ? data.jobs : []);
      } catch (err) {
        // Non-fatal: the list is informational.
      }
    };

    const cancelScheduledBuild = async (jobId) => {
      try {
        const data = await safeFetchJson("/api/trade/straddle/scheduled/cancel", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ job_id: jobId })
        });
        appendEventLog(data.success ? "success" : "error", data.success ? `Cancelled scheduled build ${jobId}` : `Cancel failed: ${data.error}`);
      } catch (err) {
        appendEventLog("error", `Cancel failed: ${err.message}`);
      }
      loadScheduledBuilds();
    };

    const buildManualHedgePayload = () => {
      const mh = manualHedgeConfig();
      return {
        user_id: executionPrefs().user_id || 'U001',
        broker_name: executionPrefs().broker_name,
        account_id: executionPrefs().account_id,
        exchange_segment: executionPrefs().exchange_segment,
        symbol: automationConfig().symbol,
        expiry: selectedExpiry() || optionChain().expiry,
        target_expiry: selectedExpiry() || optionChain().expiry,
        net_delta: Number(mh.net_delta) || 0,
        lot_size: Number(mh.lot_size) || 0,
        quantity: Number(mh.quantity) || 0
      };
    };

    const handleManualHedgePreview = async () => {
      const payload = buildManualHedgePayload();
      setManualHedgeBusy(true);
      setManualHedgeError('');
      setManualHedgeExecutions([]);
      appendEventLog("info", `Manual hedge preview via ${payload.broker_name} for ${payload.symbol}`);

      try {
        const data = await safeFetchJson("/api/trade/manual-test", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(payload)
        });

        if (data.success) {
          setManualHedgePreview(data);
          appendEventLog("success", "Manual hedge preview ready");
        } else {
          setManualHedgePreview(null);
          setManualHedgeError(data.error || "Manual hedge preview failed");
          appendEventLog("error", `Manual hedge preview failed: ${data.error || "Unknown error"}`);
        }
      } catch (err) {
        setManualHedgePreview(null);
        setManualHedgeError(err.message || "Manual hedge preview failed");
        appendEventLog("error", `Manual hedge preview error: ${err.message}`);
      } finally {
        setManualHedgeBusy(false);
      }
    };

    const handleManualHedgeExecute = async () => {
      const payload = buildManualHedgePayload();
      setManualHedgeBusy(true);
      setManualHedgeError('');
      appendEventLog("info", `Manual hedge execute via ${payload.broker_name} for ${payload.symbol}`);

      try {
        const data = await safeFetchJson("/api/trade/manual-execute", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(payload)
        });

        if (data.success) {
          setManualHedgePreview(data);
          setManualHedgeExecutions(Array.isArray(data.executions) ? data.executions : []);
          appendEventLog("success", "Manual hedge execution completed");
        } else {
          setManualHedgeExecutions([]);
          setManualHedgeError(data.error || "Manual hedge execution failed");
          appendEventLog("error", `Manual hedge execution failed: ${data.error || "Unknown error"}`);
        }
      } catch (err) {
        setManualHedgeExecutions([]);
        setManualHedgeError(err.message || "Manual hedge execution failed");
        appendEventLog("error", `Manual hedge execution error: ${err.message}`);
      } finally {
        setManualHedgeBusy(false);
      }
    };

    return (
      <div class="terminal-shell">
        <header class="topbar">
          <div class="brand">
            <div class="brand-title">TRADING TERMINAL</div>
            <div class="brand-subtitle">
              Live option chain • low-latency feed • execution ready
            </div>
          </div>
          <div class="topbar-right">
            <div class="status-chip">{wsStatus()}</div>
            <div class="status-chip muted">{dataSource()}</div>
          </div>
        </header>

        <div class="main-tabs">
          <button

            class={`tab-btn ${activeTab() === 'testing' ? 'active' : ''}`}

            onClick={() => setActiveTab('testing')}

          >

            Testing

          </button>


          <button
            class={`tab-btn ${activeTab() === 'terminal' ? 'active' : ''}`}
            onClick={() => setActiveTab('terminal')}
          >
            Terminal
          </button>
          <button
            class={`tab-btn ${activeTab() === 'portfolio' ? 'active' : ''}`}
            onClick={() => setActiveTab('portfolio')}
          >
            Portfolio
          </button>
          <button
            class={`tab-btn ${activeTab() === 'automation' ? 'active' : ''}`}
            onClick={() => setActiveTab('automation')}
          >
            Automation
          </button>
          <button
            class={`tab-btn ${activeTab() === 'lut' ? 'active' : ''}`}
            onClick={() => setActiveTab('lut')}
          >
            LUT Build
          </button>
          <button
            class={`tab-btn ${activeTab() === 'sbuild' ? 'active' : ''}`}
            onClick={() => setActiveTab('sbuild')}
          >
            Straddle Build
          </button>
          <button
            class={`tab-btn ${activeTab() === 'papersim' ? 'active' : ''}`}
            onClick={() => setActiveTab('papersim')}
          >
            Paper Sim
          </button>
          <button
            class={`tab-btn ${activeTab() === 'logs' ? 'active' : ''}`}
            onClick={() => setActiveTab('logs')}
          >
            Logs
          </button>
          <button
            class={`tab-btn ${activeTab() === 'latency' ? 'active' : ''}`}
            onClick={() => setActiveTab('latency')}
          >
            Latency
          </button>
        </div>

        <Show when={activeTab() === 'terminal'}>
          <section class="toolbar">
            <div class="toolbar-left">
              <div class="control-block">
                <label class="control-label">Symbol</label>
                <select class="symbol-select" value={selectedSymbol()} onChange={handleSymbolChange}>
                  <Index each={availableSymbols}>
                    {(s) => <option value={s()}>{s()}</option>}
                  </Index>
                </select>
              </div>

              <div class="control-block">
                <label class="control-label">Expiry</label>
                <select class="symbol-select" value={selectedExpiry()} onChange={handleExpiryChange}>
                  <For each={(optionChain().available_expiries || []).length ? optionChain().available_expiries : [optionChain().expiry || '']}>
                    {(exp) => <option value={exp}>{exp || '—'}</option>}
                  </For>
                </select>
              </div>

              <div class="control-block">
                <label class="control-label">Broker</label>
                <select
                  class="symbol-select"
                  value={executionPrefs().broker_name}
                  onChange={(e) =>
                    setExecutionPrefs((prev) => ({
                      ...prev,
                      broker_name: e.target.value
                    }))
                  }
                >
                  <For each={brokerOptions}>
                    {(b) => <option value={b}>{b.toUpperCase()}</option>}
                  </For>
                </select>
              </div>
              <div class="control-block">
                <label class="control-label">Account</label>
                <select
                  class="symbol-select"
                  value={executionPrefs().account_id}
                  onChange={(e) =>
                    setExecutionPrefs((prev) => ({
                      ...prev,
                      account_id: e.target.value
                    }))
                  }
                >
                  <For each={greeksoftAccounts}>
                    {(acc) => <option value={acc}>{acc}</option>}
                  </For>
                </select>
              </div>


<div class="range-wrap">
                <label>Visible strikes</label>
                <input
                  type="range"
                  min="8"
                  max="30"
                  value={visibleStrikes()}
                  onInput={(e) => setVisibleStrikes(parseInt(e.target.value, 10))}
                />
                <span>{visibleStrikes() * 2 + 1}</span>
              </div>
            </div>

            <div class="toolbar-right">
              <div class="metric-card">
                <div class="metric-label">Underlying (Synthetic)</div>
                <div class="metric-value spot">₹ {fmt(underlying(), 2)}</div>
                <div class="metric-sub">
                  Synthetic: {toNum(optionChain().synthetic_future) > 0 ? `₹ ${fmt(optionChain().synthetic_future, 2)}` : '—'}
                </div>
                <div class="metric-sub">
                  Future LTP: {toNum(optionChain().future_ltp) > 0 ? `₹ ${fmt(optionChain().future_ltp, 2)}` : '—'}
                </div>
              </div>

              <div class="metric-card">
                <div class="metric-label">ATM</div>
                <div class="metric-value atm-value">{optionChain().atm || '—'}</div>
                <div class="metric-sub">No UI smoothing</div>
              </div>

              <div class="metric-card">
                <div class="metric-label">Sell Quantity</div>
                <div class="metric-input-row">
                  <input
                    class="symbol-select"
                    type="text" inputmode="decimal"
                    min={lotSize()}
                    step={lotSize()}
                    value={terminalSellQtyText()}
                    onInput={(e) => setTerminalSellQtyText(e.target.value)}
                    onBlur={(e) => {
                      const ls = lotSize();
                      const rawQty = Number(e.target.value) || ls;
                      const lots = Math.max(1, Math.round(rawQty / ls));
                      setTerminalSellQty(lots);
                      setTerminalSellQtyText(String(lots * ls));
                    }}
                  />
                </div>
                <div class="metric-sub">
                  Total quantity, rounded to the nearest {lotSize()} (lot size) &middot; {terminalSellQty()} lot{terminalSellQty() === 1 ? '' : 's'}
                </div>
              </div>

              <div class="metric-card">
                <div class="metric-label">Risk &amp; Exit (manual build)</div>
                <div class="metric-input-row" style={{ display: 'flex', gap: '8px' }}>
                  <input
                    class="symbol-select"
                    type="text"
                    title="Exit time (HH:MM:SS)"
                    value={manualRiskConfig().exit_time}
                    onInput={(e) => setManualRiskConfig((prev) => ({ ...prev, exit_time: e.target.value }))}
                  />
                  <input
                    class="symbol-select"
                    type="text" inputmode="decimal"
                    title="SL (bps of spot)"
                    value={manualRiskConfig().sl_bps}
                    onInput={(e) => setManualRiskConfig((prev) => ({ ...prev, sl_bps: Number(e.target.value) || 0 }))}
                  />
                  <input
                    class="symbol-select"
                    type="text" inputmode="decimal"
                    title="TP (bps of spot)"
                    value={manualRiskConfig().tp_bps}
                    onInput={(e) => setManualRiskConfig((prev) => ({ ...prev, tp_bps: Number(e.target.value) || 0 }))}
                  />
                  <input
                    class="symbol-select"
                    type="text" inputmode="decimal"
                    step="0.5"
                    min="0"
                    title="Wings % (0 = no wings)"
                    value={manualRiskConfig().wing_pct}
                    onInput={(e) => setManualRiskConfig((prev) => ({ ...prev, wing_pct: Number(e.target.value) || 0 }))}
                  />
                </div>
                <div class="metric-sub">
                  Exit time / SL bps / TP bps / Wings % &middot; applied to SELL STRADDLE and custom sell below
                </div>
              </div>

              <div class="metric-card highlight">
                <div class="metric-label">ATM Straddle</div>
                <div class="metric-value">₹ {fmt(atmStraddle(), 2)}</div>
                <div class="metric-sub">
                  {atmRow()
                    ? `CE ${fmt(atmRow().ce_ltp, 2)} + PE ${fmt(atmRow().pe_ltp, 2)}`
                    : 'ATM row unavailable'}
                </div>
                <div class="metric-sub">
                  Broker: {executionPrefs().broker_name.toUpperCase()} • Account: {executionPrefs().account_id} • Symbol: {selectedSymbol()}
                </div>
                <button class="sell-btn" onClick={handleSellStraddle}>
                  SELL STRADDLE
                </button>
                <Show when={sellStatus()}>
                  <div class="metric-sub sell-status">{sellStatus()}</div>
                </Show>
              </div>

              <div class="metric-card">
                <div class="metric-label">Custom Strangle/Straddle</div>
                <div class="custom-strike-inputs metric-input-row">
                  <input
                    class="symbol-select"
                    type="text" inputmode="decimal"
                    placeholder="CE Strike"
                    value={customCeStrike()}
                    onInput={(e) => setCustomCeStrike(e.target.value)}
                  />
                  <input
                    class="symbol-select"
                    type="text" inputmode="decimal"
                    placeholder="PE Strike"
                    value={customPeStrike()}
                    onInput={(e) => setCustomPeStrike(e.target.value)}
                  />
                </div>
                <button class="sell-btn" onClick={handleSellCustomStraddle}>
                  SELL CUSTOM
                </button>
              </div>

              <div class="metric-card">
                <div class="metric-label">Expiry</div>
                <div class="metric-value">{selectedExpiry() || optionChain().expiry || '—'}</div>
                <div class="metric-sub">Rows: {optionChain().chain.length}</div>
              </div>

              <div class="metric-card">
                <div class="metric-label">Active Quotes</div>
                <div class="metric-value">
                  CE {stats().ceActive} / PE {stats().peActive}
                </div>
                <div class="metric-sub">Symbol: {selectedSymbol()}</div>
              </div>
            </div>
          </section>

          <section class="chain-panel">
            <div class="panel-header">
              <div class="panel-title">{selectedSymbol()} OPTION CHAIN</div>
              <div class="panel-subtitle">Raw backend ATM • synthetic-only underlying</div>
            </div>

            <Show
              when={filteredChain().length > 0}
              fallback={<div class="empty-state">Waiting for live option-chain data…</div>}
            >
              <div class="table-wrap">
                <table class="chain-table">
                  <thead>
                    <tr>
                      <th class="group-h ce-head" colSpan="6">CALLS</th>
                      <th class="strike-head">STRIKE</th>
                      <th class="group-h pe-head" colSpan="6">PUTS</th>
                    </tr>
                    <tr>
                      <th>LTP</th>
                      <th>IV</th>
                      <th>Δ</th>
                      <th>Γ</th>
                      <th>Θ</th>
                      <th>Vega</th>
                      <th class="strike-col">Strike</th>
                      <th>LTP</th>
                      <th>IV</th>
                      <th>Δ</th>
                      <th>Γ</th>
                      <th>Θ</th>
                      <th>Vega</th>
                    </tr>
                  </thead>
                  <tbody>
                    <Index each={filteredChain()}>
                      {(row) => (
                        <tr class={row().is_atm ? 'atm-row' : ''}>
                          <td class={cellClass(row().ce_ltp, 'ce')}>
                            {row().ce_ltp > 0 ? fmt(row().ce_ltp, 2) : '—'}
                          </td>
                          <td>{fmt(row().ce_iv, 4)}</td>
                          <td>{fmt(row().ce_delta, 4)}</td>
                          <td>{fmt(row().ce_gamma, 6)}</td>
                          <td>{fmt(row().ce_theta, 2)}</td>
                          <td>{fmt(row().ce_vega, 2)}</td>

                          <td class="strike-col">
                            <div class="strike-box">
                              <span class="strike-value">{row().strike}</span>
                              <Show when={row().is_atm}>
                                <span class="atm-badge">ATM</span>
                              </Show>
                            </div>
                          </td>

                          <td class={cellClass(row().pe_ltp, 'pe')}>
                            {row().pe_ltp > 0 ? fmt(row().pe_ltp, 2) : '—'}
                          </td>
                          <td>{fmt(row().pe_iv, 4)}</td>
                          <td>{fmt(row().pe_delta, 4)}</td>
                          <td>{fmt(row().pe_gamma, 6)}</td>
                          <td>{fmt(row().pe_theta, 2)}</td>
                          <td>{fmt(row().pe_vega, 2)}</td>
                        </tr>
                      )}
                    </Index>
                  </tbody>
                </table>
              </div>
            </Show>
          </section>
        </Show>


        <Show when={modifyTradeModal().open}>
          <div
            onClick={() => {
              if (!modifyTradeSaving()) {
                setModifyTradeModal({ open: false, trade: null });
                setModifyTradeError("");
              }
            }}
            style={{
              position: "fixed",
              inset: "0",
              "z-index": "9999",
              display: "flex",
              "align-items": "center",
              "justify-content": "center",
              padding: "20px",
              background: "rgba(0, 0, 0, 0.68)"
            }}
          >
            <div
              onClick={(event) => event.stopPropagation()}
              style={{
                width: "min(560px, 100%)",
                background: "#171723",
                color: "#fff",
                border: "1px solid #3d3d54",
                "border-radius": "10px",
                padding: "20px",
                "box-shadow": "0 16px 50px rgba(0, 0, 0, 0.45)"
              }}
            >
              <div style={{ display: "flex", "justify-content": "space-between", "align-items": "center", "margin-bottom": "16px" }}>
                <div>
                  <div style={{ "font-size": "18px", "font-weight": "700" }}>Modify Trade Risk Settings</div>
                  <div style={{ "font-size": "12px", color: "#9aa0a6", "margin-top": "4px", "word-break": "break-all" }}>
                    {modifyTradeModal().trade?.tradeUid || modifyTradeModal().trade?.id}
                  </div>
                </div>
                <button
                  onClick={() => {
                    if (!modifyTradeSaving()) {
                      setModifyTradeModal({ open: false, trade: null });
                      setModifyTradeError("");
                    }
                  }}
                  style={{ background: "transparent", border: "none", color: "#aab0c0", cursor: "pointer", "font-size": "22px" }}
                  aria-label="Close"
                >
                  ×
                </button>
              </div>

              <div style={{ display: "grid", "grid-template-columns": "repeat(2, minmax(0, 1fr))", gap: "12px" }}>
                <label style={{ display: "flex", "flex-direction": "column", gap: "6px", "font-size": "13px" }}>
                  SL (points / lot)
                  <input
                    type="text" inputmode="decimal"
                    step="0.01"
                    value={modifyTradeForm().sl_points_per_lot}
                    onInput={(event) => setModifyTradeForm((form) => ({ ...form, sl_points_per_lot: event.currentTarget.value }))}
                    style={{ padding: "9px", background: "#0f0f18", color: "#fff", border: "1px solid #44445a", "border-radius": "5px" }}
                  />
                </label>

                <label style={{ display: "flex", "flex-direction": "column", gap: "6px", "font-size": "13px" }}>
                  SL (bps of spot)
                  <input
                    type="text" inputmode="decimal"
                    step="0.01"
                    value={modifyTradeForm().sl_pnl_bps_of_spot}
                    onInput={(event) => setModifyTradeForm((form) => ({ ...form, sl_pnl_bps_of_spot: event.currentTarget.value }))}
                    style={{ padding: "9px", background: "#0f0f18", color: "#fff", border: "1px solid #44445a", "border-radius": "5px" }}
                  />
                </label>

                <label style={{ display: "flex", "flex-direction": "column", gap: "6px", "font-size": "13px" }}>
                  TP (bps of spot)
                  <input
                    type="text" inputmode="decimal"
                    step="0.01"
                    value={modifyTradeForm().tp_pnl_bps_of_spot}
                    onInput={(event) => setModifyTradeForm((form) => ({ ...form, tp_pnl_bps_of_spot: event.currentTarget.value }))}
                    style={{ padding: "9px", background: "#0f0f18", color: "#fff", border: "1px solid #44445a", "border-radius": "5px" }}
                  />
                </label>

                <label style={{ display: "flex", "flex-direction": "column", gap: "6px", "font-size": "13px" }}>
                  MTM exit above (blank = ∞)
                  <div style={{ display: "flex", gap: "6px" }}>
                    <input
                      type="text" inputmode="decimal"
                      step="any"
                      placeholder="∞"
                      value={modifyTradeForm().mtm_exit_level}
                      onInput={(event) => setModifyTradeForm((form) => ({ ...form, mtm_exit_level: event.currentTarget.value }))}
                      title="Closes the trade lot by lot at depth-checked IOC limits so it ends at or above this MTM (may be negative)"
                      style={{ flex: 1, padding: "9px", background: "#0f0f18", color: "#fff", border: "1px solid #44445a", "border-radius": "5px" }}
                    />
                    <input
                      type="text" inputmode="decimal"
                      min="1"
                      max="100"
                      value={modifyTradeForm().mtm_exit_pct}
                      onInput={(event) => setModifyTradeForm((form) => ({ ...form, mtm_exit_pct: event.currentTarget.value }))}
                      title="% of the ORIGINAL position this rule closes (100 = complete). Rules together never close more than what is open."
                      style={{ width: "70px", padding: "9px", background: "#0f0f18", color: "#fff", border: "1px solid #44445a", "border-radius": "5px" }}
                    />
                    <select
                      value={modifyTradeForm().mtm_exit_unit}
                      onChange={(event) => setModifyTradeForm((form) => ({ ...form, mtm_exit_unit: event.currentTarget.value }))}
                      style={{ padding: "9px", background: "#0f0f18", color: "#fff", border: "1px solid #44445a", "border-radius": "5px" }}
                    >
                      <option value="rs">₹ total</option>
                      <option value="pts">pts / straddle</option>
                      <option value="bps">bps of spot</option>
                    </select>
                  </div>
                </label>

                <label style={{ display: "flex", "flex-direction": "column", gap: "6px", "font-size": "13px" }}>
                  ATM straddle exit below (blank = off) · % of position
                  <div style={{ display: "flex", gap: "6px" }}>
                    <input
                      type="text" inputmode="decimal"
                      step="0.05"
                      placeholder="off"
                      value={modifyTradeForm().straddle_exit_below}
                      onInput={(event) => setModifyTradeForm((form) => ({ ...form, straddle_exit_below: event.currentTarget.value }))}
                      title="Square off only when the live ATM CE LTP + PE LTP falls BELOW this; above it nothing happens"
                      style={{ flex: 1, padding: "9px", background: "#0f0f18", color: "#fff", border: "1px solid #44445a", "border-radius": "5px" }}
                    />
                    <input
                      type="text" inputmode="decimal"
                      min="1"
                      max="100"
                      value={modifyTradeForm().straddle_exit_pct}
                      onInput={(event) => setModifyTradeForm((form) => ({ ...form, straddle_exit_pct: event.currentTarget.value }))}
                      title="% of the ORIGINAL position (100 = complete). Combined with the other exits it never exceeds what is open."
                      style={{ width: "70px", padding: "9px", background: "#0f0f18", color: "#fff", border: "1px solid #44445a", "border-radius": "5px" }}
                    />
                  </div>
                </label>

                <label style={{ display: "flex", "flex-direction": "column", gap: "6px", "font-size": "13px" }}>
                  Hedge Delta Threshold
                  <input
                    type="text" inputmode="decimal"
                    step="0.01"
                    value={modifyTradeForm().hedge_threshold_delta}
                    onInput={(event) => setModifyTradeForm((form) => ({ ...form, hedge_threshold_delta: event.currentTarget.value }))}
                    style={{ padding: "9px", background: "#0f0f18", color: "#fff", border: "1px solid #44445a", "border-radius": "5px" }}
                  />
                </label>

                <label style={{ display: "flex", "flex-direction": "column", gap: "6px", "font-size": "13px" }}>
                  Straddle Divisor
                  <input
                    type="text" inputmode="decimal"
                    min="0.01"
                    step="0.01"
                    value={modifyTradeForm().straddle_div}
                    onInput={(event) => setModifyTradeForm((form) => ({ ...form, straddle_div: event.currentTarget.value }))}
                    style={{ padding: "9px", background: "#0f0f18", color: "#fff", border: "1px solid #44445a", "border-radius": "5px" }}
                  />
                </label>

                <label style={{ display: "flex", "flex-direction": "column", gap: "6px", "font-size": "13px" }}>
                  Min Hedge Threshold (bps of spot)
                  <input
                    type="text" inputmode="decimal"
                    min="0"
                    step="0.1"
                    value={modifyTradeForm().hedge_min_threshold_bps}
                    onInput={(event) => setModifyTradeForm((form) => ({ ...form, hedge_min_threshold_bps: event.currentTarget.value }))}
                    style={{ padding: "9px", background: "#0f0f18", color: "#fff", border: "1px solid #44445a", "border-radius": "5px" }}
                  />
                </label>

                <label style={{ display: "flex", "flex-direction": "column", gap: "6px", "font-size": "13px" }}>
                  Hedge Divisor
                  <input
                    type="text" inputmode="decimal"
                    min="0.01"
                    step="0.01"
                    value={modifyTradeForm().hedge_div}
                    onInput={(event) => setModifyTradeForm((form) => ({ ...form, hedge_div: event.currentTarget.value }))}
                    style={{ padding: "9px", background: "#0f0f18", color: "#fff", border: "1px solid #44445a", "border-radius": "5px" }}
                  />
                </label>

                <label style={{ display: "flex", "flex-direction": "column", gap: "6px", "font-size": "13px", "grid-column": "1 / -1" }}>
                  Exit Time (HH:MM or HH:MM:SS) -- squares off the trade at this time
                  <input
                    type="text"
                    placeholder="15:15"
                    value={modifyTradeForm().square_off_hard_time}
                    onInput={(event) => setModifyTradeForm((form) => ({ ...form, square_off_hard_time: event.currentTarget.value }))}
                    style={{ padding: "9px", background: "#0f0f18", color: "#fff", border: "1px solid #44445a", "border-radius": "5px" }}
                  />
                </label>

                

              </div>

              <Show when={modifyTradeError()}>
                <div style={{ color: "#f87171", "font-size": "13px", "margin-top": "14px" }}>
                  {modifyTradeError()}
                </div>
              </Show>

              <div style={{ display: "flex", "justify-content": "flex-end", gap: "10px", "margin-top": "20px" }}>
                <button
                  onClick={() => {
                    setModifyTradeModal({ open: false, trade: null });
                    setModifyTradeError("");
                  }}
                  disabled={modifyTradeSaving()}
                  style={{ padding: "9px 14px", background: "#2e2e3d", color: "#fff", border: "none", "border-radius": "5px", cursor: "pointer" }}
                >
                  Cancel
                </button>
                <button
                  onClick={handleModifyTrade}
                  disabled={modifyTradeSaving()}
                  style={{ padding: "9px 14px", background: "#8b5cf6", color: "#fff", border: "none", "border-radius": "5px", cursor: "pointer", "font-weight": "700" }}
                >
                  {modifyTradeSaving() ? "Saving…" : "Save Changes"}
                </button>
              </div>
            </div>
          </div>
        </Show>

        <Show when={activeTab() === 'portfolio'}>
          <section class="tab-panel">
            <div class="panel-header">
              <div class="panel-title">Portfolio / Active Trades</div>
              <div class="panel-subtitle">Today's open and closed trades with realized PnL. Click a row to see what executed.</div>
            </div>

            <Show when={posCheck() && (posCheck().ok === false || (posCheck().reopened || []).length > 0)}>
              <div style={{ margin: "0 0 12px", padding: "10px 12px", border: "1px solid #c62828", "border-radius": "8px", background: "rgba(198,40,40,0.12)", "font-size": "13px" }}>
                <strong style={{ color: "#ff5252" }}>Broker and platform positions differ</strong>
                <span style={{ opacity: 0.7 }}> (checked {posCheck().at || "—"})</span>
                <Show when={posCheck().error}><div>{posCheck().error}</div></Show>
                <For each={posCheck().diffs || []}>
                  {(d) => (
                    <div>
                      {d.symbol || d.token}: broker net <strong>{d.broker_qty}</strong> vs platform <strong>{d.our_qty}</strong>
                      {(d.trades || []).length ? ` — trade ${d.trades.join(", ")}` : " — not in any platform trade"}
                    </div>
                  )}
                </For>
                <Show when={(posCheck().reopened || []).length > 0}>
                  <div>Reopened by auto broker sync (missed fills recorded): {posCheck().reopened.join(", ")} — square them off from their cards.</div>
                </Show>
              </div>
            </Show>

            <Show when={portfolioTotals()}>
              <div style={{ display: "flex", gap: "18px", "flex-wrap": "wrap", "align-items": "baseline", margin: "0 0 14px" }}>
                <span>{portfolioTotals().date}</span>
                <span>{portfolioTotals().trades} trades</span>
                <span>{portfolioTotals().open} open</span>
                <span>{portfolioTotals().closed} closed</span>
                <span>
                  Realized{" "}
                  <strong class={Number(portfolioTotals().realized_pnl) >= 0 ? "positive" : "negative"}>
                    ₹{fmt(portfolioTotals().realized_pnl, 2)}
                  </strong>{" "}
                  <span style={{ opacity: "0.6", "font-size": "12px" }}>(gross of brokerage and charges)</span>
                </span>
                <span>
                  Wings realized{" "}
                  <strong class={Number(portfolioTotals().wing_realized_pnl) >= 0 ? "positive" : "negative"}>
                    ₹{fmt(portfolioTotals().wing_realized_pnl ?? 0, 2)}
                  </strong>{" "}
                  <span style={{ opacity: "0.6", "font-size": "12px" }}>(RM only, not in Realized)</span>
                </span>
              </div>
            </Show>

            <PortfolioMTMPanel />


            <Show when={portfolioItems().length > 0}>
              <div class="portfolio-live-totals" style={{ display: "flex", gap: "18px", "flex-wrap": "wrap", "align-items": "baseline", margin: "0 0 14px", padding: "10px 12px", border: "1px solid rgba(255,255,255,0.08)", "border-radius": "8px" }}>
                <strong>Portfolio ({portfolioLive().open} open)</strong>
                <span>Net Δ <strong class={portfolioLive().delta >= 0 ? "positive" : "negative"}>{fmt(portfolioLive().delta, 2)}</strong></span>
                <span>Γ <strong>{fmt(portfolioLive().gamma, 4)}</strong></span>
                <span>Θ <strong>{fmt(portfolioLive().theta, 2)}</strong></span>
                <span>Vega <strong>{fmt(portfolioLive().vega, 2)}</strong></span>
                <span>Unrealized <strong class={portfolioLive().unrealized >= 0 ? "positive" : "negative"}>₹{fmt(portfolioLive().unrealized, 2)}</strong></span>
                <span>Realized <strong class={portfolioLive().realized >= 0 ? "positive" : "negative"}>₹{fmt(portfolioLive().realized, 2)}</strong></span>
                <span>Total PnL <strong class={portfolioLive().total >= 0 ? "positive" : "negative"}>₹{fmt(portfolioLive().total, 2)}</strong></span>
                <span style={{ opacity: 0.8 }}>Wings <strong class={portfolioLive().wing >= 0 ? "positive" : "negative"}>₹{fmt(portfolioLive().wing, 2)}</strong> <span style={{ opacity: "0.6", "font-size": "12px" }}>(RM only, not in totals)</span></span>
              </div>
            </Show>

            <Show
              when={portfolioItems().length > 0 || sbStore.holding().length > 0}
              fallback={
                <div class="empty-state">
                  No current-account portfolio items yet. Build/sell a straddle or press SYNC after broker execution.
                </div>
              }
            >
              <div class="positions-table-wrapper" style={{ "margin-bottom": "24px", "overflow-x": "auto" }}>
                <table class="positions-table" style={{ width: "100%", "border-collapse": "collapse", "white-space": "nowrap" }}>
                  <thead>
                    <tr style={{ background: "#202020", "text-align": "left" }}>
                      <th style={{ padding: "10px" }} title="Last 8 characters of the trade id -- click a row for full details">UID</th>
                      <th style={{ padding: "10px" }}>Symbol</th>
                      <th style={{ padding: "10px" }} title="Strike of the sold straddle">Strike</th>
                      <th style={{ padding: "10px" }} title="ACTIVE = monitored; PARTIAL = built less than requested; CLOSED_* = how it was closed">Status</th>
                      <th style={{ padding: "10px" }} title="Contracts of the CE leg currently sold">CE Qty</th>
                      <th style={{ padding: "10px" }} title="Last traded price of the CE leg (avg exit price once closed)">CE LTP</th>
                      <th style={{ padding: "10px" }} title="Contracts of the PE leg currently sold">PE Qty</th>
                      <th style={{ padding: "10px" }} title="Last traded price of the PE leg (avg exit price once closed)">PE LTP</th>
                      <th style={{ padding: "10px" }} title="Net delta of the whole position incl. hedge legs (wings excluded)">Net Δ</th>
                      <th style={{ padding: "10px" }} title="How far the position has drifted (delta / straddle divisor) -- the minute-end hedge fires when this exceeds Points Allowed">Points Out</th>
                      <th style={{ padding: "10px" }} title="Drift allowed before the minute-end hedge acts (from straddle / IV and the floor)">Points Allowed</th>
                      
                      
                      
                      
                      <th style={{ padding: "10px" }} title="Open P&L at last traded prices">Unrealized</th>
                      <th style={{ padding: "10px" }} title="Booked P&L -- only partial / full / MTM exits book it (hedges do not)">Realized</th>
                      <th style={{ padding: "10px" }} title="Unrealized + Realized">Total PnL</th>
                      <th style={{ padding: "10px" }} title="Total P&L per straddle unit (P&L ÷ lots × lot size)">PnL / Straddle</th>
                      <th style={{ padding: "10px" }} title="Wings are RM only: not in Realized / Total PnL">Wing PnL</th>
                      <th style={{ padding: "10px" }}>Actions</th>
                    </tr>
                  </thead>
                  <tbody>
                    <SBPortfolioRows expanded={expandedTrade()} onToggle={(u) => setExpandedTrade(expandedTrade() === u ? null : u)} />
                    <For each={portfolioItems()}>
                      {(item) => {
                        const uid = item.tradeUid || item.id;
                        const metrics = () => straddleMetrics()[uid] || {};
                        const isExpanded = () => expandedTrade() === uid;
                        const toggle = () => setExpandedTrade(isExpanded() ? null : uid);

                        // A closed trade's stored status wins: the last live snapshot
                        // was pushed while it was still active and is never updated.
                        const status = () => String(item.status || "").toUpperCase().replace(/^SBUILD_/, "").startsWith("CLOSED")
                          ? item.status
                          : (metrics().status ?? item.status ?? "ACTIVE");
                        const isClosed = () => String(status()).toUpperCase().replace(/^SBUILD_/, "").startsWith("CLOSED");
                        const isPending = () => String(status()).toUpperCase() === "PENDING";
                        // ACTIVE (partial): the build stopped part-way (e.g. margin) and
                        // the trade is tracked at what really filled.
                        const partialCfg = () => (live().config || item.config || {});
                        const isPartialFill = () => !isClosed() && Boolean(partialCfg().partial_fill);
                        const partialTitle = () => `Partial build: filled CE ${ceQty()} / PE ${peQty()} of requested CE ${partialCfg().requested_ce_qty || "?"} / PE ${partialCfg().requested_pe_qty || "?"}`;

                        // Values below are from GET /api/snapshots/:tradeUid.
                        const live = () => metrics() || {};
                        const monitorPositions = () =>
                          Array.isArray(live().live_positions) ? live().live_positions : [];
                        // No monitor running (e.g. a trade reopened by Sync with Broker):
                        // show the open legs from the DB (exchange fills) at live prices.
                        const [dbLegs, setDbLegs] = createSignal([]);
                        createEffect(() => {
                          if (!isExpanded() || isClosed() || monitorPositions().length > 0) return;
                          let stop = false;
                          const pull = async () => {
                            try {
                              const d = await (await fetch(`/api/trade/open-legs?trade_uid=${encodeURIComponent(uid)}`)).json();
                              if (!stop && d.success) setDbLegs(d.legs || []);
                            } catch (_) { /* gateway restarting */ }
                          };
                          pull();
                          const t = setInterval(pull, 3000);
                          onCleanup(() => { stop = true; clearInterval(t); });
                        });
                        const livePositions = () => {
                          if (monitorPositions().length > 0) return monitorPositions();
                          if (isClosed()) return [];
                          return dbLegs().map((l) => ({
                            option_type: l.option_type, strike: l.strike, action: l.action, quantity: Math.abs(Number(l.qty) || 0),
                            entry_price: l.entry_price, ltp: l.ltp, pnl: l.pnl, iv: 0, token: l.token, fromDb: true,
                          }));
                        };

                        const leg = (type) => livePositions().find(
                          (position) => String(position.option_type || "").toUpperCase() === type
                        );

                        const ceLtp = () =>
                          metrics().position_ltps?.[item.ceToken] ??
                          leg("CE")?.ltp ??
                          live().ce_ltp ??
                          item.ceLtp ??
                          0;

                        const peLtp = () =>
                          metrics().position_ltps?.[item.peToken] ??
                          leg("PE")?.ltp ??
                          live().pe_ltp ??
                          item.peLtp ??
                          0;

                        const positivePrice = (...values) => {
                          for (const value of values) {
                            const n = Number(value);
                            if (Number.isFinite(n) && n > 0) return n;
                          }
                          return 0;
                        };

                        const ceEntry = () =>
                          positivePrice(
                            item.ceEntry,
                            live().ce_entry_price,
                            live().ceEntry,
                            leg("CE")?.entry_price
                          );

                        const peEntry = () =>
                          positivePrice(
                            item.peEntry,
                            live().pe_entry_price,
                            live().peEntry,
                            leg("PE")?.entry_price
                          );

                        const ceQty = () => leg("CE")?.quantity ?? live().ce_quantity ?? item.ceQty ?? 0;
                        const peQty = () => leg("PE")?.quantity ?? live().pe_quantity ?? item.peQty ?? 0;
                        const netDelta = () => live().net_delta ?? item.netDelta ?? 0;
                        const netGamma = () => live().net_gamma ?? item.netGamma ?? 0;

                        // Realized = booked so far from exchange fills (partial
                        // exits included); the live monitor only values the legs
                        // still open, so for an open trade total = realized + open MTM.
                        const rPnl = () => Number(item.realizedPnl ?? live().realized_pnl ?? 0) || 0;
                        const uPnl = () => isClosed() ? 0 : (Number(live().unrealized_pnl ?? item.unrealizedPnl ?? 0) || 0);
                        const totalPnl = () => (isClosed() ? rPnl() : rPnl() + uPnl());

                        // Closed: realized / (lots x lot size) from the summary.
                        // Open: the live monitor's own pnl_per_straddle.
                        const pnlPerStraddleValue = () => {
                          if (isClosed()) return Number(item.pnlPerStraddle ?? 0) || 0;
                          const q = Number(live().straddle_quantity) || 0;
                          return q > 0 ? totalPnl() / q : (Number(live().pnl_per_straddle ?? item.pnlPerStraddle ?? 0) || 0);
                        };
                        // Wings: realized so far (e.g. sold back on a hedge or
                        // at exit) + live MTM of wings still held (open only).
                        const wingPnlValue = () =>
                          (Number(item.wingRealizedPnl) || 0) +
                          (isClosed() ? 0 : (Number(live().wing_pnl) || 0));

                        const pointsOut = () => {
                          const value = live().points_out ?? live().pts_out;
                          if (value != null && Number.isFinite(Number(value))) return Number(value);
                          const gamma = Number(netGamma()) || 0;
                          return Math.abs(gamma) > 1e-6
                            ? Math.abs(Number(netDelta()) || 0) / Math.abs(gamma)
                            : 0;
                        };

                        const pointsAllowed = () => {
                          const value = live().points_allowed ?? live().config?.points_allowed;
                          if (value != null && Number.isFinite(Number(value))) return Number(value);
                          return item.points_allowed ?? item.pointsAllowed ?? 0;
                        };

                        // Pushed live from executeBuild over the straddle_update
                        // websocket while status is BUILDING (see build_orders_submitted/
                        // build_orders_total in TradeSnapshot) -- not present once a
                        // trade leaves BUILDING, so this only renders during a build.
                        const buildTotal = () => Number(live().build_orders_total) || 0;
                        const buildSubmitted = () => Number(live().build_orders_submitted) || 0;
                        const isBuilding = () => String(status()).toUpperCase() === "BUILDING";

                        return (
                          <>
                            <tr
                              class={isExpanded() ? "details-open" : ""}
                              onClick={toggle}
                              style={{
                                cursor: "pointer",
                                transition: "background 0.2s",
                                background: isExpanded() ? "#2a2a3e" : "transparent",
                                "border-bottom": "1px solid #333"
                              }}
                            >
                              <td style={{ padding: "10px" }}>{uid.slice(-8)}</td>
                              <td style={{ padding: "10px" }}>{item.symbol}</td>
                              <td style={{ padding: "10px" }}>{item.strike || metrics().strike || "—"}</td>
                              <td style={{ padding: "10px" }}>
                                <span class={`status-badge ${(status()).toLowerCase()}`}>
                                  {status()}<Show when={isPartialFill()}><span title={partialTitle()}> · PARTIAL</span></Show>
                                </span>
                                <Show when={item.lastReject}>
                                  <div style={{ "font-size": "11px", color: "#ff6b6b", "margin-top": "3px", "font-weight": 700 }} title={item.lastReject.reason}>
                                    ⚠ {item.rejectCount > 1 ? `${item.rejectCount} orders` : "order"} REJECTED · last {rejectTime(item.lastReject)} {item.lastReject.leg} {item.lastReject.side} @{fmt(item.lastReject.limit_price, 2)}
                                  </div>
                                </Show>
                                <Show when={isBuilding() && buildTotal() > 0}>
                                  <div style={{ "font-size": "11px", opacity: 0.75, "margin-top": "2px" }}>
                                    Building {buildSubmitted()}/{buildTotal()} orders placed
                                  </div>
                                </Show>
                              </td>
                              <td style={{ padding: "10px" }}>{ceQty()}</td>
                              <td style={{ padding: "10px" }}>₹{fmt(ceLtp(), 2)}</td>
                              <td style={{ padding: "10px" }}>{peQty()}</td>
                              <td style={{ padding: "10px" }}>₹{fmt(peLtp(), 2)}</td>
                              <td style={{ padding: "10px" }}>{fmt(netDelta(), 4)}</td>
                              <td style={{ padding: "10px" }}>{fmt(pointsOut(), 2)}</td>
                              <td style={{ padding: "10px" }}>{fmt(pointsAllowed(), 2)}</td>
                              
                              
                              
                              
                              <td style={{ padding: "10px" }} class={uPnl() >= 0 ? "positive" : "negative"}>₹{fmt(uPnl(), 2)}</td>
                              <td style={{ padding: "10px" }} class={rPnl() >= 0 ? "positive" : "negative"}>₹{fmt(rPnl(), 2)}</td>
                              <td style={{ padding: "10px" }} class={totalPnl() >= 0 ? "positive" : "negative"}>₹{fmt(totalPnl(), 2)}</td>
                              <td style={{ padding: "10px" }} class={pnlPerStraddleValue() >= 0 ? "positive" : "negative"}>₹{fmt(pnlPerStraddleValue(), 2)}</td>
                              <td style={{ padding: "10px", opacity: 0.8 }} class={wingPnlValue() >= 0 ? "positive" : "negative"}>
                                <Show when={wingPnlValue() !== 0 || Number(live().wing_pct) > 0} fallback={"—"}>₹{fmt(wingPnlValue(), 2)}</Show>
                              </td>
                              <td style={{ padding: "6px 10px" }} onClick={(event) => event.stopPropagation()}>
                                <Show when={!isClosed()} fallback={"—"}>
                                  <div style={{ display: "flex", gap: "4px", "flex-wrap": "nowrap" }}>
                                    <Show when={String(status()).toUpperCase() === "BUILDING"}>
                                      <button class="dashboard-btn yellow" style={rowBtn} title="Stop Building" onClick={() => handlePortfolioStopBuild(item)}>Stop</button>
                                    </Show>
                                    <button class="dashboard-btn purple" style={rowBtn} title="Modify Config (SL / TP / MTM / exit time / hedge)" onClick={() => openModifyTradeModal(item)}>Config</button>
                                    <button class="dashboard-btn blue" style={rowBtn} title="Hedge Now" onClick={() => handlePortfolioHedge(item)}>Hedge</button>
                                    <button class="dashboard-btn yellow" style={rowBtn} title="Partial Exit" onClick={() => handlePortfolioPartialSquareOff(item)}>Partial</button>
                                    <button class="dashboard-btn red" style={rowBtn} title="Full Exit" onClick={() => handlePortfolioSquareOff(item)}>Exit</button>
                                  </div>
                                </Show>
                              </td>
                            </tr>
                            <Show when={isExpanded()}>
                              <tr class="details-row">
                                <td colSpan="18" style={{ padding: "16px", background: "#101a33" }}>
                                  <div class="trade-dashboard">

                                    <div class="trade-dashboard-header">
                                      <div>
                                        <div class="trade-dashboard-title">Trade Details</div>
                                        <div class="trade-dashboard-subtitle">{uid}</div>
                                      </div>

                                      <span class={`status-badge ${String(status()).toLowerCase()}`}>
                                        {status()}<Show when={isPartialFill()}><span title={partialTitle()}> · PARTIAL</span></Show>
                                      </span>
                                    </div>

                                    <Show when={item.lastReject}>
                                      <div style={{ margin: "10px 0", padding: "10px 12px", "border-radius": "8px", border: "1px solid #b23b3b", background: "rgba(178,59,59,0.12)" }}>
                                        <div style={{ color: "#ff6b6b", "font-weight": 700 }}>
                                          ⚠ Broker REJECTED {item.rejectCount > 1 ? `${item.rejectCount} orders of this trade — latest` : "an order of this trade"}: {rejectTime(item.lastReject)} · {item.lastReject.leg} {item.lastReject.strike ? fmt(item.lastReject.strike, 0) : ""} {item.lastReject.side} @ ₹{fmt(item.lastReject.limit_price, 2)} · {item.lastReject.phase || "—"} · broker order {item.lastReject.broker_order_id || "—"}
                                        </div>
                                        <div style={{ "margin-top": "6px", "font-family": "monospace", "font-size": "12px", "white-space": "pre-wrap", "word-break": "break-word" }}>{item.lastReject.reason}</div>
                                      </div>
                                    </Show>

                                    <div class="trade-dashboard-grid">

                                      <section class="trade-card pnl-risk-card">
                                        <div class="trade-card-title">PnL &amp; Risk</div>

                                        <div class="trade-metric-row">
                                          <span>Total PnL</span>
                                          <strong class={totalPnl() >= 0 ? "positive" : "negative"}>
                                            ₹{fmt(totalPnl(), 2)}
                                          </strong>
                                        </div>

                                        <div class="trade-metric-row">
                                          <span>Unrealized PnL</span>
                                          <strong class={uPnl() >= 0 ? "positive" : "negative"}>
                                            ₹{fmt(uPnl(), 2)}
                                          </strong>
                                        </div>

                                        <div class="trade-metric-row">
                                          <span>Realized PnL</span>
                                          <strong class={rPnl() >= 0 ? "positive" : "negative"}>
                                            ₹{fmt(rPnl(), 2)}
                                          </strong>
                                        </div>

                                        <div class="trade-metric-row">
                                          <span>Points Out</span>
                                          <strong>{fmt(pointsOut(), 2)}</strong>
                                        </div>

                                        <div class="trade-metric-row">
                                          <span>Points Allowed</span>
                                          <strong>{fmt(pointsAllowed(), 2)}</strong>
                                        </div>
                                        <div class="trade-metric-row">
                                          <span>&nbsp;&nbsp;Straddle ÷ S-Div{live().allowed_strike ? ` (${Math.round(live().allowed_strike)})` : ""}</span>
                                          <strong>{fmt(live().points_allowed_straddle ?? 0, 2)}</strong>
                                        </div>
                                        <div class="trade-metric-row">
                                          <span>&nbsp;&nbsp;Spot × IV ÷ H-Div</span>
                                          <strong>{fmt(live().points_allowed_iv ?? 0, 2)}</strong>
                                        </div>

                                        <div class="trade-metric-row">
                                          <span>PnL / Straddle</span>
                                          <strong>
                                            {(() => {
                                              const lotSize = Number(item.lotSize) || 0;
                                              const configuredLots = Number(item.lots) || 0;

                                              // Preferred: requested straddles = lots × lot size.
                                              const originalStraddleQty =
                                                configuredLots > 0 && lotSize > 0
                                                  ? configuredLots * lotSize
                                                  : 0;

                                              // Fallback for unequal legs after hedges/rolls:
                                              // count only complete live CE/PE straddle units.
                                              const liveCompleteStraddleQty = Math.min(
                                                Number(ceQty()) || 0,
                                                Number(peQty()) || 0
                                              );

                                              const straddleQty =
                                                originalStraddleQty || liveCompleteStraddleQty;

                                              return straddleQty > 0
                                                ? `₹${fmt(totalPnl() / straddleQty, 2)}`
                                                : "—";
                                            })()}
                                          </strong>
                                        </div>

                                        <div class="trade-metric-row">
                                          <span>Underlying</span>
                                          <strong>{fmt(live().underlying ?? 0, 2)}</strong>
                                        </div>
                                      </section>

                                      <section class="trade-card">
                                        <div class="trade-card-title">Net Greeks</div>

                                        <div class="trade-metric-row">
                                          <span>Net Delta</span>
                                          <strong>{fmt(netDelta(), 4)}</strong>
                                        </div>

                                        <div class="trade-metric-row">
                                          <span>Net Gamma</span>
                                          <strong>{fmt(netGamma(), 6)}</strong>
                                        </div>

                                        <div class="trade-metric-row">
                                          <span>Net Theta</span>
                                          <strong>{fmt(live().net_theta ?? item.netTheta ?? 0, 2)}</strong>
                                        </div>

                                        <div class="trade-metric-row">
                                          <span>Net Vega</span>
                                          <strong>{fmt(live().net_vega ?? item.netVega ?? 0, 2)}</strong>
                                        </div>

                                        <div class="trade-metric-row">
                                          <span>Lot Size</span>
                                          <strong>{item.lotSize || "—"}</strong>
                                        </div>

                                        <div class="trade-metric-row">
                                          <span>Absolute Delta</span>
                                          <strong>{fmt(Math.abs(Number(netDelta()) || 0), 2)}</strong>
                                        </div>
                                      </section>

                                      <section class="trade-card">
                                        <div class="trade-card-title">Monitor Status</div>

                                        <div class="monitor-row">
                                          <span>Stop Loss</span>
                                          <span class="monitor-value">
                                            <span class="monitor-dot" style={isTradeClosed(item) ? { background: "#6b7280", "box-shadow": "none" } : undefined}></span>
                                            {isTradeClosed(item) ? "Stopped" : "Running"}
                                          </span>
                                        </div>

                                        <div class="monitor-row">
                                          <span>SL Points / Lot</span>
                                          <strong>
                                            {item.config?.sl_points_per_lot ??
                                              item.config?.slPointsPerLot ??
                                              "—"}
                                          </strong>
                                        </div>

                                        <div class="monitor-row">
                                          <span>SL (bps of spot)</span>
                                          <strong>
                                            {(() => {
                                              const bps = item.config?.sl_pnl_bps_of_spot ?? item.config?.slPnlBpsOfSpot;
                                              const underlying = Number(live().underlying) || 0;
                                              if (!bps || Number(bps) <= 0) return "—";
                                              const threshold = -(underlying * Number(bps)) / 10000;
                                              return underlying > 0
                                                ? `${bps} bps (₹${fmt(threshold, 2)}/straddle)`
                                                : `${bps} bps`;
                                            })()}
                                          </strong>
                                        </div>

                                        <div class="monitor-row">
                                          <span>TP (bps of spot)</span>
                                          <strong>
                                            {(() => {
                                              const bps = item.config?.tp_pnl_bps_of_spot ?? item.config?.tpPnlBpsOfSpot;
                                              const underlying = Number(live().underlying) || 0;
                                              if (!bps || Number(bps) <= 0) return "—";
                                              const threshold = (underlying * Number(bps)) / 10000;
                                              return underlying > 0
                                                ? `${bps} bps (₹${fmt(threshold, 2)}/straddle)`
                                                : `${bps} bps`;
                                            })()}
                                          </strong>
                                        </div>

                                        <div class="monitor-row">
                                          <span>Hard Square-Off</span>
                                          <strong>
                                            {(() => {
                                              const raw = item.config?.square_off_hard_time ?? item.config?.squareOffHardTime ?? "";
                                              if (!raw) return "Not configured";
                                              const d = new Date(raw);
                                              // An unset time.Time on the Go side serializes as the
                                              // year-1 zero value, not an empty string -- guard on
                                              // year, not just NaN, or a browser's timezone table can
                                              // render it as a bogus wall-clock time (pre-1906 India
                                              // used +5:53:28, not +5:30).
                                              return Number.isNaN(d.getTime()) || d.getUTCFullYear() < 2000
                                                ? "Not configured"
                                                : d.toLocaleTimeString("en-IN", { hour: "2-digit", minute: "2-digit", second: "2-digit" });
                                            })()}
                                          </strong>
                                        </div>

                                        <div class="monitor-row">
                                          <span>Hedge</span>
                                          <span class="monitor-value">
                                            <span class="monitor-dot" style={isTradeClosed(item) ? { background: "#6b7280", "box-shadow": "none" } : undefined}></span>
                                            {isTradeClosed(item) ? "Stopped" : "Running"}
                                          </span>
                                        </div>

                                        <div class="monitor-row">
                                          <span>H-Div / S-Div</span>
                                          <strong>
                                            {item.config?.hedge_div ?? item.config?.hedgeDiv ?? 57}
                                            {" / "}
                                            {item.config?.straddle_div ?? item.config?.straddleDiv ?? 4}
                                          </strong>
                                        </div>

                                        <div class="monitor-row">
                                          <span>Delta Threshold</span>
                                          <strong>
                                            {item.config?.hedge_threshold_delta ??
                                              item.config?.hedgeThresholdDelta ??
                                              "—"}
                                          </strong>
                                        </div>

                                        

                                        <div class="monitor-row">
                                          {/* Mirrors execution-gateway's decideHedge: the floor under
                                              points_allowed is hedge_min_threshold_bps (default 8) of
                                              the live spot, not a fixed number of points. */}
                                          <span>Minimum Hedge Points</span>
                                          <strong>
                                            {(() => {
                                              const bps = Number(item.config?.hedge_min_threshold_bps ?? 8);
                                              const spot = Number(live().underlying) || 0;
                                              return spot > 0 && bps > 0
                                                ? `${fmt((spot * bps) / 10000, 2)} (${bps} bps)`
                                                : "—";
                                            })()}
                                          </strong>
                                        </div>

                                        <div class="monitor-row">
                                          <span>Lot Requirement</span>
                                          <strong>Δ {item.lotSize || 1}</strong>
                                        </div>

                                        <div class="monitor-row">
                                          <span>Risk Action</span>
                                          <strong>
                                            {(() => {
                                              const pointsOutValue = Number(pointsOut()) || 0;
                                              const pointsAllowedValue = Number(pointsAllowed()) || 0;
                                              const absDelta = Math.abs(Number(netDelta()) || 0);
                                              const lotSize = Number(item.lotSize) || 1;
                                              const bps = Number(item.config?.hedge_min_threshold_bps ?? 8);
                                              const spot = Number(live().underlying) || 0;
                                              const floor = spot > 0 && bps > 0 ? (spot * bps) / 10000 : 0;
                                              const effectiveAllowed = Math.max(pointsAllowedValue, floor);

                                              if (pointsAllowedValue <= 0 || pointsOutValue <= pointsAllowedValue) return "OK";
                                              if (pointsOutValue <= effectiveAllowed) return "BELOW MIN THRESHOLD";
                                              if (absDelta < lotSize) return "BELOW ONE LOT";
                                              return `HEDGE ELIGIBLE (${Math.floor(absDelta / lotSize)} lot)`;
                                            })()}
                                          </strong>
                                        </div>
                                      </section>

                                      <section class="trade-card recent-events-card">
                                        <div class="trade-card-title">Modifications</div>
                                        <Show
                                          when={(item.config?.modifications || []).filter((m) => m.field !== "Auto risk execution").length > 0}
                                          fallback={<div class="trade-events-empty">No changes made to this trade</div>}
                                        >
                                          <For each={[...(item.config?.modifications || []).filter((m) => m.field !== "Auto risk execution")].reverse()}>
                                            {(m) => (
                                              <div class="monitor-row">
                                                <span>{m.time} · {m.field}</span>
                                                <strong>{m.from} → {m.to}</strong>
                                              </div>
                                            )}
                                          </For>
                                        </Show>
                                      </section>

                                      <section class="trade-card manual-actions-card">
                                        <div class="trade-card-title">Broker check</div>
                                        <div class="manual-actions">
                                          <button
                                            class="dashboard-btn cyan"
                                            onClick={(event) => {
                                              event.stopPropagation();
                                              handlePortfolioBrokerSync(item);
                                            }}
                                          >
                                            Sync with Broker
                                          </button>
                                          <span style={{ "font-size": "12px", opacity: "0.65", "align-self": "center" }}>
                                            Matches this trade's orders with the broker order book (broker order id + our order id). Preview first; never sends an order.
                                          </span>
                                        </div>
                                      </section>

                                      <Show when={!isClosed()}>
                                      <section class="trade-card manual-actions-card">
                                        <div class="trade-card-title">Manual Actions</div>

                                        <div class="manual-actions">
                                          <Show when={String(status()).toUpperCase() === "BUILDING"}>
                                            <button
                                              class="dashboard-btn yellow"
                                              onClick={(event) => {
                                                event.stopPropagation();
                                                handlePortfolioStopBuild(item);
                                              }}
                                            >
                                              Stop Building
                                            </button>
                                          </Show>
                                          <button
                                            class="dashboard-btn blue"
                                            onClick={(event) => {
                                              event.stopPropagation();
                                              handlePortfolioHedge(item);
                                            }}
                                          >
                                            Hedge Now
                                          </button>

                                          <button
                                            class="dashboard-btn purple"
                                            onClick={(event) => {
                                              event.stopPropagation();
                                              openModifyTradeModal(item);
                                            }}
                                          >
                                            Modify Config
                                          </button>


                                          <button
                                            class="dashboard-btn yellow"
                                            onClick={(event) => {
                                              event.stopPropagation();
                                              handlePortfolioPartialSquareOff(item);
                                            }}
                                          >
                                            Partial Exit
                                          </button>

                                          <button
                                            class="dashboard-btn red"
                                            onClick={(event) => {
                                              event.stopPropagation();
                                              handlePortfolioSquareOff(item);
                                            }}
                                          >
                                            Full Exit
                                          </button>
                                        </div>
                                      </section>
                                      <ManualLegPanel item={item} onDone={() => refreshPortfolio()} />
                                      </Show>

                                      <Show when={(item.config?.hedge_events || []).length > 0}>
                                        <section class="trade-card position-details-card">
                                          <div class="trade-card-title">Hedges — {item.config?.hedge_count || 0} filled · {(item.config?.hedge_events || []).length} attempt(s)</div>
                                          <div class="position-table-wrap">
                                            <table class="trade-position-table">
                                              <thead><tr><th>Time</th><th>Trigger</th><th>Result</th><th>Lots / qty</th><th>CE</th><th>PE</th><th>Strike</th><th title="Net delta of the position before this hedge">Δ before</th><th>Tranches</th><th>Reason / error</th></tr></thead>
                                              <tbody>
                                                <For each={[...(item.config?.hedge_events || [])].reverse()}>{(h) => (
                                                  <tr>
                                                    <td>{h.time}</td>
                                                    <td>{h.trigger}</td>
                                                    <td class={h.result === 'FILLED' ? 'positive' : h.result === 'PARTIAL' ? 'yellow' : 'negative'}>{h.result}</td>
                                                    <td>{h.lots} / {h.qty}</td>
                                                    <td>{h.ce_side} {h.ce_filled}</td>
                                                    <td>{h.pe_side} {h.pe_filled}</td>
                                                    <td>{h.strike ? fmt(h.strike, 0) : '—'}</td>
                                                    <td>{fmt(h.delta_before, 2)}</td>
                                                    <td>{h.tranches}</td>
                                                    <td style={{ "white-space": "normal", "max-width": "380px", opacity: 0.8 }}>{h.error || h.reason || ''}</td>
                                                  </tr>
                                                )}</For>
                                              </tbody>
                                            </table>
                                          </div>
                                        </section>
                                      </Show>
                                      <section class="trade-card position-details-card">
                                        <div class="trade-card-title">Trade Parameters (current values{(item.config?.modifications || []).filter((m) => m.field !== "Auto risk execution").length ? ", after modifications" : ""})</div>
                                        <div style={{ display: "grid", "grid-template-columns": "repeat(auto-fit, minmax(210px, 1fr))", gap: "4px 18px" }}>
                                          <div class="monitor-row"><span>Expiry / Strike</span><strong>{item.expiry || "—"} / {item.strike || "—"}</strong></div>
                                          <div class="monitor-row"><span>Lots × lot size</span><strong>{item.lots || "—"} × {item.lotSize || "—"}</strong></div>
                                          <div class="monitor-row"><span>Broker / account</span><strong>{item.brokerName} / {item.accountId}</strong></div>
                                          <div class="monitor-row"><span>SL (bps of spot)</span><strong>{item.config?.sl_pnl_bps_of_spot || "off"}</strong></div>
                                          <div class="monitor-row"><span>TP (bps of spot)</span><strong>{item.config?.tp_pnl_bps_of_spot || "off"}</strong></div>
                                          <div class="monitor-row"><span>MTM exit above</span><strong>{item.config?.mtm_exit_level != null ? `${item.config.mtm_exit_level} ${({ rs: "₹", pts: "pts/straddle", bps: "bps" })[item.config.mtm_exit_unit || "rs"]}` : "∞"}{item.config?.mtm_exit_level != null ? ` · ${item.config?.mtm_exit_pct || 100}%` : ""}</strong></div>
                                          <div class="monitor-row"><span>ATM straddle exit below</span><strong>{item.config?.straddle_exit_below != null ? `${item.config.straddle_exit_below} · ${item.config?.straddle_exit_pct || 100}%` : "off"}</strong></div>
                                          <div class="monitor-row"><span>Exit time</span><strong>{(() => {
                                            const d = new Date(item.config?.square_off_hard_time || "");
                                            return Number.isNaN(d.getTime()) || d.getUTCFullYear() < 2000 ? "not set" : d.toLocaleTimeString("en-IN", { hour: "2-digit", minute: "2-digit", second: "2-digit" });
                                          })()}</strong></div>
                                          <div class="monitor-row"><span>Straddle div / Hedge div</span><strong>{item.config?.straddle_div || 4} / {item.config?.hedge_div || 57}</strong></div>
                                          <div class="monitor-row"><span>Hedge min (bps of spot)</span><strong>{item.config?.hedge_min_threshold_bps ?? "8 (default)"}</strong></div>
                                          <div class="monitor-row"><span>Wings</span><strong>{Number(item.config?.wing_pct) > 0 ? `${item.config.wing_pct}%` : "off"}</strong></div>
                                          <div class="monitor-row"><span>Buy / sell buffer</span><strong>{item.config?.buy_buffer ?? "—"} / {item.config?.sell_buffer ?? "—"}</strong></div>
                                          <div class="monitor-row"><span>Order lots per call</span><strong>{item.config?.order_lots_per_call || "auto"}</strong></div>
                                          <div class="monitor-row"><span>Partial build</span><strong>{item.config?.partial_fill ? `yes (requested CE ${item.config.requested_ce_qty} / PE ${item.config.requested_pe_qty})` : "no"}</strong></div>
                                        </div>
                                      </section>

                                      <section class="trade-card position-details-card">
                                        <div class="trade-card-title">Position Details</div>
                                        <Show when={livePositions()[0]?.fromDb}>
                                          <div class="metric-sub">From exchange fills (no monitor running on this trade) &middot; live LTP / PnL</div>
                                        </Show>
                                        <Show when={Number(live().wing_pct) > 0}>
                                          <div class="metric-sub">
                                            Wings {live().wing_pct}% &middot; RM only, not in delta / PnL / SL / TP
                                            {" "}&middot; wing PnL ₹{fmt(live().wing_pnl ?? 0, 2)}
                                            {" "}&middot;{" "}
                                            <span class={(Number(live().wing_net_ce) === 0 && Number(live().wing_net_pe) === 0) ? "positive" : "negative"}>
                                              net CE {live().wing_net_ce ?? 0} / PE {live().wing_net_pe ?? 0}
                                              {(Number(live().wing_net_ce) === 0 && Number(live().wing_net_pe) === 0) ? " (balanced)" : " (NOT balanced)"}
                                            </span>
                                          </div>
                                        </Show>

                                        <div class="position-table-wrap">
                                          <table class="trade-position-table">
                                            <thead>
                                              <tr>
                                                <th>Leg</th>
                                                <th>Strike</th>
                                                <th>Action</th>
                                                <th>Qty</th>
                                                <th>Entry</th>
                                                <th>LTP</th>
                                                <th>PnL</th>
                                                <th>IV</th>
                                                <th></th>
                                              </tr>
                                            </thead>

                                            <tbody>
                                              <For each={livePositions()}>
                                                {(position) => (
                                                  <tr style={position.wing ? { opacity: 0.6 } : undefined}>
                                                    <td>
                                                      {position.option_type || "—"}{position.wing ? " WING (RM)" : ""}
                                                      <Show when={position.expiry && position.expiry !== item.expiry}><span style={{ "font-size": "11px", opacity: 0.8 }}> {position.expiry}</span></Show>
                                                      <Show when={position.excluded}><span title="kept out of this trade's P&L / greeks" style={{ "font-size": "10px", "margin-left": "4px", padding: "1px 5px", "border-radius": "8px", background: "rgba(255,255,255,0.12)" }}>not in P&amp;L</span></Show>
                                                    </td>
                                                    <td>{position.strike || "—"}</td>
                                                    <td class={String(position.action || "").toUpperCase() === "BUY" ? "positive" : "negative"}>
                                                      {position.action || "—"}
                                                    </td>
                                                    <td>{position.quantity ?? "—"}</td>
                                                    <td>₹{fmt(
                                                      // Straddle legs keep the reconciled trade entry; any other
                                                      // leg (e.g. an ATM hedge) shows its own fill price.
                                                      position.fromDb ? (position.entry_price ?? 0)
                                                        : Number(position.token) === Number(item.ceToken) ? ceEntry()
                                                        : Number(position.token) === Number(item.peToken) ? peEntry()
                                                        : (position.entry_price ?? 0), 2)}</td>
                                                    <td>₹{fmt(position.ltp ?? 0, 2)}</td>
                                                    <td class={Number(position.pnl || 0) >= 0 ? "positive" : "negative"}>
                                                      ₹{fmt(position.pnl ?? 0, 2)}
                                                    </td>
                                                    <td>{fmt(position.iv ?? 0, 2)}</td>
                                                    <td>
                                                      <Show when={!isClosed() && !position.wing && Number(position.quantity) > 0 && position.token}>
                                                        <button class="dashboard-btn red" style={{ padding: "2px 8px", "font-size": "11px" }}
                                                          onClick={(event) => { event.stopPropagation(); closeManualLeg(uid, position, item.lotSize || 65, () => refreshPortfolio()); }}>
                                                          Close
                                                        </button>
                                                      </Show>
                                                    </td>
                                                  </tr>
                                                )}
                                              </For>
                                            </tbody>
                                          </table>
                                        </div>
                                      </section>

                                      <Show when={(item.executions || []).length > 0}>
                                        <section class="trade-card position-details-card">
                                          <div class="trade-card-title">
                                            Executions
                                            <Show when={item.closeReason}>
                                              {" "}&mdash; {item.closeReason}
                                              <Show when={item.closedAt}> at {new Date(item.closedAt).toLocaleTimeString()}</Show>
                                            </Show>
                                          </div>
                                          <div class="position-table-wrap">
                                            <table class="trade-position-table">
                                              <thead>
                                                <tr>
                                                  <th>Time</th>
                                                  <th>Type</th>
                                                  <th>Leg</th>
                                                  <th>Side</th>
                                                  <th>Qty</th>
                                                  <th>Price</th>
                                                  <th>Status</th>
                                                  <th>Order</th>
                                                </tr>
                                              </thead>
                                              <tbody>
                                                <For each={item.executions}>
                                                  {(e) => (
                                                    <tr>
                                                      <td>{new Date(e.time).toLocaleTimeString()}</td>
                                                      <td>{e.kind}</td>
                                                      <td>{e.leg || "—"}{e.strike ? ` ${e.strike}` : ""}</td>
                                                      <td class={String(e.side).toUpperCase() === "BUY" ? "positive" : "negative"}>{e.side}</td>
                                                      <td>{e.filled_qty}{e.filled_qty !== e.quantity ? ` / ${e.quantity}` : ""}</td>
                                                      <td>{e.avg_price > 0 ? `₹${fmt(e.avg_price, 2)}` : "—"}</td>
                                                      <td>{e.status}</td>
                                                      <td>{e.broker_order_id || "—"}</td>
                                                    </tr>
                                                  )}
                                                </For>
                                              </tbody>
                                            </table>
                                          </div>
                                          <Show when={item.ceLeg || item.peLeg}>
                                            <div class="trade-metric-row">
                                              <span>CE sold / bought avg</span>
                                              <strong>
                                                {item.ceLeg ? `₹${fmt(item.ceLeg.sold_avg, 2)} / ₹${fmt(item.ceLeg.bought_avg, 2)} → ₹${fmt(item.ceLeg.realized_pnl, 2)}` : "—"}
                                              </strong>
                                            </div>
                                            <div class="trade-metric-row">
                                              <span>PE sold / bought avg</span>
                                              <strong>
                                                {item.peLeg ? `₹${fmt(item.peLeg.sold_avg, 2)} / ₹${fmt(item.peLeg.bought_avg, 2)} → ₹${fmt(item.peLeg.realized_pnl, 2)}` : "—"}
                                              </strong>
                                            </div>
                                          </Show>
                                          <div class="trade-metric-row">
                                            <span>Realized PnL (gross)</span>
                                            <strong class={Number(item.realizedPnl) >= 0 ? "positive" : "negative"}>
                                              ₹{fmt(item.realizedPnl, 2)}
                                            </strong>
                                          </div>
                                        </section>
                                      </Show>



                                    </div>
                                  </div>
                                </td>
                              </tr>
                            </Show>
                          </>
                        );
                      }}
                    </For>
                  </tbody>
                </table>
              </div>
            </Show>

            <div class="positions-header">
              <div class="positions-title">Broker Positions</div>
              <button
                class="btn"
                style={{ "margin-left": "auto" }}
                onClick={fetchBrokerPositions}
                disabled={positionsLoading()}
              >
                {positionsLoading() ? "Refreshing…" : "Refresh Positions"}
              </button>
            </div>

            <Show
              when={positionsError() === "" && positions().length > 0}
              fallback={
                positionsLoading() ? (
                  <div class="empty-state">🔄 Fetching positions…</div>
                ) : positionsError() ? (
                  <div class="empty-state" style={{ color: "#ef4444" }}>
                    ❌ {positionsError()}
                  </div>
                ) : (
                  <div class="empty-state">No broker positions found</div>
                )
              }
            >
              <div id="positions-display" class="positions-table-wrapper">
                <table class="positions-table">
                  <thead>
                    <tr>
                      <th>Symbol</th>
                      <th>Token</th>
                      <th>Side</th>
                      <th>Net Qty</th>
                      <th>Product</th>
                      <th>Day net amount</th>
                    </tr>
                  </thead>
                  <tbody>
                    <For each={positions()}>
                      {(pos) => {
                        // Broker's own net position (GreekSoft NPRequest).
                        const q = Number(pos.net_qty) || 0;
                        return (
                          <tr>
                            <td>{pos.symbol}</td>
                            <td>{pos.token}</td>
                            <td class={q < 0 ? "negative" : "positive"}>{q < 0 ? "SHORT" : "LONG"}</td>
                            <td>{q}</td>
                            <td>{pos.product_type || "—"}</td>
                            <td>₹{(Number(pos.day_net_amount) || 0).toFixed(2)}</td>
                          </tr>
                        );
                      }}
                    </For>
                  </tbody>
                </table>
              </div>
            </Show>
          </section>
        </Show>

        <Show when={activeTab() === 'automation'}>
          <section class="tab-panel">
            <div class="panel-header">
              <div class="panel-title">Automation</div>
              <div class="panel-subtitle">Config-driven build UI mapped to current backend</div>
            </div>

            <div class="automation-grid">
              <div class="section-title">Entry</div>

              <div class="control-block">
                <label class="control-label">Symbol</label>
                <select
                  class="symbol-select"
                  value={automationConfig().symbol}
                  onChange={handleSymbolChange}
                >
                  <For each={availableSymbols}>
                    {(sym) => <option value={sym}>{sym}</option>}
                  </For>
                </select>
              </div>

              <div class="control-block">
                <label class="control-label">Expiry (required)</label>
                <select
                  class="symbol-select"
                  value={automationExpiry()}
                  onChange={(e) => setAutomationExpiry(e.target.value)}
                  style={automationExpiry() ? undefined : { "border-color": "#f87171" }}
                >
                  <option value="">— choose expiry —</option>
                  <For each={(optionChain().available_expiries || []).length ? optionChain().available_expiries : [optionChain().expiry].filter(Boolean)}>
                    {(exp) => <option value={exp}>{exp}</option>}
                  </For>
                </select>
              </div>

              <div class="control-block">
                <label class="control-label">Broker</label>
                <select
                  class="symbol-select"
                  value={executionPrefs().broker_name}
                  onChange={(e) =>
                    setExecutionPrefs((prev) => ({
                      ...prev,
                      broker_name: e.target.value
                    }))
                  }
                >
                  <For each={brokerOptions}>
                    {(b) => <option value={b}>{b.toUpperCase()}</option>}
                  </For>
                </select>
              </div>

              <div class="control-block">
                <label class="control-label">Account</label>
                <select
                  class="symbol-select"
                  value={executionPrefs().account_id}
                  onChange={(e) =>
                    setExecutionPrefs((prev) => ({
                      ...prev,
                      account_id: e.target.value
                    }))
                  }
                >
                  <For each={greeksoftAccounts}>
                    {(acc) => <option value={acc}>{acc}</option>}
                  </For>
                </select>
              </div>

              <div class="control-block">
                <label class="control-label">Total quantity to sell</label>
                <input
                  class="symbol-select"
                  type="text" inputmode="decimal"
                  min={lotSize()}
                  step={lotSize()}
                  value={automationSizeText()}
                  onInput={(e) => setAutomationSizeText(e.target.value)}
                  onBlur={(e) => {
                    const ls = lotSize();
                    const rawQty = Number(e.target.value) || ls;
                    const lots = Math.max(1, Math.round(rawQty / ls));
                    setAutomationConfig((prev) => ({ ...prev, size: lots }));
                    setAutomationSizeText(String(lots * ls));
                  }}
                />
                <div class="field-note">
                  Rounded to the nearest {lotSize()} (lot size) &middot; {automationConfig().size} lot{automationConfig().size === 1 ? '' : 's'}
                </div>
              </div>

              <div class="control-block">
                <label class="control-label">Entry Time</label>
                <input
                  class="symbol-select"
                  type="text"
                  value={automationConfig().entry_time}
                  onInput={(e) => setAutomationConfig((prev) => ({ ...prev, entry_time: e.target.value }))}
                />
              </div>
              <div class="field-note">
                A past entry time fires immediately instead of being skipped (e.g. "09:21:00" requested at "09:21:30" starts right away).
              </div>

              <div class="section-title">Risk &amp; Exit — real, verified exits</div>

              <div class="control-block">
                <label class="control-label">Exit Time</label>
                <input
                  class="symbol-select"
                  type="text"
                  value={automationConfig().exit_time}
                  onInput={(e) => setAutomationConfig((prev) => ({ ...prev, exit_time: e.target.value }))}
                />
              </div>

              <div class="control-block">
                <label class="control-label">SL (bps of spot)</label>
                <input
                  class="symbol-select"
                  type="text" inputmode="decimal"
                  value={automationConfig().sl_bps}
                  onInput={(e) => setAutomationConfig((prev) => ({ ...prev, sl_bps: Number(e.target.value) || 0 }))}
                />
              </div>

              <div class="control-block">
                <label class="control-label">TP (bps of spot)</label>
                <input
                  class="symbol-select"
                  type="text" inputmode="decimal"
                  value={automationConfig().tp_bps}
                  onInput={(e) => setAutomationConfig((prev) => ({ ...prev, tp_bps: Number(e.target.value) || 0 }))}
                />
              </div>

              <div class="control-block">
                <label class="control-label">Wings % (0 = off)</label>
                <input
                  class="symbol-select"
                  type="text" inputmode="decimal"
                  step="0.5"
                  min="0"
                  value={automationConfig().wing_pct}
                  onInput={(e) => setAutomationConfig((prev) => ({ ...prev, wing_pct: Number(e.target.value) || 0 }))}
                />
              </div>

              <div class="section-title">Hedge &amp; Sizing</div>

              <div class="control-block">
                <label class="control-label">Hedge Divisor</label>
                <input
                  class="symbol-select"
                  type="text" inputmode="decimal"
                  value={automationConfig().hedge_div}
                  onInput={(e) => setAutomationConfig((prev) => ({ ...prev, hedge_div: Number(e.target.value) || 0 }))}
                />
              </div>

              <div class="control-block">
                <label class="control-label">Straddle Divisor</label>
                <input
                  class="symbol-select"
                  type="text" inputmode="decimal"
                  value={automationConfig().straddle_div}
                  onInput={(e) => setAutomationConfig((prev) => ({ ...prev, straddle_div: Number(e.target.value) || 0 }))}
                />
              </div>

              <div class="control-block">
                <label class="control-label">Buy Buffer</label>
                <input
                  class="symbol-select"
                  type="text" inputmode="decimal"
                  value={automationConfig().buy_buffer}
                  onInput={(e) => setAutomationConfig((prev) => ({ ...prev, buy_buffer: Number(e.target.value) || 0 }))}
                />
              </div>

              <div class="control-block">
                <label class="control-label">Sell Buffer</label>
                <input
                  class="symbol-select"
                  type="text" inputmode="decimal"
                  value={automationConfig().sell_buffer}
                  onInput={(e) => setAutomationConfig((prev) => ({ ...prev, sell_buffer: Number(e.target.value) || 0 }))}
                />
              </div>

              <div class="control-block">
                <label class="control-label">Lots per order</label>
                <input
                  class="symbol-select"
                  type="text" inputmode="decimal"
                  value={automationConfig().order_lots_per_call}
                  onInput={(e) => setAutomationConfig((prev) => ({ ...prev, order_lots_per_call: Number(e.target.value) || 1 }))}
                />
              </div>
            </div>

            <details class="advanced-details">
              <summary>Accepted but not implemented yet (no effect on the build) — idv, roll, and the *_start_time / *_monitor_interval fields</summary>
              <div class="automation-grid">
                <div class="control-block">
                  <label class="control-label">Roll Straddle Divisor</label>
                  <input
                    class="symbol-select"
                    type="text" inputmode="decimal"
                    value={automationConfig().roll_straddle_div}
                    onInput={(e) => setAutomationConfig((prev) => ({ ...prev, roll_straddle_div: Number(e.target.value) || 0 }))}
                  />
                </div>

                <div class="control-block">
                  <label class="control-label">IDV</label>
                  <input
                    class="symbol-select"
                    type="text" inputmode="decimal"
                    step="0.1"
                    value={automationConfig().idv}
                    onInput={(e) => setAutomationConfig((prev) => ({ ...prev, idv: Number(e.target.value) || 0 }))}
                  />
                </div>

                <div class="control-block">
                  <label class="control-label">IDV Divisor</label>
                  <input
                    class="symbol-select"
                    type="text" inputmode="decimal"
                    step="0.1"
                    value={automationConfig().idv_divisor}
                    onInput={(e) => setAutomationConfig((prev) => ({ ...prev, idv_divisor: Number(e.target.value) || 0 }))}
                  />
                </div>

                <div class="control-block">
                  <label class="control-label">Straddle Price Filter</label>
                  <input
                    class="symbol-select"
                    type="text" inputmode="decimal"
                    value={automationConfig().straddle_filter}
                    onInput={(e) => setAutomationConfig((prev) => ({ ...prev, straddle_filter: Number(e.target.value) || 0 }))}
                  />
                </div>

                <div class="control-block">
                  <label class="control-label">SL Interval (s)</label>
                  <input
                    class="symbol-select"
                    type="text" inputmode="decimal"
                    value={automationConfig().sl_monitor_interval}
                    onInput={(e) => setAutomationConfig((prev) => ({ ...prev, sl_monitor_interval: Number(e.target.value) || 60 }))}
                  />
                </div>

                <div class="control-block">
                  <label class="control-label">Hedge Interval (s)</label>
                  <input
                    class="symbol-select"
                    type="text" inputmode="decimal"
                    value={automationConfig().hedge_monitor_interval}
                    onInput={(e) => setAutomationConfig((prev) => ({ ...prev, hedge_monitor_interval: Number(e.target.value) || 60 }))}
                  />
                </div>

                <div class="control-block">
                  <label class="control-label">Roll Interval (s)</label>
                  <input
                    class="symbol-select"
                    type="text" inputmode="decimal"
                    value={automationConfig().roll_monitor_interval}
                    onInput={(e) => setAutomationConfig((prev) => ({ ...prev, roll_monitor_interval: Number(e.target.value) || 60 }))}
                  />
                </div>

                <div class="control-block">
                  <label class="control-label">SL Start Time</label>
                  <input
                    class="symbol-select"
                    type="text"
                    placeholder="HH:MM:SS"
                    value={automationConfig().sl_start_time}
                    onInput={(e) => setAutomationConfig((prev) => ({ ...prev, sl_start_time: e.target.value }))}
                  />
                </div>

                <div class="control-block">
                  <label class="control-label">Hedge Start Time</label>
                  <input
                    class="symbol-select"
                    type="text"
                    placeholder="HH:MM:SS"
                    value={automationConfig().hedge_start_time}
                    onInput={(e) => setAutomationConfig((prev) => ({ ...prev, hedge_start_time: e.target.value }))}
                  />
                </div>

                <div class="control-block">
                  <label class="control-label">Roll Start Time</label>
                  <input
                    class="symbol-select"
                    type="text"
                    placeholder="HH:MM:SS"
                    value={automationConfig().roll_start_time}
                    onInput={(e) => setAutomationConfig((prev) => ({ ...prev, roll_start_time: e.target.value }))}
                  />
                </div>
              </div>
            </details>

            <div class="button-row" style={{ gap: '10px', 'flex-wrap': 'wrap' }}>
              <button
                class="sell-btn"
                onClick={handleAutomationBuild}
                disabled={manualHedgeBusy() || !automationExpiry()}
                title={automationExpiry() ? "" : "Choose the expiry first"}
              >
                START AUTOMATED BUILD
              </button>
            </div>

            <div class="panel-header" style={{ 'margin-top': '18px' }}>
              <div class="panel-title">Scheduled builds</div>
              <div class="panel-subtitle">
                Pending entries (kept in memory: a gateway restart drops them)
              </div>
            </div>
            <div class="log-container">
              <For each={scheduledBuilds()}>
                {(job) => (
                  <div class="log-line log-info">
                    <span class="log-ts">{new Date(job.run_at).toLocaleTimeString()}</span>
                    <span class="log-lvl">{job.symbol} {job.expiry} x{job.lots}</span>
                    <span class="log-msg">
                      {job.broker_name} {job.account_id} &middot; exit {job.exit_time || 'none'} &middot; SL {job.sl_bps || 0} bps &middot; TP {job.tp_bps || 0} bps
                      {' '}
                      <button class="action-btn" onClick={() => cancelScheduledBuild(job.job_id)}>
                        Cancel
                      </button>
                    </span>
                  </div>
                )}
              </For>
              <Show when={scheduledBuilds().length === 0}>
                <div class="empty-state">No pending scheduled builds.</div>
              </Show>
            </div>
          </section>
        </Show>

        <Show when={activeTab() === 'testing'}>
          <section class="tab-panel">
            <div class="panel-header">
              <div class="panel-title">Testing</div>
              <div class="panel-subtitle">Manual hedge preview and execution tools</div>
            </div>

            <div class="testing-card">
              <div class="testing-card-header">
                <div>
                  <div class="testing-card-title">Manual hedge inputs</div>
                  <div class="testing-card-subtitle">Preview computes intents only; execute sends real orders</div>
                </div>
              </div>

              <div class="testing-field-grid">
                <div class="testing-field">
                  <label>Net Delta</label>
                  <input
                    type="text" inputmode="decimal"
                    step="0.01"
                    value={manualHedgeConfig().net_delta}
                    onInput={(e) => setManualHedgeConfig((prev) => ({ ...prev, net_delta: Number(e.target.value) || 0 }))}
                  />
                </div>

                <div class="testing-field">
                  <label>Lot Size</label>
                  <input
                    type="text" inputmode="decimal"
                    value={manualHedgeConfig().lot_size}
                    onInput={(e) => setManualHedgeConfig((prev) => ({ ...prev, lot_size: Number(e.target.value) || 0 }))}
                  />
                </div>

                <div class="testing-field">
                  <label>Qty to Hedge/Sell</label>
                  <input
                    type="text" inputmode="decimal"
                    value={manualHedgeConfig().quantity}
                    onInput={(e) => setManualHedgeConfig((prev) => ({ ...prev, quantity: Number(e.target.value) || 0 }))}
                  />
                </div>
              </div>

              <div class="testing-actions">
                <button class="dashboard-btn cyan" onClick={handleManualHedgePreview} disabled={manualHedgeBusy()}>
                  {manualHedgeBusy() ? 'WORKING...' : 'PREVIEW HEDGE'}
                </button>
                <button class="dashboard-btn purple" onClick={handleManualHedgeExecute} disabled={manualHedgeBusy()}>
                  EXECUTE HEDGE
                </button>
              </div>

              <Show when={manualHedgeError()}>
                <div class="testing-status-msg err">{manualHedgeError()}</div>
              </Show>
            </div>

            <div class="testing-card">
              <div class="testing-card-header">
                <div>
                  <div class="testing-card-title">Direct manual order</div>
                  <div class="testing-card-subtitle">Sells the live ATM leg directly (bypasses hedge preview above)</div>
                </div>
              </div>

              <div class="testing-field-grid">
                <div class="testing-field">
                  <label>Total lots to sell</label>
                  <input
                    type="text" inputmode="decimal"
                    min="1"
                    step="1"
                    value={manualTotalLots()}
                    onInput={(e) => setManualTotalLots(Math.max(1, Math.floor(toNum(e.currentTarget.value))))}
                  />
                </div>

                <div class="testing-field">
                  <label>Lots in each order</label>
                  <input
                    type="text" inputmode="decimal"
                    min="1"
                    step="1"
                    value={manualLotsPerOrder()}
                    onInput={(e) => setManualLotsPerOrder(Math.max(1, Math.floor(toNum(e.currentTarget.value))))}
                  />
                </div>

                <div class="testing-field">
                  <label>Execution type</label>
                  <select
                    value={manualOrderType()}
                    onChange={(e) => setManualOrderType(e.currentTarget.value)}
                  >
                    <option value="1">LIMIT at live LTP</option>
                    <option value="2">MARKET</option>
                  </select>
                </div>

                <div class="testing-summary">
                  Order calculation
                  <strong>{`${manualTotalLots()} × ${(lotSize())} = ${manualTotalLots() * (lotSize())} qty`}</strong>
                  <div class="sub">{`${Math.ceil(manualTotalLots() / manualLotsPerOrder())} order clip(s) • ${manualLotsPerOrder()} lot(s)/clip`}</div>
                </div>
              </div>

              <div class="leg-toggle-row">
                <span class="label">Live ATM leg</span>
                <button
                  type="button"
                  class={`leg-pill ce ${directOrderLeg() === 'CE' ? 'active' : ''}`}
                  onClick={() => setDirectOrderLeg('CE')}
                  disabled={directOrderBusy()}
                >
                  CE
                </button>
                <button
                  type="button"
                  class={`leg-pill pe ${directOrderLeg() === 'PE' ? 'active' : ''}`}
                  onClick={() => setDirectOrderLeg('PE')}
                  disabled={directOrderBusy()}
                >
                  PE
                </button>
                <span class="leg-meta">
                  {atmRow()
                    ? `${selectedSymbol()} ${selectedExpiry() || optionChain().expiry} ${directOrderLeg()} ${Math.round(atmRow().strike)} | token=${directOrderLeg() === 'CE' ? atmRow().ce_token : atmRow().pe_token} | LTP=${fmt(directOrderLeg() === 'CE' ? atmRow().ce_ltp : atmRow().pe_ltp)} | lot=${manualTotalLots()} | qty=${manualTotalLots() * (lotSize())}`
                    : 'Waiting for live ATM option data…'}
                </span>
              </div>

              <button
                class="big-action-btn"
                style={{ background: 'var(--red)' }}
                onClick={handleDirectTwoLotSingleOrder}
                disabled={manualHedgeBusy() || directOrderBusy()}
              >
                {directOrderBusy()
                  ? `SUBMITTING ${manualTotalLots() * (lotSize())}...`
                  : `SELL LIVE ATM ${directOrderLeg()} — ${manualTotalLots()} LOT${manualTotalLots() === 1 ? '' : 'S'} / ${manualTotalLots() * (lotSize())} QTY / ${Math.ceil(manualTotalLots() / Math.max(1, Math.floor(toNum(manualLotsPerOrder()))))} ORDER${Math.ceil(manualTotalLots() / Math.max(1, Math.floor(toNum(manualLotsPerOrder())))) === 1 ? '' : 'S'}`}
              </button>

              <Show when={directOrderStatus()}>
                <div class={`testing-status-msg mono ${directOrderStatus().startsWith('Failed:') ? 'err' : 'ok'}`}>
                  {directOrderStatus()}
                </div>
              </Show>

              <Show when={directOrderResult()}>
                <pre
                  style={{
                    'margin-top': '10px',
                    padding: '12px',
                    overflow: 'auto',
                    'border-radius': '8px',
                    border: '1px solid var(--border)',
                    background: '#09111f',
                    color: '#b9d7ff',
                    'font-size': '12px',
                    'text-align': 'left'
                  }}
                >
                  {JSON.stringify(directOrderResult(), null, 2)}
                </pre>
              </Show>
            </div>

            <div class="testing-card">
              <div class="testing-card-header">
                <div>
                  <div class="testing-card-title">Modify order</div>
                  <div class="testing-card-subtitle">Re-price a resting order this account already placed (e.g. one from Direct manual order above)</div>
                </div>
              </div>

              <div class="testing-modify-row">
                <div class="testing-field">
                  <label>Broker order id</label>
                  <input
                    type="text"
                    placeholder="e.g. 120000037"
                    value={modifyOrderId()}
                    onInput={(e) => setModifyOrderId(e.currentTarget.value)}
                  />
                </div>
                <div class="testing-field">
                  <label>New price</label>
                  <input
                    type="text" inputmode="decimal"
                    step="0.05"
                    value={modifyOrderPrice()}
                    onInput={(e) => setModifyOrderPrice(e.currentTarget.value)}
                  />
                </div>
                <div class="testing-summary">
                  Quantity (from "Total lots to sell" above)
                  <strong>{Math.max(1, Math.floor(toNum(manualTotalLots()))) * (lotSize())}</strong>
                </div>
                <button class="dashboard-btn yellow" onClick={handleModifyOrder} disabled={modifyOrderBusy()}>
                  {modifyOrderBusy() ? 'MODIFYING...' : 'MODIFY ORDER'}
                </button>
              </div>

              <Show when={modifyOrderStatus()}>
                <div class={`testing-status-msg ${modifyOrderStatus().startsWith('Failed:') ? 'err' : 'ok'}`}>
                  {modifyOrderStatus()}
                </div>
              </Show>
            </div>

            <Show when={manualHedgePreview()}>
              <div class="portfolio-card" style={{ 'margin-top': '16px' }}>
                <div class="portfolio-header">
                  <strong>Manual Hedge Preview</strong>
                  <span class="status-badge active">
                    {(manualHedgePreview().success ?? false) ? 'READY' : 'ERROR'}
                  </span>
                </div>

                <div class="portfolio-meta">
                  <span>Symbol: <strong>{manualHedgePreview().symbol || automationConfig().symbol || '—'}</strong></span>
                  <span>Expiry: <strong>{manualHedgePreview().expiry || manualHedgePreview().target_expiry || selectedExpiry() || optionChain().expiry || '—'}</strong></span>
                </div>

                <div class="portfolio-meta">
                  <span>ATM Strike: <strong>{manualHedgePreview().atm_strike || manualHedgePreview().atmStrike || '—'}</strong></span>
                  <span>Qty: <strong>{manualHedgePreview().quantity || 0}</strong></span>
                  <span>Lot Size: <strong>{manualHedgePreview().lot_size || manualHedgePreview().lotSize || '—'}</strong></span>
                </div>

                <div style={{ display: 'flex', gap: '15px', 'margin-top': '10px' }}>
                  <div style={{ background: '#2c2c2c', padding: '10px', 'border-radius': '4px', flex: 1 }}>
                    <div style={{ color: '#4caf50', 'font-size': '12px', 'margin-bottom': '5px' }}>
                      CE LEG
                    </div>
                    <div style={{ 'font-size': '13px' }}>Token: {manualHedgePreview().ce_token || manualHedgePreview().ceToken || '—'}</div>
                    <div style={{ 'font-size': '13px' }}>Side: {manualHedgePreview().ce_side || manualHedgePreview().ceSide || '—'}</div>
                  </div>

                  <div style={{ background: '#2c2c2c', padding: '10px', 'border-radius': '4px', flex: 1 }}>
                    <div style={{ color: '#f44336', 'font-size': '12px', 'margin-bottom': '5px' }}>
                      PE LEG
                    </div>
                    <div style={{ 'font-size': '13px' }}>Token: {manualHedgePreview().pe_token || manualHedgePreview().peToken || '—'}</div>
                    <div style={{ 'font-size': '13px' }}>Side: {manualHedgePreview().pe_side || manualHedgePreview().peSide || '—'}</div>
                  </div>
                </div>

                <Show when={Array.isArray(manualHedgePreview().intents) && manualHedgePreview().intents.length > 0}>
                  <div style={{ 'margin-top': '12px' }}>
                    <div style={{ 'font-size': '12px', color: '#9aa0a6', 'margin-bottom': '6px' }}>GENERATED INTENTS</div>
                    <For each={manualHedgePreview().intents}>
                      {(intent) => (
                        <div style={{ background: '#202020', padding: '8px 10px', 'border-radius': '4px', 'margin-bottom': '6px', 'font-size': '12px' }}>
                          {intent.side || '—'} • {intent.symbol || '—'} • token {intent.token || '—'} • qty {intent.quantity || '—'}
                        </div>
                      )}
                    </For>
                  </div>
                </Show>

                <Show when={manualHedgeExecutions().length > 0}>
                  <div style={{ 'margin-top': '12px' }}>
                    <div style={{ 'font-size': '12px', color: '#9aa0a6', 'margin-bottom': '6px' }}>EXECUTIONS</div>
                    <For each={manualHedgeExecutions()}>
                      {(execItem) => (
                        <div style={{ background: '#202020', padding: '8px 10px', 'border-radius': '4px', 'margin-bottom': '6px', 'font-size': '12px' }}>
                          {execItem.success === true ? 'OK' : 'FAIL'} • {execItem.message || execItem.error || execItem.order_id || '—'}
                        </div>
                      )}
                    </For>
                  </div>
                </Show>
              </div>
            </Show>
          </section>
        </Show>

        <Show when={activeTab() === 'lut'}>
          <LutBuildTab />
        </Show>

        <Show when={activeTab() === 'sbuild'}>
          <StraddleBuildTab />
        </Show>

        <Show when={activeTab() === 'papersim'}>
          <PaperSimTab />
        </Show>

        <Show when={activeTab() === 'logs'}>
          <section class="tab-panel">
            <div class="panel-header">
              <div class="panel-title">Event Logs</div>
              <div class="panel-subtitle">UI runtime + websocket + request logs</div>
            </div>

            <div class="log-container">
              <For each={eventLogs()}>
                {(entry) => (
                  <div class={`log-line log-${entry.level.toLowerCase()}`}>
                    <span class="log-ts">{entry.ts}</span>
                    <span class="log-lvl">{entry.level}</span>
                    <span class="log-msg">{entry.message}</span>
                  </div>
                )}
              </For>
            </div>
          </section>
        </Show>

        <Show when={activeTab() === 'latency'}>
          <LatencyDashboard />
        </Show>
      </div>
    );
  }

  export default App;

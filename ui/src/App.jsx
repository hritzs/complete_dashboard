  import { createSignal, createMemo, onMount, onCleanup, Index, Show, For } from 'solid-js';
  import './App.css';

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
    const [customCeStrike, setCustomCeStrike] = createSignal('');
    const [customPeStrike, setCustomPeStrike] = createSignal('');

    const [executionPrefs, setExecutionPrefs] = createSignal({
      user_id: 'U001',
      broker_name: 'greeksoft',
      account_id: 'HRITIK',
      product_type: 'NRML',
      exchange_segment: 'NSEFO',
      order_lots_per_call: 1,
      delta_neutral: true
    });

    const [automationConfig, setAutomationConfig] = createSignal({
      symbol: 'NIFTY',
      size: 1,
      entry_time: '',
      exit_time: '',
      hedge_div: 57,
      straddle_div: 4,
      roll_straddle_div: 0.2,
      hedge_frac: 1.0,
      sl_bps: 14,
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
    const greeksoftAccounts = ['HRITIK', 'HRITIK1'];

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

          if (raw.type !== 'option_chain_update' && raw.type !== 'option_chain') {
            return;
          }

          const payload =
            raw?.data && typeof raw.data === 'object'
              ? raw.data
              : raw;

          const incomingSymbol = normalize(raw?.symbol || payload?.symbol);
          if (incomingSymbol !== normalize(selectedSymbol())) return;

          const next = normalizePayload(incomingSymbol, payload);

          if (selectedExpiry() && next.expiry !== selectedExpiry()) return;

          if (next.chain.length > 0) {
            setOptionChain(next);

            if (!selectedExpiry()) {
              setSelectedExpiry(next.expiry || '');
            }

            setDataSource('C++ Feed');
          }
        } catch (err) {
          console.error('WS parse error:', err);
          appendEventLog('error', 'WebSocket parse error');
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

    onMount(() => {
      connectSocket();
      setUiDefaults();
      loadPortfolioFromBackend();

      heartbeatTimer = setInterval(() => {
        appendEventLog('info', `Heartbeat • ${selectedSymbol()} • ${selectedExpiry() || 'no-expiry'}`);
      }, 10000);
    });

    onCleanup(() => {
      if (ws) ws.close();
      if (reconnectTimer) clearTimeout(reconnectTimer);
      if (heartbeatTimer) clearInterval(heartbeatTimer);
    });

    const handleSymbolChange = (e) => {
      const sym = e.target.value.toUpperCase();
      setSelectedSymbol(sym);
      setSelectedExpiry('');
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
        ceLtp: tradeData.ce_entry_price ?? tradeData.ce_ltp ?? 0,
        peLtp: tradeData.pe_entry_price ?? tradeData.pe_ltp ?? 0,
        netDelta: tradeData.net_delta ?? 0
      };

      setPortfolioItems((prev) => dedupePortfolio([item, ...prev]));
    };

    const buildBasePayload = () => {
      const prefs = executionPrefs();

      return {
        user_id: prefs.user_id || 'U001',
        broker_name: normalize(prefs.broker_name || 'greeksoft').toLowerCase(),
        account_id: prefs.account_id || 'HRITIK',
        symbol: selectedSymbol(),
        delta_neutral: !!prefs.delta_neutral,
        product_type: prefs.product_type || 'NRML',
        target_expiry: selectedExpiry() || optionChain().expiry,
        order_lots_per_call: toNum(prefs.order_lots_per_call) || 1,
        exchange_segment: prefs.exchange_segment || 'NSEFO'
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
        lots: Math.max(1, Math.min(1, toNum(automationConfig().size) || 1)),
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
        lots: Math.max(1, Math.min(1, toNum(automationConfig().size) || 1)),
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
        const data = await safeFetchJson("/api/straddles");

        const rows = Array.isArray(data)
          ? data
          : Array.isArray(data?.data)
            ? data.data
            : Array.isArray(data?.trades)
              ? data.trades
              : [];

        if (!rows.length) return;

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
          ceLtp: tr.ce_entry_price ?? tr.ce_ltp ?? 0,
          peLtp: tr.pe_entry_price ?? tr.pe_ltp ?? 0,
          netDelta: tr.net_delta ?? 0
        }));

        setPortfolioItems((prev) => dedupePortfolio([...mapped, ...prev]));
        appendEventLog("info", `Loaded ${mapped.length} saved trades from backend`);
      } catch (err) {
        appendEventLog("warn", `Could not load saved trades: ${err.message}`);
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

      let exitTimeStr = '15:27:00';
      const marketExit = new Date();
      marketExit.setHours(15, 27, 0, 0);

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
    };

    const handleAutomationBuild = async () => {
      const cfg = automationConfig();

      const payload = {
        user_id: executionPrefs().user_id || 'U001',
        broker_name: executionPrefs().broker_name,
        account_id: executionPrefs().account_id,
        exchange_segment: executionPrefs().exchange_segment,
        symbol: cfg.symbol,
        size: cfg.size,
        lots: cfg.size,
        entry_time: cfg.entry_time,
        exit_time: cfg.exit_time,
        idv: cfg.idv,
        idv_divisor: cfg.idv_divisor,
        straddle_filter: cfg.straddle_filter,
        sl_bps: cfg.sl_bps,
        buy_buffer: cfg.buy_buffer,
        sell_buffer: cfg.sell_buffer,
        hedge_div: cfg.hedge_div,
        straddle_div: cfg.straddle_div,
        roll_straddle_div: cfg.roll_straddle_div,
        sl_start_time: cfg.sl_start_time,
        hedge_start_time: cfg.hedge_start_time,
        roll_start_time: cfg.roll_start_time,
        order_lots_per_call: cfg.order_lots_per_call,
        target_expiry: selectedExpiry() || optionChain().expiry
      };

      appendEventLog(
        "info",
        `Automation build trigger via ${payload.broker_name} for ${payload.symbol}`
      );

      try {
        const data = await safeFetchJson("/api/trade/straddle/automated", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(payload)
        });

        if (data.success) {
          appendEventLog("success", `Automation build scheduled: ${data.message}`);
        } else {
          appendEventLog("error", `Automation build failed: ${data.error || "Unknown error"}`);
        }
      } catch (err) {
        appendEventLog("error", `Automation build error: ${err.message}`);
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
            class={`tab-btn ${activeTab() === 'logs' ? 'active' : ''}`}
            onClick={() => setActiveTab('logs')}
          >
            Logs
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
                <div class="custom-strike-inputs">
                  <input
                    type="number"
                    placeholder="CE Strike"
                    value={customCeStrike()}
                    onInput={(e) => setCustomCeStrike(e.target.value)}
                  />
                  <input
                    type="number"
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

        <Show when={activeTab() === 'portfolio'}>
          <section class="tab-panel">
            <div class="panel-header">
              <div class="panel-title">Portfolio / Recent Builds</div>
              <div class="panel-subtitle">Local UI history from successful build requests</div>
            </div>

            <Show
              when={portfolioItems().length > 0}
              fallback={<div class="empty-state">No portfolio items yet. Build or sell a straddle first.</div>}
            >
              <div class="portfolio-list">
                <For each={portfolioItems()}>
                  {(item) => (
                    <div class="portfolio-card">
                      <div class="portfolio-header">
                        <strong>{item.symbol}</strong> • {item.expiry || '—'}
                        <span class={`status-badge ${(item.status || 'ACTIVE').toLowerCase()}`}>
                          {item.status}
                        </span>
                      </div>

                      <div class="portfolio-meta">
                        <span>Strike: <strong>{item.strike || '—'}</strong></span>
                        <span>Lots: <strong>{item.lots}</strong></span>
                        <span>UID: <strong>{item.tradeUid}</strong></span>
                      </div>

                      <div class="portfolio-meta">
                        <span>Broker: <strong>{(item.brokerName || '—').toUpperCase()}</strong></span>
                        <span>Lot Size: <strong>{item.lotSize || '—'}</strong></span>
                      </div>

                      <div style={{ display: 'flex', gap: '15px', 'margin-top': '10px' }}>
                        <div style={{ background: '#2c2c2c', padding: '10px', 'border-radius': '4px', flex: 1 }}>
                          <div style={{ color: '#4caf50', 'font-size': '12px', 'margin-bottom': '5px' }}>
                            CE LEG
                          </div>
                          <div style={{ 'font-size': '13px' }}>Token: {item.ceToken || '—'}</div>
                          <div style={{ 'font-size': '13px' }}>Qty: {item.ceQty || '—'}</div>
                          <div style={{ 'font-size': '13px' }}>Price: ₹{fmt(item.ceLtp, 2)}</div>
                        </div>

                        <div style={{ background: '#2c2c2c', padding: '10px', 'border-radius': '4px', flex: 1 }}>
                          <div style={{ color: '#f44336', 'font-size': '12px', 'margin-bottom': '5px' }}>
                            PE LEG
                          </div>
                          <div style={{ 'font-size': '13px' }}>Token: {item.peToken || '—'}</div>
                          <div style={{ 'font-size': '13px' }}>Qty: {item.peQty || '—'}</div>
                          <div style={{ 'font-size': '13px' }}>Price: ₹{fmt(item.peLtp, 2)}</div>
                        </div>
                      </div>

                      <div class="portfolio-footer" style={{ 'margin-top': '15px', 'font-size': '12px', color: '#aaa' }}>
                        <span>Net Delta: {fmt(item.netDelta, 4)}</span>
                        <span style={{ float: 'right' }}>Created: {item.createdAt}</span>
                      </div>
                    </div>
                  )}
                </For>
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
              <div class="control-block">
                <label class="control-label">Symbol</label>
                <select
                  class="symbol-select"
                  value={automationConfig().symbol}
                  onChange={(e) => setAutomationConfig((prev) => ({ ...prev, symbol: e.target.value }))}
                >
                  <For each={availableSymbols}>
                    {(sym) => <option value={sym}>{sym}</option>}
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
                <label class="control-label">Number of Straddles</label>
                <input
                  class="symbol-select"
                  type="number"
                  min="1"
                  step="1"
                  value={automationConfig().size}
                  onInput={(e) => setAutomationConfig((prev) => ({ ...prev, size: Number(e.target.value) || 1 }))}
                />
                <small style={{opacity:0.75}}>
                  1 straddle = 1 CE lot + 1 PE lot
                </small>
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
                <label class="control-label">Hedge Divisor</label>
                <input
                  class="symbol-select"
                  type="number"
                  value={automationConfig().hedge_div}
                  onInput={(e) => setAutomationConfig((prev) => ({ ...prev, hedge_div: Number(e.target.value) || 0 }))}
                />
              </div>

              <div class="control-block">
                <label class="control-label">Straddle Divisor</label>
                <input
                  class="symbol-select"
                  type="number"
                  value={automationConfig().straddle_div}
                  onInput={(e) => setAutomationConfig((prev) => ({ ...prev, straddle_div: Number(e.target.value) || 0 }))}
                />
              </div>

              <div class="control-block">
                <label class="control-label">Roll Straddle Divisor</label>
                <input
                  class="symbol-select"
                  type="number"
                  value={automationConfig().roll_straddle_div}
                  onInput={(e) => setAutomationConfig((prev) => ({ ...prev, roll_straddle_div: Number(e.target.value) || 0 }))}
                />
              </div>

              <div class="control-block">
                <label class="control-label">SL (bps)</label>
                <input
                  class="symbol-select"
                  type="number"
                  value={automationConfig().sl_bps}
                  onInput={(e) => setAutomationConfig((prev) => ({ ...prev, sl_bps: Number(e.target.value) || 0 }))}
                />
              </div>

              <div class="control-block">
                <label class="control-label">IDV</label>
                <input
                  class="symbol-select"
                  type="number"
                  step="0.1"
                  value={automationConfig().idv}
                  onInput={(e) => setAutomationConfig((prev) => ({ ...prev, idv: Number(e.target.value) || 0 }))}
                />
              </div>

              <div class="control-block">
                <label class="control-label">IDV Divisor</label>
                <input
                  class="symbol-select"
                  type="number"
                  step="0.1"
                  value={automationConfig().idv_divisor}
                  onInput={(e) => setAutomationConfig((prev) => ({ ...prev, idv_divisor: Number(e.target.value) || 0 }))}
                />
              </div>

              <div class="control-block">
                <label class="control-label">Straddle Price Filter</label>
                <input
                  class="symbol-select"
                  type="number"
                  value={automationConfig().straddle_filter}
                  onInput={(e) => setAutomationConfig((prev) => ({ ...prev, straddle_filter: Number(e.target.value) || 0 }))}
                />
              </div>

              <div class="control-block">
                <label class="control-label">SL Interval (s)</label>
                <input
                  class="symbol-select"
                  type="number"
                  value={automationConfig().sl_monitor_interval}
                  onInput={(e) => setAutomationConfig((prev) => ({ ...prev, sl_monitor_interval: Number(e.target.value) || 60 }))}
                />
              </div>

              <div class="control-block">
                <label class="control-label">Hedge Interval (s)</label>
                <input
                  class="symbol-select"
                  type="number"
                  value={automationConfig().hedge_monitor_interval}
                  onInput={(e) => setAutomationConfig((prev) => ({ ...prev, hedge_monitor_interval: Number(e.target.value) || 60 }))}
                />
              </div>

              <div class="control-block">
                <label class="control-label">Roll Interval (s)</label>
                <input
                  class="symbol-select"
                  type="number"
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
                <label class="control-label">Buy Buffer</label>
                <input
                  class="symbol-select"
                  type="number"
                  value={automationConfig().buy_buffer}
                  onInput={(e) => setAutomationConfig((prev) => ({ ...prev, buy_buffer: Number(e.target.value) || 0 }))}
                />
              </div>

              <div class="control-block">
                <label class="control-label">Sell Buffer</label>
                <input
                  class="symbol-select"
                  type="number"
                  value={automationConfig().sell_buffer}
                  onInput={(e) => setAutomationConfig((prev) => ({ ...prev, sell_buffer: Number(e.target.value) || 0 }))}
                />
              </div>

              <div class="control-block">
                <label class="control-label">Lots Per Call</label>
                <input
                  class="symbol-select"
                  type="number"
                  value={automationConfig().order_lots_per_call}
                  onInput={(e) => setAutomationConfig((prev) => ({ ...prev, order_lots_per_call: Number(e.target.value) || 1 }))}
                />
              </div>
            </div>

            <div class="button-row">
              <button class="sell-btn" onClick={handleAutomationBuild}>
                START AUTOMATED BUILD
              </button>
            </div>
          </section>
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
      </div>
    );
  }

  export default App;
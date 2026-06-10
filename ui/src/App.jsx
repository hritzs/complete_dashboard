import { createSignal, onMount, For, Index, onCleanup } from 'solid-js';
import './App.css';

function App() {
  const [snapshots, setSnapshots] = createSignal({});
  const [wsStatus, setWsStatus] = createSignal('Connecting...');
  const [logs, setLogs] = createSignal([]);
  const [optionChain, setOptionChain] = createSignal([]);
  const [lastCppUpdate, setLastCppUpdate] = createSignal(0);
  const [dataSource, setDataSource] = createSignal('C++ Feed');
  const [selectedSymbol, setSelectedSymbol] = createSignal('NIFTY');
  const availableSymbols = ['NIFTY', 'BANKNIFTY', 'FINNIFTY', 'MIDCPNIFTY', 'SENSEX', 'BANKEX'];
  const [visibleStrikes, setVisibleStrikes] = createSignal(10); // Show 10 strikes above and 10 below ATM
  
  // Configuration defaults as defined in legacy JS
  const [tradeConfig, setTradeConfig] = createSignal({
    size: 77,
    sl_bps: 14,
    hedge_div: 57,
    straddle_div: 4,
    roll_straddle_div: 0.2,
    buy_buffer: 2,
    sell_buffer: 2,
    order_lots_per_call: 1
  });

  const updateConfig = (key, value) => {
    setTradeConfig(prev => ({ ...prev, [key]: parseFloat(value) || value }));
  };

  onMount(() => {
    const HOST = window.location.hostname;
    
    // --- PYTHON XTS FALLBACK POLLER ---
    const fetchPythonFallback = async () => {
      const timeSinceUpdate = Date.now() - lastCppUpdate();
      // If C++ is dead/zero for > 5 seconds, pull live chain from Python
      if (timeSinceUpdate > 5000) {
        try {
          const res = await fetch(`http://${HOST}:8001/api/option-chain/${selectedSymbol()}`);
          if (!res.ok) return; // FIX: Silently abort if the fallback API returns 404 to prevent JSON crash
          
          const text = await res.text();
          if (!text || text.includes("404")) return;
          
          let data;
          try {
            data = JSON.parse(text);
          } catch (e) { return; } // Safely ignore non-JSON error pages
          
          if (data.success && data.data) {
            const chainArray = data.data.chain || data.data.data || [];
            setOptionChain({
              spot: data.data.fut_ltp || data.data.spot || 0,
              synthetic_spot: data.data.synthetic_spot || data.data.fut_ltp || 0,
              atm: data.data.atm,
              chain: chainArray,
              lot_size: data.data.lot_size || 0,
              expiry: data.data.expiry || data.data.expiry_date || ''
            });
            setDataSource('🐍 Python XTS Fallback');
          }
        } catch (e) {
          console.error("XTS Fallback failed:", e);
        }
      }
    };

    const poller = setInterval(fetchPythonFallback, 2000);
    onCleanup(() => clearInterval(poller));
    // ----------------------------------

    const connectSnapshots = () => {
      setWsStatus('Connecting...');
      const socket = new WebSocket(`ws://${HOST}:8003/ws/snapshots`);

      socket.onopen = () => {
        setWsStatus('Connected');
      };

      socket.onmessage = (event) => {
        const data = JSON.parse(event.data);
        if (data.type === 'log') {
          setLogs(prev => [...prev, { time: new Date().toLocaleTimeString(), msg: data.message }].slice(-50));
        } else if (data.trade_uid) {
          setSnapshots(prev => ({
            ...prev,
            [data.trade_uid]: data
          }));
        } else if (data.type === 'option_chain' && data.symbol === selectedSymbol()) {
          const chainArray = data.data ?? data.chain ?? data.options ?? [];
          
          // Detect if C++ is transmitting pure zeros
          const atmRow = chainArray.find(row => row.is_atm);
          const isZero = atmRow ? (atmRow.ce_ltp === 0 && atmRow.pe_ltp === 0) : false;

          if (Array.isArray(chainArray) && chainArray.length > 0) {
            // Allow update if we have good data, OR if the screen is currently blank
            if (!isZero || optionChain()?.chain?.length === 0) {
              setOptionChain({
                spot: data.spot || data.fut_ltp || 0,
                synthetic_spot: data.synthetic_spot || data.syn_fut || data.syn_spot || data.spot || data.fut_ltp || 0,
                atm: data.atm,
                chain: chainArray,
                lot_size: data.lot_size || 0,
                expiry: data.expiry || data.expiry_date || ''
              });
              setLastCppUpdate(Date.now());
              if (!isZero) setDataSource('⚡ C++ Feed');
            }
          } else {
            console.warn("Received option chain but array is empty or missing:", data);
          }
        }
      };

      socket.onclose = () => {
        setWsStatus('Disconnected. Retrying...');
        setTimeout(connectSnapshots, 2000);
      };

      socket.onerror = (error) => socket.close();
    };

    connectSnapshots();
  });

  const handleSquareOff = async (tradeUid) => {
    try {
      setLogs(prev => [...prev, { time: new Date().toLocaleTimeString(), msg: `Sending square-off command for ${tradeUid}...` }]);
      // Hits the Go Execution Gateway
      const res = await fetch(`http://${window.location.hostname}:8005/api/trade/${tradeUid}/square-off`, { method: 'POST' });
      if (!res.ok) throw new Error('Square-off failed');
      setLogs(prev => [...prev, { time: new Date().toLocaleTimeString(), msg: `✅ Square-off triggered for ${tradeUid}` }]);
      console.log(`Square-off triggered for ${tradeUid}`);
    } catch (err) {
      setLogs(prev => [...prev, { time: new Date().toLocaleTimeString(), msg: `❌ Error squaring off: ${err.message}` }]);
      console.error(err);
    }
  };

  const handleDeployStraddle = async () => {
    try {
      const currentChain = optionChain();
      const atmRow = currentChain?.chain?.find(row => row.is_atm);
      const ce_token = atmRow?.ce_token ?? atmRow?.CE?.token ?? atmRow?.ceToken ?? 0;
      const pe_token = atmRow?.pe_token ?? atmRow?.PE?.token ?? atmRow?.peToken ?? 0;
      const lot_size = atmRow?.ce_lot_size ?? atmRow?.lot_size ?? currentChain?.lot_size ?? 0;

      setLogs(prev => [...prev, { time: new Date().toLocaleTimeString(), msg: `Initiating ${selectedSymbol()} Straddle deployment (CE: ${ce_token}, PE: ${pe_token})...` }]);
      const payload = { 
        symbol: selectedSymbol(), 
        lots: 1, 
        delta_neutral: true,
        strike: currentChain?.atm || 0,
        ce_token: ce_token,
        pe_token: pe_token,
        lot_size: lot_size,
        ...tradeConfig() // Unpacks size, hedge_div, sl_bps, etc.
      };

      // Hits the Go Execution Gateway
      const res = await fetch(`http://${window.location.hostname}:8005/api/straddle/sell`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
      });
      if (!res.ok) {
         const errText = await res.text();
         throw new Error(`Deploy failed: ${errText}`);
      }
      const data = await res.json();
      setLogs(prev => [...prev, { time: new Date().toLocaleTimeString(), msg: `✅ Go Gateway accepted! UID: ${data.trade_uid || 'Unknown'}` }]);
      console.log(`Straddle deployment initiated!`, data);
    } catch (err) {
      setLogs(prev => [...prev, { time: new Date().toLocaleTimeString(), msg: `❌ Error starting trade: ${err.message}` }]);
      console.error('Error starting trade:', err);
    }
  };

  const handleSymbolChange = (e) => {
    setSelectedSymbol(e.target.value);
    setOptionChain([]); // Clear table until new data arrives
    
    // Auto-set buffer defaults for specific symbols
    const defaultBuffer = e.target.value.toUpperCase().includes('SENSEX') ? 6 : 2;
    setTradeConfig(prev => ({
        ...prev,
        buy_buffer: defaultBuffer,
        sell_buffer: defaultBuffer
    }));
  };

  const getVisibleChain = () => {
    const chain = optionChain()?.chain || [];
    if (chain.length === 0) return [];

    const atmIndex = chain.findIndex(row => row.is_atm);
    if (atmIndex === -1) {
      // If no ATM, just show the middle part of the chain
      const middle = Math.floor(chain.length / 2);
      const start = Math.max(0, middle - visibleStrikes());
      const end = Math.min(chain.length, middle + visibleStrikes() + 1);
      return chain.slice(start, end);
    }

    const start = Math.max(0, atmIndex - visibleStrikes());
    const end = Math.min(chain.length, atmIndex + visibleStrikes() + 1);
    return chain.slice(start, end);
  };

  return (
    <div class="App">
      <header class="App-header">
        <h1>Live Trade Monitor</h1>
        <p>Unified Data Feed: <span class={wsStatus() === 'Connected' ? 'connected' : 'disconnected'}>{wsStatus()}</span></p>
      </header>
      <main>
        <div class="quick-actions" style={{ "margin-bottom": "20px", "text-align": "left", "padding": "15px", "background": "#2c2c2c", "border-radius": "8px" }}>
          <h3 style={{ "margin-top": "0" }}>Trade Configuration</h3>
          <div style={{ "display": "grid", "grid-template-columns": "repeat(auto-fit, minmax(130px, 1fr))", "gap": "10px", "margin-bottom": "15px" }}>
            <div><label style={{"font-size":"12px","color":"#aaa"}}>Lots (Size)</label><br/><input type="number" style={{"width":"100%","padding":"5px","background":"#444","color":"white","border":"none"}} value={tradeConfig().size} onInput={(e) => updateConfig('size', e.target.value)} /></div>
            <div><label style={{"font-size":"12px","color":"#aaa"}}>SL BPS</label><br/><input type="number" style={{"width":"100%","padding":"5px","background":"#444","color":"white","border":"none"}} value={tradeConfig().sl_bps} onInput={(e) => updateConfig('sl_bps', e.target.value)} /></div>
            <div><label style={{"font-size":"12px","color":"#aaa"}}>Hedge Div</label><br/><input type="number" style={{"width":"100%","padding":"5px","background":"#444","color":"white","border":"none"}} value={tradeConfig().hedge_div} onInput={(e) => updateConfig('hedge_div', e.target.value)} /></div>
            <div><label style={{"font-size":"12px","color":"#aaa"}}>Straddle Div</label><br/><input type="number" style={{"width":"100%","padding":"5px","background":"#444","color":"white","border":"none"}} value={tradeConfig().straddle_div} onInput={(e) => updateConfig('straddle_div', e.target.value)} /></div>
            <div><label style={{"font-size":"12px","color":"#aaa"}}>Roll Div</label><br/><input type="number" step="0.1" style={{"width":"100%","padding":"5px","background":"#444","color":"white","border":"none"}} value={tradeConfig().roll_straddle_div} onInput={(e) => updateConfig('roll_straddle_div', e.target.value)} /></div>
            <div><label style={{"font-size":"12px","color":"#aaa"}}>Buy Buffer</label><br/><input type="number" style={{"width":"100%","padding":"5px","background":"#444","color":"white","border":"none"}} value={tradeConfig().buy_buffer} onInput={(e) => updateConfig('buy_buffer', e.target.value)} /></div>
            <div><label style={{"font-size":"12px","color":"#aaa"}}>Sell Buffer</label><br/><input type="number" style={{"width":"100%","padding":"5px","background":"#444","color":"white","border":"none"}} value={tradeConfig().sell_buffer} onInput={(e) => updateConfig('sell_buffer', e.target.value)} /></div>
            <div><label style={{"font-size":"12px","color":"#aaa"}}>Lots/Call</label><br/><input type="number" style={{"width":"100%","padding":"5px","background":"#444","color":"white","border":"none"}} value={tradeConfig().order_lots_per_call} onInput={(e) => updateConfig('order_lots_per_call', e.target.value)} /></div>
          </div>
          <button class="action-btn deploy" style={{ "background-color": "#4CAF50", "padding": "10px 20px", "font-size": "16px", "font-weight": "bold", "width": "100%" }} onClick={handleDeployStraddle}>
            ⚡ One-Click Sell {selectedSymbol()} Straddle
          </button>
        </div>

        <table>
          <thead>
            <tr>
              <th>Trade UID</th>
              <th>Status</th>
              <th>Total P&L</th>
              <th>Unrealized P&L</th>
              <th>Net Delta</th>
              <th>Net Gamma</th>
              <th>Actions</th>
            </tr>
          </thead>
          <tbody>
            <Index each={Object.values(snapshots())}>
              {(snap) => (
                <tr>
                  <td>{snap().trade_uid}</td>
                  <td>{snap().status}</td>
                  <td class={snap().total_pnl >= 0 ? 'pnl-positive' : 'pnl-negative'}>
                    {snap().total_pnl.toFixed(2)}
                  </td>
                  <td>{snap().unrealized_pnl.toFixed(2)}</td>
                  <td>{snap().net_delta.toFixed(2)}</td>
                  <td>{snap().net_gamma.toFixed(4)}</td>
                  <td>
                    <button class="action-btn square-off" onClick={() => handleSquareOff(snap().trade_uid)}>
                      Square Off
                    </button>
                  </td>
                </tr>
              )}
            </Index>
          </tbody>
        </table>

      <div class="option-chain-container" style={{ "margin-top": "30px", "text-align": "center" }}>
        <div style={{ "display": "flex", "justify-content": "center", "align-items": "center", "gap": "15px" }}>
          <h3 style={{ margin: 0 }}>🔴 LIVE OPTION CHAIN <span style={{ "font-size": "16px", "margin-left": "10px", "color": dataSource().includes('C++') ? '#4CAF50' : '#2196F3' }}>[{dataSource()}]</span></h3>
          <select 
            value={selectedSymbol()} 
            onChange={handleSymbolChange}
            style={{ "padding": "5px 10px", "font-size": "16px", "background": "#333", "color": "#fff", "border": "1px solid #555", "border-radius": "4px", "cursor": "pointer" }}
          >
            <For each={availableSymbols}>
              {(sym) => <option value={sym}>{sym}</option>}
            </For>
          </select>
          <div style={{ "display": "flex", "align-items": "center", "gap": "10px", "margin-left": "20px" }}>
            <label for="strike-slider" style={{"font-size": "14px"}}>Strikes:</label>
            <input 
              type="range" 
              id="strike-slider"
              min="5" 
              max="40" 
              value={visibleStrikes()} 
              onInput={(e) => setVisibleStrikes(parseInt(e.target.value))}
            />
            <span>{visibleStrikes() * 2 + 1}</span>
          </div>
        </div>
        
        {optionChain() && optionChain().spot && (
          <div style={{ "margin-bottom": "15px", "font-size": "1.1rem" }}>
            <strong>Equity Spot:</strong> {optionChain().spot?.toFixed(2)} | <strong style={{ "margin-left": "15px" }}>Synthetic Spot:</strong> {optionChain().synthetic_spot?.toFixed(2)} | <strong style={{ "margin-left": "15px" }}>ATM:</strong> {optionChain().atm} | <strong style={{ "margin-left": "15px" }}>Expiry:</strong> {optionChain().expiry}
          </div>
        )}
        
        <table class="option-chain-table" style={{ "width": "100%", "background": "#1e1e1e", "border-radius": "8px", "table-layout": "fixed" }}>
          <thead>
            <tr>
              <th>CE LTP</th>
              <th>CE Delta</th>
              <th>CE Gamma</th>
              <th style={{ "background": "#333" }}>STRIKE</th>
              <th>PE LTP</th>
              <th>PE Delta</th>
              <th>PE Gamma</th>
            </tr>
          </thead>
          <tbody>
            <Index each={getVisibleChain()}>
              {(row) => (
                <tr style={{ "background-color": row().is_atm ? "#444" : "transparent" }}>
                  <td style={{"color": "#4CAF50"}}>{(row().ce_ltp ?? row().CE?.ltp ?? row().CE?.LTP ?? 0).toFixed(2)}</td>
                  <td>{(row().ce_delta ?? row().CE?.delta ?? 0).toFixed(4)}</td>
                  <td>{(row().ce_gamma ?? row().CE?.gamma ?? 0).toFixed(4)}</td>
                  <td style={{ "font-weight": "bold", "font-size": "16px", "color": row().is_atm ? "#FFD700" : "#fff" }}>
                    {row().strike} {row().is_atm ? "(ATM)" : ""}
                  </td>
                  <td style={{"color": "#F44336"}}>{(row().pe_ltp ?? row().PE?.ltp ?? row().PE?.LTP ?? 0).toFixed(2)}</td>
                  <td>{(row().pe_delta ?? row().PE?.delta ?? 0).toFixed(4)}</td>
                  <td>{(row().pe_gamma ?? row().PE?.gamma ?? 0).toFixed(4)}</td>
                </tr>
              )}
            </Index>
          </tbody>
        </table>
      </div>

        <div class="logs-container" style={{ "margin-top": "30px", "text-align": "left", "background": "#1e1e1e", "padding": "15px", "border-radius": "8px", "height": "250px", "overflow-y": "auto", "font-family": "monospace", "font-size": "14px", "border": "1px solid #333" }}>
          <h3 style={{ "margin-top": "0", "color": "#aaa", "border-bottom": "1px solid #333", "padding-bottom": "10px" }}>System Logs</h3>
          <For each={logs()}>
            {(log) => (
              <div style={{ "margin-bottom": "8px", "line-height": "1.4" }}>
                <span style={{ "color": "#4CAF50", "margin-right": "10px" }}>[{log.time}]</span>
                <span style={{ "color": "#e0e0e0" }}>{log.msg}</span>
              </div>
            )}
          </For>
        </div>
      </main>
    </div>
  );
}

export default App;
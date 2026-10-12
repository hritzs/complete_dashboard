# Overnight module

Positional NIFTY monthly trading on GreekSoft: a CE and a PE picked by delta
are bought over a set time, carried for days, then exited, partly exited or
rolled. This folder is self-contained so it can be moved to another project
or machine.

## What it depends on

Only the Go standard library, plus three things supplied from outside:

| Need | Supplied by | In this repo |
|---|---|---|
| A broker session (`Broker` interface in `broker.go`) | whoever embeds the module | `services/execution-gateway/overnight_adapter.go` |
| Live GreekSoft ticks over HTTP | the process that owns the Apollo websocket | `services/greeksoft-feed-bridge/cmd/quotes.go` |
| GreekSoft contract file `IndexTokens.csv` | downloaded from GreekSoft | repo root |

Nothing in the folder imports the intraday `trading` package, and no path,
port or account is fixed in code.

## Settings (environment variables)

| Variable | Default | Meaning |
|---|---|---|
| `OVERNIGHT_DATA_DIR` | `~/.trading-platform/overnight` | `state.json` and one `<day>_scenarios.jsonl` per day |
| `OVERNIGHT_QUOTE_URL` | `http://127.0.0.1:$BRIDGE_HEALTH_PORT` (8022) | base URL of the tick service |
| `INDEX_TOKENS_PATH` | `../../IndexTokens.csv`, then `IndexTokens.csv` | GreekSoft contract file |
| `OVERNIGHT_SYMBOL` | `NIFTY` | underlying |
| `OVERNIGHT_ACCOUNT_ID` | `GREEK_ACCOUNT_ID` | broker account (read by the adapter) |
| `OVERNIGHT_USER_ID` | `U001` | platform user (read by the adapter) |
| `OVERNIGHT_LOT_SIZE` | `65` | PAPER mode only, when the broker cannot be asked |

## Moving it

1. Copy this folder, and `ui/src/overnight/` for the screen.
2. Copy the data directory if the open position and its day-by-day PnL
   should come along.
3. Implement `Broker` over the new system's GreekSoft session (the adapter
   here is about 200 lines and shows every call).
4. Provide the two tick endpoints: `POST /api/subscribe {"tokens":[gtoken…]}`
   and `GET /api/quotes?tokens=…` returning
   `{"quotes":{"<gtoken>":{"ltp","bid","ask","at_ms"}},"index":{"Nifty 50":{"ltp"}}}`.
   They must be served by the one process holding the Apollo websocket: a
   second GreekSoft login logs the first one out.
5. Mount `Service.Handler` at `/api/overnight` and call `Service.Start`.

## Files

| File | Holds |
|---|---|
| `broker.go` | the `Broker` interface, session hours |
| `instruments.go` | nearest monthly expiry from the contract file |
| `greeks.go` | Black-76 price, implied volatility, delta |
| `scenario.go` | minute-end numbers and the delta strike search |
| `engine.go` | entry / exit / partial / roll jobs and the chunk loop |
| `paper.go` | PAPER mode (no orders leave the process) |
| `state.go` | position, day marks, orders, persistence |
| `service.go` | minute loop, live view, broker position check |
| `http.go` | `/api/overnight` |
| `feed.go` | client of the tick service |

## Order tags

Every order carries two tags to the broker. `tag` is unique per order:
`<position>-<ENT|EXI|PAR|ROL>-<strike><CE|PE>-<n>`, for example
`N1210265-ENT-23500CE-17`. `userTag` is the same for every order of one
position: `OVN-<position>`.

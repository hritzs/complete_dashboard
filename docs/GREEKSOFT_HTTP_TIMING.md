# Client HTTP timing for order submission

Branch: `fix/greeksoft-http-timing`.

NewOrderRequest calls made through PlaceOrder emit one JSON log line prefixed
`[GREEKSOFT HTTP TIMING]`, correlated by the request tag (IntentID). It is
emitted when the helper returns, on success or failure. Other API calls are
not traced. No additional HTTP requests, retries, broker sessions or orders
are introduced. Authentication headers, request/response bodies, URL query
parameters are not included in these timing lines. The correlation tag is
the existing IntentID, which can contain trade/account identifiers.

Each event has a UTC RFC3339 timestamp with fractional seconds and an
`elapsed_ms` offset from helper entry. Duration calculations use Go's
monotonic clock. Log-prefix time follows the existing service logger.

Events:
- prepare -> http_do: local JSON encoding and HTTP request setup.
- get_conn -> got_conn: connection acquisition, including pool waiting and
  connection setup; DNS/TCP/TLS events identify setup inside this interval.
- got_conn -> wrote_request: local transport/request-write work.
- wrote_request -> first_response_byte: waiting after the transport finishes
  writing the request. Includes outbound network, remote processing and
  return network time. This does NOT prove broker processing time or when
  the broker received the request.
- first_response_byte -> body_read_done: remaining headers/body transfer.
- body_read_done -> finished: decoding and helper return work.

`wrote_request` is Go transport write completion, not a remote receipt.
`got_conn` includes reused/idle connection information. Reused connections
normally have no DNS/TCP/TLS events. Concurrent connection attempts can
produce overlapping events; preserve them rather than summing durations.
Existing context traces are composed through httptrace.WithClientTrace.
Missing phases on an error are expected; stage, status and failed flags
help identify where processing stopped. Raw error strings are omitted.
An HTTP 200 or a done stage does not itself mean broker order acceptance;
correlate with the existing submitted/rejected response logs.

After deployment, inspect:

```bash
rg '\[GREEKSOFT HTTP TIMING\]' logs/4_execution.log
```

Compare by tag to the existing order request/acknowledgement logs and broker
order ID. These measurements apply to future normally authorised orders;
the six/seven-second historical gaps cannot be instrumented retroactively.

Validation uses local mock servers for TLS, connection reuse, deliberate
response-header/body delays, cancellation, decode errors and log scope.
No real broker is contacted by those tests. Timing values remain subject
to local process scheduling; first-byte wait is not exclusively broker time.

Deployment is pending. No live service was restarted. The existing owner
restriction keeps live-platform merges/restarts until after 15:40 IST with
owner approval; commit/review work can be done beforehand.

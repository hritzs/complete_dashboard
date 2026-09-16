module trading-platform/libs/broker-greeksoft

go 1.25.0

require (
	github.com/gorilla/websocket v1.5.3
	github.com/lib/pq v1.10.9
	trading-platform/libs/go-broker v0.0.0
)

replace trading-platform/libs/go-broker => ../go-broker

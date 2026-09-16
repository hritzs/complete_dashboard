module trading-platform/services/greeksoft-feed-bridge

go 1.25.0

require (
	github.com/pebbe/zmq4 v1.4.0
	trading-platform/libs/broker-greeksoft v0.0.0
	trading-platform/libs/go-broker v0.0.0
)

require (
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/lib/pq v1.10.9
)

replace trading-platform/libs/broker-greeksoft => ../../libs/broker-greeksoft

replace trading-platform/libs/go-broker => ../../libs/go-broker

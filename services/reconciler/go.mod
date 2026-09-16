module trading-platform/services/reconciler

go 1.25.0

require (
	github.com/lib/pq v1.10.9
	github.com/nats-io/nats.go v1.53.1
	github.com/sirupsen/logrus v1.10.2
	trading-platform/libs/broker-greeksoft v0.0.0
	trading-platform/libs/contracts v0.0.0
	trading-platform/libs/go-broker v0.0.0
	trading-platform/libs/go-common/events v0.0.0
)

require (
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/klauspost/compress v1.18.5 // indirect
	github.com/nats-io/nkeys v0.4.15 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	golang.org/x/crypto v0.49.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
)

replace trading-platform/libs/broker-greeksoft => ../../libs/broker-greeksoft

replace trading-platform/libs/go-broker => ../../libs/go-broker

replace trading-platform/libs/contracts => ../../libs/contracts

replace trading-platform/libs/go-common => ../../libs/go-common

replace trading-platform/libs/go-common/events => ../../libs/go-common/events

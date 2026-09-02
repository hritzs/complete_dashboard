package publisher

import (
	"log/slog"

	zmq "github.com/pebbe/zmq4"
)

type Publisher struct {
	sock *zmq.Socket
}

func NewPublisher(endpoint string) (*Publisher, error) {
	zctx, _ := zmq.NewContext()
	sock, err := zctx.NewSocket(zmq.PUB)
	if err != nil {
		return nil, err
	}
	if err := sock.Bind(endpoint); err != nil {
		return nil, err
	}
	slog.Info("✅ ZeroMQ Publisher bound", "endpoint", endpoint)
	return &Publisher{sock: sock}, nil
}

func (p *Publisher) Publish(data []byte) error {
	// Send raw binary/string payload instantly to C++ Feed Decoder
	_, err := p.sock.SendBytes(data, 0)
	return err
}

func (p *Publisher) Close() error {
	return p.sock.Close()
}

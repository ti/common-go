// Package broker provider kafka or other mq interface
package broker

import (
	"context"
	"net/url"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Broker broker for message queue.
type Broker struct {
	MQ
	uri *url.URL
}

// MQ is an interface used for CloudEvents asynchronous messaging.
type MQ interface {
	// Init connection
	Init(ctx context.Context, uri *url.URL) error
	// Close Disconnect
	Close(ctx context.Context) error
	// Publish send message
	Publish(ctx context.Context, topic string, msg *Message) error
	// Subscribe subscription: queue is the subscribed queue. If the queue is the same,
	// only one message will be received, otherwise multiple messages will be received, autoAck is automatic ACK.
	Subscribe(ctx context.Context, topic []string, queue string, h Handler, autoAck bool) error
	// Unsubscribe the topic.
	Unsubscribe(ctx context.Context, topic []string) error
}

// Handler is used to process messages via a subscription of a topic.
type Handler func(Publication) error

// Publication is given to a subscription handler for processing.
type Publication interface {
	Message() *Message
	Ack() error
	Topic() string
}

// Message a message of common msq.
type Message struct {
	Header map[string]string
	Body   []byte
}

var implements = make(map[string]NewMQ)

// RegisterImplements registration implementation class
func RegisterImplements(scheme string, i NewMQ) {
	implements[scheme] = i
}

// NewMQ new mq
type NewMQ func(ctx context.Context, u *url.URL) (MQ, error)

// New broker
func New(ctx context.Context, uri string) (*Broker, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}
	b := &Broker{}
	return b, b.Init(ctx, u)
}

// Init initialization
func (b *Broker) Init(ctx context.Context, u *url.URL) error {
	impl, ok := implements[u.Scheme]
	if !ok {
		return status.Errorf(codes.Unimplemented, "broker %s not implemented", u.Scheme)
	}
	b.uri = u
	var err error
	b.MQ, err = impl(ctx, u)
	return err
}

// URI get uri of broker
func (b *Broker) URI() *url.URL {
	return b.uri
}

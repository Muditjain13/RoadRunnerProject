// Package kafka is a thin wrapper around segmentio/kafka-go, shared by
// every service that produces or consumes booking events.
package kafka

import (
	"context"
	"fmt"

	"github.com/segmentio/kafka-go"
)

type Producer struct {
	writer *kafka.Writer
}

func NewProducer(brokers []string, topic string) *Producer {
	return &Producer{
		writer: &kafka.Writer{
			Addr:     kafka.TCP(brokers...),
			Topic:    topic,
			Balancer: &kafka.LeastBytes{},
		},
	}
}

// Publish keys the message by key (e.g. booking ID) so events for the
// same aggregate land on the same partition and are consumed in order.
func (p *Producer) Publish(ctx context.Context, key string, value []byte) error {
	if err := p.writer.WriteMessages(ctx, kafka.Message{Key: []byte(key), Value: value}); err != nil {
		return fmt.Errorf("publish to %s: %w", p.writer.Topic, err)
	}
	return nil
}

func (p *Producer) Close() error {
	return p.writer.Close()
}

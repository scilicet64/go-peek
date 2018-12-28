package kafka

import (
	"context"
	"fmt"
	"os"

	cluster "github.com/bsm/sarama-cluster"
	"github.com/ccdcoe/go-peek/internal/ingest/message"
)

type KafkaIngest struct {
	output chan message.Message
	ctx    context.Context
	cancel context.CancelFunc

	*cluster.Consumer
}

func NewKafkaIngest(config *KafkaConfig) (*KafkaIngest, error) {
	var (
		err error
		k   = &KafkaIngest{
			output: make(chan message.Message, 0),
		}
	)
	if config.SaramaConfig == nil {
		config.SaramaConfig = NewConsumerConfig()
	}
	fmt.Println(config.Topics)
	if k.Consumer, err = cluster.NewConsumer(
		config.Brokers,
		config.ConsumerGroup,
		config.Topics,
		config.SaramaConfig,
	); err != nil {
		return nil, err
	}
	k.ctx, k.cancel = context.WithCancel(context.Background())
	go func() {
	loop:
		for {
			select {
			case msg, ok := <-k.Consumer.Messages():
				if !ok {
					break loop
				}
				k.output <- message.Message{
					Data:   msg.Value,
					Offset: msg.Offset,
					Source: msg.Topic,
				}
			case <-k.ctx.Done():
				k.Consumer.Close()
			}
		}
	}()

	// *TODO* Move to separate notification handler
	go func() {
		for not := range k.Notifications() {
			fmt.Fprintf(os.Stdout, "%+v\n", not)
		}
	}()

	return k, nil
}

func (k KafkaIngest) Messages() <-chan message.Message {
	return k.output
}

func (k KafkaIngest) Halt() error {
	k.cancel()
	return k.ctx.Err()
}

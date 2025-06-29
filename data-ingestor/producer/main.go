package main

import (
	"context"
	"log"
	"time"

	"github.com/segmentio/kafka-go"
)

func main() {
	// Kafka broker address (your cluster)
	brokerAddress := "kafka:9092"

	// Kafka topic to send messages to
	topic := "example-topic"

	// Create a new writer (producer)
	writer := kafka.NewWriter(kafka.WriterConfig{
		Brokers:  []string{brokerAddress},
		Topic:    topic,
		Balancer: &kafka.LeastBytes{}, // balances messages between partitions
	})

	defer writer.Close()

	// Prepare a message to send
	msg := kafka.Message{
		Key:   []byte("Key-A"),                // optional, used for partitioning
		Value: []byte("Hello Kafka from Go!"), // message payload
	}

	// Send message with a timeout context
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := writer.WriteMessages(ctx, msg)
	if err != nil {
		log.Fatalf("could not write message: %v", err)
	}

	log.Println("Message sent successfully!")
}

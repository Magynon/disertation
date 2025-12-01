package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/segmentio/kafka-go"
)

func main() {
	// Kafka broker address (your cluster)
	brokerAddress := "kafka:9092"

	// Kafka topic to send messages to
	topic := "blood-tests"

	// Create a new writer (producer)
	writer := kafka.NewWriter(kafka.WriterConfig{
		Brokers:  []string{brokerAddress},
		Topic:    topic,
		Balancer: &kafka.LeastBytes{}, // balances messages between partitions
	})

	defer writer.Close()

	// Read a PNG file (example.png) to send as a message
	pngFilePath := "report_real.png"
	pngData, err := readPNG(pngFilePath)
	if err != nil {
		log.Fatalf("could not read PNG file: %v", err)
	}

	// Prepare a message to send
	msg := kafka.Message{
		Key:   []byte("JohnDoe,123"), // optional, used for partitioning
		Value: pngData,               // message payload
	}

	// Send message with a timeout context
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = writer.WriteMessages(ctx, msg)
	if err != nil {
		log.Fatalf("could not write message: %v", err)
	}

	log.Println("Message sent successfully!")
}

func readPNG(filePath string) ([]byte, error) {
	// Read the entire PNG file into memory
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read PNG file: %w", err)
	}

	return data, nil
}

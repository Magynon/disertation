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

	// Read a PDF file (example.pdf) to send as a message
	pdfFilePath := "report.pdf"
	pdfData, err := readPDF(pdfFilePath)
	if err != nil {
		log.Fatalf("could not read PDF file: %v", err)
	}

	// Prepare a message to send
	msg := kafka.Message{
		Key:   []byte("Key-A"), // optional, used for partitioning
		Value: pdfData,         // message payload
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

func readPDF(filePath string) ([]byte, error) {
	// Read the entire PDF file into memory
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read PDF file: %w", err)
	}

	return data, nil
}

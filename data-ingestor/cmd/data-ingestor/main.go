package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/segmentio/kafka-go"
)

const (
	bucketName = "my-local-bucket"
	queueName  = "ingestor-parser-queue"
	brokerAddr = "kafka:9092"
	topic      = "blood-tests"
	groupID    = "blood-test-consumer-group"
)

// ---------- Setup ----------

func newKafkaReader() *kafka.Reader {
	return kafka.NewReader(kafka.ReaderConfig{
		Brokers:  []string{brokerAddr},
		Topic:    topic,
		GroupID:  groupID,
		MinBytes: 10e3, // 10KB
		MaxBytes: 10e6, // 10MB
	})
}

func newAWSClients(ctx context.Context) (*s3.Client, *sqs.Client) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("unable to load AWS SDK config: %v", err)
	}

	s3Client := s3.New(s3.Options{
		Region:       cfg.Region,
		Credentials:  cfg.Credentials,
		HTTPClient:   cfg.HTTPClient,
		BaseEndpoint: cfg.BaseEndpoint,
		UsePathStyle: true,
	})

	sqsClient := sqs.New(sqs.Options{
		Region:       cfg.Region,
		Credentials:  cfg.Credentials,
		HTTPClient:   cfg.HTTPClient,
		BaseEndpoint: cfg.BaseEndpoint,
	})

	return s3Client, sqsClient
}

func getQueueURL(ctx context.Context, sqsClient *sqs.Client, name string) string {
	resp, err := sqsClient.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{QueueName: &name})
	if err != nil {
		log.Fatalf("failed to get SQS queue URL: %v", err)
	}
	return *resp.QueueUrl
}

// ---------- Core Logic ----------

func uploadToS3(ctx context.Context, client *s3.Client, bucket, key string, data []byte) error {
	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      &bucket,
		Key:         &key,
		Body:        bytes.NewReader(data),
		ContentType: awsString("application/pdf"),
		ACL:         types.ObjectCannedACLPrivate,
	})
	return err
}

func sendToSQS(ctx context.Context, client *sqs.Client, queueURL, bucket, key string) error {
	msg := fmt.Sprintf(`{"bucket":"%s","key":"%s","uploadedAt":"%s"}`,
		bucket, key, time.Now().Format(time.RFC3339))

	_, err := client.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    &queueURL,
		MessageBody: &msg,
	})
	if err == nil {
		log.Printf("Sent SQS message: %s", msg)
	}
	return err
}

func processMessage(ctx context.Context, s3Client *s3.Client, sqsClient *sqs.Client, queueURL string, msg kafka.Message) {
	log.Printf("Received Kafka message: Key=%s, Value size=%d bytes", string(msg.Key), len(msg.Value))

	// Generate unique filename
	timestamp := time.Now().Format("20060102_150405")
	key := fmt.Sprintf("pdfs/%s_%s.pdf", msg.Key, timestamp)

	// Upload to S3
	if err := uploadToS3(ctx, s3Client, bucketName, key, msg.Value); err != nil {
		log.Printf("failed to upload to S3: %v", err)
		return
	}
	log.Printf("Uploaded PDF to S3: s3://%s/%s", bucketName, key)

	// Notify SQS
	if err := sendToSQS(ctx, sqsClient, queueURL, bucketName, key); err != nil {
		log.Printf("failed to send message to SQS: %v", err)
	}
}

// ---------- Main ----------

func main() {
	ctx := context.Background()

	// Setup clients
	reader := newKafkaReader()
	defer reader.Close()

	log.Println("Kafka reader initialized")

	s3Client, sqsClient := newAWSClients(ctx)
	queueURL := getQueueURL(ctx, sqsClient, queueName)

	log.Printf("S3 client initialized, SQS queue URL: %s", queueURL)

	// Consume Kafka messages
	for {
		msg, err := reader.ReadMessage(ctx)
		if err != nil {
			log.Fatalf("could not read Kafka message: %v", err)
		}
		processMessage(ctx, s3Client, sqsClient, queueURL, msg)
	}
}

// ---------- Helpers ----------

func awsString(s string) *string { return &s }

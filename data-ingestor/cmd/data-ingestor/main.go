package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/segmentio/kafka-go"
)

func main() {
	brokerAddress := "kafka:9092"
	topic := "blood-tests"

	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  []string{brokerAddress},
		Topic:    topic,
		GroupID:  "blood-test-consumer-group",
		MinBytes: 10e3,
		MaxBytes: 10e6,
	})
	defer reader.Close()

	// Load AWS config
	cfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		log.Fatalf("unable to load AWS SDK config: %v", err)
	}

	// print cfg for debugging
	log.Printf("AWS Config: %+v\n", cfg)

	// Create S3 client with LocalStack endpoint and path-style addressing
	s3Client := s3.New(s3.Options{
		Region:           cfg.Region,
		Credentials:      cfg.Credentials,
		HTTPClient:       cfg.HTTPClient,
		EndpointResolver: s3.EndpointResolverFromURL(os.Getenv("AWS_ENDPOINT_URL")), // e.g. "http://localstack:4566"
		UsePathStyle:     true,
	})

	// Create SQS client with LocalStack endpoint
	sqsClient := sqs.New(sqs.Options{
		Region:           os.Getenv("AWS_REGION"),
		Credentials:      cfg.Credentials,
		HTTPClient:       cfg.HTTPClient,
		EndpointResolver: sqs.EndpointResolverFromURL(os.Getenv("AWS_ENDPOINT_URL")),
	})

	output, err := sqsClient.ListQueues(context.TODO(), &sqs.ListQueuesInput{})
	if err != nil {
		log.Fatalf("failed to list queues: %v", err)
	}

	fmt.Println("SQS Queues:")
	for _, url := range output.QueueUrls {
		fmt.Println(" -", url)
	}

	bucketName := "my-local-bucket"
	queueName := "ingestor-parser-queue"

	// Get the SQS queue URL from queue name (needed for SendMessage)
	queueURLResult, err := sqsClient.GetQueueUrl(context.TODO(), &sqs.GetQueueUrlInput{
		QueueName: &queueName,
	})
	if err != nil {
		log.Fatalf("failed to get SQS queue URL: %v", err)
	}
	queueURL := queueURLResult.QueueUrl

	for {
		msg, err := reader.ReadMessage(context.Background())
		if err != nil {
			log.Fatalf("could not read message: %v", err)
		}

		log.Printf("Received message: Key=%s, Value size=%d bytes\n", string(msg.Key), len(msg.Value))

		// Generate a filename for S3
		timestamp := time.Now().Format("20060102_150405")
		key := fmt.Sprintf("pdfs/%s_%s.pdf", msg.Key, timestamp)

		// Upload PDF to S3
		_, err = s3Client.PutObject(context.TODO(), &s3.PutObjectInput{
			Bucket:      &bucketName,
			Key:         &key,
			Body:        bytes.NewReader(msg.Value),
			ContentType: awsString("application/pdf"),
			ACL:         types.ObjectCannedACLPrivate,
		})
		if err != nil {
			log.Printf("failed to upload to S3: %v", err)
			continue
		}

		log.Printf("Uploaded PDF to S3: s3://%s/%s", bucketName, key)

		// Send a message to SQS with info about the uploaded file (you can customize this)
		sqsMsgBody := fmt.Sprintf(`{"bucket":"%s","key":"%s","uploadedAt":"%s"}`, bucketName, key, time.Now().Format(time.RFC3339))
		_, err = sqsClient.SendMessage(context.TODO(), &sqs.SendMessageInput{
			QueueUrl:    queueURL,
			MessageBody: &sqsMsgBody,
		})
		if err != nil {
			log.Printf("failed to send message to SQS: %v", err)
			continue
		}

		log.Printf("Sent SQS message for uploaded PDF: %s", sqsMsgBody)
	}
}

func awsString(s string) *string {
	return &s
}

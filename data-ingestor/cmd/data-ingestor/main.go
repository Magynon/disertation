package main

import (
	"bytes"
	"context"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/segmentio/kafka-go"
	"log"
	"os"
	"time"
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
	// Override S3 client options, set UsePathStyle and EndpointResolver for LocalStack
	s3Client := s3.New(s3.Options{
		Region:           cfg.Region,
		Credentials:      cfg.Credentials,
		HTTPClient:       cfg.HTTPClient,
		EndpointResolver: s3.EndpointResolverFromURL(os.Getenv("AWS_ENDPOINT_URL")), // e.g. "http://localstack:4566"
		UsePathStyle:     true,                                                      // <--- important for LocalStack
	})
	bucketName := "my-local-bucket"

	for {
		msg, err := reader.ReadMessage(context.Background())
		if err != nil {
			log.Fatalf("could not read message: %v", err)
		}

		log.Printf("Received message: Key=%s, Value size=%d bytes\n", string(msg.Key), len(msg.Value))

		// Generate a filename for S3
		timestamp := time.Now().Format("20060102_150405")
		key := fmt.Sprintf("pdfs/%s_%s.pdf", msg.Key, timestamp)

		// Upload to S3
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
	}
}

func awsString(s string) *string {
	return &s
}

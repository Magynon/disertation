package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/otiai10/gosseract/v2"
)

var inputQueueName = "ingestor-parser-queue"
var outputQueueName = "parser-analyzer-queue"

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

func readFromSQS(ctx context.Context, client *sqs.Client, queueURL, bucket, key string) (string, error) {

}

func getBlobFromS3() {

}

func processEvent(ocrClient *gosseract.Client) {
	ocrClient.SetImage("images/Architecture Diagram.png")
	text, err := ocrClient.Text()
	if err != nil {
		log.Fatalf("OCR failed: %v", err)
	}

	fmt.Println("Extracted text:", text)
}

func main() {
	ctx := context.Background()

	sqsClient, s3Client := newAWSClients(ctx)
	inputQueueURL := getQueueURL(ctx, sqsClient, inputQueueName)
	outputQueueURL := getQueueURL(ctx, sqsClient, outputQueueName)

	client := gosseract.NewClient()
	defer client.Close()

	// Hello, World!
}

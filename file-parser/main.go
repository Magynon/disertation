package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/otiai10/gosseract/v2"
	"github.com/samber/lo"
)

var inputQueueName = "ingestor-parser-queue"
var outputQueueName = "parser-analyzer-queue"

type DataIngestorMessage struct {
	Bucket     string `json:"bucket"`
	Key        string `json:"key"`
	PatientID  string `json:"patient_id"`
	UploadedAt string `json:"uploadedAt"`
}

type FileParserMessage struct {
	PatientID  string `json:"patientID"`
	Report     string `json:"report"`
	UploadedAt string `json:"uploadedAt"`
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

func sendToSQS(ctx context.Context, client *sqs.Client, queueURL, patientID, report string) error {
	message := FileParserMessage{
		PatientID:  patientID,
		Report:     report,
		UploadedAt: time.Now().Format(time.RFC3339),
	}

	msg, err := json.Marshal(message)
	if err != nil {
		return err
	}

	_, err = client.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    &queueURL,
		MessageBody: awsString(string(msg)),
	})
	if err == nil {
		log.Printf("Sent SQS message: %s", msg)
	}

	return err
}

func readFromSQS(ctx context.Context, client *sqs.Client, queueURL string) ([]*DataIngestorMessage, error) {
	resp, err := client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            &queueURL,
		MaxNumberOfMessages: 10,
		WaitTimeSeconds:     1,
	})
	if err != nil {
		return nil, err
	}
	if len(resp.Messages) == 0 {
		return nil, nil
	}

	return lo.Map(resp.Messages, func(msg types.Message, _ int) *DataIngestorMessage {
		var unmarshaled DataIngestorMessage
		err := json.Unmarshal([]byte(*msg.Body), &unmarshaled)
		if err == nil {
			return &unmarshaled
		}

		return &unmarshaled
	}), nil
}

func getBlobFromS3(s3Client *s3.Client, bucket, key string) ([]byte, error) {
	resp, err := s3Client.GetObject(context.Background(), &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get object from S3: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read object body: %w", err)
	}

	log.Printf("Downloaded object from S3: %d bytes", len(data))
	return data, nil
}

func applyOcrOnPdf(ocrClient *gosseract.Client, reportImg []byte) string {
	err := ocrClient.SetImageFromBytes(reportImg)
	if err != nil {
		log.Fatalf("failed to set image for OCR: %v", err)
	}

	text, err := ocrClient.Text()
	if err != nil {
		log.Fatalf("OCR failed: %v", err)
	}

	fmt.Println("Extracted text:", text)

	return text
}

func main() {
	ctx := context.Background()

	s3Client, sqsClient := newAWSClients(ctx)
	inputQueueURL := getQueueURL(ctx, sqsClient, inputQueueName)
	outputQueueURL := getQueueURL(ctx, sqsClient, outputQueueName)

	client := gosseract.NewClient()
	defer client.Close()

	for {
		messages, err := readFromSQS(ctx, sqsClient, inputQueueURL)
		if err != nil {
			log.Fatalf("failed to read from SQS: %v", err)
		}
		if len(messages) == 0 {
			continue
		}

		for _, msg := range messages {
			log.Printf("Processing message: %s", msg)

			reportImg, err := getBlobFromS3(s3Client, msg.Bucket, msg.Key)
			if err != nil {
				log.Printf("failed to get blob from S3: %v", err)
				continue
			}

			report := applyOcrOnPdf(client, reportImg)

			b64Report := base64.StdEncoding.EncodeToString([]byte(report))
			patientID := msg.PatientID

			err = sendToSQS(ctx, sqsClient, outputQueueURL, patientID, b64Report)
			if err != nil {
				log.Printf("failed to send to output SQS: %v", err)
			}
		}
	}
}

func awsString(s string) *string { return &s }

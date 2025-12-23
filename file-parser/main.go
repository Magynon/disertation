package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/otiai10/gosseract/v2"
	"github.com/samber/lo"
)

const (
	inputQueueName  = "ingestor-parser-queue"
	outputQueueName = "parser-analyzer-queue"
	maxWorkers      = 5
	maxRetries      = 3
	pollInterval    = 5 * time.Second
)

type DataIngestorMessage struct {
	Bucket        string `json:"bucket"`
	Key           string `json:"key"`
	PatientID     int64  `json:"patient_id"`
	UploadedAt    string `json:"uploadedAt"`
	ReceiptHandle string `json:"receiptHandle"`
}

type FileParserMessage struct {
	PatientID  int64  `json:"patientID"`
	Report     string `json:"report"`
	UploadedAt string `json:"uploadedAt"`
}

type ProcessingJob struct {
	message *DataIngestorMessage
	retries int
}

type Worker struct {
	id             int
	s3Client       *s3.Client
	sqsClient      *sqs.Client
	ocrClient      *gosseract.Client
	inputQueueURL  string
	outputQueueURL string
}

// ---------- Setup ----------

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

// ---------- Worker Methods ----------

func (w *Worker) processMessage(ctx context.Context, job ProcessingJob) error {
	msg := job.message
	log.Printf("[Worker %d] Processing message for patient %d, key: %s", w.id, msg.PatientID, msg.Key)

	// Download from S3
	reportImg, err := w.downloadFromS3(ctx, msg.Bucket, msg.Key)
	if err != nil {
		return fmt.Errorf("failed to download from S3: %w", err)
	}

	// Apply OCR
	report, err := w.applyOCR(reportImg)
	if err != nil {
		return fmt.Errorf("OCR failed: %w", err)
	}

	// Send to output queue
	b64Report := base64.StdEncoding.EncodeToString([]byte(report))
	if err := w.sendToOutputQueue(ctx, b64Report, msg.PatientID); err != nil {
		return fmt.Errorf("failed to send to output queue: %w", err)
	}

	// Delete from input queue only after successful processing
	if err := w.deleteFromInputQueue(ctx, msg); err != nil {
		log.Printf("[Worker %d] Warning: failed to delete message from input queue: %v", w.id, err)
	}

	log.Printf("[Worker %d] Successfully processed message for patient %d", w.id, msg.PatientID)
	return nil
}

func (w *Worker) downloadFromS3(ctx context.Context, bucket, key string) ([]byte, error) {
	resp, err := w.s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	log.Printf("[Worker %d] Downloaded %d bytes from S3", w.id, len(data))
	return data, nil
}

func (w *Worker) applyOCR(reportImg []byte) (string, error) {
	if err := w.ocrClient.SetImageFromBytes(reportImg); err != nil {
		return "", err
	}

	text, err := w.ocrClient.Text()
	if err != nil {
		return "", err
	}

	log.Printf("[Worker %d] OCR extracted %d characters", w.id, len(text))
	return text, nil
}

func (w *Worker) sendToOutputQueue(ctx context.Context, report string, patientID int64) error {
	message := FileParserMessage{
		PatientID:  patientID,
		Report:     report,
		UploadedAt: time.Now().Format(time.RFC3339),
	}

	msg, err := json.Marshal(message)
	if err != nil {
		return err
	}

	_, err = w.sqsClient.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    &w.outputQueueURL,
		MessageBody: awsString(string(msg)),
	})

	if err == nil {
		log.Printf("[Worker %d] Sent message to output queue for patient %d", w.id, patientID)
	}
	return err
}

func (w *Worker) deleteFromInputQueue(ctx context.Context, msg *DataIngestorMessage) error {
	_, err := w.sqsClient.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      &w.inputQueueURL,
		ReceiptHandle: awsString(msg.ReceiptHandle),
	})
	if err == nil {
		log.Printf("[Worker %d] Deleted message from input queue", w.id)
	}
	return err
}

// ---------- Queue Operations ----------

func pollMessages(ctx context.Context, sqsClient *sqs.Client, queueURL string, jobChan chan<- ProcessingJob) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			messages, err := readFromSQS(ctx, sqsClient, queueURL)
			if err != nil {
				log.Printf("Error reading from SQS: %v", err)
				time.Sleep(pollInterval)
				continue
			}

			for _, msg := range messages {
				if msg != nil {
					jobChan <- ProcessingJob{message: msg, retries: 0}
				}
			}

			if len(messages) == 0 {
				time.Sleep(pollInterval)
			}
		}
	}
}

func readFromSQS(ctx context.Context, client *sqs.Client, queueURL string) ([]*DataIngestorMessage, error) {
	resp, err := client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            &queueURL,
		MaxNumberOfMessages: 10,
		WaitTimeSeconds:     20, // Long polling
		VisibilityTimeout:   30, // Give workers 30s to process
	})
	if err != nil {
		return nil, err
	}

	return lo.Map(resp.Messages, func(msg types.Message, _ int) *DataIngestorMessage {
		var unmarshaled DataIngestorMessage
		if err := json.Unmarshal([]byte(*msg.Body), &unmarshaled); err != nil {
			log.Printf("Failed to unmarshal SQS message: %v", err)
			return nil
		}
		unmarshaled.ReceiptHandle = *msg.ReceiptHandle
		return &unmarshaled
	}), nil
}

// ---------- Worker Pool ----------

func startWorker(ctx context.Context, id int, jobChan <-chan ProcessingJob, retryChan chan<- ProcessingJob, wg *sync.WaitGroup, config WorkerConfig) {
	defer wg.Done()

	worker := &Worker{
		id:             id,
		s3Client:       config.s3Client,
		sqsClient:      config.sqsClient,
		ocrClient:      gosseract.NewClient(),
		inputQueueURL:  config.inputQueueURL,
		outputQueueURL: config.outputQueueURL,
	}
	defer worker.ocrClient.Close()

	log.Printf("Worker %d started", id)

	for {
		select {
		case <-ctx.Done():
			log.Printf("Worker %d shutting down", id)
			return
		case job, ok := <-jobChan:
			if !ok {
				log.Printf("Worker %d: job channel closed", id)
				return
			}

			if err := worker.processMessage(ctx, job); err != nil {
				log.Printf("[Worker %d] Error processing message: %v", id, err)

				// Retry logic
				if job.retries < maxRetries {
					job.retries++
					log.Printf("[Worker %d] Retrying message (attempt %d/%d)", id, job.retries, maxRetries)
					select {
					case retryChan <- job:
					case <-ctx.Done():
						return
					}
				} else {
					log.Printf("[Worker %d] Message failed after %d retries, moving to DLQ", id, maxRetries)
				}
			}
		}
	}
}

type WorkerConfig struct {
	s3Client       *s3.Client
	sqsClient      *sqs.Client
	inputQueueURL  string
	outputQueueURL string
}

// ---------- Main ----------

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize AWS clients
	s3Client, sqsClient := newAWSClients(ctx)
	inputQueueURL := getQueueURL(ctx, sqsClient, inputQueueName)
	outputQueueURL := getQueueURL(ctx, sqsClient, outputQueueName)

	log.Printf("Starting file parser with %d workers", maxWorkers)
	log.Printf("Input queue: %s", inputQueueURL)
	log.Printf("Output queue: %s", outputQueueURL)

	// Create channels
	jobChan := make(chan ProcessingJob, maxWorkers*2)
	retryChan := make(chan ProcessingJob, maxWorkers)

	// Worker configuration
	config := WorkerConfig{
		s3Client:       s3Client,
		sqsClient:      sqsClient,
		inputQueueURL:  inputQueueURL,
		outputQueueURL: outputQueueURL,
	}

	// Start workers
	var wg sync.WaitGroup
	for i := 1; i <= maxWorkers; i++ {
		wg.Add(1)
		go startWorker(ctx, i, jobChan, retryChan, &wg, config)
	}

	// Start retry handler
	go func() {
		for job := range retryChan {
			time.Sleep(time.Duration(job.retries) * time.Second)
			jobChan <- job
		}
	}()

	// Start message polling
	go pollMessages(ctx, sqsClient, inputQueueURL, jobChan)

	// Wait for interrupt signal
	sigChan := make(chan struct{})
	go func() {
		// Block forever or until interrupted
		select {}
	}()

	<-sigChan
	cancel()
	close(jobChan)
	wg.Wait()
	log.Println("All workers finished")
}

func awsString(s string) *string { return &s }

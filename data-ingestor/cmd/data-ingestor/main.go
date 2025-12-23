package main

import (
	"app/internal/model"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/segmentio/kafka-go"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

const (
	bucketName = "my-local-bucket"
	queueName  = "ingestor-parser-queue"
	brokerAddr = "kafka:9092"
	topic      = "blood-tests"
	groupID    = "blood-test-consumer-group"
	dbDSN      = "postgres://user:password@postgres:5432/postgres?sslmode=disable"

	// Concurrency settings
	maxWorkers = 10
	maxRetries = 3
)

type DataIngestorMessage struct {
	Bucket     string `json:"bucket"`
	Key        string `json:"key"`
	PatientID  int64  `json:"patient_id"`
	UploadedAt string `json:"uploadedAt"`
}

type ProcessingTask struct {
	msg         kafka.Message
	patientName string
	patientSSN  string
	s3Key       string
}

type ProcessingResult struct {
	patientID int64
	s3Key     string
	bucket    string
	err       error
}

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

// ---------- DB ----------

func newDB() *gorm.DB {
	db, err := gorm.Open(postgres.Open(dbDSN), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
		NamingStrategy: schema.NamingStrategy{
			SingularTable: true,
		},
	})
	if err != nil {
		log.Fatalf("failed to connect to DB: %v", err)
	}

	return db
}

// ---------- Core Logic with Goroutines ----------

func processMessageConcurrent(ctx context.Context, s3Client *s3.Client, sqsClient *sqs.Client, queueURL string, msg kafka.Message, db *gorm.DB) {
	log.Printf("Received Kafka message: Key=%s, Value size=%d bytes", string(msg.Key), len(msg.Value))

	// Parse patient data
	patientName, patientSSN, err := parsePatientData(msg.Key)
	if err != nil {
		log.Printf("failed to parse patient data: %v", err)
		return
	}

	s3Key := generateS3Key(msg.Key)
	task := ProcessingTask{
		msg:         msg,
		patientName: patientName,
		patientSSN:  patientSSN,
		s3Key:       s3Key,
	}

	var wg sync.WaitGroup
	resultChan := make(chan ProcessingResult, 3)

	// Upload to S3 concurrently
	wg.Add(1)
	go func() {
		defer wg.Done()
		uploadWithRetry(ctx, s3Client, task, resultChan)
	}()

	// Process patient data concurrently
	wg.Add(1)
	go func() {
		defer wg.Done()
		processPatientData(db, task, resultChan)
	}()

	// Wait for initial operations to complete
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Collect results
	var patientID int64
	var uploadSucceeded bool
	for result := range resultChan {
		if result.err != nil {
			log.Printf("Processing error: %v", result.err)
			continue
		}
		if result.patientID > 0 {
			patientID = result.patientID
		}
		if result.s3Key != "" {
			uploadSucceeded = true
		}
	}

	// Only proceed if both operations succeeded
	if patientID > 0 && uploadSucceeded {
		var finalWg sync.WaitGroup

		// Create records and send SQS message concurrently
		finalWg.Add(2)

		go func() {
			defer finalWg.Done()
			if err := createRecordsAndQueue(db, patientID, s3Key); err != nil {
				log.Printf("failed to create records: %v", err)
			}
		}()

		go func() {
			defer finalWg.Done()
			if err := sendToSQSWithRetry(ctx, sqsClient, queueURL, bucketName, s3Key, patientID); err != nil {
				log.Printf("failed to send SQS message: %v", err)
			}
		}()

		finalWg.Wait()
	}
}

func uploadWithRetry(ctx context.Context, client *s3.Client, task ProcessingTask, resultChan chan<- ProcessingResult) {
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		if err := uploadToS3(ctx, client, bucketName, task.s3Key, task.msg.Value); err != nil {
			lastErr = err
			time.Sleep(time.Duration(i+1) * time.Second)
			continue
		}
		log.Printf("Uploaded PNG to S3: s3://%s/%s", bucketName, task.s3Key)
		resultChan <- ProcessingResult{s3Key: task.s3Key, bucket: bucketName}
		return
	}
	resultChan <- ProcessingResult{err: fmt.Errorf("upload failed after %d retries: %w", maxRetries, lastErr)}
}

func processPatientData(db *gorm.DB, task ProcessingTask, resultChan chan<- ProcessingResult) {
	patient, err := upsertPatient(db, task.patientName, task.patientSSN)
	if err != nil {
		resultChan <- ProcessingResult{err: fmt.Errorf("patient processing failed: %w", err)}
		return
	}
	resultChan <- ProcessingResult{patientID: patient.ID}
}

func sendToSQSWithRetry(ctx context.Context, client *sqs.Client, queueURL, bucket, key string, patientID int64) error {
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		if err := sendToSQS(ctx, client, queueURL, bucket, key, patientID); err != nil {
			lastErr = err
			time.Sleep(time.Duration(i+1) * time.Second)
			continue
		}
		return nil
	}
	return fmt.Errorf("SQS send failed after %d retries: %w", maxRetries, lastErr)
}

func uploadToS3(ctx context.Context, client *s3.Client, bucket, key string, data []byte) error {
	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      &bucket,
		Key:         &key,
		Body:        bytes.NewReader(data),
		ContentType: awsString("image/png"),
		ACL:         types.ObjectCannedACLPrivate,
	})
	return err
}

func sendToSQS(ctx context.Context, client *sqs.Client, queueURL, bucket, key string, patientID int64) error {
	message := DataIngestorMessage{
		Bucket:     bucket,
		Key:        key,
		PatientID:  patientID,
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

func parsePatientData(key []byte) (name, ssn string, err error) {
	parts := bytes.SplitN(key, []byte(","), 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid key format, expected 'Name,SSN': %s", key)
	}
	return string(parts[0]), string(parts[1]), nil
}

func generateS3Key(messageKey []byte) string {
	timestamp := time.Now().Format("20060102_150405")
	return fmt.Sprintf("pngs/%s_%s.png", messageKey, timestamp)
}

func upsertPatient(db *gorm.DB, name, ssn string) (*model.Patient, error) {
	var patient model.Patient

	err := db.Where("ssn = ?", ssn).First(&patient).Error
	if err == nil {
		log.Printf("Patient already exists: ID=%d, Name=%s", patient.ID, patient.Name)
		return &patient, nil
	}

	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("query patient: %w", err)
	}

	patient = model.Patient{
		Ssn:  ssn,
		Name: name,
	}
	if err := db.Create(&patient).Error; err != nil {
		return nil, fmt.Errorf("create patient: %w", err)
	}

	log.Printf("Created new patient: ID=%d, Name=%s", patient.ID, patient.Name)
	return &patient, nil
}

func createRecordsAndQueue(db *gorm.DB, patientID int64, cloudRef string) error {
	return db.Transaction(func(tx *gorm.DB) error {
		record := &model.Record{
			CloudReference: cloudRef,
		}
		if err := tx.Create(record).Error; err != nil {
			return fmt.Errorf("create record: %w", err)
		}

		mapping := &model.PatientRecordMap{
			UserID:   patientID,
			RecordID: record.ID,
		}
		if err := tx.Create(mapping).Error; err != nil {
			return fmt.Errorf("create patient-record mapping: %w", err)
		}
		log.Printf("Mapped patient ID=%d to record ID=%d", patientID, record.ID)

		queueEntry := &model.MessageQueue{
			Type: model.MessageTypeParse,
			Data: []byte(fmt.Sprintf(`{"cloudReference":"%s"}`, cloudRef)),
		}
		if err := tx.Create(queueEntry).Error; err != nil {
			return fmt.Errorf("create queue entry: %w", err)
		}
		log.Printf("Created message queue entry ID=%d for parsing", queueEntry.ID)

		return nil
	})
}

// ---------- Main with Worker Pool ----------

func main() {
	ctx := context.Background()

	db := newDB()
	log.Println("Connected to DB")

	reader := newKafkaReader()
	defer reader.Close()
	log.Println("Kafka reader initialized")

	s3Client, sqsClient := newAWSClients(ctx)
	queueURL := getQueueURL(ctx, sqsClient, queueName)
	log.Printf("S3 client initialized, SQS queue URL: %s", queueURL)

	// Create worker pool
	msgChan := make(chan kafka.Message, maxWorkers)
	var wg sync.WaitGroup

	// Start workers
	for i := 0; i < maxWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			log.Printf("Worker %d started", workerID)
			for msg := range msgChan {
				processMessageConcurrent(ctx, s3Client, sqsClient, queueURL, msg, db)
			}
			log.Printf("Worker %d stopped", workerID)
		}(i)
	}

	// Read messages and send to workers
	go func() {
		for {
			msg, err := reader.ReadMessage(ctx)
			if err != nil {
				log.Printf("Error reading Kafka message: %v", err)
				close(msgChan)
				break
			}
			msgChan <- msg
		}
	}()

	wg.Wait()
	log.Println("All workers finished")
}

func awsString(s string) *string { return &s }

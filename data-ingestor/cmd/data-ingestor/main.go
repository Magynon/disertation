package main

import (
	"app/internal/model"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
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

	dbDSN = "postgres://user:password@postgres:5432/postgres?sslmode=disable"
)

type DataIngestorMessage struct {
	Bucket     string `json:"bucket"`
	Key        string `json:"key"`
	PatientID  string `json:"patient_id"`
	UploadedAt string `json:"uploadedAt"`
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

// ---------- Core Logic ----------

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

func sendToSQS(ctx context.Context, client *sqs.Client, queueURL, bucket, key, ssn string) error {
	message := DataIngestorMessage{
		Bucket:     bucket,
		Key:        key,
		PatientID:  ssn,
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

func processMessage(ctx context.Context, s3Client *s3.Client, sqsClient *sqs.Client, queueURL string, msg kafka.Message, db *gorm.DB) {
	log.Printf("Received Kafka message: Key=%s, Value size=%d bytes", string(msg.Key), len(msg.Value))

	patientData := bytes.SplitN(msg.Key, []byte(","), 2)
	if len(patientData) != 2 {
		log.Printf("invalid message key format, expected 'Name,ID': %s", msg.Key)
		return
	}

	patientName := string(patientData[0])
	patientSSN := string(patientData[1])

	// Generate unique filename
	timestamp := time.Now().Format("20060102_150405")
	key := fmt.Sprintf("pngs/%s_%s.png", msg.Key, timestamp)

	// Upload to S3
	if err := uploadToS3(ctx, s3Client, bucketName, key, msg.Value); err != nil {
		log.Printf("failed to upload to S3: %v", err)
		return
	}
	log.Printf("Uploaded PNG to S3: s3://%s/%s", bucketName, key)

	// Notify SQS
	if err := sendToSQS(ctx, sqsClient, queueURL, bucketName, key, patientSSN); err != nil {
		log.Printf("failed to send message to SQS: %v", err)
	}

	// Insert patient if not exists
	var patient model.Patient
	if err := db.Where("ssn = ?", patientSSN).First(&patient).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			patient = model.Patient{
				Ssn:  patientSSN,
				Name: patientName,
			}
			if err := db.Create(&patient).Error; err != nil {
				log.Printf("failed to insert patient into DB: %v", err)
				return
			}
			log.Printf("Inserted new patient: ID=%d, Name=%s", patient.ID, patient.Name)
		} else {
			log.Printf("failed to query patient: %v", err)
			return
		}
	} else {
		log.Printf("Patient already exists: ID=%d, Name=%s", patient.ID, patient.Name)
	}

	record := &model.Record{
		CloudReference: key,
	}
	if err := db.Create(record).Error; err != nil {
		log.Printf("failed to insert record into DB: %v", err)
	}

	patientRecordMap := &model.PatientRecordMap{
		UserID:   patient.ID,
		RecordID: record.ID,
	}
	if err := db.Create(patientRecordMap).Error; err != nil {
		log.Printf("failed to map patient to record in DB: %v", err)
	} else {
		log.Printf("Mapped patient ID=%d to record ID=%d", patient.ID, record.ID)
	}

	messageQueue := &model.MessageQueue{
		Type: model.MessageTypeParse,
		Data: []byte(fmt.Sprintf(`{"cloudReference":"%s"}`, key)),
	}
	if err := db.Create(messageQueue).Error; err != nil {
		log.Printf("failed to insert message queue entry into DB: %v", err)
	} else {
		log.Printf("Inserted message queue entry ID=%d for parsing", messageQueue.ID)
	}
}

// ---------- Main ----------

func main() {
	ctx := context.Background()

	db := newDB()
	log.Println("Connected to DB")

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
		processMessage(ctx, s3Client, sqsClient, queueURL, msg, db)
	}
}

// ---------- Helpers ----------

func awsString(s string) *string { return &s }

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log"
	"trend-analyzer/internal/model"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/samber/lo"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

const (
	dbDSN = "postgres://user:password@postgres:5432/postgres?sslmode=disable"

	inputQueueName  = "parser-analyzer-queue"
	outputQueueName = "analyzer-etl-queue"
)

type FileParserMessage struct {
	PatientID  int64  `json:"patientID"`
	Report     string `json:"report"`
	UploadedAt string `json:"uploadedAt"`
}

type MLResponse struct {
	Diagnosis string `json:"diagnosis"`
	Concern   bool   `json:"concern"`
}

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

func newSQSClient(ctx context.Context) *sqs.Client {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("unable to load AWS SDK config: %v", err)
	}

	sqsClient := sqs.New(sqs.Options{
		Region:       cfg.Region,
		Credentials:  cfg.Credentials,
		HTTPClient:   cfg.HTTPClient,
		BaseEndpoint: cfg.BaseEndpoint,
	})

	return sqsClient
}

func getQueueURL(ctx context.Context, sqsClient *sqs.Client, name string) string {
	resp, err := sqsClient.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{QueueName: &name})
	if err != nil {
		log.Fatalf("failed to get SQS queue URL: %v", err)
	}
	return *resp.QueueUrl
}

func readFromSQS(ctx context.Context, client *sqs.Client, queueURL string) ([]*FileParserMessage, error) {
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

	return lo.Map(resp.Messages, func(msg types.Message, _ int) *FileParserMessage {
		var unmarshaled FileParserMessage
		err := json.Unmarshal([]byte(*msg.Body), &unmarshaled)
		if err == nil {
			return &unmarshaled
		}

		return &unmarshaled
	}), nil
}

func emitSNSNotification(db *gorm.DB, patientID int64) error {
	patient := &model.Patient{}
	result := db.Where("id = ?", patientID).First(patient)
	if result.Error != nil {
		log.Printf("failed to get patient %d for SNS notification: %v", patientID, result.Error)
		return result.Error
	}

	log.Printf("Emitting SNS notification for patient %s (placeholder)", patient.Name)
	return nil
}

func getSanitizedReport(report string) (string, error) {
	decoded, err := base64.StdEncoding.DecodeString(report)
	if err != nil {
		return "", err
	}

	decodedString := string(decoded)
	// Placeholder for actual sanitization logic
	sanitizedReport := decodedString

	encoded := base64.StdEncoding.EncodeToString([]byte(sanitizedReport))

	return encoded, nil
}

func saveCurrentStateForPatient(db *gorm.DB, report string, patientID int64) error {
	patient := &model.Patient{}
	result := db.Where("id = ?", patientID).First(patient)
	if result.Error != nil {
		return result.Error
	}

	state := &model.State{
		Data: report,
	}
	result = db.Create(state)
	if result.Error != nil {
		return result.Error
	}

	patientStateMap := &model.PatientStateMap{
		UserID:  patient.ID,
		StateID: state.ID,
	}
	result = db.Create(patientStateMap)
	if result.Error != nil {
		return result.Error
	}

	patient.CurrentState = &state.ID
	result = db.Save(patient)
	if result.Error != nil {
		return result.Error
	}

	return nil
}

func diagnosePatient(db *gorm.DB, patientID int64) *MLResponse {
	var patientStateMaps []model.PatientStateMap
	result := db.Where("user_id = ?", patientID).Find(&patientStateMaps)
	if result.Error != nil {
		log.Printf("failed to get state maps for patient %d: %v", patientID, result.Error)
		return nil
	}

	var patientHistory string

	for _, psm := range patientStateMaps {
		state := &model.State{}
		result := db.Where("id = ?", psm.StateID).First(state)
		if result.Error != nil {
			log.Printf("failed to get state %d for patient %d: %v", psm.StateID, patientID, result.Error)
			continue
		}

		patientHistory += state.Data + "\n"
	}

	prompt := "Based on the following patient history, provide a trend analysis and see if you can spot any concerning patterns:\n" + patientHistory +
		"\nPlease provide your analysis following this json format:\n" +
		`{
  "diagnosis": "string",
  "concern": "boolean",
}`

	// Placeholder for AI model interaction
	log.Printf("AI Prompt for patient %d:\n%s", patientID, prompt)

	mlResponse := queryML()
	log.Printf("Diagnosis for patient %d completed (placeholder)", patientID)

	return mlResponse
}

func queryML() *MLResponse {
	// Placeholder for actual ML model querying logic
	return &MLResponse{
		Diagnosis: "No concerning patterns detected.",
		Concern:   false,
	}
}

func main() {
	ctx := context.Background()

	db := newDB()
	log.Println("Connected to DB")

	sqsClient := newSQSClient(ctx)
	inputQueueURL := getQueueURL(ctx, sqsClient, inputQueueName)
	_ = getQueueURL(ctx, sqsClient, outputQueueName)

	for {
		incomingSQSMessages, err := readFromSQS(context.Background(), sqsClient, inputQueueURL)
		if err != nil {
			log.Fatalf("failed to read from SQS: %v", err)
		}

		for _, msg := range incomingSQSMessages {
			report, err := getSanitizedReport(msg.Report)
			if err != nil {
				log.Printf("failed to sanitize report for patient %d: %v", msg.PatientID, err)
			}

			err = saveCurrentStateForPatient(db, report, msg.PatientID)
			if err != nil {
				log.Printf("failed to save current state for patient %d: %v", msg.PatientID, err)
			}

			// TODO ETL logic to come here

			patientTrend := diagnosePatient(db, msg.PatientID)
			log.Printf("Patient %d trend analysis: %+v", msg.PatientID, patientTrend)

			if patientTrend.Concern {
				err = emitSNSNotification(db, msg.PatientID)
				if err != nil {
					log.Printf("failed to emit SNS notification for patient %d: %v", msg.PatientID, err)
				}
			}
		}
	}
}

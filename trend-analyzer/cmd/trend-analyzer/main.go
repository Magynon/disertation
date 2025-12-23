package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
	"trend-analyzer/internal/model"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sns"
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

	snsTopicName = "system-events"
)

type FileParserMessage struct {
	PatientID  int64  `json:"patientID"`
	Report     string `json:"report"`
	UploadedAt string `json:"uploadedAt"`

	ReceiptHandle string `json:"receiptHandle"`
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
		if err != nil {
			log.Printf("failed to unmarshal SQS message: %v", err)
			return &unmarshaled
		}

		unmarshaled.ReceiptHandle = *msg.ReceiptHandle

		return &unmarshaled
	}), nil
}

func deleteMessageFromSQS(ctx context.Context, client *sqs.Client, queueURL string, msg *FileParserMessage) error {
	_, err := client.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      &queueURL,
		ReceiptHandle: awsString(msg.ReceiptHandle),
	})
	if err == nil {
		log.Printf("Deleted SQS message: %s", msg.ReceiptHandle)
	}

	return err
}

// Add SNS client initialization
func newSNSClient(ctx context.Context) *sns.Client {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("unable to load AWS SDK config: %v", err)
	}

	snsClient := sns.New(sns.Options{
		Region:       cfg.Region,
		Credentials:  cfg.Credentials,
		HTTPClient:   cfg.HTTPClient,
		BaseEndpoint: cfg.BaseEndpoint,
	})

	return snsClient
}

// Get or create SNS topic
func getTopicArn(ctx context.Context, snsClient *sns.Client, topicName string) string {
	// Try to create topic (idempotent - returns existing if already created)
	resp, err := snsClient.CreateTopic(ctx, &sns.CreateTopicInput{
		Name: awsString(topicName),
	})
	if err != nil {
		log.Fatalf("failed to create/get SNS topic: %v", err)
	}
	return *resp.TopicArn
}

// Update the emitSNSNotification function
func emitSNSNotification(ctx context.Context, snsClient *sns.Client, topicArn string, db *gorm.DB, patientID int64, diagnosis *MLResponse) error {
	patient := &model.Patient{}
	result := db.Where("id = ?", patientID).First(patient)
	if result.Error != nil {
		log.Printf("failed to get patient %d for SNS notification: %v", patientID, result.Error)
		return result.Error
	}

	// Create notification message
	message := map[string]interface{}{
		"patientId":   patientID,
		"patientName": patient.Name,
		"diagnosis":   diagnosis.Diagnosis,
		"concern":     diagnosis.Concern,
		"timestamp":   time.Now().Format(time.RFC3339),
	}

	messageJSON, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("failed to marshal SNS message: %w", err)
	}

	// Publish to SNS
	_, err = snsClient.Publish(ctx, &sns.PublishInput{
		TopicArn: awsString(topicArn),
		Message:  awsString(string(messageJSON)),
		Subject:  awsString(fmt.Sprintf("Health Concern Alert - Patient %s", patient.Name)),
	})

	if err != nil {
		return fmt.Errorf("failed to publish SNS message: %w", err)
	}

	log.Printf("Emitted SNS notification for patient %s (ID: %d)", patient.Name, patientID)
	return nil
}

func getSanitizedReport(report string) (string, error) {
	decoded, err := base64.StdEncoding.DecodeString(report)
	if err != nil {
		return "", err
	}

	decodedString := string(decoded)
	prompt := "Sanitize the following medical report by removing any irrelevant, personally identifiable information (PII) such as names, dates, addresses, phone numbers, and any other sensitive data." +
		"The report might contain some weird data because it has been through an OCR process. This was a table originally so keep that in mind when you fix the data. Please only include the report and nothing else in your answer.\n\n" + decodedString

	response, err := sendToGemini(prompt)
	if err != nil {
		return "", err
	}

	log.Printf("Sanitized report in Gemini: %s", response)

	return base64.StdEncoding.EncodeToString([]byte(response)), nil
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

	denormalizedData := &model.DenormalizedData{
		State:       report,
		PatientName: patient.Name,
		PatientSsn:  patient.Ssn,
	}
	if err := db.Create(denormalizedData).Error; err != nil {
		log.Printf("failed to insert denormalized data into DB: %v", err)
	} else {
		log.Printf("Inserted denormalized data ID=%d for patient ID=%d", denormalizedData.ID, patient.ID)
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

		decoded, err := base64.StdEncoding.DecodeString(state.Data)
		if err != nil {
			log.Printf("failed to decode state data for state %d of patient %d: %v", psm.StateID, patientID, err)
			continue
		}

		patientHistory += string(decoded) + "\n"
	}

	prompt := "Based on the following patient history, provide a trend analysis and see if you can spot any concerning patterns:\n" + patientHistory +
		"\nPlease provide your analysis following this json format:\n" +
		`{
  "diagnosis": "string",
  "concern": "boolean",
}` + "It is very important that you strictly follow the json format and nothing else because I am going to parse it programmatically."

	textResponse, err := sendToGemini(prompt)
	if err != nil {
		log.Printf("failed to get response from Gemini for patient %d: %v", patientID, err)
		return nil
	}

	textResponse = strings.TrimPrefix(textResponse, "```json")
	textResponse = strings.TrimSuffix(textResponse, "```")

	var mlResponse MLResponse
	err = json.Unmarshal([]byte(textResponse), &mlResponse)
	if err != nil {
		log.Printf("failed to unmarshal Gemini response for patient %d: %s, %v", patientID, textResponse, err)
		return nil
	}

	log.Printf("Diagnosis for patient %d completed (placeholder)", patientID)

	return &mlResponse
}

func sendToGemini(prompt string) (string, error) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return "", fmt.Errorf("GEMINI_API_KEY not set")
	}

	url := "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.0-flash:generateContent?key=" + apiKey

	reqBody := model.GeminiRequest{
		Contents: []model.Content{
			{
				Parts: []model.Part{
					{Text: prompt},
				},
			},
		},
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("gemini error: %s", body)
	}

	var geminiResp model.GeminiResponse
	if err := json.NewDecoder(resp.Body).Decode(&geminiResp); err != nil {
		return "", err
	}

	if len(geminiResp.Candidates) == 0 ||
		len(geminiResp.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("empty response from Gemini")
	}

	return geminiResp.Candidates[0].Content.Parts[0].Text, nil
}

func main() {
	ctx := context.Background()

	db := newDB()
	log.Println("Connected to DB")

	sqsClient := newSQSClient(ctx)
	snsClient := newSNSClient(ctx)
	inputQueueURL := getQueueURL(ctx, sqsClient, inputQueueName)
	_ = getQueueURL(ctx, sqsClient, outputQueueName)
	topicArn := getTopicArn(ctx, snsClient, snsTopicName)

	for {
		incomingSQSMessages, err := readFromSQS(context.Background(), sqsClient, inputQueueURL)
		if err != nil {
			log.Fatalf("failed to read from SQS: %v", err)
		}

		for _, msg := range incomingSQSMessages {
			log.Printf("Processing message for patient %d", msg.PatientID)

			err = deleteMessageFromSQS(context.Background(), sqsClient, inputQueueURL, msg)
			if err != nil {
				log.Printf("failed to delete message from SQS for patient %d: %v", msg.PatientID, err)
				continue
			}

			report, err := getSanitizedReport(msg.Report)
			if err != nil {
				log.Printf("failed to sanitize report for patient %d: %v", msg.PatientID, err)
			}

			err = saveCurrentStateForPatient(db, report, msg.PatientID)
			if err != nil {
				log.Printf("failed to save current state for patient %d: %v", msg.PatientID, err)
			}

			patientTrend := diagnosePatient(db, msg.PatientID)
			log.Printf("Patient %d trend analysis: %+v", msg.PatientID, patientTrend)

			if patientTrend.Concern {
				err = emitSNSNotification(ctx, snsClient, topicArn, db, msg.PatientID, patientTrend)
				if err != nil {
					log.Printf("failed to emit SNS notification for patient %d: %v", msg.PatientID, err)
				}
			}
		}
	}
}

func awsString(s string) *string { return &s }

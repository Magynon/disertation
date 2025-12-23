package model

import (
	"time"

	"gorm.io/datatypes"
)

type DenormalizedData struct {
	ID          int64     `gorm:"primaryKey;autoIncrement"`
	State       string    `gorm:"type:text;not null"`
	PatientName string    `gorm:"type:varchar(200);not null"`
	PatientSsn  string    `gorm:"type:varchar(11);not null"`
	CreatedAt   time.Time `gorm:"autoCreateTime"`
}

// Patient represents the patient table
type Patient struct {
	ID           int64      `gorm:"primaryKey;autoIncrement"`
	Name         string     `gorm:"type:varchar(200);not null"`
	Ssn          string     `gorm:"type:varchar(11);unique;not null"`
	CurrentState *int64     `gorm:"column:current_state"` // FK to state.id
	CreatedAt    time.Time  `gorm:"autoCreateTime"`
	UpdatedAt    time.Time  `gorm:"autoUpdateTime"`
	DeletedAt    *time.Time `gorm:"index"`
}

// PatientRecordMap maps patients to records (many-to-many)
type PatientRecordMap struct {
	UserID   int64 `gorm:"primaryKey"` // FK to patient.id
	RecordID int64 `gorm:"primaryKey"` // FK to record.id
}

// Record represents the record table
type Record struct {
	ID             int64          `gorm:"primaryKey;autoIncrement"`
	CloudReference string         `gorm:"type:text;not null"`
	Metadata       datatypes.JSON `gorm:"type:jsonb"`
	CreatedAt      time.Time      `gorm:"autoCreateTime"`
	DeletedAt      *time.Time     `gorm:"index"`
}

// MessageQueue represents the message_queue table
type MessageQueue struct {
	ID     int64          `gorm:"primaryKey;autoIncrement"`
	Type   MessageType    `gorm:"type:message_type;not null"`
	Data   datatypes.JSON `gorm:"type:jsonb"`
	Status MessageStatus  `gorm:"type:message_status;not null;default:READY"`
}

type MessageType string

const (
	MessageTypeParse  MessageType = "PARSE"
	MessageTypeAssess MessageType = "ASSESS"
	MessageTypeNotify MessageType = "NOTIFY"
)

type MessageStatus string

const (
	MessageStatusReady     MessageStatus = "READY"
	MessageStatusRetry     MessageStatus = "RETRY"
	MessageStatusPublished MessageStatus = "PUBLISHED"
)

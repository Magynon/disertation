package model

import "time"

type DenormalizedData struct {
	ID          int64     `gorm:"primaryKey;autoIncrement"`
	State       string    `gorm:"type:text;not null"`
	Diagnosis   string    `gorm:"type:text;not null"`
	Concern     bool      `gorm:"not null"`
	PatientName string    `gorm:"type:varchar(200);not null"`
	PatientSsn  string    `gorm:"type:varchar(11);not null"`
	CreatedAt   time.Time `gorm:"autoCreateTime"`
}

type Patient struct {
	ID           int64      `gorm:"primaryKey;autoIncrement"`
	Name         string     `gorm:"type:varchar(200);not null"`
	Ssn          string     `gorm:"type:varchar(11);unique;not null"`
	CurrentState *int64     `gorm:"column:current_state"` // FK to state.id
	CreatedAt    time.Time  `gorm:"autoCreateTime"`
	UpdatedAt    time.Time  `gorm:"autoUpdateTime"`
	DeletedAt    *time.Time `gorm:"index"`
}

type State struct {
	ID        int64      `gorm:"primaryKey;autoIncrement"`
	Data      string     `gorm:"type:text;not null"`
	CreatedAt time.Time  `gorm:"autoCreateTime"`
	DeletedAt *time.Time `gorm:"index"`
}

type PatientStateMap struct {
	UserID  int64 `gorm:"primaryKey"` // FK to patient.id
	StateID int64 `gorm:"primaryKey"` // FK to state.id
}

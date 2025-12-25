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

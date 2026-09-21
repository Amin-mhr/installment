package repo

import (
	"time"

	"github.com/google/uuid"

	"interview/internal/domain"
)

// GORM models mirror migrations/001_init.sql. They are private to this package so
// the domain never carries persistence tags.

type loanModel struct {
	ID              int64 `gorm:"primaryKey"`
	UserID          int64
	CreditCompanyID int64
	TotalAmount     int64
	TermMonths      int
	StartDate       time.Time `gorm:"type:date"`
	CreditBalance   int64
	CreatedAt       time.Time
}

func (loanModel) TableName() string { return "loans" }

type installmentModel struct {
	ID                int64 `gorm:"primaryKey"`
	LoanID            int64
	UserID            int64
	CreditCompanyID   int64
	InstallmentNumber int
	Amount            int64
	DueDate           time.Time `gorm:"type:date"`
	Status            string
	PaidAt            *time.Time
	PaymentEventID    *uuid.UUID `gorm:"type:uuid"`
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

func (installmentModel) TableName() string { return "installments" }

// paymentModel has a composite primary key (company, event). autoIncrement is
// switched off because GORM would otherwise treat the first integer key as a serial.
type paymentModel struct {
	CreditCompanyID    int64     `gorm:"primaryKey;autoIncrement:false"`
	EventID            uuid.UUID `gorm:"primaryKey;type:uuid"`
	UserID             int64
	LoanID             int64
	Amount             int64
	PaidAt             time.Time
	CreditBalanceAfter int64
	CreatedAt          time.Time
}

func (paymentModel) TableName() string { return "payments" }

type creditCompanyModel struct {
	ID            int64 `gorm:"primaryKey"`
	Name          string
	WebhookSecret string
	CreatedAt     time.Time
}

func (creditCompanyModel) TableName() string { return "credit_companies" }

func loanToModel(l *domain.Loan) loanModel {
	return loanModel{
		ID:              l.ID,
		UserID:          l.UserID,
		CreditCompanyID: l.CreditCompanyID,
		TotalAmount:     l.TotalAmount,
		TermMonths:      l.TermMonths,
		StartDate:       l.StartDate,
		CreditBalance:   l.CreditBalance,
	}
}

func (m loanModel) toDomain() *domain.Loan {
	return &domain.Loan{
		ID:              m.ID,
		UserID:          m.UserID,
		CreditCompanyID: m.CreditCompanyID,
		TotalAmount:     m.TotalAmount,
		TermMonths:      m.TermMonths,
		StartDate:       m.StartDate,
		CreditBalance:   m.CreditBalance,
	}
}

func installmentToModel(i domain.Installment) installmentModel {
	return installmentModel{
		ID:                i.ID,
		LoanID:            i.LoanID,
		UserID:            i.UserID,
		CreditCompanyID:   i.CreditCompanyID,
		InstallmentNumber: i.Number,
		Amount:            i.Amount,
		DueDate:           i.DueDate,
		Status:            string(i.Status),
		PaidAt:            i.PaidAt,
		PaymentEventID:    i.PaymentEventID,
	}
}

func (m installmentModel) toDomain() domain.Installment {
	return domain.Installment{
		ID:              m.ID,
		LoanID:          m.LoanID,
		UserID:          m.UserID,
		CreditCompanyID: m.CreditCompanyID,
		Number:          m.InstallmentNumber,
		Amount:          m.Amount,
		DueDate:         m.DueDate,
		Status:          domain.InstallmentStatus(m.Status),
		PaidAt:          m.PaidAt,
		PaymentEventID:  m.PaymentEventID,
	}
}

func paymentToModel(p *domain.Payment) paymentModel {
	return paymentModel{
		CreditCompanyID:    p.CreditCompanyID,
		EventID:            p.EventID,
		UserID:             p.UserID,
		LoanID:             p.LoanID,
		Amount:             p.Amount,
		PaidAt:             p.PaidAt,
		CreditBalanceAfter: p.CreditBalanceAfter,
	}
}

// toDomain leaves Settled empty; the payment repo loads it separately.
func (m paymentModel) toDomain() *domain.Payment {
	return &domain.Payment{
		CreditCompanyID:    m.CreditCompanyID,
		EventID:            m.EventID,
		UserID:             m.UserID,
		LoanID:             m.LoanID,
		Amount:             m.Amount,
		PaidAt:             m.PaidAt.UTC(),
		CreditBalanceAfter: m.CreditBalanceAfter,
	}
}

func (m creditCompanyModel) toDomain() *domain.CreditCompany {
	return &domain.CreditCompany{ID: m.ID, Name: m.Name, WebhookSecret: m.WebhookSecret}
}

package domain

import (
	"time"

	"github.com/google/uuid"
)

type InstallmentStatus string

const (
	StatusPending InstallmentStatus = "pending"
	StatusPaid    InstallmentStatus = "paid"
)

type Installment struct {
	ID              int64
	LoanID          int64
	UserID          int64
	CreditCompanyID int64
	Number          int
	Amount          int64     // minor units
	DueDate         time.Time // calendar date, UTC midnight
	Status          InstallmentStatus
	PaidAt          *time.Time // provider-reported time of the payment that settled it
	PaymentEventID  *uuid.UUID // the payment that settled it
}

// settle marks the installment as paid by the given payment.
func (i *Installment) settle(eventID uuid.UUID, paidAt time.Time) {
	i.Status = StatusPaid
	i.PaidAt = &paidAt
	i.PaymentEventID = &eventID
}

// IsOverdue is derived, never stored: a pending installment whose due date is before today (UTC).
func (i *Installment) IsOverdue(now time.Time) bool {
	y, m, d := now.UTC().Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return i.Status == StatusPending && i.DueDate.Before(today)
}

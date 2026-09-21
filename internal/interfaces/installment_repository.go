package interfaces

import (
	"context"

	"interview/internal/domain"
)

type InstallmentRepository interface {
	CreateBatch(ctx context.Context, installments []domain.Installment) error
	// ListUnpaid returns the loan's pending installments, oldest first.
	ListUnpaid(ctx context.Context, loanID int64) ([]domain.Installment, error)
	// SaveSettled marks payment.Settled as paid and links them to the payment,
	// which must already be stored.
	SaveSettled(ctx context.Context, payment *domain.Payment) error
}

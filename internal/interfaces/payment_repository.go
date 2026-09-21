package interfaces

import (
	"context"

	"github.com/google/uuid"

	"interview/internal/domain"
)

type PaymentRepository interface {
	// GetByEvent returns the payment with its Settled installments, or domain.ErrNotFound.
	GetByEvent(ctx context.Context, companyID int64, eventID uuid.UUID) (*domain.Payment, error)
	// Create stores the payment (not its Settled installments). Returns
	// domain.ErrEventReused if the company already has a payment with this event ID.
	Create(ctx context.Context, payment *domain.Payment) error
}

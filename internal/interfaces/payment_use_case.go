package interfaces

import (
	"context"
	"time"

	"github.com/google/uuid"

	"interview/internal/domain"
)

type PaymentCommand struct {
	CompanyID int64
	LoanID    int64
	EventID   uuid.UUID
	Amount    int64
	PaidAt    time.Time
}

type PaymentResult struct {
	Payment   *domain.Payment
	Duplicate bool // true when this event had already been applied; nothing was changed
}

// PaymentUseCase is what the webhook handler needs from the application layer.
type PaymentUseCase interface {
	SecretProvider
	// HandlePayment applies a provider payment to a loan. It is idempotent per
	// (company, event ID): a re-send returns the original result with Duplicate set.
	HandlePayment(ctx context.Context, cmd PaymentCommand) (PaymentResult, error)
}

package interfaces

import (
	"context"

	"interview/internal/domain"
)

type LoanRepository interface {
	// Create persists the loan and assigns loan.ID.
	Create(ctx context.Context, loan *domain.Loan) error
	// GetForUpdate locks the loan row until the surrounding transaction ends, so it
	// must run inside TxManager.WithinTx. Returns domain.ErrNotFound if the loan does
	// not exist or belongs to a different credit company.
	GetForUpdate(ctx context.Context, id, companyID int64) (*domain.Loan, error)
	// SaveCreditBalance persists loan.CreditBalance.
	SaveCreditBalance(ctx context.Context, loan *domain.Loan) error
}

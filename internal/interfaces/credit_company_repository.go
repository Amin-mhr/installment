package interfaces

import (
	"context"

	"interview/internal/domain"
)

type CreditCompanyRepository interface {
	// GetByID returns domain.ErrNotFound if there is no such company.
	GetByID(ctx context.Context, id int64) (*domain.CreditCompany, error)
}

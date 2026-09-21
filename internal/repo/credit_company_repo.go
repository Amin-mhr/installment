package repo

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"interview/internal/domain"
)

type CreditCompanyRepo struct {
	db *gorm.DB
}

func NewCreditCompanyRepo(db *gorm.DB) *CreditCompanyRepo {
	return &CreditCompanyRepo{db: db}
}

func (r *CreditCompanyRepo) GetByID(ctx context.Context, id int64) (*domain.CreditCompany, error) {
	var m creditCompanyModel
	err := conn(ctx, r.db).Take(&m, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return m.toDomain(), nil
}

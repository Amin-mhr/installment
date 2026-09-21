package repo

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"interview/internal/domain"
)

type PaymentRepo struct {
	db *gorm.DB
}

func NewPaymentRepo(db *gorm.DB) *PaymentRepo {
	return &PaymentRepo{db: db}
}

func (r *PaymentRepo) Create(ctx context.Context, payment *domain.Payment) error {
	m := paymentToModel(payment)
	err := conn(ctx, r.db).Create(&m).Error
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return domain.ErrEventReused
	}
	return err
}

func (r *PaymentRepo) GetByEvent(ctx context.Context, companyID int64, eventID uuid.UUID) (*domain.Payment, error) {
	db := conn(ctx, r.db)

	var m paymentModel
	err := db.Take(&m, "credit_company_id = ? AND event_id = ?", companyID, eventID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	var settled []installmentModel
	err = db.
		Where("credit_company_id = ? AND payment_event_id = ?", companyID, eventID).
		Order("installment_number").
		Find(&settled).Error
	if err != nil {
		return nil, err
	}

	payment := m.toDomain()
	if len(settled) > 0 {
		payment.Settled = installmentsToDomain(settled)
	}
	return payment, nil
}

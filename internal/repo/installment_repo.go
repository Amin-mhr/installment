package repo

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"interview/internal/domain"
)

type InstallmentRepo struct {
	db *gorm.DB
}

func NewInstallmentRepo(db *gorm.DB) *InstallmentRepo {
	return &InstallmentRepo{db: db}
}

func (r *InstallmentRepo) CreateBatch(ctx context.Context, installments []domain.Installment) error {
	if len(installments) == 0 {
		return nil
	}
	models := make([]installmentModel, len(installments))
	for i, inst := range installments {
		models[i] = installmentToModel(inst)
	}
	return conn(ctx, r.db).Create(&models).Error
}

func (r *InstallmentRepo) ListUnpaid(ctx context.Context, loanID int64) ([]domain.Installment, error) {
	var models []installmentModel
	err := conn(ctx, r.db).
		Where("loan_id = ? AND status = ?", loanID, string(domain.StatusPending)).
		Order("installment_number").
		Find(&models).Error
	if err != nil {
		return nil, err
	}
	return installmentsToDomain(models), nil
}

func (r *InstallmentRepo) SaveSettled(ctx context.Context, payment *domain.Payment) error {
	if len(payment.Settled) == 0 {
		return nil
	}
	ids := make([]int64, len(payment.Settled))
	for i, inst := range payment.Settled {
		ids[i] = inst.ID
	}

	// A map (not a struct) so every column is written; GORM also bumps updated_at.
	res := conn(ctx, r.db).Model(&installmentModel{}).
		Where("id IN ? AND status = ?", ids, string(domain.StatusPending)).
		Updates(map[string]any{
			"status":           string(domain.StatusPaid),
			"paid_at":          payment.PaidAt,
			"payment_event_id": payment.EventID,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != int64(len(ids)) {
		// The loan row is locked for the whole payment, so this means the caller's view was stale.
		return fmt.Errorf("repo: settled %d of %d installments; the rest were not pending", res.RowsAffected, len(ids))
	}
	return nil
}

func installmentsToDomain(models []installmentModel) []domain.Installment {
	out := make([]domain.Installment, len(models))
	for i, m := range models {
		out[i] = m.toDomain()
	}
	return out
}

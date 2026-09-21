package repo

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"interview/internal/domain"
)

type LoanRepo struct {
	db *gorm.DB
}

func NewLoanRepo(db *gorm.DB) *LoanRepo {
	return &LoanRepo{db: db}
}

func (r *LoanRepo) Create(ctx context.Context, loan *domain.Loan) error {
	m := loanToModel(loan)
	if err := conn(ctx, r.db).Create(&m).Error; err != nil {
		return err
	}
	loan.ID = m.ID
	return nil
}

func (r *LoanRepo) GetForUpdate(ctx context.Context, id, companyID int64) (*domain.Loan, error) {
	tx, ok := txFrom(ctx)
	if !ok {
		// Outside a transaction the row lock would be released immediately.
		return nil, errors.New("repo: GetForUpdate must run inside WithinTx")
	}

	var m loanModel
	err := tx.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ? AND credit_company_id = ?", id, companyID).
		Take(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return m.toDomain(), nil
}

func (r *LoanRepo) SaveCreditBalance(ctx context.Context, loan *domain.Loan) error {
	res := conn(ctx, r.db).Model(&loanModel{}).
		Where("id = ?", loan.ID).
		Update("credit_balance", loan.CreditBalance)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return domain.ErrNotFound
	}
	return nil
}

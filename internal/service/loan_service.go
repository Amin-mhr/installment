package service

import (
	"context"
	"time"

	"interview/internal/domain"
	"interview/internal/interfaces"
)

type CreateLoanCommand struct {
	UserID          int64
	CreditCompanyID int64
	TotalAmount     int64
	TermMonths      int
	StartDate       time.Time
}

type LoanService struct {
	loans        interfaces.LoanRepository
	installments interfaces.InstallmentRepository
	tx           interfaces.TxManager
}

func NewLoanService(loans interfaces.LoanRepository, installments interfaces.InstallmentRepository, tx interfaces.TxManager) *LoanService {
	return &LoanService{loans: loans, installments: installments, tx: tx}
}

// CreateLoan stores the loan and its full installment schedule, all or nothing.
func (s *LoanService) CreateLoan(ctx context.Context, cmd CreateLoanCommand) (*domain.Loan, error) {
	loan, err := domain.NewLoan(cmd.UserID, cmd.CreditCompanyID, cmd.TotalAmount, cmd.TermMonths, cmd.StartDate)
	if err != nil {
		return nil, err
	}

	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.loans.Create(ctx, loan); err != nil {
			return err
		}
		return s.installments.CreateBatch(ctx, loan.Schedule())
	})
	if err != nil {
		return nil, err
	}
	return loan, nil
}

package service

import (
	"context"
	"errors"

	"interview/internal/domain"
	"interview/internal/interfaces"
)

type PaymentService struct {
	companies    interfaces.CreditCompanyRepository
	loans        interfaces.LoanRepository
	installments interfaces.InstallmentRepository
	payments     interfaces.PaymentRepository
	tx           interfaces.TxManager
}

func NewPaymentService(
	companies interfaces.CreditCompanyRepository,
	loans interfaces.LoanRepository,
	installments interfaces.InstallmentRepository,
	payments interfaces.PaymentRepository,
	tx interfaces.TxManager,
) *PaymentService {
	return &PaymentService{companies: companies, loans: loans, installments: installments, payments: payments, tx: tx}
}

// WebhookSecret returns the key a company signs its webhooks with, or domain.ErrNotFound.
func (s *PaymentService) WebhookSecret(ctx context.Context, companyID int64) (string, error) {
	company, err := s.companies.GetByID(ctx, companyID)
	if err != nil {
		return "", err
	}
	return company.WebhookSecret, nil
}

// HandlePayment applies a provider's payment notification to a loan: it pays off as
// many installments as the money covers, oldest first, and saves the rest as credit.
// It is safe to call repeatedly with the same event: the loan row is locked for the
// whole transaction, so concurrent deliveries are applied exactly once and a repeat
// gets the original result back.
func (s *PaymentService) HandlePayment(ctx context.Context, cmd interfaces.PaymentCommand) (interfaces.PaymentResult, error) {
	var result interfaces.PaymentResult
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		loan, err := s.loans.GetForUpdate(ctx, cmd.LoanID, cmd.CompanyID)
		if err != nil {
			return err
		}

		existing, err := s.payments.GetByEvent(ctx, cmd.CompanyID, cmd.EventID)
		switch {
		case err == nil:
			if !existing.Matches(cmd.LoanID, cmd.Amount) {
				return domain.ErrEventReused
			}
			result = interfaces.PaymentResult{Payment: existing, Duplicate: true}
			return nil
		case !errors.Is(err, domain.ErrNotFound):
			return err
		}

		unpaid, err := s.installments.ListUnpaid(ctx, loan.ID)
		if err != nil {
			return err
		}
		payment, err := loan.ApplyPayment(cmd.EventID, cmd.Amount, cmd.PaidAt, unpaid)
		if err != nil {
			return err
		}

		if err := s.payments.Create(ctx, payment); err != nil {
			return err
		}
		if err := s.installments.SaveSettled(ctx, payment); err != nil {
			return err
		}
		if err := s.loans.SaveCreditBalance(ctx, loan); err != nil {
			return err
		}
		result = interfaces.PaymentResult{Payment: payment}
		return nil
	})
	if err != nil {
		return interfaces.PaymentResult{}, err
	}
	return result, nil
}

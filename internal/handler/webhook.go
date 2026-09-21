package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"interview/internal/domain"
	"interview/internal/interfaces"
)

type paymentRequest struct {
	EventID uuid.UUID `json:"event_id"`
	LoanID  int64     `json:"loan_id"`
	Amount  int64     `json:"amount"`
	PaidAt  time.Time `json:"paid_at"`
}

func (r paymentRequest) validate() error {
	switch {
	case r.EventID == uuid.Nil:
		return errors.New("event_id is required and must be a non-nil UUID")
	case r.LoanID <= 0:
		return errors.New("loan_id must be a positive integer")
	case r.Amount <= 0:
		return errors.New("amount must be a positive integer (minor units)")
	case r.PaidAt.IsZero():
		return errors.New("paid_at is required (RFC 3339)")
	}
	return nil
}

type settledInstallment struct {
	ID     int64 `json:"id"`
	Number int   `json:"number"`
	Amount int64 `json:"amount"`
}

type paymentResponse struct {
	Status           string               `json:"status"` // "applied" or "already_processed"
	InstallmentsPaid []settledInstallment `json:"installments_paid"`
	CreditBalance    int64                `json:"credit_balance"` // the loan's saved credit after this payment
}

func newPaymentResponse(res interfaces.PaymentResult) paymentResponse {
	status := "applied"
	if res.Duplicate {
		status = "already_processed"
	}
	paid := make([]settledInstallment, len(res.Payment.Settled)) // non-nil, so an underpayment encodes as []
	for i, inst := range res.Payment.Settled {
		paid[i] = settledInstallment{ID: inst.ID, Number: inst.Number, Amount: inst.Amount}
	}
	return paymentResponse{Status: status, InstallmentsPaid: paid, CreditBalance: res.Payment.CreditBalanceAfter}
}

type webhookHandler struct {
	payments interfaces.PaymentUseCase
	log      *slog.Logger
}

// payment handles "the user paid some money toward a loan" from a credit provider.
// Any amount is accepted: it pays off installments oldest-first and the rest is saved
// as credit on the loan. Any 2xx means received; providers may safely re-send the same event_id.
func (h *webhookHandler) payment(c *gin.Context) {
	companyID := c.GetInt64(companyIDKey)

	var req paymentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abort(c, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if err := req.validate(); err != nil {
		abort(c, http.StatusBadRequest, err.Error())
		return
	}

	result, err := h.payments.HandlePayment(c.Request.Context(), interfaces.PaymentCommand{
		CompanyID: companyID,
		LoanID:    req.LoanID,
		EventID:   req.EventID,
		Amount:    req.Amount,
		PaidAt:    req.PaidAt,
	})

	status, body := http.StatusOK, any(nil)
	switch {
	case err == nil:
		body = newPaymentResponse(result)
	case errors.Is(err, domain.ErrNotFound):
		// Same answer for "no such loan" and "belongs to another company".
		status, body = http.StatusNotFound, gin.H{"error": "loan not found"}
	case errors.Is(err, domain.ErrEventReused):
		status, body = http.StatusConflict, gin.H{"error": err.Error()}
	case errors.Is(err, domain.ErrInvalidPayment):
		status, body = http.StatusUnprocessableEntity, gin.H{"error": err.Error()}
	default:
		h.log.Error("handle payment", "company_id", companyID, "loan_id", req.LoanID,
			"event_id", req.EventID, "err", err)
		abort(c, http.StatusInternalServerError, "internal error")
		return
	}

	h.log.Info("payment webhook", "company_id", companyID, "loan_id", req.LoanID,
		"event_id", req.EventID, "amount", req.Amount, "status", status, "duplicate", result.Duplicate)
	c.JSON(status, body)
}

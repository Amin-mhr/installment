package tests

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"interview/internal/handler"
	"interview/internal/interfaces"
)

// panickingUseCase authenticates fine and then blows up while handling the payment.
type panickingUseCase struct{}

func (panickingUseCase) WebhookSecret(context.Context, int64) (string, error) { return "s3cret", nil }

func (panickingUseCase) HandlePayment(context.Context, interfaces.PaymentCommand) (interfaces.PaymentResult, error) {
	panic("boom")
}

func TestRouter_APanicBecomesA500AndIsLoggedWithItsStack(t *testing.T) {
	var logs bytes.Buffer
	router := handler.NewRouter(panickingUseCase{}, slog.New(slog.NewJSONHandler(&logs, nil)))
	body := paymentBody(uuid.New(), 1, 100)

	for i := 0; i < 2; i++ { // the second request proves the server survived the first panic
		req := httptest.NewRequest(http.MethodPost, webhookPath(1), strings.NewReader(string(body)))
		req.Header.Set("X-Signature", sign("s3cret", body))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("request %d: status %d, want 500", i, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "internal error") || strings.Contains(rec.Body.String(), "boom") {
			t.Fatalf("request %d: body %q must say internal error and never leak the panic value", i, rec.Body.String())
		}
	}

	out := logs.String()
	for _, want := range []string{`"msg":"panic recovered"`, `"panic":"boom"`, `"stack":"goroutine`, `"status":500`} {
		if !strings.Contains(out, want) {
			t.Errorf("log is missing %s\n%s", want, out)
		}
	}
}

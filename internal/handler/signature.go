package handler

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"interview/internal/domain"
	"interview/internal/interfaces"
)

const (
	maxBodyBytes    = 64 << 10
	signatureHeader = "X-Signature"
	companyIDKey    = "company_id"
)

// verifySignature authenticates a webhook: X-Signature must be
// "sha256=" + hex(HMAC-SHA256(company webhook secret, raw body)). On success it
// stores the company ID in the gin context and restores the body for binding.
func verifySignature(secrets interfaces.SecretProvider, log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		companyID, err := strconv.ParseInt(c.Param("company_id"), 10, 64)
		if err != nil {
			abort(c, http.StatusBadRequest, "invalid company id")
			return
		}

		body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes))
		if err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				abort(c, http.StatusRequestEntityTooLarge, "body too large")
			} else {
				abort(c, http.StatusBadRequest, "could not read body")
			}
			return
		}

		// An unknown company and a bad signature look identical to the caller.
		secret, err := secrets.WebhookSecret(c.Request.Context(), companyID)
		switch {
		case errors.Is(err, domain.ErrNotFound):
			rejectSignature(c, log, companyID)
			return
		case err != nil:
			log.Error("load webhook secret", "company_id", companyID, "err", err)
			abort(c, http.StatusInternalServerError, "internal error")
			return
		}
		if !validSignature(secret, body, c.GetHeader(signatureHeader)) {
			rejectSignature(c, log, companyID)
			return
		}

		c.Request.Body = io.NopCloser(bytes.NewReader(body))
		c.Set(companyIDKey, companyID)
		c.Next()
	}
}

func rejectSignature(c *gin.Context, log *slog.Logger, companyID int64) {
	log.Warn("webhook rejected: bad signature or unknown company", "company_id", companyID)
	abort(c, http.StatusUnauthorized, "invalid signature")
}

func validSignature(secret string, body []byte, header string) bool {
	hexSig, ok := strings.CutPrefix(header, "sha256=")
	if !ok {
		return false
	}
	got, err := hex.DecodeString(hexSig)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}

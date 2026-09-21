package handler

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/gin-gonic/gin"

	"interview/internal/interfaces"
)

func NewRouter(payments interfaces.PaymentUseCase, log *slog.Logger) *gin.Engine {
	r := gin.New()
	r.HandleMethodNotAllowed = true
	// accessLog is outermost so it still records the 500 that recovery produces.
	r.Use(accessLog(log), recovery(log))

	h := &webhookHandler{payments: payments, log: log}
	r.POST("/webhooks/credit-companies/:company_id/payments",
		verifySignature(payments, log), h.payment)
	return r
}

func accessLog(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		log.Info("request", "method", c.Request.Method, "route", route,
			"status", c.Writer.Status(), "duration_ms", time.Since(start).Milliseconds())
	}
}

// recovery turns a panic into a 500 and logs it, with its stack, through the app
// logger. gin's stock recovery would print to stderr with ANSI colour codes instead.
func recovery(log *slog.Logger) gin.HandlerFunc {
	return gin.CustomRecoveryWithWriter(io.Discard, func(c *gin.Context, recovered any) {
		log.Error("panic recovered", "route", c.FullPath(),
			"panic", fmt.Sprint(recovered), "stack", string(debug.Stack()))
		abort(c, http.StatusInternalServerError, "internal error")
	})
}

func abort(c *gin.Context, status int, msg string) {
	c.AbortWithStatusJSON(status, gin.H{"error": msg})
}

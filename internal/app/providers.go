package app

import (
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/wire"
	"gorm.io/gorm"

	"interview/internal/handler"
	"interview/internal/interfaces"
	"interview/internal/logging"
	"interview/internal/repo"
	"interview/internal/service"
)

// App is what the injector hands back: the server to run and the logger to run it with.
type App struct {
	Server *http.Server
	Log    *slog.Logger
}

// ProvideLogger builds the console + file logger; the returned func closes the file.
func ProvideLogger(cfg Config) (*slog.Logger, func(), error) {
	return logging.New(cfg.Log, os.Stdout)
}

// ProvideDB opens the database; the returned func closes the connection pool.
func ProvideDB(cfg Config, log *slog.Logger) (*gorm.DB, func(), error) {
	db, err := repo.Open(cfg.DatabaseURL, log) // fails fast if the database is unreachable
	if err != nil {
		return nil, nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, nil, err
	}
	return db, func() { sqlDB.Close() }, nil
}

func ProvideHTTPServer(cfg Config, router *gin.Engine, log *slog.Logger) *http.Server {
	return &http.Server{
		Addr:              cfg.Addr,
		Handler:           router,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelError), // net/http's own errors
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

// providers is everything Wire needs to build the app. Each wire.Bind says which
// concrete type satisfies an interface, so services and handlers only ever see
// interfaces and this file is the one place that names the adapters.
var providers = wire.NewSet(
	ProvideLogger,
	ProvideDB,
	ProvideHTTPServer,

	repo.NewTxManager,
	wire.Bind(new(interfaces.TxManager), new(*repo.TxManager)),
	repo.NewLoanRepo,
	wire.Bind(new(interfaces.LoanRepository), new(*repo.LoanRepo)),
	repo.NewInstallmentRepo,
	wire.Bind(new(interfaces.InstallmentRepository), new(*repo.InstallmentRepo)),
	repo.NewPaymentRepo,
	wire.Bind(new(interfaces.PaymentRepository), new(*repo.PaymentRepo)),
	repo.NewCreditCompanyRepo,
	wire.Bind(new(interfaces.CreditCompanyRepository), new(*repo.CreditCompanyRepo)),

	service.NewPaymentService,
	wire.Bind(new(interfaces.PaymentUseCase), new(*service.PaymentService)),

	handler.NewRouter,

	wire.Struct(new(App), "*"),
)

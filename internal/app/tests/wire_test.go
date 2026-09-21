package tests

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"interview/internal/app"
	"interview/internal/logging"
	"interview/internal/repo"
	"interview/internal/service"
	"interview/internal/testutil"
)

func testConfig(dsn, logDir string) app.Config {
	return app.Config{
		DatabaseURL: dsn,
		Addr:        "127.0.0.1:0",
		GinMode:     gin.TestMode,
		Log:         logging.Config{Dir: logDir, Level: "info", MaxSizeMB: 10, MaxBackups: 3, MaxAgeDays: 7},
	}
}

// The other tests build their objects by hand. This one goes through the Wire
// injector, so it proves the generated graph binds real implementations to every
// interface, that the server it returns works, and that what happens is written to
// the log file.
func TestInitialize_BuildsAWorkingServerThatLogsToAFile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dsn := testutil.NewDSN(t)
	db := testutil.OpenDB(t, dsn)
	logDir := t.TempDir()

	const secret = "wire-secret"
	userID := testutil.SeedUser(t, db)
	companyID := testutil.SeedCompany(t, db, secret)
	tx := repo.NewTxManager(db)
	loan, err := service.NewLoanService(repo.NewLoanRepo(db), repo.NewInstallmentRepo(db), tx).CreateLoan(
		context.Background(), service.CreateLoanCommand{
			UserID: userID, CreditCompanyID: companyID, TotalAmount: 400_000, TermMonths: 4,
			StartDate: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		})
	if err != nil {
		t.Fatalf("create loan: %v", err)
	}

	a, cleanup, err := app.Initialize(testConfig(dsn, logDir))
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	t.Cleanup(cleanup)

	if a.Log == nil || a.Server.ErrorLog == nil || a.Server.Addr != "127.0.0.1:0" ||
		a.Server.ReadHeaderTimeout == 0 || a.Server.WriteTimeout == 0 {
		t.Errorf("app was not built from the config: %+v", a.Server)
	}
	ts := httptest.NewServer(a.Server.Handler)
	t.Cleanup(ts.Close)

	event := uuid.New()
	body := fmt.Sprintf(`{"event_id":%q,"loan_id":%d,"amount":250000,"paid_at":"2026-02-14T10:30:00Z"}`, event, loan.ID)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	signature := hex.EncodeToString(mac.Sum(nil))

	post := func(sig string) (int, string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/webhooks/credit-companies/%d/payments", ts.URL, companyID), strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if sig != "" {
			req.Header.Set("X-Signature", "sha256="+sig)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	if code, raw := post(""); code != http.StatusUnauthorized {
		t.Fatalf("unsigned request: status %d (%s), want 401", code, raw)
	}
	code, raw := post(signature)
	if code != http.StatusOK {
		t.Fatalf("signed request: status %d (%s), want 200", code, raw)
	}
	var reply struct {
		Status           string `json:"status"`
		InstallmentsPaid []struct {
			Number int `json:"number"`
		} `json:"installments_paid"`
		CreditBalance int64 `json:"credit_balance"`
	}
	if err := json.Unmarshal([]byte(raw), &reply); err != nil {
		t.Fatal(err)
	}
	if reply.Status != "applied" || len(reply.InstallmentsPaid) != 2 || reply.CreditBalance != 50_000 {
		t.Fatalf("reply = %s, want applied, two installments, credit 50000", raw)
	}
	if got := testutil.PaidNumbers(t, db, loan.ID); !slices.Equal(got, []int{1, 2}) {
		t.Fatalf("paid in DB = %v, want [1 2]", got)
	}
	testutil.AssertLedger(t, db, loan.ID)

	// Everything above must now be in <logDir>/app.log, and nothing secret may be.
	logPath := filepath.Join(logDir, logging.FileName)
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("no log file: %v", err)
	}
	if strings.Contains(string(content), secret) || strings.Contains(string(content), signature) {
		t.Fatal("the webhook secret or a signature leaked into the log file")
	}

	var sawWebhook, sawRejected, sawRequest bool
	sc := bufio.NewScanner(strings.NewReader(string(content)))
	for sc.Scan() {
		var rec map[string]any
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			t.Fatalf("log line is not JSON: %q", sc.Text())
		}
		switch rec["msg"] {
		case "payment webhook":
			sawWebhook = rec["event_id"] == event.String() && rec["loan_id"] == float64(loan.ID) &&
				rec["status"] == float64(200) && rec["duplicate"] == false
		case "webhook rejected: bad signature or unknown company":
			sawRejected = rec["level"] == "WARN" && rec["company_id"] == float64(companyID)
		case "request":
			sawRequest = sawRequest || rec["status"] == float64(200)
		}
	}
	if !sawWebhook || !sawRejected || !sawRequest {
		t.Errorf("log file is missing entries (payment webhook=%v, rejected=%v, request=%v):\n%s",
			sawWebhook, sawRejected, sawRequest, content)
	}
}

func TestInitialize_FailsFastWhenTheLogDirectoryCannotBeCreated(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("i am a file"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := app.Initialize(testConfig("postgres://u:p@127.0.0.1:1/none", filepath.Join(blocker, "logs")))
	if err == nil || !strings.Contains(err.Error(), "log directory") {
		t.Fatalf("got %v, want an error about the log directory", err)
	}
}

func TestInitialize_FailsFastWhenTheDatabaseIsUnreachable(t *testing.T) {
	logDir := t.TempDir()

	_, _, err := app.Initialize(testConfig("postgres://u:p@127.0.0.1:1/none?connect_timeout=2", logDir))
	if err == nil {
		t.Fatal("expected a connection error")
	}
	if _, statErr := os.Stat(filepath.Join(logDir, logging.FileName)); statErr != nil {
		t.Errorf("the logger should have started before the database was tried: %v", statErr)
	}
}

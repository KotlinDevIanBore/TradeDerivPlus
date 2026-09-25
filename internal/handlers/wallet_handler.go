package handlers

import (
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/middleware"
	"github.com/KotlinDevIanBore/tradederivplus/internal/service"
	"github.com/KotlinDevIanBore/tradederivplus/pkg/mpesaclient"
)

type WalletHandler struct{ wallet *service.WalletService }

func NewWalletHandler(wallet *service.WalletService) *WalletHandler {
	return &WalletHandler{wallet: wallet}
}

type depositRequest struct {
	AccountID   string `json:"account_id" binding:"required"`
	AmountKES   string `json:"amount_kes" binding:"required"`
	PhoneNumber string `json:"phone_number" binding:"required"`
}

func (h *WalletHandler) Deposit(c *gin.Context) {
	var req depositRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "Amount and phone number are required", "")
		return
	}

	accountID, err := uuid.Parse(req.AccountID)
	if err != nil {
		badRequest(c, "Invalid account id", "")
		return
	}
	amount, err := decimal.NewFromString(req.AmountKES)
	if err != nil {
		badRequest(c, "Enter a valid amount", "amount")
		return
	}

	resp, err := h.wallet.Deposit(c.Request.Context(), service.DepositInput{
		UserID:      middleware.UserID(c),
		AccountID:   accountID,
		AmountKES:   amount,
		PhoneNumber: req.PhoneNumber,
	})
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusAccepted, resp)
}

type withdrawRequest struct {
	AccountID   string `json:"account_id" binding:"required"`
	AmountUSD   string `json:"amount_usd" binding:"required"`
	PhoneNumber string `json:"phone_number" binding:"required"`
}

func (h *WalletHandler) Withdraw(c *gin.Context) {
	var req withdrawRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "Amount and phone number are required", "")
		return
	}

	accountID, err := uuid.Parse(req.AccountID)
	if err != nil {
		badRequest(c, "Invalid account id", "")
		return
	}
	amount, err := decimal.NewFromString(req.AmountUSD)
	if err != nil {
		badRequest(c, "Enter a valid amount", "amount")
		return
	}

	tx, err := h.wallet.Withdraw(c.Request.Context(), service.WithdrawInput{
		UserID:      middleware.UserID(c),
		AccountID:   accountID,
		AmountUSD:   amount,
		PhoneNumber: req.PhoneNumber,
	})
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusAccepted, tx)
}

// Transactions serves the paginated ledger (spec gap G7). `type` may repeat.
func (h *WalletHandler) Transactions(c *gin.Context) {
	accountID, err := uuid.Parse(c.Query("account_id"))
	if err != nil {
		badRequest(c, "account_id is required", "")
		return
	}

	limit := clampInt(c.Query("limit"), 25, 1, 100)
	page, offset := resolvePage(c, limit)

	var types []string
	if raw := c.QueryArray("type"); len(raw) > 0 {
		types = raw
	}

	result, err := h.wallet.Transactions(c.Request.Context(), middleware.UserID(c), accountID, types, limit, offset)
	if err != nil {
		fail(c, err)
		return
	}
	result.Page = page
	result.Pages = pageCount(result.Total, limit)
	c.JSON(http.StatusOK, result)
}

// Transaction is the deposit screen's polling fallback (spec gap G10).
func (h *WalletHandler) Transaction(c *gin.Context) {
	txID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		badRequest(c, "Invalid transaction id", "")
		return
	}

	tx, err := h.wallet.Transaction(c.Request.Context(), middleware.UserID(c), txID)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, tx)
}

// Rate exposes the KES/USD conversion the deposit sheet shows (spec gap G8).
func (h *WalletHandler) Rate(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"kes_per_usd": h.wallet.Rate()})
}

// MpesaCallback is public: Daraja calls it with no bearer token. It always
// answers 200 so Safaricom does not retry a payload we have already stored.
func (h *WalletHandler) MpesaCallback(mpesa *service.MpesaService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var cb mpesaclient.Callback
		if err := c.ShouldBindJSON(&cb); err != nil {
			c.JSON(http.StatusOK, gin.H{"ResultCode": 0, "ResultDesc": "Accepted"})
			return
		}
		if err := mpesa.HandleCallback(c.Request.Context(), cb); err != nil {
			_ = c.Error(err)
		}
		c.JSON(http.StatusOK, gin.H{"ResultCode": 0, "ResultDesc": "Accepted"})
	}
}

// B2CResult and B2CTimeout are two routes on purpose, and the only thing that
// distinguishes them is which one Daraja posted to.
//
// That distinction cannot be recovered from the body, and it decides whether an
// outcome may be acted on: a payload on the timeout channel means Daraja's queue
// gave up before it knew what happened, so acting on its result code can refund a
// customer who was paid. Routing both to one handler — which is what a
// neighbouring production system does — throws the distinction away at the door.
//
// Both always answer 200. Daraja retries anything else, and a retry storm against
// a payout callback is the last thing worth inviting; a body we cannot handle is
// logged and dropped rather than argued with.
func (h *WalletHandler) B2CResult(payouts *service.MpesaPayoutService) gin.HandlerFunc {
	return h.b2cCallback(payouts, mpesaclient.ArrivedAtResultURL)
}

func (h *WalletHandler) B2CTimeout(payouts *service.MpesaPayoutService) gin.HandlerFunc {
	return h.b2cCallback(payouts, mpesaclient.ArrivedAtTimeoutURL)
}

func (h *WalletHandler) b2cCallback(payouts *service.MpesaPayoutService, arrival mpesaclient.PayoutArrival) gin.HandlerFunc {
	return func(c *gin.Context) {
		body, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
		if err != nil {
			_ = c.Error(err)
			c.JSON(http.StatusOK, gin.H{"ResultCode": 0, "ResultDesc": "Accepted"})
			return
		}
		if err := payouts.HandleResult(c.Request.Context(), body, arrival); err != nil {
			_ = c.Error(err)
		}
		c.JSON(http.StatusOK, gin.H{"ResultCode": 0, "ResultDesc": "Accepted"})
	}
}

// resolvePage turns ?page= (1-based, the spec's form) into an offset, while
// still honouring ?offset= for one release so a client mid-rollout keeps
// working. page wins when both are present.
func resolvePage(c *gin.Context, limit int) (page, offset int) {
	page = clampInt(c.Query("page"), 0, 1, 1_000_000)
	if page > 0 {
		return page, (page - 1) * limit
	}
	offset = clampInt(c.Query("offset"), 0, 0, 1_000_000)
	return offset/limit + 1, offset
}

func clampInt(raw string, def, minV, maxV int) int {
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	if v < minV {
		return minV
	}
	if v > maxV {
		return maxV
	}
	return v
}

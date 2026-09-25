package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/KotlinDevIanBore/tradederivplus/internal/middleware"
	"github.com/KotlinDevIanBore/tradederivplus/internal/service"
)

// KYCHandler is the user-facing side of identity verification: ask for somewhere
// to put a document, then say it is there.
//
// The bytes never pass through this API. The client uploads them straight to the
// object store with a signed URL, which keeps a 10 MiB body off the application
// and means no request-size limit is needed here.
type KYCHandler struct {
	kyc *service.KYCService
}

func NewKYCHandler(kyc *service.KYCService) *KYCHandler {
	return &KYCHandler{kyc: kyc}
}

type uploadURLRequest struct {
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
}

// UploadURL issues a presigned PUT for exactly one document.
//
// The declared type and size are checked here and signed into the URL, so the
// browser cannot then upload something else: a mismatch is refused by the store
// before any byte is stored. What it can still do is send bytes that are not
// really the type they claim, which is why Confirm sniffs the stored object.
func (h *KYCHandler) UploadURL(c *gin.Context) {
	var req uploadURLRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "Tell us the file type and size", "")
		return
	}

	target, err := h.kyc.PresignUpload(c.Request.Context(), middleware.UserID(c), req.ContentType, req.SizeBytes)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, target)
}

type submitRequest struct {
	AttemptID string `json:"attempt_id"`
	IDNumber  string `json:"id_number"`
}

// Submit says the document is in the bucket. Everything the client claimed at
// presign time is re-checked here against what is actually stored, including a
// content sniff, so a label cannot stand in for the bytes.
func (h *KYCHandler) Submit(c *gin.Context) {
	var req submitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "Tell us which upload to confirm", "")
		return
	}
	attemptID, err := uuid.Parse(req.AttemptID)
	if err != nil {
		badRequest(c, "Invalid upload id", "attempt_id")
		return
	}

	submission, err := h.kyc.Submit(c.Request.Context(), middleware.UserID(c), attemptID, req.IDNumber)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, submission)
}

// State reports where the caller stands, including whether an upload was started
// and never finished — which is what lets the client offer "continue" instead of
// starting over silently.
func (h *KYCHandler) State(c *gin.Context) {
	state, err := h.kyc.State(c.Request.Context(), middleware.UserID(c))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, state)
}

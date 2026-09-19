// Package mpesaclient wraps the raw Safaricom Daraja HTTP protocol.
// It has no knowledge of our database or domain types.
package mpesaclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

type Client struct {
	baseURL        string
	consumerKey    string
	consumerSecret string
	shortcode      string
	passkey        string
	callbackURL    string
	http           *http.Client

	mu          sync.Mutex
	token       string
	tokenExpiry time.Time
}

type Options struct {
	BaseURL        string
	ConsumerKey    string
	ConsumerSecret string
	Shortcode      string
	Passkey        string
	CallbackURL    string
}

func New(o Options) *Client {
	return &Client{
		baseURL:        o.BaseURL,
		consumerKey:    o.ConsumerKey,
		consumerSecret: o.ConsumerSecret,
		shortcode:      o.Shortcode,
		passkey:        o.Passkey,
		callbackURL:    o.CallbackURL,
		http:           &http.Client{Timeout: 30 * time.Second},
	}
}

type STKPushRequest struct {
	PhoneNumber string
	Amount      int64
	AccountRef  string
	Description string
}

type STKPushResponse struct {
	MerchantRequestID   string `json:"MerchantRequestID"`
	CheckoutRequestID   string `json:"CheckoutRequestID"`
	ResponseCode        string `json:"ResponseCode"`
	ResponseDescription string `json:"ResponseDescription"`
	CustomerMessage     string `json:"CustomerMessage"`
	ErrorCode           string `json:"errorCode"`
	ErrorMessage        string `json:"errorMessage"`
}

// Callback is the payload Daraja POSTs to our callback URL.
type Callback struct {
	Body struct {
		STKCallback struct {
			MerchantRequestID string `json:"MerchantRequestID"`
			CheckoutRequestID string `json:"CheckoutRequestID"`
			ResultCode        int    `json:"ResultCode"`
			ResultDesc        string `json:"ResultDesc"`
			CallbackMetadata  struct {
				Item []struct {
					Name  string `json:"Name"`
					Value any    `json:"Value"`
				} `json:"Item"`
			} `json:"CallbackMetadata"`
		} `json:"stkCallback"`
	} `json:"Body"`
}

// ReceiptNumber pulls MpesaReceiptNumber out of the callback metadata.
func (c Callback) ReceiptNumber() string {
	for _, it := range c.Body.STKCallback.CallbackMetadata.Item {
		if it.Name == "MpesaReceiptNumber" {
			if s, ok := it.Value.(string); ok {
				return s
			}
		}
	}
	return ""
}

func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.tokenExpiry) {
		return c.token, nil
	}

	url := c.baseURL + "/oauth/v1/generate?grant_type=client_credentials"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(c.consumerKey, c.consumerSecret)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("daraja auth: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("daraja auth status %d: %s", resp.StatusCode, string(body))
	}

	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   string `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	secs, _ := strconv.Atoi(out.ExpiresIn)
	if secs == 0 {
		secs = 3599
	}
	c.token = out.AccessToken
	// Refresh a minute early so a request never races the expiry.
	c.tokenExpiry = time.Now().Add(time.Duration(secs-60) * time.Second)
	return c.token, nil
}

func (c *Client) STKPush(ctx context.Context, r STKPushRequest) (*STKPushResponse, error) {
	token, err := c.accessToken(ctx)
	if err != nil {
		return nil, err
	}

	timestamp := time.Now().Format("20060102150405")
	password := base64.StdEncoding.EncodeToString([]byte(c.shortcode + c.passkey + timestamp))

	payload := map[string]any{
		"BusinessShortCode": c.shortcode,
		"Password":          password,
		"Timestamp":         timestamp,
		"TransactionType":   "CustomerPayBillOnline",
		"Amount":            r.Amount,
		"PartyA":            r.PhoneNumber,
		"PartyB":            c.shortcode,
		"PhoneNumber":       r.PhoneNumber,
		"CallBackURL":       c.callbackURL,
		"AccountReference":  r.AccountRef,
		"TransactionDesc":   r.Description,
	}
	b, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/mpesa/stkpush/v1/processrequest", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("stk push: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var out STKPushResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("stk push decode: %w (body %s)", err, string(body))
	}
	if out.ErrorCode != "" {
		return &out, fmt.Errorf("daraja %s: %s", out.ErrorCode, out.ErrorMessage)
	}
	if out.ResponseCode != "0" {
		return &out, fmt.Errorf("daraja rejected: %s", out.ResponseDescription)
	}
	return &out, nil
}

// NormalizePhone converts 07XXXXXXXX / +2547XXXXXXXX / 2547XXXXXXXX to 2547XXXXXXXX.
func NormalizePhone(p string) string {
	cleaned := make([]byte, 0, len(p))
	for i := 0; i < len(p); i++ {
		if p[i] >= '0' && p[i] <= '9' {
			cleaned = append(cleaned, p[i])
		}
	}
	s := string(cleaned)
	switch {
	case len(s) == 10 && s[0] == '0':
		return "254" + s[1:]
	case len(s) == 9 && (s[0] == '7' || s[0] == '1'):
		return "254" + s
	default:
		return s
	}
}

// ValidPhone reports whether p normalizes to a Safaricom-shaped MSISDN.
func ValidPhone(p string) bool {
	n := NormalizePhone(p)
	return len(n) == 12 && n[:3] == "254" && (n[3] == '7' || n[3] == '1')
}

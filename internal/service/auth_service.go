package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"golang.org/x/crypto/bcrypt"

	"github.com/KotlinDevIanBore/tradederivplus/internal/config"
	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
	"github.com/KotlinDevIanBore/tradederivplus/internal/repository"
	"github.com/KotlinDevIanBore/tradederivplus/pkg/mpesaclient"
)

type AuthService struct {
	store *repository.Store
	cfg   *config.Config
}

func NewAuthService(store *repository.Store, cfg *config.Config) *AuthService {
	return &AuthService{store: store, cfg: cfg}
}

// Token scopes. A user token and an admin token are distinguished by a claim,
// and each middleware requires its own, so neither opens the other's routes.
const (
	ScopeUser  = "user"
	ScopeAdmin = "admin"

	// RoleRestricted marks an admin session that may do nothing but change
	// its own password.
	RoleRestricted = "restricted"
)

type Claims struct {
	UserID uuid.UUID `json:"uid"`
	Kind   string    `json:"knd"` // access | refresh
	Scope  string    `json:"scp"` // empty or "user"; never "admin"
	jwt.RegisteredClaims
}

type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

type AuthResult struct {
	User    *models.User    `json:"user"`
	Account *models.Account `json:"account"` // the demo account created at signup
	Tokens  TokenPair       `json:"tokens"`
}

type RegisterInput struct {
	FullName    string
	PhoneNumber string
	Email       string
	Password    string
}

// Register creates the user, their demo account, its wallet, and credits the
// demo starting balance as bonus -- all in one transaction (spec gap G1).
// The client never has to make a second call to get a usable account.
func (s *AuthService) Register(ctx context.Context, in RegisterInput) (*AuthResult, error) {
	phone := mpesaclient.NormalizePhone(in.PhoneNumber)
	if !mpesaclient.ValidPhone(phone) {
		return nil, invalid("phone_number", "Enter a valid Safaricom number")
	}
	if len(in.Password) < 8 {
		return nil, invalid("password", "Password must be at least 8 characters")
	}
	if !hasDigit(in.Password) {
		return nil, invalid("password", "Password must contain at least one number")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	user := &models.User{PhoneNumber: phone, PasswordHash: string(hash)}
	if name := strings.TrimSpace(in.FullName); name != "" {
		user.FullName = &name
	}
	if email := strings.TrimSpace(strings.ToLower(in.Email)); email != "" {
		user.Email = &email
	}

	var account *models.Account
	err = s.store.WithTx(ctx, func(tx *repository.Store) error {
		if err := tx.Users.Create(ctx, user); err != nil {
			if errors.Is(err, repository.ErrConflict) {
				if strings.Contains(err.Error(), "email") {
					return ErrEmailTaken
				}
				return ErrPhoneTaken
			}
			return err
		}

		account = &models.Account{UserID: user.ID, AccountType: models.AccountDemo, Currency: "USD"}
		if err := tx.Accounts.Create(ctx, account); err != nil {
			return err
		}

		starting := decimal.NewFromFloat(s.cfg.DemoStartingBalance)
		if err := tx.Wallets.Create(ctx, account.ID, decimal.Zero, starting); err != nil {
			return err
		}

		// Demo funds are bonus balance, so they can never be withdrawn.
		ref := "demo-starting-balance"
		return tx.Transactions.Insert(ctx, &models.Transaction{
			AccountID:   account.ID,
			Type:        models.TxBonusCredit,
			BalanceType: models.BalanceBonus,
			Amount:      starting,
			Status:      models.StatusCompleted,
			Reference:   &ref,
			Metadata:    map[string]any{"note": "Demo account starting balance"},
		})
	})
	if err != nil {
		return nil, err
	}

	tokens, err := s.issueTokens(user.ID)
	if err != nil {
		return nil, err
	}
	return &AuthResult{User: user, Account: account, Tokens: *tokens}, nil
}

func (s *AuthService) Login(ctx context.Context, phoneNumber, password string) (*AuthResult, error) {
	phone := mpesaclient.NormalizePhone(phoneNumber)

	user, err := s.store.Users.GetByPhone(ctx, phone)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			// Same error and timing shape as a wrong password, so the response
			// does not reveal whether a number is registered.
			bcrypt.CompareHashAndPassword([]byte("$2a$10$invalidinvalidinvalidinvalidinvalidinvalidinvalidinvalidin"), []byte(password))
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		return nil, ErrInvalidCredentials
	}

	tokens, err := s.issueTokens(user.ID)
	if err != nil {
		return nil, err
	}
	return &AuthResult{User: user, Tokens: *tokens}, nil
}

// Refresh exchanges a valid refresh token for a new pair.
func (s *AuthService) Refresh(ctx context.Context, refreshToken string) (*TokenPair, error) {
	claims, err := s.ParseToken(refreshToken)
	if err != nil {
		return nil, err
	}
	if claims.Kind != "refresh" {
		return nil, ErrInvalidToken
	}
	if _, err := s.store.Users.GetByID(ctx, claims.UserID); err != nil {
		return nil, ErrInvalidToken
	}
	return s.issueTokens(claims.UserID)
}

// Me backs GET /auth/me (spec gap G9) -- the client needs phone and kyc_status
// for the deposit prefill and the withdraw gate.
func (s *AuthService) Me(ctx context.Context, userID uuid.UUID) (*models.User, error) {
	u, err := s.store.Users.GetByID(ctx, userID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, ErrNotFound
	}
	return u, err
}

func (s *AuthService) ParseToken(raw string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(raw, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		return []byte(s.cfg.JWTSecret), nil
	})
	if err != nil || !token.Valid {
		return nil, ErrInvalidToken
	}
	claims, ok := token.Claims.(*Claims)
	if !ok {
		return nil, ErrInvalidToken
	}
	// An admin token must never open a user route, even though both are
	// signed with the same secret.
	if claims.Scope == ScopeAdmin {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

func (s *AuthService) issueTokens(userID uuid.UUID) (*TokenPair, error) {
	access, err := s.sign(userID, "access", s.cfg.AccessTokenTTL)
	if err != nil {
		return nil, err
	}
	refresh, err := s.sign(userID, "refresh", s.cfg.RefreshTokenTTL)
	if err != nil {
		return nil, err
	}
	return &TokenPair{
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresIn:    int(s.cfg.AccessTokenTTL.Seconds()),
	}, nil
}

func (s *AuthService) sign(userID uuid.UUID, kind string, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID: userID,
		Kind:   kind,
		Scope:  ScopeUser,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			ID:        uuid.NewString(),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(s.cfg.JWTSecret))
}

func hasDigit(s string) bool {
	for _, r := range s {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}

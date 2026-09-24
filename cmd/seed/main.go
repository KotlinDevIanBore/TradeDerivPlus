// Command seed inserts development-only fixtures: the default admin account
// and a few placeholder KYC submissions for the review queue to act on.
//
// This is a command and NOT a migration on purpose. Migrations run in every
// environment; this refuses to run anywhere but development. That is what
// makes "the default credential can exist in dev but never in prod" a
// guarantee rather than a hope.
//
//	go run ./cmd/seed
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/shopspring/decimal"
	"golang.org/x/crypto/bcrypt"

	"github.com/KotlinDevIanBore/tradederivplus/internal/config"
	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
	"github.com/KotlinDevIanBore/tradederivplus/internal/repository"
	"github.com/KotlinDevIanBore/tradederivplus/internal/service"
)

func main() {
	if err := run(); err != nil {
		slog.Error("seed failed", "err", err)
		os.Exit(1)
	}
}

func run() error {
	// The audit log holds a foreign key to admin_users on purpose: an admin's
	// trail must survive them, so the row cannot simply be deleted and
	// recreated. -reset-admin exists because of that, for the development case
	// where the seeded password has been changed and needs putting back.
	resetAdmin := flag.Bool("reset-admin", false,
		"put the seeded admin back on the default password and re-arm the change prompt")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// The refusal that makes this safe.
	if cfg.Env != "development" {
		return fmt.Errorf("refusing to seed: APP_ENV is %q, not development", cfg.Env)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pool, err := repository.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := repository.Migrate(ctx, pool, "migrations"); err != nil {
		return err
	}
	store := repository.NewStore(pool)

	if err := seedAdmin(ctx, store, *resetAdmin); err != nil {
		return err
	}
	if err := seedKYC(ctx, store, cfg); err != nil {
		return err
	}
	if err := enableBotForDevelopment(ctx, store); err != nil {
		return err
	}

	fmt.Println("\nSeed complete. Sign in at /admin/login with admin / " + service.SeededAdminPassword)
	fmt.Println("You will be asked to change the password immediately.")
	return nil
}

func seedAdmin(ctx context.Context, store *repository.Store, reset bool) error {
	existing, err := store.Admins.GetByUsername(ctx, "admin")
	if err == nil {
		if !reset {
			fmt.Println("admin user already exists, leaving it alone (use -reset-admin to restore the default password)")
			return nil
		}

		hash, hErr := bcrypt.GenerateFromPassword([]byte(service.SeededAdminPassword), bcrypt.DefaultCost)
		if hErr != nil {
			return hErr
		}
		if err := store.Admins.SetPassword(ctx, existing.ID, string(hash)); err != nil {
			return err
		}
		// SetPassword clears the change prompt, so re-arm it: the point of a
		// reset is to put the account back exactly as a fresh seed leaves it.
		if _, err := store.Pool().Exec(ctx,
			`UPDATE admin_users SET must_change_password = true WHERE id = $1`, existing.ID); err != nil {
			return err
		}
		fmt.Println("reset admin to the default password; the change prompt is armed again")
		return nil
	} else if !errors.Is(err, repository.ErrNotFound) {
		return err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(service.SeededAdminPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	admin := &models.AdminUser{
		Username:           "admin",
		PasswordHash:       string(hash),
		Role:               models.RoleAdmin,
		MustChangePassword: true,
	}
	if err := store.Admins.Create(ctx, admin); err != nil {
		return err
	}
	fmt.Printf("created admin user %q (must change password on first sign-in)\n", admin.Username)
	return nil
}

// seedKYC gives the review queue something real to act against. The document
// URL is deliberately not a link: the user-facing upload flow is out of scope
// this round, and a plausible-looking URL would invite someone to click it.
func seedKYC(ctx context.Context, store *repository.Store, cfg *config.Config) error {
	existing, err := store.KYC.PendingCount(ctx)
	if err != nil {
		return err
	}
	if existing > 0 {
		fmt.Printf("%d pending KYC submission(s) already present, not adding more\n", existing)
		return nil
	}

	const placeholderDoc = "PLACEHOLDER — no upload flow yet"

	for i := 1; i <= 3; i++ {
		phone := fmt.Sprintf("2547%08d", 10000000+i)

		user, err := store.Users.GetByPhone(ctx, phone)
		if errors.Is(err, repository.ErrNotFound) {
			hash, hErr := bcrypt.GenerateFromPassword([]byte("hunter2024"), bcrypt.DefaultCost)
			if hErr != nil {
				return hErr
			}
			name := fmt.Sprintf("KYC Fixture %d", i)
			user = &models.User{PhoneNumber: phone, PasswordHash: string(hash), FullName: &name}
			if err := store.Users.Create(ctx, user); err != nil {
				return err
			}

			// Give each fixture a real account so an admin can exercise
			// freeze and adjust against them too.
			account := &models.Account{UserID: user.ID, AccountType: models.AccountReal, Currency: "USD"}
			if err := store.Accounts.Create(ctx, account); err != nil {
				return err
			}
			seedBalance := decimal.NewFromInt(int64(100 * i))
			if err := store.Wallets.Create(ctx, account.ID, seedBalance, decimal.Zero); err != nil {
				return err
			}
			ref := "seed-fixture"
			if err := store.Transactions.Insert(ctx, &models.Transaction{
				AccountID: account.ID, Type: models.TxDeposit, BalanceType: models.BalanceReal,
				Amount: seedBalance, Status: models.StatusCompleted, Reference: &ref,
				Metadata: map[string]any{"note": "development fixture"},
			}); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}

		if err := store.KYC.Create(ctx, &models.KYCSubmission{
			UserID:      user.ID,
			IDNumber:    fmt.Sprintf("2%07d", 1000000+i),
			DocumentURL: placeholderDoc,
		}); err != nil {
			return err
		}
	}

	fmt.Println("created 3 pending KYC submissions with placeholder documents")
	return nil
}

// enableBotForDevelopment turns automated trading on so a developer and CI can
// exercise it without a SQL statement.
//
// It is unconditional here only because this whole command already refuses to
// run outside development (see run()). Production seeds nothing: bot_enabled
// defaults to false there and stays false until someone decides otherwise,
// which is the point of the flag while the payout table is a placeholder.
//
// Real money stays off even in development. A local run should not be able to
// practise on the one path that has not been reviewed.
func enableBotForDevelopment(ctx context.Context, store *repository.Store) error {
	if err := store.Settings.Set(ctx, repository.SettingBotEnabled, "true"); err != nil {
		return err
	}
	fmt.Println("automated trading enabled (development only); real-money runs remain off")
	return nil
}

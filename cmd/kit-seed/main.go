// Command kit-seed sets up a local development workspace: a tenant, an
// admin user you can sign in as through /<slug>/dev-login, and sample data
// for the apps that have a seeder. It refuses to run unless KIT_ENV=dev, so
// it can't be pointed at production by accident. Running it again is safe:
// it reuses the workspace and skips apps that already have data.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/mrdon/kit/internal/config"
	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/database"
	"github.com/mrdon/kit/internal/logger"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
)

const (
	devTeamID   = "T_DEV"
	devSlug     = "dev-brewery"
	devName     = "Dev Brewery"
	devUserID   = "U_DEV"
	devUserName = "Dev Admin"
	devTimezone = "America/Denver"
)

// seeder fills one app with sample data for the dev workspace. It should
// do nothing if the app already has data.
type seeder struct {
	name string
	run  func(ctx context.Context, pool *pgxpool.Pool, admin *services.Caller) error
}

var seeders = []seeder{
	{name: "cogs", run: seedCogs},
}

func main() {
	logger.Init()
	if err := run(context.Background()); err != nil {
		slog.Error("seeding failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	if cfg.Env != "dev" {
		return errors.New("KIT_ENV must be dev: this command writes sample data")
	}
	pool, err := database.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}
	defer pool.Close()
	sqlDB := stdlib.OpenDBFromPool(pool)
	if err := database.Migrate(sqlDB); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}
	_ = sqlDB.Close()

	admin, err := ensureWorkspace(ctx, pool, cfg.EncryptionKey)
	if err != nil {
		return err
	}
	for _, s := range seeders {
		if err := s.run(ctx, pool, admin); err != nil {
			return fmt.Errorf("seeding %s: %w", s.name, err)
		}
	}
	printURLs(cfg.Port)
	return nil
}

// ensureWorkspace creates (or reuses) the dev tenant and its admin user.
func ensureWorkspace(ctx context.Context, pool *pgxpool.Pool, encKey string) (*services.Caller, error) {
	enc, err := crypto.NewEncryptor(encKey)
	if err != nil {
		return nil, fmt.Errorf("ENCRYPTION_KEY: %w", err)
	}
	// A real-looking encrypted token so code that decrypts it doesn't
	// fail; Slack calls with it fail, which is expected locally.
	token, err := enc.Encrypt("xoxb-dev-placeholder")
	if err != nil {
		return nil, fmt.Errorf("encrypting placeholder token: %w", err)
	}
	tenant, err := models.UpsertTenant(ctx, pool, devTeamID, devName, token, devSlug, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("creating tenant: %w", err)
	}
	if err := models.UpdateTenantSetup(ctx, pool, tenant.ID, "brewery", devTimezone); err != nil {
		return nil, err
	}
	user, err := models.GetOrCreateUser(ctx, pool, tenant.ID, devUserID, devUserName, devTimezone)
	if err != nil {
		return nil, fmt.Errorf("creating user: %w", err)
	}
	if _, err := models.GetOrCreateRole(ctx, pool, tenant.ID, models.RoleAdmin, "Workspace admins"); err != nil {
		return nil, fmt.Errorf("creating admin role: %w", err)
	}
	if err := models.AssignRole(ctx, pool, tenant.ID, user.ID, models.RoleAdmin); err != nil {
		return nil, fmt.Errorf("making user admin: %w", err)
	}
	return &services.Caller{
		TenantID: tenant.ID, UserID: user.ID, Identity: devUserID, Label: devUserName,
		Roles: []string{models.RoleAdmin}, IsAdmin: true, Timezone: devTimezone,
	}, nil
}

func printURLs(port string) {
	if port == "" {
		port = "8080"
	}
	base := "http://localhost:" + port + "/" + devSlug
	fmt.Println()
	fmt.Println("Dev workspace ready. Start Kit (make serve), then open:")
	fmt.Println("  Sign in:  " + base + "/dev-login?user=" + devUserID)
	fmt.Println("  Console:  " + base + "/web")
	fmt.Println("  COGS:     " + base + "/web/cogs")
	fmt.Println()
}

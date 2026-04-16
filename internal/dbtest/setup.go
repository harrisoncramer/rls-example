package dbtest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/chariot-giving/blueprint"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DBTX matches the interface that SQLC and pgxpool both satisfy.
type DBTX interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// New creates a test database with all migrations applied.
func New(t *testing.T, preFuncs ...blueprint.PreFunc) (*blueprint.TestDb, error) {
	migrations, err := getMigrations()
	if err != nil {
		return nil, fmt.Errorf("failed to get migrations: %w", err)
	}

	dbURLBase := os.Getenv("TEST_DATABASE_URL_BASE")
	dbName := os.Getenv("TEST_DATABASE_NAME")

	if dbURLBase == "" {
		return nil, fmt.Errorf("TEST_DATABASE_URL_BASE is not set")
	}
	if dbName == "" {
		return nil, fmt.Errorf("TEST_DATABASE_NAME is not set")
	}

	return blueprint.New(t,
		blueprint.WithDatabaseURLBase(dbURLBase),
		blueprint.WithDatabaseName(dbName),
		blueprint.WithMigrations(migrations...),
		blueprint.WithPreFuncs(preFuncs...),
	)
}

// SetOrg sets the app.current_org session variable on a connection.
func SetOrg(ctx context.Context, db DBTX, orgID string) error {
	_, err := db.Exec(ctx, fmt.Sprintf("SET app.current_org = '%s'", orgID))
	if err != nil {
		return fmt.Errorf("failed to set app.current_org: %w", err)
	}
	return nil
}

// ResetOrg clears the app.current_org session variable.
func ResetOrg(ctx context.Context, db DBTX) error {
	_, err := db.Exec(ctx, "RESET app.current_org")
	if err != nil {
		return fmt.Errorf("failed to reset app.current_org: %w", err)
	}
	return nil
}

func getMigrations() ([]blueprint.Migration, error) {
	migrationPaths, err := getAllMigrationPaths()
	if err != nil {
		return nil, fmt.Errorf("failed to get migration paths: %w", err)
	}

	var migrations []blueprint.Migration
	for _, path := range migrationPaths {
		migration, err := blueprint.NewFileMigration(path)
		if err != nil {
			return nil, fmt.Errorf("failed to create migration from %s: %w", path, err)
		}
		migrations = append(migrations, migration)
	}

	return migrations, nil
}

func getAllMigrationPaths() ([]string, error) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		return nil, fmt.Errorf("failed to get current file path")
	}

	migrationDir := filepath.Join(filepath.Dir(currentFile), "../../prisma/migrations")

	entries, err := os.ReadDir(migrationDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read migrations directory: %w", err)
	}

	var migrationDirs []string
	for _, entry := range entries {
		if entry.IsDir() {
			migrationDirs = append(migrationDirs, entry.Name())
		}
	}
	sort.Strings(migrationDirs)

	var migrationPaths []string
	for _, dir := range migrationDirs {
		dirPath := filepath.Join(migrationDir, dir)
		files, err := os.ReadDir(dirPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read migration directory %s: %w", dir, err)
		}

		for _, file := range files {
			if !strings.HasSuffix(file.Name(), ".sql") {
				continue
			}
			migrationPaths = append(migrationPaths, filepath.Join(dirPath, file.Name()))
		}
	}

	return migrationPaths, nil
}

package dbtest

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/chariot-giving/blueprint"
)

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

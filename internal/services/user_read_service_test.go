package services

import (
	"testing"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

// userTestEnv contains test data for user read service tests
type userTestEnv struct {
	UserID1 int
	UserID2 int
}

// createUserTestDB creates a test database for user read service tests
func createUserTestDB(t *testing.T) database.Database {
	t.Helper()

	tdb := testutils.CreateTestDB(t, true)
	if tdb.Engine == "sqlite" {
		t.Cleanup(func() { _ = tdb.Close() })
	}
	return tdb.DB
}

// setupUserTestEnv creates test data for user read service tests
func setupUserTestEnv(t *testing.T, db database.Database) userTestEnv {
	t.Helper()

	// Create test users
	userID1 := testutils.InsertID(t, db, `
		INSERT INTO users (username, email, first_name, last_name, password_hash, is_active, created_at, updated_at)
		VALUES ('user1', 'user1@example.com', 'First', 'User', 'hash', true, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`)

	userID2 := testutils.InsertID(t, db, `
		INSERT INTO users (username, email, first_name, last_name, password_hash, is_active, created_at, updated_at)
		VALUES ('user2', 'user2@example.com', 'Second', 'User', 'hash', true, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`)

	return userTestEnv{
		UserID1: userID1,
		UserID2: userID2,
	}
}

func TestUserReadService_List(t *testing.T) {
	db := createUserTestDB(t)

	service := NewUserReadService(db)
	env := setupUserTestEnv(t, db)
	_ = env // suppress unused warning

	t.Run("ReturnsActiveUsers", func(t *testing.T) {
		users, total, err := service.List(PaginationParams{Limit: 100, Offset: 0})
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if len(users) < 2 {
			t.Errorf("Expected at least 2 users, got %d", len(users))
		}
		if total < 2 {
			t.Errorf("Expected total at least 2, got %d", total)
		}
	})

	t.Run("Pagination", func(t *testing.T) {
		users, _, err := service.List(PaginationParams{Limit: 1, Offset: 0})
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if len(users) != 1 {
			t.Errorf("Expected 1 user with limit 1, got %d", len(users))
		}
	})

	t.Run("ExcludesInactiveUsers", func(t *testing.T) {
		// Create an inactive user
		db.Exec(`
			INSERT INTO users (username, email, first_name, last_name, password_hash, is_active, created_at, updated_at)
			VALUES ('inactive', 'inactive@example.com', 'Inactive', 'User', 'hash', FALSE, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		`)

		users, _, err := service.List(PaginationParams{Limit: 100, Offset: 0})
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		for _, u := range users {
			if u.Username == "inactive" {
				t.Error("Expected inactive user to be excluded from list")
			}
		}
	})
}

func TestUserReadService_GetByID(t *testing.T) {
	db := createUserTestDB(t)

	service := NewUserReadService(db)
	env := setupUserTestEnv(t, db)

	t.Run("Success", func(t *testing.T) {
		user, err := service.GetByID(env.UserID1)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if user.ID != env.UserID1 {
			t.Errorf("Expected user ID %d, got %d", env.UserID1, user.ID)
		}
		if user.Username != "user1" {
			t.Errorf("Expected username 'user1', got '%s'", user.Username)
		}
		if user.FullName != "First User" {
			t.Errorf("Expected full name 'First User', got '%s'", user.FullName)
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		_, err := service.GetByID(99999)
		if err == nil {
			t.Error("Expected error for non-existent user")
		}
	})
}

func TestUserReadService_Exists(t *testing.T) {
	db := createUserTestDB(t)

	service := NewUserReadService(db)
	env := setupUserTestEnv(t, db)

	t.Run("ExistingUser", func(t *testing.T) {
		exists, err := service.Exists(env.UserID1)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if !exists {
			t.Error("Expected user to exist")
		}
	})

	t.Run("NonExistentUser", func(t *testing.T) {
		exists, err := service.Exists(99999)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if exists {
			t.Error("Expected user to not exist")
		}
	})
}

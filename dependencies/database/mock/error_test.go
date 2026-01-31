package mock_test

import (
	"encoding/json"
	"testing"

	"github.com/ti/common-go/dependencies/database/mock"
)

func TestErrorJSONFormat(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		expectedJSON  string
		checkFields   map[string]string
	}{
		{
			name:         "NotFoundError",
			err:          mock.NewNotFoundError("users"),
			expectedJSON: `{"error_code":"not_found","error_message":"record not found","error_description":"no record found in table 'users'"}`,
			checkFields: map[string]string{
				"error_code":        "not_found",
				"error_message":     "record not found",
				"error_description": "no record found in table 'users'",
			},
		},
		{
			name:         "InvalidArgumentError",
			err:          mock.NewInvalidArgumentError("row_position", "position out of range"),
			expectedJSON: `{"error_code":"invalid_argument","error_message":"invalid argument","error_description":"argument 'row_position': position out of range"}`,
			checkFields: map[string]string{
				"error_code":        "invalid_argument",
				"error_message":     "invalid argument",
				"error_description": "argument 'row_position': position out of range",
			},
		},
		{
			name:         "TransactionError",
			err:          mock.NewTransactionError("commit", "transaction already committed"),
			expectedJSON: `{"error_code":"transaction_error","error_message":"transaction operation failed","error_description":"commit: transaction already committed"}`,
			checkFields: map[string]string{
				"error_code":        "transaction_error",
				"error_message":     "transaction operation failed",
				"error_description": "commit: transaction already committed",
			},
		},
		{
			name:         "DatabaseError",
			err:          mock.NewDatabaseError("connection failed"),
			expectedJSON: `{"error_code":"database_error","error_message":"connection failed"}`,
			checkFields: map[string]string{
				"error_code":    "database_error",
				"error_message": "connection failed",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test JSON marshaling
			jsonBytes, err := json.Marshal(tt.err)
			if err != nil {
				t.Fatalf("Failed to marshal error to JSON: %v", err)
			}

			jsonStr := string(jsonBytes)
			t.Logf("JSON output: %s", jsonStr)

			// Verify it matches expected JSON
			if jsonStr != tt.expectedJSON {
				t.Errorf("JSON mismatch\nExpected: %s\nGot:      %s", tt.expectedJSON, jsonStr)
			}

			// Unmarshal and verify snake_case field names
			var result map[string]interface{}
			err = json.Unmarshal(jsonBytes, &result)
			if err != nil {
				t.Fatalf("Failed to unmarshal JSON: %v", err)
			}

			// Verify all expected fields exist with correct values
			for key, expectedValue := range tt.checkFields {
				actualValue, ok := result[key]
				if !ok {
					t.Errorf("Field '%s' not found in JSON", key)
					continue
				}
				if actualValue != expectedValue {
					t.Errorf("Field '%s': expected '%v', got '%v'", key, expectedValue, actualValue)
				}
			}

			// Ensure no camelCase fields exist
			camelCaseFields := []string{"errorCode", "errorMessage", "errorDescription"}
			for _, field := range camelCaseFields {
				if _, ok := result[field]; ok {
					t.Errorf("Found camelCase field '%s', all fields should be snake_case", field)
				}
			}
		})
	}
}

func TestErrorInterface(t *testing.T) {
	err := mock.NewNotFoundError("users")

	// Test that it implements error interface
	var _ error = err

	// Test Error() method returns the message
	if err.Error() != "record not found" {
		t.Errorf("Expected error message 'record not found', got '%s'", err.Error())
	}
}

func TestCommonErrors(t *testing.T) {
	// Test pre-defined errors
	tests := []struct {
		name         string
		err          *mock.Error
		expectedCode string
		expectedMsg  string
	}{
		{
			name:         "ErrNotFound",
			err:          mock.ErrNotFound,
			expectedCode: "not_found",
			expectedMsg:  "record not found",
		},
		{
			name:         "ErrInvalidArgument",
			err:          mock.ErrInvalidArgument,
			expectedCode: "invalid_argument",
			expectedMsg:  "invalid argument",
		},
		{
			name:         "ErrAlreadyExists",
			err:          mock.ErrAlreadyExists,
			expectedCode: "already_exists",
			expectedMsg:  "record already exists",
		},
		{
			name:         "ErrInvalidOperation",
			err:          mock.ErrInvalidOperation,
			expectedCode: "invalid_operation",
			expectedMsg:  "invalid operation",
		},
		{
			name:         "ErrTransactionClosed",
			err:          mock.ErrTransactionClosed,
			expectedCode: "transaction_closed",
			expectedMsg:  "transaction already closed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Verify JSON format is snake_case
			jsonBytes, err := json.Marshal(tt.err)
			if err != nil {
				t.Fatalf("Failed to marshal error: %v", err)
			}

			var result map[string]interface{}
			json.Unmarshal(jsonBytes, &result)

			if result["error_code"] != tt.expectedCode {
				t.Errorf("Expected error_code '%s', got '%v'", tt.expectedCode, result["error_code"])
			}
			if result["error_message"] != tt.expectedMsg {
				t.Errorf("Expected error_message '%s', got '%v'", tt.expectedMsg, result["error_message"])
			}

			// Ensure snake_case fields exist
			if _, ok := result["error_code"]; !ok {
				t.Error("Field 'error_code' not found - should be snake_case")
			}
			if _, ok := result["error_message"]; !ok {
				t.Error("Field 'error_message' not found - should be snake_case")
			}
		})
	}
}

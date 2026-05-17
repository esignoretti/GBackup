package gws

import (
	"errors"
	"strings"

	"google.golang.org/api/googleapi"
)

// IsRetryable returns true for transient Google API errors: HTTP 429 and 5xx.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	var ge *googleapi.Error
	if errors.As(err, &ge) {
		return ge.Code == 429 || (ge.Code >= 500 && ge.Code < 600)
	}
	return false
}

// IsServiceDisabled returns true when the underlying API is not enabled
// in the Google Cloud project (403 with reason accessNotConfigured / SERVICE_DISABLED).
func IsServiceDisabled(err error) bool {
	if err == nil {
		return false
	}
	var ge *googleapi.Error
	if !errors.As(err, &ge) {
		return false
	}
	if ge.Code != 403 {
		return false
	}
	for _, item := range ge.Errors {
		switch item.Reason {
		case "accessNotConfigured", "SERVICE_DISABLED":
			return true
		}
	}
	if ge.Message != "" {
		for _, marker := range []string{"SERVICE_DISABLED", "accessNotConfigured", "not been used in project"} {
			if strings.Contains(ge.Message, marker) {
				return true
			}
		}
	}
	return false
}

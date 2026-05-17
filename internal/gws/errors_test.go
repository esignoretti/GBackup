package gws

import (
	"errors"
	"fmt"
	"testing"

	"google.golang.org/api/googleapi"
)

func TestIsRetryable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"429", &googleapi.Error{Code: 429}, true},
		{"500", &googleapi.Error{Code: 500}, true},
		{"503", &googleapi.Error{Code: 503}, true},
		{"403", &googleapi.Error{Code: 403}, false},
		{"400", &googleapi.Error{Code: 400}, false},
		{"wrapped 503", fmt.Errorf("wrapping: %w", &googleapi.Error{Code: 503}), true},
		{"plain error", errors.New("nope"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsRetryable(c.err); got != c.want {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestIsServiceDisabled(t *testing.T) {
	e := &googleapi.Error{Code: 403, Errors: []googleapi.ErrorItem{{Reason: "accessNotConfigured"}}}
	if !IsServiceDisabled(e) {
		t.Fatal("expected IsServiceDisabled true for accessNotConfigured")
	}
	withMsg := &googleapi.Error{Code: 403, Message: "Gmail API has not been used in project 123 before"}
	if !IsServiceDisabled(withMsg) {
		t.Fatal("expected IsServiceDisabled true for message-only marker")
	}
	if IsServiceDisabled(&googleapi.Error{Code: 500}) {
		t.Fatal("expected IsServiceDisabled false for 500")
	}
	if IsServiceDisabled(&googleapi.Error{Code: 403, Message: "forbidden"}) {
		t.Fatal("expected IsServiceDisabled false for plain 403")
	}
}

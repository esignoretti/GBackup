package gws

import (
	"context"
	"fmt"
	"os"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const (
	driveScope     = "https://www.googleapis.com/auth/drive.readonly"
	gmailScope     = "https://www.googleapis.com/auth/gmail.readonly"
	calendarScope  = "https://www.googleapis.com/auth/calendar.readonly"
	contactsScope  = "https://www.googleapis.com/auth/contacts.readonly"
	directoryScope = "https://www.googleapis.com/auth/admin.directory.user.readonly"
)

var serviceScopes = map[string][]string{
	"drive":    {driveScope},
	"gmail":    {gmailScope},
	"calendar": {calendarScope},
	"contacts": {contactsScope},
}

func ScopesForService(service string) []string {
	return serviceScopes[service]
}

func AllScopes() []string {
	return []string{driveScope, gmailScope, calendarScope, contactsScope, directoryScope}
}

type AuthConfig struct {
	ServiceAccountFile string
	AdminEmail         string
}

// UserTokenSource returns an oauth2.TokenSource configured to impersonate the
// given user via Google Workspace domain-wide delegation (JWT with Subject).
//
// This is the only correct way to "act as" a user from a service account
// against per-user Google APIs (Gmail/Drive/Calendar/People). The other
// option (option.ImpersonateCredentials) is for service-account-to-service-account
// impersonation and silently falls back to the service account's own identity
// when given a user email — which means callers get empty results instead of
// errors.
func UserTokenSource(ctx context.Context, serviceAccountFile, user string, scopes []string) (oauth2.TokenSource, error) {
	keyData, err := os.ReadFile(serviceAccountFile)
	if err != nil {
		return nil, fmt.Errorf("reading service account key: %w", err)
	}
	jwtCfg, err := google.JWTConfigFromJSON(keyData, scopes...)
	if err != nil {
		return nil, fmt.Errorf("creating JWT config: %w", err)
	}
	jwtCfg.Subject = user
	return jwtCfg.TokenSource(ctx), nil
}

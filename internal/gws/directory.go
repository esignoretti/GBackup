package gws

import (
	"context"
	"fmt"
	"time"

	admin "google.golang.org/api/admin/directory/v1"
	"google.golang.org/api/option"
)

func newDirectoryService(ctx context.Context, auth *AuthConfig) (*admin.Service, error) {
	ts, err := UserTokenSource(ctx, auth.ServiceAccountFile, auth.AdminEmail, []string{directoryScope})
	if err != nil {
		return nil, err
	}
	return admin.NewService(ctx, option.WithTokenSource(ts))
}

type UserInfo struct {
	PrimaryEmail string
	FullName     string
	IsSuspended  bool
}

type DirectoryService struct {
	service *admin.Service
}

func NewDirectoryService(auth *AuthConfig) (*DirectoryService, error) {
	ctx := context.Background()
	svc, err := newDirectoryService(ctx, auth)
	if err != nil {
		return nil, fmt.Errorf("creating directory service: %w", err)
	}
	return &DirectoryService{service: svc}, nil
}

func (d *DirectoryService) ListUsers(ctx context.Context) ([]*UserInfo, error) {
	var users []*UserInfo
	pageToken := ""
	for {
		listCtx, listCancel := context.WithTimeout(ctx, 60*time.Second)
		call := d.service.Users.List().Context(listCtx).
			Customer("my_customer").
			MaxResults(500).
			OrderBy("email")
		if pageToken != "" {
			call.PageToken(pageToken)
		}
		resp, err := call.Do()
		listCancel()
		if err != nil {
			return nil, fmt.Errorf("listing users: %w", err)
		}
		for _, u := range resp.Users {
			users = append(users, &UserInfo{
				PrimaryEmail: u.PrimaryEmail,
				FullName:     u.Name.FullName,
				IsSuspended:  u.Suspended,
			})
		}
		pageToken = resp.NextPageToken
		if pageToken == "" {
			break
		}
	}
	return users, nil
}

func (d *DirectoryService) Close() error {
	return nil
}

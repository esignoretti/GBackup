package gws

import (
	"context"
	"fmt"

	admin "google.golang.org/api/admin/directory/v1"
	"google.golang.org/api/option"
)

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
	svc, err := admin.NewService(ctx,
		option.WithCredentialsFile(auth.ServiceAccountFile),
		option.WithScopes(directoryScope),
		option.ImpersonateCredentials(auth.AdminEmail),
	)
	if err != nil {
		return nil, fmt.Errorf("creating directory service: %w", err)
	}
	return &DirectoryService{service: svc}, nil
}

func (d *DirectoryService) ListUsers(ctx context.Context) ([]*UserInfo, error) {
	var users []*UserInfo
	pageToken := ""
	for {
		call := d.service.Users.List().Context(ctx).
			Customer("my_customer").
			MaxResults(500).
			OrderBy("email")
		if pageToken != "" {
			call.PageToken(pageToken)
		}
		resp, err := call.Do()
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

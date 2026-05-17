package main

import (
	"context"
	"fmt"
	"os"

	"golang.org/x/oauth2/google"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

func main() {
	if len(os.Args) < 4 {
		fmt.Println("Usage: debuggmail <key.json> <impersonate-user> <scope>")
		os.Exit(1)
	}
	keyFile := os.Args[1]
	user := os.Args[2]
	scope := os.Args[3]

	ctx := context.Background()

	keyData, err := os.ReadFile(keyFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR reading key: %v\n", err)
		os.Exit(1)
	}

	jwtCfg, err := google.JWTConfigFromJSON(keyData, scope)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR JWTConfigFromJSON: %v\n", err)
		os.Exit(1)
	}
	jwtCfg.Subject = user

	ts := jwtCfg.TokenSource(ctx)
	gmailSvc, err := gmail.NewService(ctx,
		option.WithTokenSource(ts),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR create gmail svc: %v\n", err)
		os.Exit(1)
	}

	_, err = gmailSvc.Users.GetProfile(user).Do()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR GetProfile: %v\n", err)
	} else {
		fmt.Printf("Profile OK\n")
	}
}

package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/aarondl/authboss/v3"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/endpoints"
)

const (
	googleProvider    = "google"
	googleUserInfoURL = "https://openidconnect.googleapis.com/v1/userinfo"
	oauth2PIDPrefix   = "oauth2;;"

	// Keys of the details map that FindUserDetails hands to Storer.NewFromOAuth2.
	detailUID           = "uid"
	detailEmail         = "email"
	detailEmailVerified = "email_verified"
	detailName          = "name"
)

// GoogleConfig enables "Sign in with Google". The client comes from the Google Cloud console
// (OAuth client of type "Web application") with {APIURL}/auth/oauth2/callback/google as an
// authorized redirect URI.
type GoogleConfig struct {
	ClientID     string
	ClientSecret string

	// Endpoint and UserInfoURL default to Google's; tests point them at a fake.
	Endpoint    oauth2.Endpoint
	UserInfoURL string
}

func (google GoogleConfig) enabled() bool { return google.ClientID != "" && google.ClientSecret != "" }

func (google GoogleConfig) provider() authboss.OAuth2Provider {
	endpoint := google.Endpoint
	if endpoint.AuthURL == "" {
		endpoint = endpoints.Google
	}
	userInfoURL := google.UserInfoURL
	if userInfoURL == "" {
		userInfoURL = googleUserInfoURL
	}
	return authboss.OAuth2Provider{
		OAuth2Config: &oauth2.Config{
			ClientID:     google.ClientID,
			ClientSecret: google.ClientSecret,
			Endpoint:     endpoint,
			Scopes:       []string{"openid", "email", "profile"},
			// RedirectURL is set by the oauth2 module from Paths.RootURL.
		},
		// Always show the account chooser, so signing out of the app and back in can pick a
		// different Google account.
		AdditionalParams: url.Values{"prompt": {"select_account"}},
		FindUserDetails:  googleUserDetails(userInfoURL),
	}
}

type googleUserInfo struct {
	Sub           string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
}

// googleUserDetails reads the signed-in Google account from the OpenID Connect userinfo endpoint.
func googleUserDetails(userInfoURL string) func(context.Context, oauth2.Config, *oauth2.Token) (map[string]string, error) {
	return func(ctx context.Context, config oauth2.Config, token *oauth2.Token) (map[string]string, error) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, userInfoURL, nil)
		if err != nil {
			return nil, err
		}
		response, err := config.Client(ctx, token).Do(request)
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("auth: google userinfo answered %s", response.Status)
		}
		var info googleUserInfo
		if err := json.NewDecoder(response.Body).Decode(&info); err != nil {
			return nil, fmt.Errorf("auth: reading google userinfo: %w", err)
		}
		return map[string]string{
			detailUID:           info.Sub,
			detailEmail:         info.Email,
			detailEmailVerified: strconv.FormatBool(info.EmailVerified),
			detailName:          info.Name,
		}, nil
	}
}

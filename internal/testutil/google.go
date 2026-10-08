package testutil

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/oauth2"

	"meta-frames-server/internal/auth"
)

// FakeGoogle stands in for Google's token and userinfo endpoints. It accepts the code "good-code"
// and answers userinfo with Account.
type FakeGoogle struct {
	Server      *httptest.Server
	CallbackURL string
	Account     map[string]any
}

func NewFakeGoogle(test *testing.T, callbackURL string) *FakeGoogle {
	google := &FakeGoogle{
		CallbackURL: callbackURL,
		Account:     map[string]any{"sub": "g-1", "email": "Ansel@Example.com", "email_verified": true, "name": "Ansel Adams"},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, request *http.Request) {
		if request.FormValue("code") != "good-code" || request.FormValue("redirect_uri") != google.CallbackURL {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"access","token_type":"Bearer","expires_in":3600}`))
	})
	mux.HandleFunc("GET /userinfo", func(w http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer access" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(google.Account)
	})
	google.Server = httptest.NewServer(mux)
	test.Cleanup(google.Server.Close)
	return google
}

// Config points auth at the fake.
func (google *FakeGoogle) Config() auth.GoogleConfig {
	return auth.GoogleConfig{
		ClientID:     "client",
		ClientSecret: "secret",
		Endpoint:     oauth2.Endpoint{AuthURL: google.Server.URL + "/auth", TokenURL: google.Server.URL + "/token", AuthStyle: oauth2.AuthStyleInParams},
		UserInfoURL:  google.Server.URL + "/userinfo",
	}
}

package openai

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"ox/internal/keyring"
)

// Ox signs in with the Codex CLI's OAuth client, whose tokens the Codex
// backend accepts for ChatGPT subscription usage.
const (
	clientID     = "app_EMoamEEZ73f0CkXaXp7hrann"
	authorizeURL = "https://auth.openai.com/oauth/authorize"
	tokenURL     = "https://auth.openai.com/oauth/token"
	// The client accepts only this redirect URI.
	redirectURI    = "http://localhost:1455/auth/callback"
	callbackListen = "127.0.0.1:1455"
)

// Credential is the saved OpenAI sign-in.
type Credential struct {
	Access    string    `json:"access"`
	Refresh   string    `json:"refresh"`
	Expires   time.Time `json:"expires"`
	AccountID string    `json:"account_id"`
}

// Login signs in through the browser, saves the credential in the keyring,
// and reports progress to out. The browser is opened when possible; the
// sign-in URL is printed either way.
func Login(ctx context.Context, out io.Writer) error {
	verifier := rand.Text() + rand.Text()
	challenge := sha256.Sum256([]byte(verifier))
	state := rand.Text()
	query := url.Values{
		"response_type":              {"code"},
		"client_id":                  {clientID},
		"redirect_uri":               {redirectURI},
		"scope":                      {"openid profile email offline_access"},
		"code_challenge":             {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method":      {"S256"},
		"state":                      {state},
		"id_token_add_organizations": {"true"},
		"codex_cli_simplified_flow":  {"true"},
		"originator":                 {originator},
	}
	listener, err := net.Listen("tcp", callbackListen)
	if err != nil {
		return fmt.Errorf("could not listen for the OpenAI sign-in callback on %s; close any other sign-in using it: %w", callbackListen, err)
	}
	codes := make(chan string, 1)
	failures := make(chan error, 1)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/callback" {
			http.NotFound(w, r)
			return
		}
		params := r.URL.Query()
		switch {
		case params.Get("state") != state:
			http.Error(w, "The sign-in state does not match. Start the sign-in again.", http.StatusBadRequest)
			return
		case params.Get("error") != "":
			http.Error(w, "Sign-in failed.", http.StatusBadRequest)
			select {
			case failures <- fmt.Errorf("OpenAI sign-in failed: %s %s", params.Get("error"), params.Get("error_description")):
			default:
			}
			return
		case params.Get("code") == "":
			http.Error(w, "The sign-in returned no code.", http.StatusBadRequest)
			return
		}
		fmt.Fprintln(w, "Signed in to Ox. You can close this tab.")
		select {
		case codes <- params.Get("code"):
		default:
		}
	})}
	go server.Serve(listener)
	defer server.Close()

	link := authorizeURL + "?" + query.Encode()
	fmt.Fprintf(out, "Sign in to OpenAI in your browser:\n\n%s\n\n", link)
	openBrowser(link)

	var code string
	select {
	case code = <-codes:
	case err := <-failures:
		return err
	case <-ctx.Done():
		return errors.New("the OpenAI sign-in was cancelled")
	}
	credential, err := requestTokens(ctx, tokenURL, url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {redirectURI},
	})
	if err != nil {
		return err
	}
	return save(credential)
}

func openBrowser(link string) {
	command := "xdg-open"
	if runtime.GOOS == "darwin" {
		command = "open"
	}
	// The printed URL still works when no browser opens.
	_ = exec.Command(command, link).Start()
}

// requestTokens posts form to the token endpoint and returns the credential
// it grants.
func requestTokens(ctx context.Context, endpoint string, form url.Values) (Credential, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return Credential{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return Credential{}, fmt.Errorf("OpenAI token request failed: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return Credential{}, fmt.Errorf("OpenAI token request failed: %w", err)
	}
	if response.StatusCode/100 != 2 {
		return Credential{}, fmt.Errorf("OpenAI token request returned %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	var tokens struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokens); err != nil || tokens.AccessToken == "" || tokens.ExpiresIn <= 0 {
		return Credential{}, errors.New("OpenAI token response lacks an access token or its lifetime")
	}
	accountID, err := chatGPTAccountID(tokens.AccessToken)
	if err != nil {
		return Credential{}, err
	}
	return Credential{
		Access:    tokens.AccessToken,
		Refresh:   tokens.RefreshToken,
		Expires:   time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second),
		AccountID: accountID,
	}, nil
}

// chatGPTAccountID reads the ChatGPT account ID that the Codex backend
// requires as a header from the access token's claims.
func chatGPTAccountID(accessToken string) (string, error) {
	parts := strings.Split(accessToken, ".")
	if len(parts) != 3 {
		return "", errors.New("the OpenAI access token is not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("the OpenAI access token is not a JWT: %w", err)
	}
	var claims struct {
		Auth struct {
			AccountID string `json:"chatgpt_account_id"`
		} `json:"https://api.openai.com/auth"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Auth.AccountID == "" {
		return "", errors.New("the OpenAI access token has no ChatGPT account ID")
	}
	return claims.Auth.AccountID, nil
}

// Saved reports whether an OpenAI sign-in is saved.
func Saved() (bool, error) {
	text, err := keyring.Get(keyring.OpenAI)
	return text != "", err
}

func load() (Credential, error) {
	text, err := keyring.Get(keyring.OpenAI)
	if err != nil {
		return Credential{}, err
	}
	if text == "" {
		return Credential{}, errors.New("OpenAI sign-in required; run `ox auth login openai`")
	}
	var credential Credential
	if err := json.Unmarshal([]byte(text), &credential); err != nil {
		return Credential{}, errors.New("the saved OpenAI sign-in is malformed; run `ox auth login openai`")
	}
	return credential, nil
}

func save(credential Credential) error {
	text, err := json.Marshal(credential)
	if err != nil {
		return err
	}
	return keyring.Set(keyring.OpenAI, string(text))
}

// Logout removes the saved sign-in and reports whether one was saved.
func Logout() (bool, error) {
	return keyring.Delete(keyring.OpenAI)
}

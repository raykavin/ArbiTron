// internal/exchange/kucoin/auth.go
package kucoin

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// ApiCredentials holds the authentication credentials for KuCoin API
type ApiCredentials struct {
	ApiKey     string
	ApiSecret  string
	Passphrase string
}

type InstanceServer struct {
	Endpoint     string `json:"endpoint"`
	Protocol     string `json:"protocol"`
	Encrypt      bool   `json:"encrypt"`
	PingInterval int    `json:"pingInterval"`
	PingTimeout  int    `json:"pingTimeout"`
}

type TokenResponseData struct {
	Token           string           `json:"token"`
	InstanceServers []InstanceServer `json:"instanceServers"`
}

// TokenResponse represents the response from KuCoin's bullet API
type TokenResponse struct {
	Code string            `json:"code"`
	Data TokenResponseData `json:"data"`
}

const (
	// API endpoints
	publicBulletEndpoint  = "/api/v1/bullet-public"
	privateBulletEndpoint = "/api/v1/bullet-private"

	// Base API URL
	baseURL = "https://api.kucoin.com"

	// Success code from KuCoin API
	successCode = "200000"

	// HTTP request timeout
	defaultTimeout = 15 * time.Second
)

// GetToken retrieves a WebSocket connection token from KuCoin
func GetToken(apiKey, apiSecret, passphrase string, isPrivate bool) (*TokenResponse, error) {
	creds := ApiCredentials{
		ApiKey:     apiKey,
		ApiSecret:  apiSecret,
		Passphrase: passphrase,
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	return GetTokenWithContext(ctx, creds, isPrivate)
}

// GetTokenWithContext retrieves a WebSocket connection token from KuCoin with context support
func GetTokenWithContext(ctx context.Context, creds ApiCredentials, isPrivate bool) (*TokenResponse, error) {
	endpoint := publicBulletEndpoint
	if isPrivate {
		endpoint = privateBulletEndpoint
	}

	// Create request with empty JSON body
	reqBody := []byte(`{}`)
	req, err := createSignedRequest(ctx, "POST", endpoint, reqBody, creds)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Execute request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	// Parse response
	tokenResp, err := parseTokenResponse(resp)
	if err != nil {
		return nil, err
	}

	return tokenResp, nil
}

// createSignedRequest creates a new HTTP request with KuCoin authentication headers
func createSignedRequest(ctx context.Context, method, endpoint string, body []byte, creds ApiCredentials) (*http.Request, error) {
	timestamp := strconv.FormatInt(time.Now().UnixNano()/1e6, 10)

	// Create the signature
	strToSign := timestamp + method + endpoint
	signature := createSignature(strToSign, creds.ApiSecret)

	// Create the request
	req, err := http.NewRequestWithContext(ctx, method, baseURL+endpoint, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("KC-API-KEY", creds.ApiKey)
	req.Header.Set("KC-API-SIGN", signature)
	req.Header.Set("KC-API-TIMESTAMP", timestamp)
	req.Header.Set("KC-API-PASSPHRASE", creds.Passphrase)

	return req, nil
}

// createSignature generates an HMAC-SHA256 signature for KuCoin API authentication
func createSignature(message, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(message))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// parseTokenResponse parses the HTTP response into a TokenResponse
func parseTokenResponse(resp *http.Response) (*TokenResponse, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Check response status code
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected HTTP status: %d, body: %s", resp.StatusCode, string(body))
	}

	var tokenResp TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w, body: %s", err, string(body))
	}

	// Check API response code
	if tokenResp.Code != successCode {
		return nil, fmt.Errorf("API error: code=%s, body=%s", tokenResp.Code, string(body))
	}

	return &tokenResp, nil
}

// IsValidToken checks if a token response contains valid data
func IsValidToken(token *TokenResponse) bool {
	return token != nil &&
		token.Code == successCode &&
		token.Data.Token != "" &&
		len(token.Data.InstanceServers) > 0 &&
		token.Data.InstanceServers[0].Endpoint != ""
}

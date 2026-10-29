package provisioning

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/verirelay/protocol/veriprov1"
)

const maxResponseBytes int64 = 1 << 20

// Reporter returns a raw Attestation Report with reportData as REPORT_DATA.
type Reporter interface {
	Report(ctx context.Context, reportData [64]byte) ([]byte, error)
}

// Client provisions a Runtime Config from VeriProv over veriprov/1.
type Client struct {
	BaseURL    *url.URL
	HTTPClient *http.Client
	Reporter   Reporter
	// IdentityKey is the VeriProv identity public key that must sign the
	// provision response envelope.
	IdentityKey ed25519.PublicKey
}

type httpStatusError struct {
	Method      string
	Endpoint    string
	Status      int
	RequestID   string
	ErrorCode   string
	ErrorDetail string
}

func (err *httpStatusError) Error() string {
	detail := fmt.Sprintf("VeriProv %s %s returned HTTP %d", err.Method, err.Endpoint, err.Status)
	if err.ErrorCode != "" {
		detail += fmt.Sprintf(" (%s", err.ErrorCode)
		if err.RequestID != "" {
			detail += fmt.Sprintf(", request_id=%s", err.RequestID)
		}
		detail += ")"
	}
	if err.ErrorDetail != "" {
		detail += ": " + err.ErrorDetail
	}
	return detail
}

// FetchRuntimeConfig runs one challenge and provision exchange and returns
// the validated Runtime Config.
func (client *Client) FetchRuntimeConfig(ctx context.Context) (veriprov1.RuntimeConfigV1, error) {
	if client.Reporter == nil {
		return veriprov1.RuntimeConfigV1{}, ErrReportUnavailable
	}
	challenge, err := client.fetchChallenge(ctx)
	if err != nil {
		return veriprov1.RuntimeConfigV1{}, err
	}
	nonce, err := veriprov1.DecodeBase64URL(challenge.Nonce, "nonce", veriprov1.NonceSize)
	if err != nil {
		return veriprov1.RuntimeConfigV1{}, fmt.Errorf("%w: %w", ErrInvalidResponse, err)
	}
	privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return veriprov1.RuntimeConfigV1{}, fmt.Errorf("generate provisioning key: %w", err)
	}
	publicKey := privateKey.PublicKey().Bytes()
	reportData, err := veriprov1.ReportData(nonce, publicKey)
	if err != nil {
		return veriprov1.RuntimeConfigV1{}, fmt.Errorf("%w: %w", ErrInvalidResponse, err)
	}
	report, err := client.Reporter.Report(ctx, reportData)
	if err != nil {
		return veriprov1.RuntimeConfigV1{}, fmt.Errorf("%w: %w", ErrReportUnavailable, err)
	}
	response, err := client.fetchProvision(ctx, veriprov1.ProvisionRequest{
		ChallengeID:           challenge.ChallengeID,
		ProvisioningPublicKey: veriprov1.EncodeBase64URL(publicKey),
		AttestationReport:     veriprov1.EncodeBase64URL(report),
	})
	if err != nil {
		return veriprov1.RuntimeConfigV1{}, err
	}
	envelope := response.Envelope
	if envelope.ChallengeID != challenge.ChallengeID {
		return veriprov1.RuntimeConfigV1{}, fmt.Errorf("%w: provision envelope challenge does not match request", ErrInvalidResponse)
	}
	if err := veriprov1.VerifyEnvelopeSignature(client.IdentityKey, envelope); err != nil {
		return veriprov1.RuntimeConfigV1{}, fmt.Errorf("%w: %w", ErrEnvelopeSignature, err)
	}
	plaintext, err := veriprov1.Open(privateKey.Bytes(), envelope)
	if err != nil {
		return veriprov1.RuntimeConfigV1{}, fmt.Errorf("%w: %w", ErrHPKEOpen, err)
	}
	return decodeRuntimeConfig(plaintext)
}

func decodeRuntimeConfig(data []byte) (veriprov1.RuntimeConfigV1, error) {
	var runtimeConfig veriprov1.RuntimeConfigV1
	if err := veriprov1.DecodeJSON(data, &runtimeConfig); err != nil {
		return veriprov1.RuntimeConfigV1{}, fmt.Errorf("decode runtime config: %w", err)
	}
	return runtimeConfig, nil
}

func (client *Client) fetchChallenge(ctx context.Context) (veriprov1.ChallengeResponse, error) {
	var response veriprov1.ChallengeResponse
	if err := client.doJSON(ctx, http.MethodPost, "/v1/challenge", []byte("{}"), &response); err != nil {
		return response, err
	}
	if err := response.Validate(); err != nil {
		return response, fmt.Errorf("%w: %w", ErrInvalidResponse, err)
	}
	return response, nil
}

func (client *Client) fetchProvision(ctx context.Context, request veriprov1.ProvisionRequest) (veriprov1.ProvisionResponse, error) {
	var response veriprov1.ProvisionResponse
	if err := request.Validate(); err != nil {
		return response, fmt.Errorf("invalid provision request: %w", err)
	}
	body, err := veriprov1.MarshalJSON(request)
	if err != nil {
		return response, err
	}
	if err := client.doJSON(ctx, http.MethodPost, "/v1/provision", body, &response); err != nil {
		return response, err
	}
	if err := response.Validate(); err != nil {
		return response, fmt.Errorf("%w: %w", ErrInvalidResponse, err)
	}
	return response, nil
}

func (client *Client) doJSON(ctx context.Context, method, endpoint string, body []byte, destination any) error {
	if client.BaseURL == nil || client.BaseURL.Scheme != "https" || client.BaseURL.Host == "" || client.BaseURL.User != nil {
		return fmt.Errorf("%w: invalid VeriProv URL", ErrUnavailable)
	}
	base := *client.BaseURL
	base.Path = strings.TrimSuffix(base.Path, "/") + endpoint
	base.RawQuery = ""
	base.Fragment = ""
	request, err := http.NewRequestWithContext(ctx, method, base.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	httpClient := client.HTTPClient
	if httpClient == nil {
		httpClient = NewHTTPClient()
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("%w: read VeriProv %s response: %w", ErrUnavailable, endpoint, err)
	}
	if int64(len(data)) > maxResponseBytes {
		return fmt.Errorf("%w: VeriProv %s response exceeds size limit", ErrInvalidResponse, endpoint)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		statusErr := &httpStatusError{Method: method, Endpoint: endpoint, Status: response.StatusCode, RequestID: response.Header.Get("X-Request-ID")}
		var errorResponse struct {
			Error struct {
				Code      string `json:"code"`
				Message   string `json:"message"`
				RequestID string `json:"request_id"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &errorResponse) == nil {
			statusErr.ErrorCode = errorResponse.Error.Code
			statusErr.ErrorDetail = errorResponse.Error.Message
			if errorResponse.Error.RequestID != "" {
				statusErr.RequestID = errorResponse.Error.RequestID
			}
		}
		return fmt.Errorf("%w: %w", ErrUnavailable, statusErr)
	}
	if err := veriprov1.DecodeJSON(data, destination); err != nil {
		return fmt.Errorf("%w: decode VeriProv %s response: %w", ErrInvalidResponse, endpoint, err)
	}
	return nil
}

// NewHTTPClient returns the client that reaches VeriProv: TLS validated
// against the system CA bundle, and no redirects.
func NewHTTPClient() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

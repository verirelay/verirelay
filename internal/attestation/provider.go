package attestation

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/google/go-sev-guest/abi"
	"github.com/verirelay/protocol/avr1"
)

// b64 encodes binary avr/1 values as unpadded base64url.
func b64(data []byte) string { return base64.RawURLEncoding.EncodeToString(data) }

var (
	ErrQuoteProviderUnavailable = errors.New("quote provider unavailable")
	ErrUnsupportedReportSigner  = errors.New("unsupported SEV-SNP report signer")
	ErrInvalidAttestationReport = errors.New("invalid attestation report")
	ErrIncompleteEndorsements   = errors.New("incomplete SEV-SNP endorsements")
)

type QuoteRequest struct {
	ReportData [64]byte
}

// Quote is a raw AMD SEV-SNP Attestation Report and its AMD endorsements.
type Quote struct {
	RawReport    []byte
	Endorsements avr1.AttestationEndorsements
}

type QuoteProvider interface {
	Quote(ctx context.Context, req QuoteRequest) (Quote, error)
}

// NewSEVSNPEndorsements returns the endorsements for the DER certificates of
// the report signer (VCEK or VLEK), the ASK and the ARK.
func NewSEVSNPEndorsements(vcek, vlek, ask, ark []byte) (avr1.AttestationEndorsements, error) {
	if len(ask) == 0 || len(ark) == 0 || (len(vcek) == 0 && len(vlek) == 0) {
		return avr1.AttestationEndorsements{}, ErrIncompleteEndorsements
	}
	return avr1.AttestationEndorsements{
		Format:   avr1.AttestationEndorsementsFormat,
		Encoding: avr1.AttestationEndorsementsEncoding,
		VCEK:     b64(vcek),
		VLEK:     b64(vlek),
		ASK:      b64(ask),
		ARK:      b64(ark),
	}, nil
}

func validEndorsements(e avr1.AttestationEndorsements) bool {
	return e.Format == avr1.AttestationEndorsementsFormat && e.Encoding == avr1.AttestationEndorsementsEncoding && e.ASK != "" && e.ARK != "" && (e.VCEK != "" || e.VLEK != "")
}

// HostData returns the HOST_DATA of a raw Attestation Report.
func HostData(report []byte) ([]byte, error) {
	if len(report) != avr1.AttestationReportSize {
		return nil, fmt.Errorf("%w: %d bytes, want %d", ErrInvalidAttestationReport, len(report), avr1.AttestationReportSize)
	}
	parsed, err := abi.ReportToProto(report)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidAttestationReport, err)
	}
	return parsed.GetHostData(), nil
}

type Request struct {
	Nonce            []byte
	ReceiptPublicKey ed25519.PublicKey
	KeyID            string
}

type Manager struct {
	provider         QuoteProvider
	disclosureReader DisclosureReader
	bootstrap        string
}

// ManagerOptions configures a Manager. Bootstrap is the exact Bootstrap file
// bytes, disclosed as bootstrap; it is empty in development mode.
type ManagerOptions struct {
	DisclosureReader DisclosureReader
	Bootstrap        []byte
}

func NewManagerWithOptions(provider QuoteProvider, opts ManagerOptions) *Manager {
	reader := opts.DisclosureReader
	if reader == nil {
		reader = NewFileDisclosureReader()
	}
	manager := &Manager{provider: provider, disclosureReader: reader}
	if len(opts.Bootstrap) > 0 {
		manager.bootstrap = b64(opts.Bootstrap)
	}
	return manager
}

// Attestation returns a fresh attestation response binding req.ReceiptPublicKey
// to req.Nonce.
func (m *Manager) Attestation(ctx context.Context, req Request) (avr1.AttestationResponse, error) {
	if m == nil || m.provider == nil {
		return avr1.AttestationResponse{}, ErrQuoteProviderUnavailable
	}
	if m.disclosureReader == nil {
		return avr1.AttestationResponse{}, ErrDisclosureUnavailable
	}
	manifest, err := m.disclosureReader.ReadManifest(ctx)
	if err != nil {
		return avr1.AttestationResponse{}, err
	}
	reportData, err := avr1.ReportDataAVR1(req.ReceiptPublicKey, req.Nonce)
	if err != nil {
		return avr1.AttestationResponse{}, err
	}
	quote, err := m.provider.Quote(ctx, QuoteRequest{ReportData: reportData})
	if err != nil {
		return avr1.AttestationResponse{}, err
	}
	if len(quote.RawReport) != avr1.AttestationReportSize {
		return avr1.AttestationResponse{}, fmt.Errorf("%w: %d bytes, want %d", ErrInvalidAttestationReport, len(quote.RawReport), avr1.AttestationReportSize)
	}
	if !validEndorsements(quote.Endorsements) {
		return avr1.AttestationResponse{}, ErrIncompleteEndorsements
	}
	reportDataHex := hex.EncodeToString(reportData[:])
	return avr1.AttestationResponse{
		Version: avr1.AVRVersion,
		TEEType: avr1.AttestationTEETypeSEVSNP,
		Evidence: avr1.AttestationEvidence{
			Format:     avr1.AttestationEvidenceFormat,
			Report:     b64(quote.RawReport),
			ReportData: reportDataHex,
		},
		Endorsements:            quote.Endorsements,
		Nonce:                   b64(req.Nonce),
		ReceiptSigningPublicKey: b64(req.ReceiptPublicKey),
		KeyID:                   req.KeyID,
		KeyBinding: avr1.AttestationKeyBinding{
			Rule:               avr1.AttestationBindingRuleV1,
			ProtocolVersion:    avr1.AVRVersion,
			SignatureAlgorithm: avr1.AVRSignatureAlgorithm,
			ReportDataSHA512:   reportDataHex,
			ReportDataEncoding: avr1.AttestationReportDataEncoding,
			PublicKeyEncoding:  avr1.AttestationPublicKeyEncoding,
			NonceEncoding:      avr1.AttestationNonceEncoding,
		},
		Source:        manifest.Source,
		LaunchProfile: manifest.LaunchProfile,
		Bootstrap:     m.bootstrap,
	}, nil
}

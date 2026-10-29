//go:build linux

package sevsnp

import (
	"bytes"
	"context"
	"crypto/x509"
	"fmt"

	"github.com/google/go-sev-guest/abi"
	sevclient "github.com/google/go-sev-guest/client"
	"github.com/google/go-sev-guest/kds"
	sevsnppb "github.com/google/go-sev-guest/proto/sevsnp"
	"github.com/google/go-sev-guest/verify/trust"
	"github.com/verirelay/protocol/avr1"

	"github.com/verirelay/verirelay/internal/attestation"
)

// reportFunc returns a raw Attestation Report with reportData as REPORT_DATA
// and the product of the platform that produced it.
type reportFunc func(reportData [64]byte) ([]byte, *sevsnppb.SevProduct, error)

// Provider collects Attestation Reports from the SEV guest device or
// configfs-tsm, whichever the kernel offers, and fetches their endorsements
// from the AMD KDS.
type Provider struct {
	getter    trust.HTTPSGetter
	getReport reportFunc
}

func NewProvider() *Provider {
	return newProvider(NewKDSGetter(KDSGetterOptions{}), collectReport)
}

func newProvider(getter trust.HTTPSGetter, getReport reportFunc) *Provider {
	return &Provider{getter: getter, getReport: getReport}
}

func collectReport(reportData [64]byte) ([]byte, *sevsnppb.SevProduct, error) {
	quoteProvider, err := sevclient.GetQuoteProvider()
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", attestation.ErrQuoteProviderUnavailable, err)
	}
	if _, ok := quoteProvider.(*sevclient.LinuxIoctlQuoteProvider); ok {
		device, err := sevclient.OpenDevice()
		if err != nil {
			return nil, nil, fmt.Errorf("open SEV guest device: %w", err)
		}
		defer func() { _ = device.Close() }()
		raw, err := sevclient.GetRawReport(device, reportData) //nolint:staticcheck // SA1019: kept as-is; switching to QuoteProvider changes the report path
		if err != nil {
			return nil, nil, err
		}
		return rawReport(quoteProvider, raw)
	}
	return collectReportFromProvider(quoteProvider, reportData)
}

func collectReportFromProvider(quoteProvider sevclient.QuoteProvider, reportData [64]byte) ([]byte, *sevsnppb.SevProduct, error) {
	raw, err := quoteProvider.GetRawQuote(reportData)
	if err != nil {
		return nil, nil, err
	}
	return rawReport(quoteProvider, raw)
}

// rawReport keeps the report and drops any certificate table that follows it.
func rawReport(quoteProvider sevclient.QuoteProvider, raw []byte) ([]byte, *sevsnppb.SevProduct, error) {
	if len(raw) < abi.ReportSize {
		return nil, nil, fmt.Errorf("SEV-SNP raw report is %d bytes, want at least %d", len(raw), abi.ReportSize)
	}
	return append([]byte(nil), raw[:abi.ReportSize]...), quoteProvider.Product(), nil //nolint:staticcheck // SA1019: Product is the product-line fallback for reports without CPUID fields
}

// Report returns a raw Attestation Report without fetching endorsements.
func (p *Provider) Report(ctx context.Context, reportData [64]byte) ([]byte, error) {
	report, _, err := p.report(ctx, reportData)
	return report, err
}

func (p *Provider) Quote(ctx context.Context, req attestation.QuoteRequest) (attestation.Quote, error) {
	report, product, err := p.report(ctx, req.ReportData)
	if err != nil {
		return attestation.Quote{}, err
	}
	endorsements, err := p.fetchEndorsements(ctx, report, product)
	if err != nil {
		return attestation.Quote{}, err
	}
	return attestation.Quote{RawReport: report, Endorsements: endorsements}, nil
}

func (p *Provider) report(ctx context.Context, reportData [64]byte) ([]byte, *sevsnppb.SevProduct, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if p == nil || p.getter == nil || p.getReport == nil {
		return nil, nil, attestation.ErrQuoteProviderUnavailable
	}
	return p.getReport(reportData)
}

func (p *Provider) fetchEndorsements(ctx context.Context, raw []byte, product *sevsnppb.SevProduct) (avr1.AttestationEndorsements, error) {
	report, err := abi.ReportToProto(raw)
	if err != nil {
		return avr1.AttestationEndorsements{}, fmt.Errorf("parse SEV-SNP report: %w", err)
	}
	if len(report.GetChipId()) != abi.ChipIDSize {
		return avr1.AttestationEndorsements{}, fmt.Errorf("SEV-SNP report chip_id length is %d, want %d", len(report.GetChipId()), abi.ChipIDSize)
	}
	info, err := abi.ParseSignerInfo(report.GetSignerInfo())
	if err != nil {
		return avr1.AttestationEndorsements{}, fmt.Errorf("parse SEV-SNP report signer: %w", err)
	}
	if info.SigningKey != abi.VcekReportSigner {
		return avr1.AttestationEndorsements{}, fmt.Errorf("%w %v: AMD KDS VLEK retrieval requires CSP credentials", attestation.ErrUnsupportedReportSigner, info.SigningKey)
	}
	productLine := productLine(report, product)
	if productLine == "Unknown" {
		return avr1.AttestationEndorsements{}, fmt.Errorf("could not determine AMD product line from SEV-SNP report")
	}
	vcekURL := kds.VCEKCertURL(productLine, report.GetChipId(), kds.TCBVersion(report.GetReportedTcb()))
	vcek, err := trust.GetWith(ctx, p.getter, vcekURL)
	if err != nil {
		return avr1.AttestationEndorsements{}, fmt.Errorf("fetch AMD KDS VCEK: %w", err)
	}
	if err := validateVCEK(vcek, productLine, report.GetChipId(), report.GetReportedTcb()); err != nil {
		return avr1.AttestationEndorsements{}, fmt.Errorf("validate AMD KDS VCEK: %w", err)
	}
	chainURL := kds.ProductCertChainURL(abi.VcekReportSigner, productLine)
	chain, err := trust.GetWith(ctx, p.getter, chainURL)
	if err != nil {
		return avr1.AttestationEndorsements{}, fmt.Errorf("fetch AMD KDS product chain: %w", err)
	}
	ask, ark, err := kds.ParseProductCertChain(chain)
	if err != nil {
		return avr1.AttestationEndorsements{}, fmt.Errorf("parse AMD KDS product chain: %w", err)
	}
	if _, err := x509.ParseCertificate(ask); err != nil {
		return avr1.AttestationEndorsements{}, fmt.Errorf("parse AMD KDS ASK: %w", err)
	}
	if _, err := x509.ParseCertificate(ark); err != nil {
		return avr1.AttestationEndorsements{}, fmt.Errorf("parse AMD KDS ARK: %w", err)
	}
	return attestation.NewSEVSNPEndorsements(vcek, nil, ask, ark)
}

func productLine(report *sevsnppb.Report, product *sevsnppb.SevProduct) string {
	if fms := report.GetCpuid1EaxFms(); fms != 0 {
		return kds.ProductLineFromFms(fms)
	}
	return kds.ProductLine(product)
}

func validateVCEK(vcek []byte, productLine string, chipID []byte, reportedTCB uint64) error {
	cert, err := x509.ParseCertificate(vcek)
	if err != nil {
		return err
	}
	extensions, err := kds.VcekCertificateExtensions(cert)
	if err != nil {
		return err
	}
	if !bytes.Equal(extensions.HWID, chipID) {
		return fmt.Errorf("VCEK HWID does not match report chip_id")
	}
	if uint64(extensions.TCBVersion) != reportedTCB {
		return fmt.Errorf("VCEK TCB does not match report reported_tcb")
	}
	product, err := kds.ParseProductName(extensions.ProductName, abi.VcekReportSigner)
	if err != nil {
		return fmt.Errorf("parse VCEK product name: %w", err)
	}
	if kds.ProductLine(product) != productLine {
		return fmt.Errorf("VCEK product line %q does not match report product line %q", kds.ProductLine(product), productLine)
	}
	return nil
}

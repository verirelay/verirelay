//go:build linux

package sevsnp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/go-sev-guest/abi"
	sevclient "github.com/google/go-sev-guest/client"
	"github.com/google/go-sev-guest/kds"
	sevsnppb "github.com/google/go-sev-guest/proto/sevsnp"
	sevtesting "github.com/google/go-sev-guest/testing"
	"github.com/google/go-sev-guest/verify/trust"

	"github.com/verirelay/verirelay/internal/attestation"
)

func TestCollectReportFromProviderIgnoresCertificateTableTail(t *testing.T) {
	var reportData [64]byte
	reportData[0] = 7
	rawReport := sevtesting.TestRawReportV3(reportData, 0x00a00f10)
	provider := &rawQuoteProvider{
		rawQuote: append(append([]byte(nil), rawReport[:abi.ReportSize]...), []byte("poison certificate table")...),
		product:  &sevsnppb.SevProduct{Name: sevsnppb.SevProduct_SEV_PRODUCT_GENOA},
	}

	report, product, err := collectReportFromProvider(provider, reportData)
	if err != nil {
		t.Fatal(err)
	}
	if provider.calls != 1 || provider.reportData != reportData {
		t.Fatalf("calls = %d, report_data = %x", provider.calls, provider.reportData)
	}
	if !bytes.Equal(report, rawReport[:abi.ReportSize]) || product.GetName() != sevsnppb.SevProduct_SEV_PRODUCT_GENOA {
		t.Fatalf("collected %d bytes, product %v", len(report), product)
	}
}

func TestCollectReportFromProviderRejectsShortReport(t *testing.T) {
	provider := &rawQuoteProvider{rawQuote: make([]byte, abi.ReportSize-1)}
	if _, _, err := collectReportFromProvider(provider, [64]byte{}); err == nil {
		t.Fatal("short raw report was accepted")
	}
}

func TestCollectReportFromProviderPropagatesQuoteError(t *testing.T) {
	wantErr := errors.New("quote unavailable")
	provider := &rawQuoteProvider{err: wantErr}
	if _, _, err := collectReportFromProvider(provider, [64]byte{}); !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
}

func TestProviderFetchesEndorsementsOnlyFromKDS(t *testing.T) {
	report, signer := testReport(t, abi.VcekReportSigner)
	getter := &fixtureGetter{bodies: map[string][]byte{
		kds.VCEKCertURL("Milan", signer.HWID[:], signer.TCB):   signer.Vcek.Raw,
		kds.ProductCertChainURL(abi.VcekReportSigner, "Milan"): pemChain(signer.Ask.Raw, signer.Ark.Raw),
	}}
	provider := newProvider(getter, fixedReport(report))

	quote, err := provider.Quote(context.Background(), attestation.QuoteRequest{})
	if err != nil {
		t.Fatal(err)
	}
	b64 := base64.RawURLEncoding.EncodeToString
	if quote.Endorsements.VCEK != b64(signer.Vcek.Raw) || quote.Endorsements.ASK != b64(signer.Ask.Raw) || quote.Endorsements.ARK != b64(signer.Ark.Raw) || quote.Endorsements.VLEK != "" {
		t.Fatalf("endorsements = %+v", quote.Endorsements)
	}
	if len(getter.urls) != 2 {
		t.Fatalf("KDS calls = %v", getter.urls)
	}
	if !bytes.Equal(quote.RawReport, report) {
		t.Fatal("quote does not carry the raw report")
	}
}

func TestProviderReportSkipsKDS(t *testing.T) {
	report, _ := testReport(t, abi.VcekReportSigner)
	getter := &fixtureGetter{err: errors.New("KDS must not be called")}
	got, err := newProvider(getter, fixedReport(report)).Report(context.Background(), [64]byte{})
	if err != nil || !bytes.Equal(got, report) || len(getter.urls) != 0 {
		t.Fatalf("Report = %d bytes, %v, KDS calls %v", len(got), err, getter.urls)
	}
}

func TestProviderFailsClosedWhenKDSUnavailable(t *testing.T) {
	report, _ := testReport(t, abi.VcekReportSigner)
	provider := newProvider(&fixtureGetter{err: errors.New("KDS unavailable")}, fixedReport(report))
	if _, err := provider.Quote(context.Background(), attestation.QuoteRequest{}); err == nil || errors.Is(err, attestation.ErrQuoteProviderUnavailable) {
		t.Fatalf("KDS error = %v", err)
	}
}

func TestProviderRejectsVLEKSignerBeforeKDSFetch(t *testing.T) {
	vlek, _ := testReport(t, abi.VlekReportSigner)
	getter := &fixtureGetter{}
	_, err := newProvider(getter, fixedReport(vlek)).Quote(context.Background(), attestation.QuoteRequest{})
	if !errors.Is(err, attestation.ErrUnsupportedReportSigner) {
		t.Fatalf("VLEK error = %v, want ErrUnsupportedReportSigner", err)
	}
	if !strings.Contains(err.Error(), "CSP credentials") {
		t.Fatalf("VLEK error does not explain CSP boundary: %v", err)
	}
	if len(getter.urls) != 0 {
		t.Fatalf("VLEK quote unexpectedly fetched KDS URLs: %v", getter.urls)
	}
}

func TestProviderRejectsMalformedReport(t *testing.T) {
	report, _ := testReport(t, abi.VcekReportSigner)
	report[0x4C] = 1 // must-be-zero byte after SIGNER_INFO
	if _, err := newProvider(&fixtureGetter{}, fixedReport(report)).Quote(context.Background(), attestation.QuoteRequest{}); err == nil {
		t.Fatal("malformed report was accepted")
	}
}

func testReport(t *testing.T, reportSigner abi.ReportSigner) ([]byte, *sevtesting.AmdSigner) {
	t.Helper()
	signer, err := sevtesting.DefaultTestOnlyCertChain("Milan-B1", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	report, err := abi.ReportToAbiBytes(&sevsnppb.Report{
		Version:         3,
		Policy:          abi.SnpPolicyToBytes(abi.SnpPolicy{}),
		FamilyId:        make([]byte, abi.FamilyIDSize),
		ImageId:         make([]byte, abi.ImageIDSize),
		ChipId:          signer.HWID[:],
		ReportedTcb:     uint64(signer.TCB),
		SignerInfo:      abi.ComposeSignerInfo(abi.SignerInfo{SigningKey: reportSigner}),
		Cpuid1EaxFms:    0x00a00f10,
		ReportData:      make([]byte, abi.ReportDataSize),
		Measurement:     make([]byte, abi.MeasurementSize),
		HostData:        make([]byte, abi.HostDataSize),
		IdKeyDigest:     make([]byte, abi.IDKeyDigestSize),
		AuthorKeyDigest: make([]byte, abi.AuthorKeyDigestSize),
		ReportId:        make([]byte, abi.ReportIDSize),
		ReportIdMa:      make([]byte, abi.ReportIDMASize),
		Signature:       make([]byte, abi.SignatureSize),
	})
	if err != nil {
		t.Fatal(err)
	}
	return report, signer
}

func fixedReport(report []byte) reportFunc {
	return func([64]byte) ([]byte, *sevsnppb.SevProduct, error) {
		return append([]byte(nil), report...), nil, nil
	}
}

type rawQuoteProvider struct {
	rawQuote   []byte
	err        error
	product    *sevsnppb.SevProduct
	calls      int
	reportData [64]byte
}

func (p *rawQuoteProvider) IsSupported() bool {
	return true
}

func (p *rawQuoteProvider) GetRawQuote(reportData [64]byte) ([]uint8, error) {
	p.calls++
	p.reportData = reportData
	if p.err != nil {
		return nil, p.err
	}
	return append([]byte(nil), p.rawQuote...), nil
}

func (p *rawQuoteProvider) Product() *sevsnppb.SevProduct {
	return p.product
}

var _ sevclient.QuoteProvider = (*rawQuoteProvider)(nil)

type fixtureGetter struct {
	bodies map[string][]byte
	err    error
	urls   []string
}

func (g *fixtureGetter) Get(url string) ([]byte, error) {
	return g.GetContext(context.Background(), url)
}

func (g *fixtureGetter) GetContext(_ context.Context, url string) ([]byte, error) {
	g.urls = append(g.urls, url)
	if g.err != nil {
		return nil, g.err
	}
	body, ok := g.bodies[url]
	if !ok {
		return nil, errors.New("missing KDS fixture")
	}
	return append([]byte(nil), body...), nil
}

func pemChain(ask, ark []byte) []byte {
	return append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ask}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ark})...)
}

var _ trust.HTTPSGetter = (*fixtureGetter)(nil)

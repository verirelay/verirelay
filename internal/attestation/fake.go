package attestation

import (
	"context"

	"github.com/verirelay/protocol/avr1"
)

// fakeReportDataOffset is where REPORT_DATA sits in an Attestation Report.
const fakeReportDataOffset = 0x50

// FakeProvider returns unsigned placeholder Attestation Reports and
// endorsements for development and tests. No verifier accepts them.
type FakeProvider struct{}

// Report returns a zeroed Attestation Report that carries only reportData.
func (FakeProvider) Report(_ context.Context, reportData [64]byte) ([]byte, error) {
	report := make([]byte, avr1.AttestationReportSize)
	copy(report[fakeReportDataOffset:], reportData[:])
	return report, nil
}

func (p FakeProvider) Quote(ctx context.Context, req QuoteRequest) (Quote, error) {
	report, err := p.Report(ctx, req.ReportData)
	if err != nil {
		return Quote{}, err
	}
	endorsements, err := NewSEVSNPEndorsements([]byte("fake-vcek-der"), nil, []byte("fake-ask-der"), []byte("fake-ark-der"))
	if err != nil {
		return Quote{}, err
	}
	return Quote{RawReport: report, Endorsements: endorsements}, nil
}

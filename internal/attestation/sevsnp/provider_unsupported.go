//go:build !linux

package sevsnp

import (
	"context"
	"fmt"

	"github.com/verirelay/verirelay/internal/attestation"
)

type Provider struct{}

func NewProvider() *Provider {
	return &Provider{}
}

var errUnsupported = fmt.Errorf("%w: SEV-SNP quote provider requires linux", attestation.ErrQuoteProviderUnavailable)

func (Provider) Report(context.Context, [64]byte) ([]byte, error) {
	return nil, errUnsupported
}

func (Provider) Quote(context.Context, attestation.QuoteRequest) (attestation.Quote, error) {
	return attestation.Quote{}, errUnsupported
}

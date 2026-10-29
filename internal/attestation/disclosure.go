package attestation

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/verirelay/protocol/avr1"
	"github.com/verirelay/protocol/guest"
)

var (
	ErrDisclosureUnavailable = errors.New("attestation disclosure unavailable")
	ErrInvalidManifest       = errors.New("invalid guest manifest")
)

// ManifestDisclosure is the part of the Guest Manifest that the attestation
// response discloses.
type ManifestDisclosure struct {
	Source        avr1.AttestationSource
	LaunchProfile avr1.AttestationLaunchProfile
}

type DisclosureReader interface {
	ReadManifest(ctx context.Context) (ManifestDisclosure, error)
}

// FileDisclosureReader reads the Guest Manifest at ManifestPath, by default
// guest.ManifestPath.
type FileDisclosureReader struct {
	ManifestPath string
}

func NewFileDisclosureReader() FileDisclosureReader {
	return FileDisclosureReader{ManifestPath: guest.ManifestPath}
}

func (r FileDisclosureReader) ReadManifest(ctx context.Context) (ManifestDisclosure, error) {
	if err := ctx.Err(); err != nil {
		return ManifestDisclosure{}, fmt.Errorf("%w: %w", ErrDisclosureUnavailable, err)
	}
	path := r.ManifestPath
	if path == "" {
		path = guest.ManifestPath
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ManifestDisclosure{}, fmt.Errorf("%w: read manifest: %w", ErrDisclosureUnavailable, err)
	}
	return decodeManifestDisclosure(data)
}

// decodeManifestDisclosure decodes and validates a Guest Manifest and returns
// its disclosure.
func decodeManifestDisclosure(data []byte) (ManifestDisclosure, error) {
	manifest, err := guest.DecodeManifest(data)
	if err != nil {
		return ManifestDisclosure{}, fmt.Errorf("%w: %w", ErrInvalidManifest, err)
	}
	if err := manifest.Validate(); err != nil {
		return ManifestDisclosure{}, fmt.Errorf("%w: %w", ErrInvalidManifest, err)
	}
	source, profile := manifest.Source, manifest.LaunchProfile
	return ManifestDisclosure{
		Source: avr1.AttestationSource{
			VeriRelay: avr1.AttestationSourceRef{Repository: source.VeriRelay.Repository, Commit: source.VeriRelay.Commit},
			VeriForge: avr1.AttestationSourceRef{Repository: source.VeriForge.Repository, Commit: source.VeriForge.Commit},
		},
		LaunchProfile: avr1.AttestationLaunchProfile{
			Version:            profile.Version,
			MeasurementProfile: profile.MeasurementProfile,
			VCPUType:           profile.VCPUType,
			GuestFeatures:      profile.GuestFeatures,
			KernelHashes:       profile.KernelHashes,
			KernelAppend:       profile.KernelAppend,
		},
	}, nil
}

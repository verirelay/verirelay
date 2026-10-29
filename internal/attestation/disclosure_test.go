package attestation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/verirelay/protocol/avr1"
	"github.com/verirelay/protocol/guest"
)

const (
	testVeriRelayRepository = "https://github.com/verirelay/verirelay"
	testVeriForgeRepository = "https://github.com/verirelay/veriforge"
	testVeriRelayCommit     = "0123456789abcdef0123456789abcdef01234567"
	testVeriForgeCommit     = "89abcdef0123456789abcdef0123456789abcdef"
	testKernelAppend        = "console=ttyS0 quiet panic=1"
)

func TestFileDisclosureReaderReadsGuestManifest(t *testing.T) {
	manifest, err := (FileDisclosureReader{ManifestPath: filepath.Join("testdata", "guest-manifest.json")}).ReadManifest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if manifest != testManifestDisclosure() {
		t.Fatalf("disclosure = %+v, want %+v", manifest, testManifestDisclosure())
	}
}

func TestFileDisclosureReaderFailsWhenManifestIsMissing(t *testing.T) {
	reader := FileDisclosureReader{ManifestPath: filepath.Join(t.TempDir(), "missing-manifest.json")}
	if _, err := reader.ReadManifest(context.Background()); !errors.Is(err, ErrDisclosureUnavailable) {
		t.Fatalf("manifest err = %v", err)
	}
}

func TestDecodeManifestDisclosureAppliesProtocolRules(t *testing.T) {
	valid, err := os.ReadFile(filepath.Join("testdata", "guest-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, old, new string }{
		{name: "unknown field", old: `"kernel_hashes": true,`, new: `"kernel_hashes": true, "vcpus": 1,`},
		{name: "provisioning block", old: "\n}", new: `, "provisioning": {}}`},
		{name: "old version", old: guest.ManifestVersion, new: "verirelay-manifest-v1"},
		{name: "repository with .git", old: testVeriRelayRepository + `"`, new: testVeriRelayRepository + `.git"`},
		{name: "uppercase commit", old: testVeriRelayCommit, new: strings.ToUpper(testVeriRelayCommit)},
		{name: "control character in kernel_append", old: testKernelAppend, new: `console=ttyS0\tpanic=1`},
		{name: "trailing value", old: "\n}\n", new: "\n}\n{}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := strings.Replace(string(valid), tc.old, tc.new, 1)
			if input == string(valid) {
				t.Fatal("fixture was not changed")
			}
			if _, err := decodeManifestDisclosure([]byte(input)); !errors.Is(err, ErrInvalidManifest) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func testManifestDisclosure() ManifestDisclosure {
	return ManifestDisclosure{
		Source: avr1.AttestationSource{
			VeriRelay: avr1.AttestationSourceRef{Repository: testVeriRelayRepository, Commit: testVeriRelayCommit},
			VeriForge: avr1.AttestationSourceRef{Repository: testVeriForgeRepository, Commit: testVeriForgeCommit},
		},
		LaunchProfile: avr1.AttestationLaunchProfile{
			Version:            guest.LaunchProfileVersion,
			MeasurementProfile: guest.MeasurementProfile,
			VCPUType:           guest.VCPUType,
			GuestFeatures:      guest.GuestFeatures,
			KernelHashes:       guest.KernelHashes,
			KernelAppend:       testKernelAppend,
		},
	}
}

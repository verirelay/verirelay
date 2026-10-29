English | [简体中文](glossary.zh-CN.md)

# Glossary

## AVR

| Term | Definition |
| --- | --- |
| AVR (Attested Verifiable Relay) | VeriRelay's attested forwarding mechanism: each exchange gets a Receipt signed by a key bound to the Attestation Report. |

## Components

| Term | Definition |
| --- | --- |
| VeriRelay | The transparent proxy that runs in a CVM and issues a Receipt for every forward. Also the name of the project. |
| VeriForge | The reproducible builder of the Boot Artifact Set. |
| VeriProv | The provisioning service that delivers Runtime Config to a CVM after Attestation. |
| Registry | Optional service that maps a Launch Measurement to its build identity (repository, commit, version). |
| Verifier | The reference verification tool for VeriRelay; anyone can implement verification from the Protocol. |
| Protocol | The public wire formats: `avr/1`, `veriprov/1`, and the guest file schemas. |
| Release | The release pipeline that produces the Boot Artifact Set and the Release Evidence Chain. |

## Roles

| Term | Definition |
| --- | --- |
| Operator | The party that deploys and runs VeriRelay, including whoever holds `admin_token`. |
| Cloud Provider | The party that provides the CPUs with confidential computing support. |
| Upstream | The service VeriRelay forwards a request to. |
| AI Gateway | A typical deployment scenario: a third-party service that calls model APIs on users' behalf, with VeriRelay placed between it and the upstream. |

## Runtime environment

| Term | Definition |
| --- | --- |
| CVM | The SEV-SNP confidential VM that runs VeriRelay. |
| TEE | The trusted execution environment that provides isolation and attestation. |
| Workload | The software running in the protected environment, together with its fixed boot contents. |
| VM | A virtual machine in general. |
| Guest | The software environment inside a VM, seen from the virtualization side. |
| Instance | One concrete CVM run managed by the deployment system. |

## Boot and attestation

| Term | Definition |
| --- | --- |
| Boot Artifacts | The files that boot a CVM: OVMF firmware, kernel image (vmlinuz), and initrd. Called the Boot Artifact Set when emphasizing that the three are released together as one version. |
| Guest Manifest | `/etc/verirelay/manifest.json` in the initrd, which records the build sources and the launch parameters other than the vCPU count. |
| Launch Measurement | The hash AMD SEV-SNP computes over the Boot Artifact Set and launch parameters when a CVM starts, recorded in the Attestation Report. It depends on the vCPU count, so the same Boot Artifact Set has one Launch Measurement per vCPU count. |
| Attestation | The process by which the TEE proves the identity and state of the running Workload. |
| Attestation Report | The SEV-SNP report signed by the AMD secure processor, containing the Launch Measurement, `REPORT_DATA`, and `HOST_DATA`. |
| HOST_DATA | A 32-byte field of the Attestation Report that the host sets when it launches the CVM. For VeriRelay it is the SHA-256 of the Bootstrap, and VeriProv uses it to pick the CVM's Runtime Config. |
| Attestation Evidence | The `evidence` field of the attestation response: the raw Attestation Report. |
| Attestation Nonce | A 32-byte random value from the verifying party, placed in `REPORT_DATA` to prove the Attestation Report is fresh. |

## Receipts

| Term | Definition |
| --- | --- |
| Receipt | The `avr/1` record VeriRelay issues for every forward, containing the request and response hashes and the upstream TLS identity. Counted per forward. |
| Receipt Signing Key | The Ed25519 key generated inside the CVM that signs Receipts, bound to the Attestation Report through `REPORT_DATA`. |
| Receipt Evidence Bundle | The `avr/1` response of `/v1/avr/evidence`: a Receipt plus the attestation response archived for its Receipt Signing Key, or `null` when none is archived. |
| Raw Capture | Optional feature that stores the raw request and response encrypted, retrievable with a key, so they can be checked against the Receipt. |

## Provisioning

| Term | Definition |
| --- | --- |
| Provisioning | The process in which VeriProv delivers Runtime Config to a CVM over `veriprov/1` after it passes Attestation. |
| Runtime Config | The sensitive configuration delivered to VeriRelay: `admin_token` and the stores. |
| Bootstrap | The file on the Config Disk that names the VeriProv origin and the VeriProv Identity Key. Its SHA-256 is the CVM's `HOST_DATA`. |
| Config Disk | The read-only virtio disk attached to the CVM that holds the Bootstrap. |
| VeriProv Identity Key | VeriProv's long-lived Ed25519 key, which signs the Runtime Config it delivers. The Bootstrap names its public key. |

## Release and provenance

| Term | Definition |
| --- | --- |
| Release Evidence Chain | The `chain.json` attached to every release, linking the Launch Measurement to each component's source commit. |
| Build Report | The `build-report.json` VeriForge writes for one build: the Guest Manifest, pinned inputs, and artifact digests. Not a Build Record. |
| Release Key | The Ed25519 key with which Release signs Signed Build Records and their revocations. |
| Signed Build Record | A record in Registry signed with the Release Key that maps a Launch Measurement to a build identity. Short form: Build Record. Each release also publishes its Build Records as the `build-records.json` asset. |

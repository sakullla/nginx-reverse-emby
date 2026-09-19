package wasm

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

// VerifiedArtifact is the only input accepted by CompileGeneration. Its
// fields are private so callers cannot accidentally bypass digest and
// signature verification at the runtime boundary.
type VerifiedArtifact struct {
	wasm   []byte
	digest [sha256.Size]byte
	valid  bool
}

// AcceptVerifiedArtifact turns verifier output into a runtime capability.
// Signature verification remains owned by the package verifier; the host
// independently rechecks the content digest before accepting the bytes.
func AcceptVerifiedArtifact(wasmBytes []byte, expectedSHA256 string, signatureVerified bool) (VerifiedArtifact, error) {
	if !signatureVerified {
		return VerifiedArtifact{}, errors.New("wasm artifact signature is not verified")
	}
	if len(wasmBytes) == 0 {
		return VerifiedArtifact{}, errors.New("wasm artifact is empty")
	}
	expected, err := hex.DecodeString(expectedSHA256)
	if err != nil || len(expected) != sha256.Size {
		return VerifiedArtifact{}, fmt.Errorf("invalid wasm artifact SHA-256 %q", expectedSHA256)
	}
	digest := sha256.Sum256(wasmBytes)
	if !equalDigest(digest[:], expected) {
		return VerifiedArtifact{}, errors.New("wasm artifact digest mismatch")
	}
	return VerifiedArtifact{wasm: append([]byte(nil), wasmBytes...), digest: digest, valid: true}, nil
}

func (artifact VerifiedArtifact) Digest() string {
	return hex.EncodeToString(artifact.digest[:])
}

func equalDigest(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var difference byte
	for index := range left {
		difference |= left[index] ^ right[index]
	}
	return difference == 0
}

// Package asr provides ASR (Automatic Speech Recognition) adapter interfaces and implementations.
package asr

import (
	"context"
)

// ASRAdapter defines the interface for voice transcription.
type ASRAdapter interface {
	// TranscribeVoice transcribes audio to text.
	// Parameters:
	//   - ctx: Context for cancellation and timeouts
	//   - audioBytes: Raw audio bytes (various formats supported by provider)
	//
	// Returns transcribed text or an error.
	TranscribeVoice(ctx context.Context, audioBytes []byte) (string, error)
}

// StubAdapter is a stub implementation of ASRAdapter for development/testing.
// This returns fixed mock data until a real ASR provider is selected.
type StubAdapter struct{}

// NewStubAdapter creates a new stub ASR adapter.
func NewStubAdapter() *StubAdapter {
	return &StubAdapter{}
}

// TranscribeVoice implements the ASRAdapter interface with mock data.
// This stub always returns the same fixed transcription for testing purposes.
func (s *StubAdapter) TranscribeVoice(ctx context.Context, audioBytes []byte) (string, error) {
	// Return fixed mock text as specified in the requirements
	return "餐饮支出 50 元", nil
}

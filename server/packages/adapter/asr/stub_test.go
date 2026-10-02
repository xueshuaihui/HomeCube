package asr

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStubAdapter_TranscribeVoice(t *testing.T) {
	adapter := NewStubAdapter()
	ctx := context.Background()

	// Test with empty audio bytes
	text, err := adapter.TranscribeVoice(ctx, []byte{})
	assert.NoError(t, err)
	assert.Equal(t, "餐饮支出 50 元", text)

	// Test with non-empty audio bytes (should still return same mock text)
	fakeAudio := []byte("fake audio data")
	text2, err := adapter.TranscribeVoice(ctx, fakeAudio)
	assert.NoError(t, err)
	assert.Equal(t, "餐饮支出 50 元", text2)
}

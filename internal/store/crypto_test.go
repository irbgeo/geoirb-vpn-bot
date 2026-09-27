package store

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

var testKey = bytes.Repeat([]byte{7}, 32)

func TestSealerRoundTrip(t *testing.T) {
	s, err := newSealer(testKey)
	require.NoError(t, err)

	a, err := s.seal(
		sealInput{
			Text: "PRIV=",
			AAD:  "PUB=",
		},
	)
	require.NoError(t, err)
	b, err := s.seal(
		sealInput{
			Text: "PRIV=",
			AAD:  "PUB=",
		},
	)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(a, "v1:"))
	require.NotContains(t, a, "PRIV=")
	require.NotEqual(t, a, b, "random nonce: same input, different output")

	plain, err := s.open(
		sealInput{
			Text: a,
			AAD:  "PUB=",
		},
	)
	require.NoError(t, err)
	require.Equal(t, "PRIV=", plain)
}

func TestSealerRejectsTampering(t *testing.T) {
	s, err := newSealer(testKey)
	require.NoError(t, err)
	sealed, err := s.seal(
		sealInput{
			Text: "PRIV=",
			AAD:  "PUB=",
		},
	)
	require.NoError(t, err)

	_, err = s.open(
		sealInput{
			Text: sealed,
			AAD:  "OTHER=",
		},
	)
	require.Error(t, err, "ciphertext moved to another peer")

	other, err := newSealer(bytes.Repeat([]byte{8}, 32))
	require.NoError(t, err)
	_, err = other.open(
		sealInput{
			Text: sealed,
			AAD:  "PUB=",
		},
	)
	require.Error(t, err, "wrong key")

	_, err = s.open(
		sealInput{
			Text: "PRIV=",
			AAD:  "PUB=",
		},
	)
	require.ErrorContains(t, err, "not encrypted")

	_, err = s.open(
		sealInput{
			Text: "v1:!!!",
			AAD:  "PUB=",
		},
	)
	require.Error(t, err, "bad base64")

	_, err = s.open(
		sealInput{
			Text: "v1:AAAA",
			AAD:  "PUB=",
		},
	)
	require.Error(t, err, "too short")
}

func TestNewSealerKeyLength(t *testing.T) {
	_, err := newSealer([]byte("short"))
	require.ErrorContains(t, err, "32 bytes")
}

// Package ids defines the stable identifier seam used by the local service.
package ids

import (
	cryptorand "crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

// IDGenerator creates stable text identifiers without exposing the entropy
// source to callers.
type IDGenerator interface {
	NewID() (string, error)
}

// ErrExhausted reports that a deterministic sequence has no IDs left.
var ErrExhausted = errors.New("ID sequence exhausted")

// RandomGenerator creates RFC 4122 version 4 identifiers from a cryptographic
// entropy source.
type RandomGenerator struct {
	reader io.Reader
}

// NewRandom returns a production generator backed by crypto/rand.
func NewRandom() *RandomGenerator {
	return &RandomGenerator{reader: cryptorand.Reader}
}

// NewRandomWithReader returns a generator using reader. It is intended for
// deterministic or failure-injection tests and never accepts a nil reader.
func NewRandomWithReader(reader io.Reader) (*RandomGenerator, error) {
	if reader == nil {
		return nil, errors.New("ID entropy reader is required")
	}
	return &RandomGenerator{reader: reader}, nil
}

// NewID returns one UUID v4 text identifier.
func (g *RandomGenerator) NewID() (string, error) {
	if g == nil || g.reader == nil {
		return "", errors.New("ID entropy reader is required")
	}

	var value [16]byte
	if _, err := io.ReadFull(g.reader, value[:]); err != nil {
		return "", fmt.Errorf("read ID entropy: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80

	return hex.EncodeToString(value[0:4]) + "-" +
		hex.EncodeToString(value[4:6]) + "-" +
		hex.EncodeToString(value[6:8]) + "-" +
		hex.EncodeToString(value[8:10]) + "-" +
		hex.EncodeToString(value[10:16]), nil
}

// SequenceGenerator supplies caller-owned IDs in order for deterministic
// tests. It is intentionally not safe for concurrent use.
type SequenceGenerator struct {
	values []string
	next   int
}

// NewSequence returns a deterministic generator over a private copy of
// values.
func NewSequence(values ...string) *SequenceGenerator {
	return &SequenceGenerator{values: append([]string(nil), values...)}
}

// NewID returns the next configured ID or ErrExhausted after the sequence is
// consumed.
func (g *SequenceGenerator) NewID() (string, error) {
	if g == nil || g.next >= len(g.values) {
		return "", ErrExhausted
	}
	id := g.values[g.next]
	g.next++
	return id, nil
}

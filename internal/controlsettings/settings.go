// Package controlsettings defines values shared by the sandboxctl client and daemon.
package controlsettings

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

const (
	// APIVersionMetadataKey names the metadata field for the control API version.
	APIVersionMetadataKey = "sandboxctl-api-version"
	// RequestIDMetadataKey names the metadata field for a request identifier.
	RequestIDMetadataKey = "x-request-id"
	// APIVersion is the current control API version.
	APIVersion = "v1"
	// MaxMessageSize limits gRPC messages to one MiB.
	MaxMessageSize = 1 << 20

	// MaxOperationDuration limits ordinary control operations.
	MaxOperationDuration = 10 * time.Second
	// MaxCreateDuration limits sandbox creation.
	MaxCreateDuration = 2 * time.Minute
	// MaxStartDuration limits sandbox startup.
	MaxStartDuration = 2 * time.Minute
	// MaxStopDuration limits sandbox shutdown.
	MaxStopDuration = 30 * time.Second
	// MaxResetDuration limits state disk reset.
	MaxResetDuration = 3 * time.Minute
	// MaxDeleteDuration limits sandbox deletion.
	MaxDeleteDuration = 30 * time.Second
	// MaxInstallDuration limits image installation.
	MaxInstallDuration = 30 * time.Minute
)

// NewRequestID creates a random 128-bit request identifier.
func NewRequestID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("create request identifier: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}

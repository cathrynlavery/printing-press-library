// Copyright 2026 Damien Stevens and contributors. Licensed under Apache-2.0.

package granola

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// VerifyWebhookSignature verifies Granola's Svix-compatible delivery
// signature over "webhook-id.webhook-timestamp.raw-body". Callers must pass
// the body bytes exactly as received; parsing and re-marshalling changes the
// signature input.
func VerifyWebhookSignature(webhookID, timestamp, signature string, body []byte, secret string, now time.Time, tolerance time.Duration) error {
	if webhookID == "" || timestamp == "" || signature == "" {
		return fmt.Errorf("webhook-id, webhook-timestamp, and webhook-signature are required")
	}
	if !strings.HasPrefix(secret, "whsec_") {
		return fmt.Errorf("invalid webhook secret format")
	}
	secretBytes, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	if err != nil || len(secretBytes) == 0 {
		return fmt.Errorf("invalid webhook secret encoding")
	}
	unix, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid webhook timestamp")
	}
	if tolerance <= 0 {
		tolerance = 5 * time.Minute
	}
	delta := now.Sub(time.Unix(unix, 0))
	if delta < 0 {
		delta = -delta
	}
	if delta > tolerance {
		return fmt.Errorf("webhook timestamp is outside the %s replay window", tolerance)
	}

	mac := hmac.New(sha256.New, secretBytes)
	_, _ = fmt.Fprintf(mac, "%s.%s.", webhookID, timestamp)
	_, _ = mac.Write(body)
	want := mac.Sum(nil)
	for _, candidate := range strings.Fields(signature) {
		version, encoded, ok := strings.Cut(candidate, ",")
		if !ok || version != "v1" {
			continue
		}
		got, err := base64.StdEncoding.DecodeString(encoded)
		if err == nil && hmac.Equal(got, want) {
			return nil
		}
	}
	return fmt.Errorf("webhook signature mismatch")
}

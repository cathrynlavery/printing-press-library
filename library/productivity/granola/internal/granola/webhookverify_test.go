// Copyright 2026 Damien Stevens and contributors. Licensed under Apache-2.0.

package granola

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"testing"
	"time"
)

func webhookSignature(secret []byte, id, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = fmt.Fprintf(mac, "%s.%s.", id, timestamp)
	_, _ = mac.Write(body)
	return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func TestVerifyWebhookSignature(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	secretBytes := []byte("synthetic-signing-key")
	secret := "whsec_" + base64.StdEncoding.EncodeToString(secretBytes)
	body := []byte(`{"event_id":"evt_test","type":"note.edited"}`)
	timestamp := fmt.Sprintf("%d", now.Unix())
	signature := "v1,invalid " + webhookSignature(secretBytes, "evt_test", timestamp, body)

	if err := VerifyWebhookSignature("evt_test", timestamp, signature, body, secret, now, 5*time.Minute); err != nil {
		t.Fatalf("VerifyWebhookSignature: %v", err)
	}
	if err := VerifyWebhookSignature("evt_test", timestamp, signature, append(body, ' '), secret, now, 5*time.Minute); err == nil {
		t.Fatal("expected modified body to fail")
	}
	stale := fmt.Sprintf("%d", now.Add(-6*time.Minute).Unix())
	if err := VerifyWebhookSignature("evt_test", stale, webhookSignature(secretBytes, "evt_test", stale, body), body, secret, now, 5*time.Minute); err == nil {
		t.Fatal("expected stale timestamp to fail")
	}
}

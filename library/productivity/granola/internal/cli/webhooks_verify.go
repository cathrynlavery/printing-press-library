// Copyright 2026 Damien Stevens and contributors. Licensed under Apache-2.0.

package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mvanhorn/printing-press-library/library/productivity/granola/internal/granola"
	"github.com/spf13/cobra"
)

func newWebhooksVerifyCmd(flags *rootFlags) *cobra.Command {
	var webhookID, timestamp, signature, bodyFile string
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Verify a captured webhook delivery offline",
		Long:  "Verifies the exact raw request body before decoding it. The signing secret is read only from GRANOLA_WEBHOOK_SECRET.",
		Annotations: map[string]string{
			"mcp:read-only": "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			secret := os.Getenv("GRANOLA_WEBHOOK_SECRET")
			if secret == "" {
				return authErr(fmt.Errorf("GRANOLA_WEBHOOK_SECRET is required"))
			}
			body, err := readWebhookBody(cmd, bodyFile)
			if err != nil {
				return err
			}
			if err := granola.VerifyWebhookSignature(webhookID, timestamp, signature, body, secret, time.Now(), 5*time.Minute); err != nil {
				return authErr(err)
			}
			var event map[string]any
			if err := json.Unmarshal(body, &event); err != nil {
				return fmt.Errorf("signature valid but body is not a JSON object: %w", err)
			}
			if eventID, _ := event["event_id"].(string); eventID != webhookID {
				return fmt.Errorf("signature valid but event_id %q does not match webhook-id %q", eventID, webhookID)
			}
			return emitJSON(cmd, flags, map[string]any{"verified": true, "event": event})
		},
	}
	cmd.Flags().StringVar(&webhookID, "webhook-id", "", "Value of the webhook-id header")
	cmd.Flags().StringVar(&timestamp, "webhook-timestamp", "", "Value of the webhook-timestamp header")
	cmd.Flags().StringVar(&signature, "webhook-signature", "", "Value of the webhook-signature header")
	cmd.Flags().StringVar(&bodyFile, "body-file", "-", "Raw request body file ('-' reads stdin)")
	_ = cmd.MarkFlagRequired("webhook-id")
	_ = cmd.MarkFlagRequired("webhook-timestamp")
	_ = cmd.MarkFlagRequired("webhook-signature")
	return cmd
}

func readWebhookBody(cmd *cobra.Command, path string) ([]byte, error) {
	if path == "" || path == "-" {
		body, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return nil, fmt.Errorf("read webhook body from stdin: %w", err)
		}
		return body, nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read webhook body %s: %w", path, err)
	}
	return body, nil
}

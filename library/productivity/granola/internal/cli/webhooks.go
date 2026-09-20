// Copyright 2026 Damien Stevens and contributors. Licensed under Apache-2.0.

package cli

import (
	"fmt"
	"net/url"
	"regexp"

	"github.com/spf13/cobra"
)

var webhookScopes = map[string]bool{"personal": true, "public": true, "workspace": true}
var webhookEvents = map[string]bool{"note.access_granted": true, "note.edited": true, "note.generated": true}
var webhookEndpointIDPattern = regexp.MustCompile(`^whe_[A-Za-z0-9]{14}$`)
var webhookFolderIDPattern = regexp.MustCompile(`^fol_[A-Za-z0-9]{14}$`)

func newWebhooksCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "webhooks",
		Short: "Manage public API webhook endpoints and verify deliveries offline",
	}
	cmd.AddCommand(newWebhooksListCmd(flags))
	cmd.AddCommand(newWebhooksCreateCmd(flags))
	cmd.AddCommand(newWebhooksUpdateCmd(flags))
	cmd.AddCommand(newWebhooksDeleteCmd(flags))
	cmd.AddCommand(newWebhooksVerifyCmd(flags))
	return cmd
}

func newWebhooksListCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List webhook endpoints",
		Annotations: map[string]string{
			"mcp:read-only": "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := flags.newClient()
			if err != nil {
				return err
			}
			raw, err := c.Get("/v1/webhook-endpoints", nil)
			if err != nil {
				return classifyAPIError(err, flags)
			}
			return printOutputWithFlags(cmd.OutOrStdout(), raw, flags)
		},
	}
}

func newWebhooksCreateCmd(flags *rootFlags) *cobra.Command {
	var endpointURL string
	var scopes, events, folderIDs []string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a webhook endpoint (the signing secret is returned once)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if endpointURL == "" {
				return usageErr(fmt.Errorf("--url is required"))
			}
			if err := validateWebhookURL(endpointURL); err != nil {
				return usageErr(err)
			}
			if err := validateWebhookFilters(scopes, events, folderIDs, true); err != nil {
				return usageErr(err)
			}
			body := map[string]any{"url": endpointURL, "scopes": scopes}
			if len(events) > 0 {
				body["events"] = events
			}
			if len(folderIDs) > 0 {
				body["folder_ids"] = folderIDs
			}
			c, err := flags.newClient()
			if err != nil {
				return err
			}
			raw, _, err := c.Post("/v1/webhook-endpoints", body)
			if err != nil {
				return classifyAPIError(err, flags)
			}
			// Never compact create output: signing_secret is shown only once.
			return printOutput(cmd.OutOrStdout(), raw, true)
		},
	}
	cmd.Flags().StringVar(&endpointURL, "url", "", "HTTPS delivery URL")
	cmd.Flags().StringSliceVar(&scopes, "scope", nil, "Delivery scope (personal, public, workspace); repeatable")
	cmd.Flags().StringSliceVar(&events, "event", nil, "Event type; repeatable (defaults to all events)")
	cmd.Flags().StringSliceVar(&folderIDs, "folder-id", nil, "Limit delivery to a folder; repeatable (maximum 100)")
	_ = cmd.MarkFlagRequired("scope")
	return cmd
}

func newWebhooksUpdateCmd(flags *rootFlags) *cobra.Command {
	var endpointURL string
	var scopes, events, folderIDs []string
	var enabled, clearFolders bool
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a webhook endpoint",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !webhookEndpointIDPattern.MatchString(args[0]) {
				return usageErr(fmt.Errorf("webhook endpoint id must match whe_<14 alphanumeric characters>"))
			}
			if clearFolders && cmd.Flags().Changed("folder-id") {
				return usageErr(fmt.Errorf("--clear-folders and --folder-id cannot be used together"))
			}
			if err := validateWebhookFilters(scopes, events, folderIDs, false); err != nil {
				return usageErr(err)
			}
			body := map[string]any{}
			if cmd.Flags().Changed("url") {
				if err := validateWebhookURL(endpointURL); err != nil {
					return usageErr(err)
				}
				body["url"] = endpointURL
			}
			if cmd.Flags().Changed("scope") {
				body["scopes"] = scopes
			}
			if cmd.Flags().Changed("event") {
				body["events"] = events
			}
			if cmd.Flags().Changed("folder-id") {
				body["folder_ids"] = folderIDs
			}
			if clearFolders {
				body["folder_ids"] = []string{}
			}
			if cmd.Flags().Changed("enabled") {
				body["enabled"] = enabled
			}
			if len(body) == 0 {
				return usageErr(fmt.Errorf("provide at least one field to update"))
			}
			c, err := flags.newClient()
			if err != nil {
				return err
			}
			raw, _, err := c.Patch("/v1/webhook-endpoints/"+url.PathEscape(args[0]), body)
			if err != nil {
				return classifyAPIError(err, flags)
			}
			return printOutputWithFlags(cmd.OutOrStdout(), raw, flags)
		},
	}
	cmd.Flags().StringVar(&endpointURL, "url", "", "HTTPS delivery URL")
	cmd.Flags().StringSliceVar(&scopes, "scope", nil, "Replace delivery scopes")
	cmd.Flags().StringSliceVar(&events, "event", nil, "Replace event types")
	cmd.Flags().StringSliceVar(&folderIDs, "folder-id", nil, "Replace folder filters")
	cmd.Flags().BoolVar(&clearFolders, "clear-folders", false, "Remove all folder filters")
	cmd.Flags().BoolVar(&enabled, "enabled", true, "Enable or disable deliveries")
	return cmd
}

func newWebhooksDeleteCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a webhook endpoint",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !webhookEndpointIDPattern.MatchString(args[0]) {
				return usageErr(fmt.Errorf("webhook endpoint id must match whe_<14 alphanumeric characters>"))
			}
			if !flags.dryRun && !flags.yes {
				return usageErr(fmt.Errorf("deleting a webhook endpoint requires --yes"))
			}
			c, err := flags.newClient()
			if err != nil {
				return err
			}
			raw, _, err := c.Delete("/v1/webhook-endpoints/" + url.PathEscape(args[0]))
			if err != nil {
				return classifyAPIError(err, flags)
			}
			if len(raw) > 0 {
				return printOutputWithFlags(cmd.OutOrStdout(), raw, flags)
			}
			return emitJSON(cmd, flags, map[string]any{"deleted": true, "id": args[0]})
		},
	}
}

func validateWebhookFilters(scopes, events, folderIDs []string, requireScopes bool) error {
	if requireScopes && len(scopes) == 0 {
		return fmt.Errorf("at least one --scope is required")
	}
	for _, scope := range scopes {
		if !webhookScopes[scope] {
			return fmt.Errorf("invalid scope %q: use personal, public, or workspace", scope)
		}
	}
	if len(scopes) > 1 {
		for _, scope := range scopes {
			if scope == "workspace" {
				return fmt.Errorf("workspace scope must be used alone")
			}
		}
	}
	for _, event := range events {
		if !webhookEvents[event] {
			return fmt.Errorf("invalid event %q: use note.access_granted, note.edited, or note.generated", event)
		}
	}
	if len(folderIDs) > 100 {
		return fmt.Errorf("at most 100 folder ids are allowed")
	}
	for _, id := range folderIDs {
		if !webhookFolderIDPattern.MatchString(id) {
			return fmt.Errorf("folder id %q must match fol_<14 alphanumeric characters>", id)
		}
	}
	return nil
}

func validateWebhookURL(raw string) error {
	u, err := url.ParseRequestURI(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("webhook --url must be an absolute HTTPS URL")
	}
	return nil
}

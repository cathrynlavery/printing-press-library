// Copyright 2026 Greg Ceccarelli and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

func newVideosClipsUndoLastCutsCmd(flags *rootFlags) *cobra.Command {
	var snapshotPath string
	var apply bool
	cmd := &cobra.Command{
		Use:     "undo-last-cuts <id> <clipId>",
		Short:   "Restore the most recent cuts snapshot saved before a clean/apply workflow",
		Example: "  tella-pp-cli videos clips undo-last-cuts vid_abc cl_xyz --apply",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			videoID, clipID := args[0], args[1]
			path := snapshotPath
			if path == "" {
				var err error
				path, err = latestCutSnapshotPath(videoID, clipID)
				if err != nil {
					return err
				}
			}
			snapshot, err := readCutSnapshot(path)
			if err != nil {
				return err
			}
			if snapshot.VideoID != videoID || snapshot.ClipID != clipID {
				return usageErr(fmt.Errorf("snapshot belongs to video %s clip %s, not %s/%s", snapshot.VideoID, snapshot.ClipID, videoID, clipID))
			}
			result := map[string]any{"video_id": videoID, "clip_id": clipID, "snapshot": path, "body": map[string]any{"cuts": snapshot.Cuts}}
			if flags.dryRun || !apply {
				result["dry_run"] = true
				result["applied"] = false
				return printJSONFiltered(cmd.OutOrStdout(), result, flags)
			}
			api, err := flags.newClient()
			if err != nil {
				return err
			}
			status, err := restoreCutSnapshot(api, snapshot)
			if err != nil {
				return classifyAPIError(err, flags)
			}
			result["dry_run"] = false
			result["applied"] = true
			result["status"] = status
			return printJSONFiltered(cmd.OutOrStdout(), result, flags)
		},
	}
	cmd.Flags().StringVar(&snapshotPath, "snapshot", "", "Snapshot JSON path; defaults to latest snapshot for the clip")
	cmd.Flags().BoolVar(&apply, "apply", false, "Actually restore cuts; default prints request body")
	return cmd
}

func newVideosClipsRestoreCutsCmd(flags *rootFlags) *cobra.Command {
	var cutsJSON string
	var snapshotPath string
	var apply bool
	cmd := &cobra.Command{
		Use:     "restore-cuts <id> <clipId>",
		Short:   "Restore clip cuts from inline JSON or a snapshot file",
		Example: "  tella-pp-cli videos clips restore-cuts vid_abc cl_xyz --cuts '[{\"startTimeMs\":100,\"durationMs\":150}]' --apply",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			videoID, clipID := args[0], args[1]
			var cuts any
			if snapshotPath != "" {
				snapshot, err := readCutSnapshot(snapshotPath)
				if err != nil {
					return err
				}
				if snapshot.VideoID != videoID || snapshot.ClipID != clipID {
					return usageErr(fmt.Errorf("snapshot belongs to video %s clip %s, not %s/%s", snapshot.VideoID, snapshot.ClipID, videoID, clipID))
				}
				cuts = snapshot.Cuts
			} else {
				if cutsJSON == "" {
					return usageErr(fmt.Errorf("pass --cuts JSON or --snapshot <path>"))
				}
				if err := json.Unmarshal([]byte(cutsJSON), &cuts); err != nil {
					return fmt.Errorf("parsing --cuts JSON: %w", err)
				}
				if _, ok := cuts.([]any); !ok {
					return usageErr(fmt.Errorf("--cuts must be a JSON array"))
				}
			}
			body := map[string]any{"cuts": cuts}
			result := map[string]any{"video_id": videoID, "clip_id": clipID, "body": body}
			if snapshotPath != "" {
				result["snapshot"] = snapshotPath
			}
			if flags.dryRun || !apply {
				result["dry_run"] = true
				result["applied"] = false
				return printJSONFiltered(cmd.OutOrStdout(), result, flags)
			}
			api, err := flags.newClient()
			if err != nil {
				return err
			}
			_, status, err := api.Patch(fmt.Sprintf("/v1/videos/%s/clips/%s", videoID, clipID), body)
			if err != nil {
				return classifyAPIError(err, flags)
			}
			result["dry_run"] = false
			result["applied"] = true
			result["status"] = status
			return printJSONFiltered(cmd.OutOrStdout(), result, flags)
		},
	}
	cmd.Flags().StringVar(&cutsJSON, "cuts", "", "Exact stored cuts JSON array to restore")
	cmd.Flags().StringVar(&snapshotPath, "snapshot", "", "Snapshot JSON path to restore")
	cmd.Flags().BoolVar(&apply, "apply", false, "Actually restore cuts; default prints request body")
	return cmd
}

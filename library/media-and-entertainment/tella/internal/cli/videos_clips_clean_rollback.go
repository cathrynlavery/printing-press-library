// Copyright 2026 Greg Ceccarelli and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"fmt"
	"reflect"
)

type cleanExpectedCuts struct {
	Cuts  any
	Known bool
}

type cleanRollbackResult struct {
	VideoID   string `json:"video_id"`
	ClipID    string `json:"clip_id"`
	Status    int    `json:"status,omitempty"`
	Restored  bool   `json:"restored"`
	Unchanged bool   `json:"unchanged,omitempty"`
	Conflict  bool   `json:"conflict,omitempty"`
	Error     string `json:"error,omitempty"`
}

func mutationOutcomeIndeterminate(status int) bool {
	return status == 0 || status >= 500
}

func rollbackCleanClips(
	api cleanAPI,
	touched []string,
	snapshots map[string]cutSnapshot,
	expected map[string]cleanExpectedCuts,
) []cleanRollbackResult {
	results := make([]cleanRollbackResult, 0, len(touched))
	for i := len(touched) - 1; i >= 0; i-- {
		key := touched[i]
		snapshot := snapshots[key]
		item := cleanRollbackResult{VideoID: snapshot.VideoID, ClipID: snapshot.ClipID}
		current, err := captureCutSnapshot(api, snapshot.VideoID, snapshot.ClipID)
		if err != nil {
			item.Error = fmt.Sprintf("checking current cuts before rollback: %v", err)
			results = append(results, item)
			continue
		}

		state := expected[key]
		if !state.Known {
			if reflect.DeepEqual(current.Cuts, snapshot.Cuts) {
				item.Unchanged = true
				results = append(results, item)
				continue
			}
			item.Conflict = true
			item.Error = "rollback skipped: mutation outcome is indeterminate and current cuts differ from the snapshot"
			results = append(results, item)
			continue
		}
		if !reflect.DeepEqual(current.Cuts, state.Cuts) {
			item.Conflict = true
			item.Error = "rollback skipped: current cuts changed after this cleanup operation"
			results = append(results, item)
			continue
		}

		item.Status, err = restoreCutSnapshot(api, snapshot)
		if err != nil {
			item.Error = err.Error()
		} else {
			item.Restored = true
		}
		results = append(results, item)
	}
	return results
}

func allRollbacksSucceeded(results []cleanRollbackResult) bool {
	if len(results) == 0 {
		return false
	}
	for _, result := range results {
		if result.Error != "" {
			return false
		}
	}
	return true
}

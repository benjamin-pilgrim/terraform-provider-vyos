package vyos

import (
	"errors"
	"fmt"
	"sort"
)

func pathContains(parent, child []string) bool {
	return len(parent) <= len(child) && pathsOverlap(parent, child)
}

func wholeSelectionCovers(entries Batch, path []string) bool {
	for _, entry := range entries {
		if !entry.Partial && pathContains(entry.Path, path) {
			return true
		}
	}
	return false
}

// Removed whole-value ownership is deleted except where the new batch still
// manages a descendant. Traverse the live object to remove only departed
// branches, so narrowing ownership does not rebuild retained configuration.
func departedWholePaths(path []string, value any, next Batch) ([][]string, error) {
	if value == nil || wholeSelectionCovers(next, path) {
		return nil, nil
	}
	descendants := false
	for _, entry := range next {
		if pathKey(entry.Path) == pathKey(path) {
			return nil, nil
		}
		if !entry.Absent && pathContains(path, entry.Path) {
			descendants = true
		}
	}
	if !descendants {
		return [][]string{path}, nil
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("cannot narrow a scalar or list selection to child paths")
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var removed [][]string
	for _, key := range keys {
		child := append(append([]string{}, path...), key)
		paths, err := departedWholePaths(child, object[key], next)
		if err != nil {
			return nil, err
		}
		removed = append(removed, paths...)
	}
	return removed, nil
}

func priorIncludeMembers(prior Batch, path []string, actual []string) []string {
	members := []string{}
	for _, name := range batchNames(prior) {
		entry := prior[name]
		if !entry.Partial && !entry.Absent && pathContains(entry.Path, path) {
			return actual
		}
		if entry.Partial && pathKey(entry.Path) == pathKey(path) {
			members = append(members, entry.Include...)
		}
	}
	return members
}

func addRemovalCheck(desired Batch, current map[string]any, entry BatchEntry, value any) {
	for index := 0; ; index++ {
		name := fmt.Sprintf("_removed_%d", index)
		if _, exists := desired[name]; !exists {
			desired[name] = entry
			current[name] = value
			return
		}
	}
}

// A whole-path deletion subsumes child deletions and member deletions at that
// path. This also deduplicates removals shared by multiple include entries.
func compactDeletes(ops []map[string]any) []map[string]any {
	result := []map[string]any{}
	for _, op := range ops {
		path := op["path"].([]string)
		value, partial := op["value"]
		covered := false
		for _, previous := range result {
			oldPath := previous["path"].([]string)
			oldValue, oldPartial := previous["value"]
			if !oldPartial && pathContains(oldPath, path) || partial && oldPartial && pathKey(path) == pathKey(oldPath) && value == oldValue {
				covered = true
				break
			}
		}
		if covered {
			continue
		}
		if !partial {
			kept := result[:0]
			for _, previous := range result {
				if !pathContains(path, previous["path"].([]string)) {
					kept = append(kept, previous)
				}
			}
			result = kept
		}
		result = append(result, op)
	}
	return result
}

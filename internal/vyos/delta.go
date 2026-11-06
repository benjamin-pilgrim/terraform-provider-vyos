package vyos

import (
	"encoding/json"
	"sort"
)

// changedOperations retains whole-subtree ownership but does not rebuild
// unchanged leaves. Removing a subtree deletes its highest absent node, so
// empty tag nodes cannot survive. Changed scalar/list leaves are replaced.
func changedOperations(path []string, before, after any) (deletes, writes []map[string]any, err error) {
	raw, err := json.Marshal(after)
	if err != nil {
		return nil, nil, err
	}
	if Equivalent(string(raw), before) {
		return nil, nil, nil
	}
	oldMap, oldObject := before.(map[string]any)
	newMap, newObject := after.(map[string]any)
	if oldObject && newObject && len(newMap) > 0 {
		keys := map[string]bool{}
		for key := range oldMap {
			keys[key] = true
		}
		for key := range newMap {
			keys[key] = true
		}
		ordered := make([]string, 0, len(keys))
		for key := range keys {
			ordered = append(ordered, key)
		}
		sort.Strings(ordered)
		for _, key := range ordered {
			child := append(append([]string{}, path...), key)
			old, next := oldMap[key], newMap[key]
			switch {
			case next == nil:
				if old != nil {
					deletes = append(deletes, map[string]any{"op": "delete", "path": child})
				}
			case old == nil:
				ops, e := Operations(child, next)
				if e != nil {
					return nil, nil, e
				}
				writes = append(writes, ops...)
			default:
				d, w, e := changedOperations(child, old, next)
				if e != nil {
					return nil, nil, e
				}
				deletes = append(deletes, d...)
				writes = append(writes, w...)
			}
		}
		return deletes, writes, nil
	}
	writes, err = Operations(path, after)
	if err != nil {
		return nil, nil, err
	}
	return []map[string]any{{"op": "delete", "path": path}}, writes, nil
}

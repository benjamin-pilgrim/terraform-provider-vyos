package vyos

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"unicode"
)

// BatchEntry exclusively owns the subtree at Path. Value has the same JSON
// representation as an individual vyos_config value.
type BatchEntry struct {
	Path  []string
	Value string
}

type Batch map[string]BatchEntry

type pathClaim struct {
	path  []string
	owner string
}

func pathKey(path []string) string {
	encoded, _ := json.Marshal(path)
	return string(encoded)
}

func pathsOverlap(a, b []string) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func validatePaths(paths [][]string) error {
	for i, path := range paths {
		if len(path) == 0 {
			return errors.New("paths must contain at least one component")
		}
		for _, component := range path {
			if strings.TrimSpace(component) == "" || strings.IndexFunc(component, unicode.IsControl) >= 0 {
				return errors.New("path components must be nonempty and contain no control characters")
			}
		}
		for _, other := range paths[:i] {
			if pathsOverlap(path, other) {
				return errors.New("batch paths must be distinct and must not overlap")
			}
		}
	}
	return nil
}

// claimPaths atomically claims an entire batch, sharing ownership checks with
// individual resources. Array keys preserve spaces inside tag names.
func (vc *VyosConfig) claimPaths(paths [][]string, owner string) error {
	if err := validatePaths(paths); err != nil {
		return err
	}
	for _, path := range paths {
		key := pathKey(path)
		for otherKey, other := range vc.managedPaths {
			if key == otherKey && owner == other.owner {
				continue
			}
			if pathsOverlap(path, other.path) {
				return errors.New("overlapping resource paths are unsafe; each resource must exclusively own disjoint subtrees")
			}
		}
	}
	if vc.managedPaths == nil {
		vc.managedPaths = map[string]pathClaim{}
	}
	for _, path := range paths {
		vc.managedPaths[pathKey(path)] = pathClaim{path: append([]string(nil), path...), owner: owner}
	}
	return nil
}

func (vc *VyosConfig) release(path []string) {
	delete(vc.managedPaths, pathKey(path))
}

// BatchIdentity records names and exact path components, independently of values.
// Changing those paths replaces a batch; value-only changes update it in place.
func BatchIdentity(entries Batch) (string, error) {
	if len(entries) == 0 {
		return "", errors.New("a batch must contain at least one entry")
	}
	identity := map[string][]string{}
	var paths [][]string
	for name, entry := range entries {
		if strings.TrimSpace(name) == "" || strings.IndexFunc(name, unicode.IsControl) >= 0 {
			return "", errors.New("batch entry names must be nonempty and contain no control characters")
		}
		identity[name] = entry.Path
		paths = append(paths, entry.Path)
	}
	if err := validatePaths(paths); err != nil {
		return "", err
	}
	raw, _ := json.Marshal(identity)
	return string(raw), nil
}

func ParseBatchIdentity(raw string) (Batch, error) {
	var identity map[string][]string
	if json.Unmarshal([]byte(raw), &identity) != nil {
		return nil, errors.New("batch import ID must be a JSON object mapping entry names to path arrays")
	}
	entries := Batch{}
	for name, path := range identity {
		entries[name] = BatchEntry{Path: path}
	}
	if _, err := BatchIdentity(entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func batchNames(entries Batch) []string {
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (vc *VyosConfig) batchSnapshot(ctx context.Context, entries Batch) (map[string]any, error) {
	root, err := vc.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	values := map[string]any{}
	for _, name := range batchNames(entries) {
		value, err := configNode(root, entries[name].Path)
		if err != nil {
			return nil, err
		}
		if value != nil {
			values[name] = value
		}
	}
	return values, nil
}

func (vc *VyosConfig) ShowBatch(ctx context.Context, entries Batch) (map[string]any, error) {
	owner, err := BatchIdentity(entries)
	if err != nil {
		return nil, err
	}
	vc.mutex.Lock()
	defer vc.mutex.Unlock()
	var paths [][]string
	for _, entry := range entries {
		paths = append(paths, entry.Path)
	}
	if err := vc.claimPaths(paths, owner); err != nil {
		return nil, err
	}
	values, err := vc.batchSnapshot(ctx, entries)
	if err != nil {
		return nil, err
	}
	for name, entry := range entries {
		if _, present := values[name]; !present {
			vc.release(entry.Path)
		}
	}
	return values, nil
}

// MutateBatch returns attempted=true once a configure request has been sent.
// Even an error can mean partial/uncertain application across VyOS components,
// so a failed create must retain all owned paths in state for cleanup/retry.
func (vc *VyosConfig) MutateBatch(ctx context.Context, entries Batch, mode string) (attempted bool, err error) {
	if mode != "create" && mode != "update" && mode != "delete" {
		return false, errors.New("invalid batch mutation")
	}
	owner, err := BatchIdentity(entries)
	if err != nil {
		return false, err
	}
	names := batchNames(entries)
	sets := map[string][]map[string]any{}
	var paths [][]string
	for _, name := range names {
		entry := entries[name]
		paths = append(paths, entry.Path)
		if mode != "delete" {
			value, err := DecodeValue(entry.Value)
			if err != nil {
				return false, err
			}
			sets[name], err = Operations(entry.Path, value)
			if err != nil {
				return false, err
			}
		}
	}
	vc.mutex.Lock()
	defer vc.mutex.Unlock()
	if err := vc.claimPaths(paths, owner); err != nil {
		return false, err
	}
	current, err := vc.batchSnapshot(ctx, entries)
	if err != nil {
		return false, err
	}
	var deletes, writes []map[string]any
	for _, name := range names {
		entry := entries[name]
		value, present := current[name]
		if mode == "create" && present {
			return false, errors.New("batch path already exists; import the batch before managing existing configuration")
		}
		if mode == "update" && present && Equivalent(entry.Value, value) {
			continue
		}
		if present {
			deletes = append(deletes, map[string]any{"op": "delete", "path": entry.Path})
		}
		writes = append(writes, sets[name]...)
	}
	operations := append(deletes, writes...)
	if len(operations) > 0 {
		if _, err := vc.apiClient.request(ctx, "configure", operations); err != nil {
			return true, err
		}
		if err := vc.save(ctx); err != nil {
			return true, err
		}
	}
	if mode == "delete" {
		for _, entry := range entries {
			vc.release(entry.Path)
		}
	}
	return len(operations) > 0, nil
}

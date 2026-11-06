package vyos

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"unicode"
)

// BatchEntry owns either the subtree at Path or selected list values (Partial).
// Value uses the same JSON representation as an individual vyos_config value.
type BatchEntry struct {
	Path     []string
	Value    string
	Partial  bool
	Include  []string
	Exclude  []string // Verification-only values that must be absent.
	Preserve []string // Unchanged live values that must survive this mutation.
}

type Batch map[string]BatchEntry

type pathClaim struct {
	path    []string
	owner   string
	partial bool
	member  string
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

// BatchIdentity records names, exact path components and ownership modes,
// independently of values. The provider uses this to validate ownership scopes.
func BatchIdentity(entries Batch) (string, error) {
	if len(entries) == 0 {
		return "", errors.New("a batch must contain at least one entry")
	}
	identity := map[string]any{}
	for name, entry := range entries {
		if strings.TrimSpace(name) == "" || strings.IndexFunc(name, unicode.IsControl) >= 0 {
			return "", errors.New("batch entry names must be nonempty and contain no control characters")
		}
		if entry.Partial {
			identity[name] = map[string]any{"path": entry.Path, "include": true}
		} else {
			identity[name] = entry.Path
		}
	}
	if err := validateBatchEntries(entries); err != nil {
		return "", err
	}
	raw, _ := json.Marshal(identity)
	return string(raw), nil
}

func ParseBatchIdentity(raw string) (Batch, error) {
	var identity map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &identity) != nil {
		return nil, errors.New("batch import ID must map entry names to paths or include selectors")
	}
	var owner string
	if nested, ok := identity["_entries"]; ok && json.Unmarshal(identity["_owner"], &owner) == nil {
		if owner == "" {
			return nil, errors.New("invalid empty batch owner")
		}
		decoded := map[string]json.RawMessage{}
		if json.Unmarshal(nested, &decoded) != nil {
			return nil, errors.New("invalid batch identity")
		}
		identity = decoded
	}
	entries := Batch{}
	for name, raw := range identity {
		var components []string
		if json.Unmarshal(raw, &components) == nil {
			entries[name] = BatchEntry{Path: components}
			continue
		}
		var partial struct {
			Path    []string        `json:"path"`
			Include json.RawMessage `json:"include"`
		}
		if json.Unmarshal(raw, &partial) != nil || len(partial.Include) == 0 {
			return nil, errors.New("invalid batch entry identity")
		}
		entry := BatchEntry{Path: partial.Path, Partial: true}
		if string(partial.Include) != "true" && json.Unmarshal(partial.Include, &entry.Include) != nil {
			return nil, errors.New("include selectors must be arrays of strings")
		}
		entries[name] = entry
	}
	if _, err := BatchIdentity(entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// OwnedBatchIdentity gives partial batches distinct ownership even when their
// paths are identical. Existing complete-value IDs retain their original form.
func OwnedBatchIdentity(entries Batch, owner string) (string, error) {
	identity, err := BatchIdentity(entries)
	if err != nil {
		return "", err
	}
	partial := false
	for _, entry := range entries {
		partial = partial || entry.Partial
	}
	if !partial {
		return identity, nil
	}
	raw, _ := json.Marshal(map[string]any{"_entries": json.RawMessage(identity), "_owner": owner})
	return string(raw), nil
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
	return vc.ShowOwnedBatch(ctx, entries, owner)
}

// ShowOwnedBatch refreshes whole values and intersects only tracked includes.
func (vc *VyosConfig) ShowOwnedBatch(ctx context.Context, entries Batch, owner string) (map[string]any, error) {
	_, err := BatchIdentity(entries)
	if err != nil {
		return nil, err
	}
	vc.mutex.Lock()
	defer vc.mutex.Unlock()
	if err := vc.claimBatch(entries, owner); err != nil {
		return nil, err
	}
	values, err := vc.batchSnapshot(ctx, entries)
	if err != nil {
		return nil, err
	}
	for name, entry := range entries {
		if entry.Partial {
			actual, err := listValues(values[name])
			if err != nil {
				return nil, err
			}
			present := stringSet(actual)
			owned := []string{}
			for _, member := range entry.Include {
				if present[member] {
					owned = append(owned, member)
				} else {
					delete(vc.managedPaths, memberKey(entry.Path, member))
				}
			}
			values[name] = owned
			continue
		}
		if _, present := values[name]; !present {
			vc.release(entry.Path)
		}
	}
	return values, nil
}

// MutateBatch returns attempted=true once a configure request has been sent.
// Even an error can mean partial/uncertain application across VyOS components,
// so a failed create must retain all owned paths in state for cleanup/retry.
func (vc *VyosConfig) MutateBatch(ctx context.Context, entries Batch, mode string) (bool, error) {
	owner, err := BatchIdentity(entries)
	if err != nil {
		return false, err
	}
	return vc.MutateOwnedBatch(ctx, entries, entries, mode, owner)
}

// MutateOwnedBatch uses the stable resource ID to claim ownership; prior carries
// the previously owned selections so removed includes can be deleted precisely.
func (vc *VyosConfig) MutateOwnedBatch(ctx context.Context, entries, prior Batch, mode, owner string) (attempted bool, err error) {
	if mode != "create" && mode != "update" && mode != "delete" {
		return false, errors.New("invalid batch mutation")
	}
	_, err = BatchIdentity(entries)
	if err != nil {
		return false, err
	}
	names := batchNames(entries)
	sets := map[string][]map[string]any{}
	for _, name := range names {
		entry := entries[name]
		if mode != "delete" && !entry.Partial {
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
	if err := vc.claimBatch(entries, owner); err != nil {
		return false, err
	}
	if mode == "update" {
		if err := vc.claimBatch(prior, owner); err != nil {
			return false, err
		}
	}
	current, err := vc.batchSnapshot(ctx, entries)
	if err != nil {
		return false, err
	}
	var deletes, writes []map[string]any
	desired := Batch{}
	wantedByPath := map[string]map[string]bool{}
	if mode != "delete" {
		for _, entry := range entries {
			if entry.Partial {
				key := pathKey(entry.Path)
				if wantedByPath[key] == nil {
					wantedByPath[key] = map[string]bool{}
				}
				for _, value := range entry.Include {
					wantedByPath[key][value] = true
				}
			}
		}
	}
	for _, name := range names {
		entry := entries[name]
		value, present := current[name]
		if entry.Partial {
			actual, err := listValues(value)
			if err != nil {
				return false, err
			}
			existing := stringSet(actual)
			wanted := entry.Include
			old := prior[name].Include
			if mode == "create" {
				old = nil
			}
			if mode == "delete" {
				wanted = nil
				old = entry.Include
			}
			wantSet := wantedByPath[pathKey(entry.Path)]
			removed := []string{}
			for _, member := range old {
				if !wantSet[member] {
					removed = append(removed, member)
					if existing[member] {
						deletes = append(deletes, map[string]any{"op": "delete", "path": entry.Path, "value": member})
					}
				}
			}
			for _, member := range wanted {
				if !existing[member] {
					writes = append(writes, map[string]any{"op": "set", "path": entry.Path, "value": member})
				}
			}
			desired[name] = BatchEntry{Path: entry.Path, Partial: true, Include: wanted, Exclude: removed}
			continue
		}
		desired[name] = entry
		if mode == "delete" {
			desired[name] = BatchEntry{Path: entry.Path}
		}
		if mode == "create" && present {
			return false, errors.New("batch path already exists; import the batch before managing existing configuration")
		}
		if mode == "update" && present && Equivalent(entry.Value, value) {
			continue
		}
		if mode == "update" && present {
			wanted, decodeErr := DecodeValue(entry.Value)
			if decodeErr != nil {
				return false, decodeErr
			}
			d, w, deltaErr := changedOperations(entry.Path, value, wanted)
			if deltaErr != nil {
				return false, deltaErr
			}
			deletes = append(deletes, d...)
			writes = append(writes, w...)
			continue
		}
		if present {
			deletes = append(deletes, map[string]any{"op": "delete", "path": entry.Path})
		}
		writes = append(writes, sets[name]...)
	}
	for name, entry := range desired {
		if !entry.Partial {
			continue
		}
		live, _ := listValues(current[name])
		removed := map[string]bool{}
		for _, other := range desired {
			if other.Partial && pathKey(other.Path) == pathKey(entry.Path) {
				for _, value := range other.Exclude {
					removed[value] = true
				}
			}
		}
		for _, value := range live {
			if !removed[value] {
				entry.Preserve = append(entry.Preserve, value)
			}
		}
		desired[name] = entry
	}
	operations := append(deletes, writes...)
	if len(operations) > 0 {
		if attempted, err := vc.configure(ctx, operations, desired); err != nil {
			return attempted, err
		}
	}
	for _, entry := range prior {
		if entry.Partial {
			keep := wantedByPath[pathKey(entry.Path)]
			for _, member := range entry.Include {
				if mode == "delete" || !keep[member] {
					delete(vc.managedPaths, memberKey(entry.Path, member))
				}
			}
		} else if mode == "delete" {
			vc.release(entry.Path)
		}
	}
	return len(operations) > 0, nil
}

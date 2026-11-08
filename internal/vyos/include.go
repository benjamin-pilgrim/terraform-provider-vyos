package vyos

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode"
)

func stringSet(values []string) map[string]bool {
	result := map[string]bool{}
	for _, value := range values {
		result[value] = true
	}
	return result
}
func listValues(node any) ([]string, error) {
	switch v := node.(type) {
	case nil:
		return nil, nil
	case string:
		return []string{v}, nil
	case []string:
		return v, nil
	case []any:
		result := make([]string, 0, len(v))
		for _, item := range v {
			value, ok := item.(string)
			if !ok {
				return nil, errors.New("include requires a string-valued list leaf")
			}
			result = append(result, value)
		}
		return result, nil
	default:
		return nil, errors.New("include requires a string-valued list leaf, not a subtree")
	}
}

// ValidateInclude rejects values that cannot safely identify individual list items.
func ValidateInclude(values []string) error {
	seen := map[string]bool{}
	for _, value := range values {
		if strings.TrimSpace(value) == "" || strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return errors.New("include values must be nonempty strings without control characters")
		}
		if seen[value] {
			return errors.New("include values must be unique")
		}
		seen[value] = true
	}
	return nil
}
func validateBatchEntries(entries Batch) error {
	names := batchNames(entries)
	for i, name := range names {
		entry := entries[name]
		if err := validatePaths([][]string{entry.Path}); err != nil {
			return err
		}
		if entry.Partial {
			if err := ValidateInclude(entry.Include); err != nil {
				return err
			}
		}
		for _, otherName := range names[:i] {
			other := entries[otherName]
			if !pathsOverlap(entry.Path, other.Path) {
				continue
			}
			if entry.Partial && other.Partial && pathKey(entry.Path) == pathKey(other.Path) {
				otherValues := stringSet(other.Include)
				for _, value := range entry.Include {
					if otherValues[value] {
						return errors.New("include ownership overlaps another batch entry")
					}
				}
				continue
			}
			return errors.New("whole-value and include ownership must not overlap; paths must otherwise be disjoint")
		}
	}
	return nil
}
func memberKey(path []string, value string) string {
	raw, _ := json.Marshal(value)
	return pathKey(path) + "|" + string(raw)
}
func (vc *VyosConfig) claimBatch(entries Batch, owner string) error {
	if err := validateBatchEntries(entries); err != nil {
		return err
	}
	claims := map[string]pathClaim{}
	for _, entry := range entries {
		if entry.Partial {
			for _, value := range entry.Include {
				claims[memberKey(entry.Path, value)] = pathClaim{path: entry.Path, owner: owner, partial: true, member: value}
			}
		} else {
			claims[pathKey(entry.Path)] = pathClaim{path: entry.Path, owner: owner}
		}
	}
	for key, claim := range claims {
		for otherKey, other := range vc.managedPaths {
			if key == otherKey && owner == other.owner {
				continue
			}
			if !pathsOverlap(claim.path, other.path) {
				continue
			}
			if claim.partial && other.partial && pathKey(claim.path) == pathKey(other.path) && claim.member != other.member {
				continue
			}
			return errors.New("overlapping resource ownership: each include value must have one owner, and cannot overlap whole-value ownership")
		}
	}
	if vc.managedPaths == nil {
		vc.managedPaths = map[string]pathClaim{}
	}
	for key, claim := range claims {
		claim.path = append([]string(nil), claim.path...)
		vc.managedPaths[key] = claim
	}
	return nil
}

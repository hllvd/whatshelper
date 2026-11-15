package brain

import (
	"fmt"
	"sort"
	"strings"

	"whatshelper/internal/pack"
)

// CollectionChange registra uma alteração auditável em um item de coleção.
type CollectionChange struct {
	Collection string         `json:"collection"`
	ItemID     string         `json:"item_id"`
	Action     string         `json:"action"` // "added" | "updated" | "removed"
	Item       map[string]any `json:"item,omitempty"`
}

// CollectionDiff sumariza as adições, atualizações e remoções em coleções de fatos.
type CollectionDiff struct {
	Added   []CollectionChange `json:"added,omitempty"`
	Updated []CollectionChange `json:"updated,omitempty"`
	Removed []CollectionChange `json:"removed,omitempty"`
}

// MergeAndDiffCollections aplica de forma pura, determinística e idempotente um patch de coleções (WIN-05).
func MergeAndDiffCollections(
	existing map[string][]map[string]any,
	patch map[string][]map[string]any,
	defs []pack.CollectionDef,
) (map[string][]map[string]any, CollectionDiff) {
	defByKey := make(map[string]pack.CollectionDef, len(defs))
	for _, d := range defs {
		defByKey[d.Key] = d
	}

	merged := make(map[string][]map[string]any, len(existing)+len(patch))
	for k, items := range existing {
		cloned := make([]map[string]any, 0, len(items))
		for _, it := range items {
			cloned = append(cloned, cloneItemMap(it))
		}
		merged[k] = cloned
	}

	var diff CollectionDiff
	patchKeys := make([]string, 0, len(patch))
	for k := range patch {
		patchKeys = append(patchKeys, k)
	}
	sort.Strings(patchKeys)

	for _, colKey := range patchKeys {
		def := defByKey[colKey]
		currentList := merged[colKey]
		idxByID := make(map[string]int, len(currentList))
		for idx, item := range currentList {
			id := resolveCollectionItemID(item, def)
			if id != "" {
				idxByID[id] = idx
			}
		}

		for _, rawPatchItem := range patch[colKey] {
			if len(rawPatchItem) == 0 {
				continue
			}
			itemID := resolveCollectionItemID(rawPatchItem, def)
			if itemID == "" {
				continue
			}

			if isExplicitDelete(rawPatchItem) {
				if existingIdx, ok := idxByID[itemID]; ok && currentList[existingIdx] != nil {
					removedCopy := cloneItemMap(currentList[existingIdx])
					currentList[existingIdx] = nil
					delete(idxByID, itemID)
					diff.Removed = append(diff.Removed, CollectionChange{
						Collection: colKey,
						ItemID:     itemID,
						Action:     "removed",
						Item:       removedCopy,
					})
				}
				continue
			}

			cleanItem := cloneItemMap(rawPatchItem)
			delete(cleanItem, "_delete")
			idField := strings.TrimSpace(def.IDField)
			if idField == "" {
				idField = "id"
			}
			if _, hasID := cleanItem[idField]; !hasID {
				cleanItem[idField] = itemID
			}

			if existingIdx, ok := idxByID[itemID]; ok && currentList[existingIdx] != nil {
				mergedItem, changed := mergeItemFields(currentList[existingIdx], cleanItem)
				if changed {
					currentList[existingIdx] = mergedItem
					diff.Updated = append(diff.Updated, CollectionChange{
						Collection: colKey,
						ItemID:     itemID,
						Action:     "updated",
						Item:       cloneItemMap(mergedItem),
					})
				}
			} else {
				idxByID[itemID] = len(currentList)
				currentList = append(currentList, cleanItem)
				diff.Added = append(diff.Added, CollectionChange{
					Collection: colKey,
					ItemID:     itemID,
					Action:     "added",
					Item:       cloneItemMap(cleanItem),
				})
			}
		}

		compacted := make([]map[string]any, 0, len(currentList))
		for _, it := range currentList {
			if it != nil {
				compacted = append(compacted, it)
			}
		}
		merged[colKey] = compacted
	}

	return merged, diff
}

func resolveCollectionItemID(item map[string]any, def pack.CollectionDef) string {
	idField := strings.TrimSpace(def.IDField)
	if idField == "" {
		idField = "id"
	}
	if v, ok := item[idField]; ok {
		if s := strings.TrimSpace(fmt.Sprint(v)); s != "" && s != "<nil>" {
			return s
		}
	}
	if len(def.NaturalKey) > 0 {
		parts := make([]string, 0, len(def.NaturalKey))
		for _, k := range def.NaturalKey {
			v, ok := item[k]
			if !ok {
				return ""
			}
			s := strings.TrimSpace(fmt.Sprint(v))
			if s == "" || s == "<nil>" {
				return ""
			}
			parts = append(parts, strings.ToLower(s))
		}
		return strings.Join(parts, ":")
	}
	return ""
}

func isExplicitDelete(item map[string]any) bool {
	v, ok := item["_delete"]
	if !ok {
		return false
	}
	switch b := v.(type) {
	case bool:
		return b
	case string:
		return strings.EqualFold(strings.TrimSpace(b), "true")
	default:
		return false
	}
}

func cloneItemMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func mergeItemFields(existing, patch map[string]any) (map[string]any, bool) {
	out := cloneItemMap(existing)
	changed := false
	for k, v := range patch {
		prev, exists := out[k]
		if !exists || fmt.Sprint(prev) != fmt.Sprint(v) {
			out[k] = v
			changed = true
		}
	}
	return out, changed
}

package config

// DeepMergeJSON recursively merges overlay over base; overlay wins per
// key. Only map values recurse -- everything else (including lists) is
// replaced wholesale by the overlay's value. Ports
// roster/shared/src/resolve.py's deep_merge, the single shared
// implementation of this repository's structured-overlay merge rule (used
// here for .agents/shared/<filename> overlays, and by
// internal/orchestration/routing_overlay.go for its own, differently
// (per-section) constrained routing.json overlay -- see that file's own
// per-section merge rules for where it does NOT use this function
// directly).
func DeepMergeJSON(base, overlay map[string]any) map[string]any {
	result := make(map[string]any, len(base))
	for k, v := range base {
		result[k] = v
	}
	for key, value := range overlay {
		if overlayMap, ok := value.(map[string]any); ok {
			if baseMap, ok := result[key].(map[string]any); ok {
				result[key] = DeepMergeJSON(baseMap, overlayMap)
				continue
			}
		}
		result[key] = value
	}
	return result
}

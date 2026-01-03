package hoard

import (
	"reflect"
	"strings"

	"github.com/hearth-insights/holt/internal/spine"
	"github.com/hearth-insights/holt/pkg/blackboard"
)

// SelectFields extracts specific fields from an artefact into a map.
// Supports top-level fields of Artefact struct.
// Also supports "spine" virtual field if spineInfo is provided.
func SelectFields(artefact *blackboard.Artefact, spineInfo *spine.SpineInfo, fields []string) (map[string]interface{}, error) {
	result := make(map[string]interface{})
	val := reflect.ValueOf(artefact).Elem()
	typ := val.Type()

	// Create a map of json tag -> field value
	jsonMap := make(map[string]interface{})
	for i := 0; i < val.NumField(); i++ {
		field := typ.Field(i)
		tag := field.Tag.Get("json")
		// Handle "name,omitempty"
		parts := strings.Split(tag, ",")
		jsonName := parts[0]
		if jsonName == "" || jsonName == "-" {
			continue
		}
		jsonMap[jsonName] = val.Field(i).Interface()
	}

	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}

		// Handle virtual "spine" field
		if field == "spine" {
			if spineInfo != nil {
				result["spine"] = spineInfo
			} else {
				result["spine"] = nil
			}
			continue
		}

		// Handle standard fields
		if v, ok := jsonMap[field]; ok {
			result[field] = v
		} else {
			// If field not found, we could error or ignore. Ignoring is safer for CLI.
			// But for debugging, maybe we want to know?
			// Let's ignore for now to match design doc "ignore it or return null".
		}
	}

	return result, nil
}

package hoard

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/dyluth/holt/internal/spine"
	"github.com/dyluth/holt/pkg/blackboard"
)

// FormatTable writes a list of artefacts as a human-readable table.
// If spineInfos is provided, it adds a SPINE column.
func FormatTable(w io.Writer, artefacts []*blackboard.Artefact, spineInfos map[string]*spine.SpineInfo, instanceName string) {
	if len(artefacts) == 0 {
		fmt.Fprintf(w, "No artefacts found for instance '%s'\n", instanceName)
		return
	}


	// Print header
	fmt.Fprintf(w, "Artefacts for instance '%s':\n\n", instanceName)

	hasSpine := len(spineInfos) > 0

	// Print header row
	if hasSpine {
		fmt.Fprintf(w, "%-10s %-5s %-10s %-18s %-8s %-10s %s\n",
			"ID", "VER", "TYPE", "BY", "AGE", "SPINE", "PAYLOAD")
		fmt.Fprintf(w, "%-10s %-5s %-10s %-18s %-8s %-10s %s\n",
			"----------", "-----", "----------", "------------------", "--------", "----------", "----------------------------------------")
	} else {
		fmt.Fprintf(w, "%-10s %-5s %-10s %-18s %-8s %s\n",
			"ID", "VER", "TYPE", "BY", "AGE", "PAYLOAD")
		fmt.Fprintf(w, "%-10s %-5s %-10s %-18s %-8s %s\n",
			"----------", "-----", "----------", "------------------", "--------", "----------------------------------------")
	}

	// Print data rows
	for _, a := range artefacts {
		if hasSpine {
			spineStr := "-"
			if info, ok := spineInfos[a.ID]; ok && !info.IsDetached {
				spineStr = info.GitCommit[:8] // Show short commit hash
			}
			fmt.Fprintf(w, "%-10s %-5s %-10s %-18s %-8s %-10s %s\n",
				formatID(a.ID),
				formatVersion(a.Version),
				formatType(a.Type),
				formatProducedBy(a.ProducedByRole),
				formatTimestamp(a.CreatedAtMs),
				spineStr,
				formatPayload(a.Payload),
			)
		} else {
			fmt.Fprintf(w, "%-10s %-5s %-10s %-18s %-8s %s\n",
				formatID(a.ID),
				formatVersion(a.Version),
				formatType(a.Type),
				formatProducedBy(a.ProducedByRole),
				formatTimestamp(a.CreatedAtMs),
				formatPayload(a.Payload),
			)
		}
	}

	// Print count
	countMsg := "artefact"
	if len(artefacts) != 1 {
		countMsg = "artefacts"
	}
	fmt.Fprintf(w, "\n%d %s found\n", len(artefacts), countMsg)

}

// FormatJSON writes a list of artefacts as a JSON array.
// If fields are provided, it selects only those fields.
// If withSpine is true, it resolves and includes spine info.
func FormatJSON(artefacts []*blackboard.Artefact, spineInfos map[string]*spine.SpineInfo, fields []string) ([]byte, error) {
	var output []interface{}

	for _, art := range artefacts {
		var item interface{} = art

		// If fields are specified or spine is requested (and not already in fields), we need to select/augment
		if len(fields) > 0 {
			spine := spineInfos[art.ID]
			selected, err := SelectFields(art, spine, fields)
			if err != nil {
				return nil, err
			}
			item = selected
		} else if spineInfos != nil {
			// If no specific fields requested but spine is available (implied --with-spine but no --fields),
			// we probably want full artefact + spine.
			// But standard JSON marshaling won't add "spine" field to Artefact struct.
			// So we convert to map and add it.
			// This is a bit expensive but correct.
			artMap := make(map[string]interface{})
			tmp, _ := json.Marshal(art)
			json.Unmarshal(tmp, &artMap)
			if spine, ok := spineInfos[art.ID]; ok {
				artMap["spine"] = spine
			}
			item = artMap
		}

		output = append(output, item)
	}

	return json.MarshalIndent(output, "", "  ")
}

// FormatJSONL writes artefacts as line-delimited JSON (JSONL) to the provided writer.
// Each artefact is written as a single JSON object on its own line.
// This format is ideal for streaming and processing with tools like jq.
func FormatJSONL(w io.Writer, artefacts []*blackboard.Artefact) error {
	for _, artefact := range artefacts {
		// Marshal artefact to JSON (compact, no indentation)
		data, err := json.Marshal(artefact)
		if err != nil {
			return fmt.Errorf("failed to marshal artefact to JSON: %w", err)
		}

		// Write as single line
		_, err = fmt.Fprintf(w, "%s\n", string(data))
		if err != nil {
			return fmt.Errorf("failed to write JSONL output: %w", err)
		}
	}

	return nil
}

// artefactToMap converts an Artefact struct to a map[string]interface{}.
// This is useful for dynamic field selection and adding extra fields like 'spine'.
func artefactToMap(art *blackboard.Artefact) (map[string]interface{}, error) {
	data, err := json.Marshal(art)
	if err != nil {
		return nil, err
	}
	var m map[string]interface{}
	err = json.Unmarshal(data, &m)
	return m, err
}

// spineToMap converts a SpineInfo struct to a map[string]interface{}.
// This is useful for merging spine information into an artefact map.
func spineToMap(info *spine.SpineInfo) (map[string]interface{}, error) {
	data, err := json.Marshal(info)
	if err != nil {
		return nil, err
	}
	var m map[string]interface{}
	err = json.Unmarshal(data, &m)
	return m, err
}

// FormatSingleJSON writes a single artefact as pretty-printed JSON.
// Optionally includes relationship information if provided.
func FormatSingleJSON(w io.Writer, artefact *blackboard.Artefact, relationships ...*RelationshipInfo) error {
	// Convert artefact to map to allow adding extra fields
	var artMap map[string]interface{}
	tmp, err := json.Marshal(artefact)
	if err != nil {
		return fmt.Errorf("failed to marshal artefact to JSON: %w", err)
	}
	if err := json.Unmarshal(tmp, &artMap); err != nil {
		return fmt.Errorf("failed to unmarshal artefact to map: %w", err)
	}

	// Add relationships if provided
	if len(relationships) > 0 && relationships[0] != nil {
		artMap["relationships"] = relationships[0]
	}

	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(artMap); err != nil {
		return fmt.Errorf("failed to write JSON output: %w", err)
	}
	return nil
}

// formatID truncates artefact ID to first 8 characters for compact display.
func formatID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// formatType truncates type names for compact display.
// Shortens common types to save space.
func formatType(typeName string) string {
	// Shorten common type names
	switch typeName {
	case "TerraformCode":
		return "TfCode"
	case "TerraformDocumentation":
		return "TfDocs"
	case "FormattedDocumentation":
		return "FmtDocs"
	case "PackagedModule":
		return "Package"
	case "GoalDefined":
		return "Goal"
	case "ToolExecutionFailure":
		return "Failure"
	}

	// Truncate long type names
	if len(typeName) > 20 {
		return typeName[:17] + "..."
	}
	return typeName
}

// formatPayload truncates payload to first line with max 40 characters for table display.
// Multi-line payloads show only the first line. Empty payloads return "-".
func formatPayload(payload string) string {
	if payload == "" {
		return "-"
	}

	// Get first non-empty line
	lines := strings.Split(payload, "\n")
	var firstLine string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			firstLine = trimmed
			break
		}
	}

	// If all lines were empty
	if firstLine == "" {
		return "-"
	}

	// Truncate to 40 chars (shorter for compact display)
	if len(firstLine) > 40 {
		return firstLine[:37] + "..."
	}

	return firstLine
}

// formatProducedBy formats the produced_by_role field for table display.
// Empty values return "-".
func formatProducedBy(role string) string {
	if role == "" {
		return "-"
	}
	return role
}

// formatVersion formats the version number for table display.
// Shows "v1", "v2", etc. for versions > 1, or "-" for version 1 (initial artefact).
func formatVersion(version int) string {
	if version <= 1 {
		return "-"
	}
	return fmt.Sprintf("v%d", version)
}

// formatTimestamp formats Unix timestamp in milliseconds to human-readable time.
// Shows relative time like "2m ago", "1h ago", etc.
func formatTimestamp(timestampMs int64) string {
	if timestampMs == 0 {
		return "-"
	}

	// Convert ms to time
	t := time.Unix(timestampMs/1000, (timestampMs%1000)*1000000)

	// Calculate time difference from now
	diff := time.Since(t)

	// Format as relative time
	if diff < time.Minute {
		return fmt.Sprintf("%ds ago", int(diff.Seconds()))
	} else if diff < time.Hour {
		return fmt.Sprintf("%dm ago", int(diff.Minutes()))
	} else if diff < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(diff.Hours()))
	} else {
		return fmt.Sprintf("%dd ago", int(diff.Hours()/24))
	}
}

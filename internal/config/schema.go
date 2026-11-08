package config

import (
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ConfigFormSection represents a group of related fields.
type ConfigFormSection struct {
	Title       string              `json:"title"`
	Description string              `json:"description,omitempty"`
	Key         string              `json:"key"`
	Fields      []ConfigFormField   `json:"fields,omitempty"`
	Sections    []ConfigFormSection `json:"sections,omitempty"`
}

// ConfigFormField represents a single editable config field.
type ConfigFormField struct {
	Name        string      `json:"name"` // dotted yaml key path, e.g. "position_manager.enabled"
	Label       string      `json:"label"`
	Type        string      `json:"type"` // string, number, bool, select, text
	Value       interface{} `json:"value"`
	Options     []string    `json:"options,omitempty"`
	Description string      `json:"description,omitempty"`
	Required    bool        `json:"required,omitempty"`
	Min         interface{} `json:"min,omitempty"`
	Max         interface{} `json:"max,omitempty"`
	Step        interface{} `json:"step,omitempty"`
	Comment     string      `json:"comment,omitempty"` // original yaml comment
}

// GenerateSchemaFromYAML parses a YAML document and produces a form schema,
// preserving key order and using comments as field descriptions.
func GenerateSchemaFromYAML(content []byte, fileName string) ([]ConfigFormSection, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}

	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		return buildSectionFromNode(root.Content[0], "", "General")
	}
	return buildSectionFromNode(&root, "", "General")
}

func buildSectionFromNode(node *yaml.Node, prefix, title string) ([]ConfigFormSection, error) {
	if node.Kind != yaml.MappingNode {
		return nil, nil
	}

	sections := []ConfigFormSection{
		{
			Title:  title,
			Key:    prefix,
			Fields: []ConfigFormField{},
		},
	}

	// YAML mapping nodes have alternating key/value children
	for i := 0; i+1 < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		valueNode := node.Content[i+1]

		key := keyNode.Value
		fullKey := key
		if prefix != "" {
			fullKey = prefix + "." + key
		}

		// Derive description from the key's head comment (lines starting with #)
		description := extractComment(keyNode.HeadComment)

		switch valueNode.Kind {
		case yaml.MappingNode:
			// Nested section: recurse
			nestedSections, err := buildSectionFromNode(valueNode, fullKey, formatLabel(key))
			if err != nil {
				return nil, err
			}
			for _, ns := range nestedSections {
				// If a nested section has only fields and no sub-sections, we could
				// either inline it into the parent or nest it. Nesting is safer for
				// long configs like position_manager.
				sections[0].Sections = append(sections[0].Sections, ns)
			}

		case yaml.ScalarNode:
			field := buildScalarField(fullKey, key, valueNode, description)
			sections[0].Fields = append(sections[0].Fields, field)

		case yaml.SequenceNode:
			// Check if the sequence contains mappings (complex items) or scalars.
			if isSequenceOfMappings(valueNode) {
				// Each item becomes a nested subsection (e.g. venues[0], venues[1]).
				itemSections := buildSectionsFromSequence(fullKey, key, valueNode, description)
				sections[0].Sections = append(sections[0].Sections, itemSections...)
			} else {
				// Simple scalar sequence → render as a textarea.
				sections[0].Fields = append(sections[0].Fields, buildSequenceField(fullKey, key, valueNode, description))
			}

		default:
			// Skip alias nodes, etc.
			continue
		}
	}

	// If the section has no fields (only sub-sections), keep it but don't duplicate.
	// If it has both, keep both. This naturally handles the hierarchy.
	return sections, nil
}

func buildScalarField(fullKey, label string, node *yaml.Node, description string) ConfigFormField {
	field := ConfigFormField{
		Name:        fullKey,
		Label:       formatLabel(label),
		Description: description,
		Comment:     node.LineComment,
	}

	val := node.Value
	tag := node.Tag

	// Determine type from the YAML tag
	switch tag {
	case "!!bool":
		field.Type = "bool"
		field.Value = strings.ToLower(val) == "true"
	case "!!int":
		field.Type = "number"
		if n, err := strconv.ParseInt(val, 10, 64); err == nil {
			field.Value = n
		} else {
			field.Value = 0
		}
		field.Step = 1
	case "!!float":
		field.Type = "number"
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			field.Value = f
		} else {
			field.Value = 0.0
		}
		field.Step = 0.01
	default:
		// !!str or !!null or unrecognized → treat as string
		field.Type = "string"
		field.Value = val
	}

	// Known enum fields: upgrade to select
	if options := knownOptions(fullKey); len(options) > 0 {
		field.Type = "select"
		field.Options = options
	}

	// Infer step from description hints (e.g., "ms", "seconds", "percentage")
	if strings.Contains(description, "ms") || strings.Contains(label, "_ms") {
		field.Step = 100
	} else if strings.Contains(description, "seconds") || strings.Contains(label, "_sec") {
		field.Step = 1
	} else if strings.Contains(description, "percentage") || strings.Contains(label, "_pct") || strings.Contains(label, "pct") {
		field.Step = 0.1
		if field.Min == nil {
			field.Min = 0
		}
	}

	// Required for top-level keys
	if fullKey == "environment" {
		field.Required = true
	}

	return field
}

func buildSequenceField(fullKey, label string, node *yaml.Node, description string) ConfigFormField {
	field := ConfigFormField{
		Name:        fullKey,
		Label:       formatLabel(label),
		Type:        "textarea",
		Description: description,
		Comment:     node.LineComment,
		Step:        nil,
	}

	// Serialize the sequence as a YAML list string so the user can edit it
	// naturally (one item per line with "- " prefix).
	var items []string
	for _, child := range node.Content {
		switch child.Kind {
		case yaml.ScalarNode:
			items = append(items, "- "+child.Value)
		case yaml.MappingNode, yaml.SequenceNode:
			// Complex items (mappings or nested sequences) — marshal to YAML
			// so the full structure is preserved in the textarea.
			out, err := yaml.Marshal(wrapNode(child))
			if err != nil {
				items = append(items, "- # <error serialising>")
				continue
			}
			// yaml.Marshal adds a trailing newline; trim it.
			block := strings.TrimRight(string(out), "\n")
			// Prefix each line with two spaces under "- " for readability.
			lines := strings.Split(block, "\n")
			items = append(items, "- "+lines[0])
			for _, l := range lines[1:] {
				items = append(items, "  "+l)
			}
		}
	}
	field.Value = strings.Join(items, "\n")
	return field
}

// isSequenceOfMappings returns true if any child in the sequence is a mapping node.
func isSequenceOfMappings(seq *yaml.Node) bool {
	for _, child := range seq.Content {
		if child.Kind == yaml.MappingNode {
			return true
		}
	}
	return false
}

// sequenceItemLabel tries to find a human-readable label for a sequence item.
// It looks for "name", "venue", "symbol", "id" keys in the mapping node.
func sequenceItemLabel(item *yaml.Node, index int) string {
	if item.Kind != yaml.MappingNode {
		return fmt.Sprintf("Item %d", index+1)
	}
	labelKeys := []string{"name", "venue", "symbol", "id", "key", "slug"}
	for i := 0; i+1 < len(item.Content); i += 2 {
		key := item.Content[i].Value
		for _, lk := range labelKeys {
			if key == lk && item.Content[i+1].Kind == yaml.ScalarNode {
				return item.Content[i+1].Value
			}
		}
	}
	return fmt.Sprintf("Item %d", index+1)
}

// buildSectionsFromSequence expands a sequence of mapping nodes into
// individual subsections, each containing all the fields of that item.
func buildSectionsFromSequence(fullKey, label string, seq *yaml.Node, description string) []ConfigFormSection {
	var result []ConfigFormSection

	for idx, child := range seq.Content {
		if child.Kind != yaml.MappingNode {
			continue
		}
		itemLabel := sequenceItemLabel(child, idx)
		itemKey := fmt.Sprintf("%s[%d]", fullKey, idx)

		nestedSections, err := buildSectionFromNode(child, itemKey, itemLabel)
		if err != nil {
			continue
		}
		result = append(result, nestedSections...)
	}
	return result
}

// extractComment strips leading "# " from each line and joins them.
func extractComment(raw string) string {
	if raw == "" {
		return ""
	}
	var lines []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "# ")
		line = strings.TrimPrefix(line, "#")
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, " ")
}

// formatLabel converts a snake_case or camelCase key to Title Case.
func formatLabel(key string) string {
	// snake_case → spaces
	key = strings.ReplaceAll(key, "_", " ")
	// camelCase → add space before capitals
	var b strings.Builder
	for i, r := range key {
		if i > 0 && r >= 'A' && r <= 'Z' && key[i-1] != ' ' {
			b.WriteRune(' ')
		}
		b.WriteRune(r)
	}
	words := strings.Fields(b.String())
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

// knownOptions returns select options for well-known enum fields.
func knownOptions(fullKey string) []string {
	switch fullKey {
	case "environment":
		return []string{"paper", "demo", "live"}
	case "aggressiveness":
		return []string{"conservative", "balanced", "aggressive"}
	case "log.level":
		return []string{"debug", "info", "warn", "error"}
	case "log.format":
		return []string{"json", "text"}
	case "position_manager.llm_failure_action":
		return []string{"tighten_breakeven", "hold"}
	default:
		return nil
	}
}

// wrapNode converts a yaml.Node back into a marshallable Go structure.
// This is used to serialize complex nested structures (like sequences of mappings)
// back into YAML text for display in textareas.
func wrapNode(node *yaml.Node) interface{} {
	switch node.Kind {
	case yaml.ScalarNode:
		// Return the raw value; yaml.Marshal will handle type inference
		return node.Value
	case yaml.SequenceNode:
		var arr []interface{}
		for _, child := range node.Content {
			arr = append(arr, wrapNode(child))
		}
		return arr
	case yaml.MappingNode:
		m := make(map[string]interface{})
		for i := 0; i < len(node.Content); i += 2 {
			keyNode := node.Content[i]
			valNode := node.Content[i+1]
			m[keyNode.Value] = wrapNode(valNode)
		}
		return m
	default:
		return nil
	}
}

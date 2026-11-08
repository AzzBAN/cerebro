package config

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ExtractValuesFromYAML extracts all values from YAML into a map
func ExtractValuesFromYAML(content []byte) (map[string]interface{}, error) {
	var rootNode yaml.Node
	if err := yaml.Unmarshal(content, &rootNode); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}

	if rootNode.Kind != yaml.DocumentNode || len(rootNode.Content) == 0 {
		return nil, fmt.Errorf("invalid yaml document")
	}

	root := rootNode.Content[0]
	values := make(map[string]interface{})
	extractValuesRecursive(root, "", values)
	return values, nil
}

// UpdateYAMLWithFields updates YAML content with new field values while preserving
// comments, formatting, and order of existing keys.
func UpdateYAMLWithFields(content []byte, fields map[string]interface{}) ([]byte, error) {
	var rootNode yaml.Node
	if err := yaml.Unmarshal(content, &rootNode); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}

	if rootNode.Kind != yaml.DocumentNode || len(rootNode.Content) == 0 {
		return nil, fmt.Errorf("invalid yaml document")
	}

	root := rootNode.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("root must be a mapping")
	}

	// Apply field updates
	for key, value := range fields {
		updateFieldInNode(root, key, value)
	}

	// Sanitize: strip explicit tags that a previous buggy write may have left.
	// yaml.v3 infers the correct tag from the scalar value itself.
	stripExplicitTags(&rootNode)

	// Marshal back to YAML
	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(&rootNode); err != nil {
		return nil, fmt.Errorf("encode yaml: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("close encoder: %w", err)
	}

	return buf.Bytes(), nil
}

// extractValuesRecursive recursively extracts values from a YAML node
func extractValuesRecursive(node *yaml.Node, prefix string, values map[string]interface{}) {
	switch node.Kind {
	case yaml.MappingNode:
		for i := 0; i < len(node.Content)-1; i += 2 {
			keyNode := node.Content[i]
			valueNode := node.Content[i+1]
			key := keyNode.Value

			fullKey := key
			if prefix != "" {
				fullKey = prefix + "." + key
			}

			extractValuesRecursive(valueNode, fullKey, values)
		}

	case yaml.ScalarNode:
		values[prefix] = parseScalarValue(node)

	case yaml.SequenceNode:
		// Emit flat array for simple sequences.
		values[prefix] = extractArrayValue(node)

		// Also emit per-item indexed values for sequences of mappings
		// (e.g. venues[0].venue, venues[0].defaults.leverage, ...)
		if hasMappingItems(node) {
			for idx, item := range node.Content {
				if item.Kind != yaml.MappingNode {
					continue
				}
				itemPrefix := fmt.Sprintf("%s[%d]", prefix, idx)
				extractMappingValues(item, itemPrefix, values)
			}
		}
	}
}

// hasMappingItems returns true if any item in the sequence is a mapping.
func hasMappingItems(seq *yaml.Node) bool {
	for _, child := range seq.Content {
		if child.Kind == yaml.MappingNode {
			return true
		}
	}
	return false
}

// extractMappingValues walks a mapping node and writes all leaf values
// into the values map using dotted key paths relative to prefix.
func extractMappingValues(node *yaml.Node, prefix string, values map[string]interface{}) {
	if node.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i < len(node.Content)-1; i += 2 {
		keyNode := node.Content[i]
		valNode := node.Content[i+1]
		fullKey := prefix + "." + keyNode.Value
		switch valNode.Kind {
		case yaml.ScalarNode:
			values[fullKey] = parseScalarValue(valNode)
		case yaml.MappingNode:
			extractMappingValues(valNode, fullKey, values)
		case yaml.SequenceNode:
			values[fullKey] = extractArrayValue(valNode)
			if hasMappingItems(valNode) {
				for idx, item := range valNode.Content {
					if item.Kind == yaml.MappingNode {
						extractMappingValues(item, fmt.Sprintf("%s[%d]", fullKey, idx), values)
					}
				}
			}
		}
	}
}

// parseKeyParts splits a single path segment into a mapping key and optional array index.
// e.g. "venues[0]" → ("venues", 0, true), "venue" → ("venue", -1, false)
func parseKeyParts(part string) (string, int, bool) {
	idx := strings.Index(part, "[")
	if idx == -1 {
		return part, -1, false
	}
	key := part[:idx]
	indexStr := part[idx+1:]
	indexStr = strings.TrimSuffix(indexStr, "]")
	i, err := strconv.Atoi(indexStr)
	if err != nil {
		return part, -1, false
	}
	return key, i, true
}

// navigateToIndex finds the child node at the given key (and optional array index)
// within a mapping node.
func navigateToIndex(current *yaml.Node, key string, arrIdx int, hasArr bool) *yaml.Node {
	// First find the mapping key
	var valueNode *yaml.Node
	for j := 0; j < len(current.Content)-1; j += 2 {
		if current.Content[j].Value == key {
			valueNode = current.Content[j+1]
			break
		}
	}
	if valueNode == nil {
		return nil
	}
	if hasArr && valueNode.Kind == yaml.SequenceNode {
		if arrIdx >= 0 && arrIdx < len(valueNode.Content) {
			return valueNode.Content[arrIdx]
		}
		return nil
	}
	return valueNode
}

// updateFieldInNode updates a field in a YAML mapping node
func updateFieldInNode(root *yaml.Node, key string, value interface{}) {
	parts := strings.Split(key, ".")
	current := root

	// Navigate to the parent node
	for i := 0; i < len(parts)-1; i++ {
		part := parts[i]
		k, arrIdx, hasArr := parseKeyParts(part)

		next := navigateToIndex(current, k, arrIdx, hasArr)
		if next == nil {
			// Create intermediate nodes
			newKeyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: k}
			if hasArr {
				newSeqNode := &yaml.Node{Kind: yaml.SequenceNode}
				newMappingNode := &yaml.Node{Kind: yaml.MappingNode}
				newSeqNode.Content = append(newSeqNode.Content, newMappingNode)
				current.Content = append(current.Content, newKeyNode, newSeqNode)
				current = newMappingNode
			} else {
				newValueNode := &yaml.Node{Kind: yaml.MappingNode}
				current.Content = append(current.Content, newKeyNode, newValueNode)
				current = newValueNode
			}
		} else {
			current = next
		}
	}

	// Update the leaf field
	fieldName := parts[len(parts)-1]
	found := false

	for i := 0; i < len(current.Content)-1; i += 2 {
		keyNode := current.Content[i]
		valueNode := current.Content[i+1]

		if keyNode.Value == fieldName {
			updateScalarNode(valueNode, value)
			found = true
			break
		}
	}

	if !found {
		// Add new field
		newKeyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: fieldName}
		newValueNode := createScalarNode(value)
		current.Content = append(current.Content, newKeyNode, newValueNode)
	}
}

// parseScalarValue parses a YAML scalar node into a Go value
func parseScalarValue(node *yaml.Node) interface{} {
	switch node.Tag {
	case "!!bool":
		return node.Value == "true"
	case "!!int":
		if i, err := strconv.ParseInt(node.Value, 10, 64); err == nil {
			return i
		}
		return node.Value
	case "!!float":
		if f, err := strconv.ParseFloat(node.Value, 64); err == nil {
			return f
		}
		return node.Value
	default:
		return node.Value
	}
}

// extractArrayValue extracts an array from a YAML sequence node
func extractArrayValue(node *yaml.Node) []interface{} {
	result := []interface{}{}
	for _, item := range node.Content {
		switch item.Kind {
		case yaml.ScalarNode:
			result = append(result, parseScalarValue(item))
		case yaml.MappingNode, yaml.SequenceNode:
			// Serialize complex items to YAML string for textarea display
			out, err := yaml.Marshal(wrapNode(item))
			if err != nil {
				continue
			}
			result = append(result, strings.TrimRight(string(out), "\n"))
		}
	}
	return result
}

// updateScalarNode updates a scalar node with a new value.
//
// Tag is intentionally left empty: yaml.v3 infers the correct YAML tag from
// the rendered value (e.g. "200" → !!int, "0.5" → !!float, "true" → !!bool).
// Setting an explicit tag that disagrees with the value causes yaml.v3 to
// emit the tag as visible text in the output (e.g. "!!float 200").
func updateScalarNode(node *yaml.Node, value interface{}) {
	node.Kind = yaml.ScalarNode
	node.Tag = ""  // let yaml.v3 infer the tag from the value
	node.Style = 0 // plain style — no quotes for numbers/bools

	switch v := value.(type) {
	case bool:
		if v {
			node.Value = "true"
		} else {
			node.Value = "false"
		}
	case int:
		node.Value = strconv.FormatInt(int64(v), 10)
	case int64:
		node.Value = strconv.FormatInt(v, 10)
	case float64:
		node.Value = strconv.FormatFloat(v, 'g', -1, 64)
	case string:
		node.Value = v
	default:
		node.Value = fmt.Sprintf("%v", v)
	}
}

// createScalarNode creates a new scalar node from a value
func createScalarNode(value interface{}) *yaml.Node {
	node := &yaml.Node{Kind: yaml.ScalarNode}
	updateScalarNode(node, value)
	return node
}

// stripExplicitTags recursively clears explicit YAML tags (!!bool, !!int,
// !!float, !!str) from all scalar nodes in the tree. yaml.v3 infers the
// correct tag from the scalar value — explicit tags only cause rendering
// artefacts (e.g. "!!float 200" instead of "200").
//
// This acts as a safety net: if a prior buggy write embedded explicit tags
// into a config file, re-encoding the file will clean them up automatically.
func stripExplicitTags(n *yaml.Node) {
	if n == nil {
		return
	}
	if n.Kind == yaml.ScalarNode && n.Tag != "" {
		n.Tag = ""
	}
	for _, child := range n.Content {
		stripExplicitTags(child)
	}
}

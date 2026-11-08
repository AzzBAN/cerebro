package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// patch.go provides surgical, schema-preserving edits to the YAML config files.
//
// We deliberately do NOT marshal a loaded *Config back to disk: strategies.yaml
// uses the preset-map schema with a shared `defaults:` block deep-merged into
// each preset (see loadStrategies). Marshalling StrategiesConfig would flatten
// it to list-style, expand every inherited field, and drop comments — an
// irreversible schema change. Instead we walk the yaml.Node tree and replace
// only the targeted scalar, leaving structure, ordering, and comments intact.

// PatchStrategyField updates a single field on a single preset in
// strategies.yaml. dottedKey addresses the field within the preset mapping and
// may be nested, e.g. "risk_pct_per_trade" or "stop_loss.fixed_pct" or
// "take_profit_levels.0.rr_ratio" (numeric segments index a sequence node).
//
// The write is atomic (temp file + rename). The value is rendered with
// renderScalar so booleans/ints/floats keep their natural YAML form.
func PatchStrategyField(path, preset, dottedKey string, value any) error {
	return patchFile(path, func(root *yaml.Node) error {
		doc, err := mappingRoot(root)
		if err != nil {
			return err
		}
		presetNode, err := childValue(doc, preset)
		if err != nil {
			return fmt.Errorf("preset %q: %w", preset, err)
		}
		if presetNode.Kind != yaml.MappingNode {
			return fmt.Errorf("preset %q is not a mapping", preset)
		}
		return setDotted(presetNode, strings.Split(dottedKey, "."), value)
	})
}

// PatchAppField updates a single top-level scalar field in app.yaml, e.g.
// "aggressiveness". Nested keys are supported via dotted notation.
func PatchAppField(path, dottedKey string, value any) error {
	return patchFile(path, func(root *yaml.Node) error {
		doc, err := mappingRoot(root)
		if err != nil {
			return err
		}
		return setDotted(doc, strings.Split(dottedKey, "."), value)
	})
}

// patchFile loads path into a yaml.Node tree, applies mutate, and writes the
// result back atomically. The document is re-encoded with 2-space indentation
// to match the existing files.
func patchFile(path string, mutate func(root *yaml.Node) error) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if err := mutate(&root); err != nil {
		return err
	}

	// Sanitize: strip explicit tags that a previous buggy write may have left.
	stripExplicitTags(&root)

	var buf strings.Builder
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&root); err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	_ = enc.Close()

	return writeFileAtomic(path, []byte(buf.String()))
}

// writeFileAtomic writes data to a sibling temp file then renames it over path,
// so a crash mid-write never leaves a half-written config on disk.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename temp over %s: %w", path, err)
	}
	return nil
}

// mappingRoot unwraps a document node to its top-level mapping node.
func mappingRoot(root *yaml.Node) (*yaml.Node, error) {
	if root.Kind == yaml.DocumentNode {
		if len(root.Content) == 0 {
			return nil, fmt.Errorf("empty yaml document")
		}
		root = root.Content[0]
	}
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("yaml root is not a mapping")
	}
	return root, nil
}

// childValue returns the value node for key within a mapping node.
func childValue(mapping *yaml.Node, key string) (*yaml.Node, error) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1], nil
		}
	}
	return nil, fmt.Errorf("key %q not found", key)
}

// setDotted walks a path of keys (and numeric sequence indices) from node and
// sets the final scalar to value. Intermediate keys must already exist — we
// never create new structure, only edit known fields.
func setDotted(node *yaml.Node, keys []string, value any) error {
	cur := node
	for i, k := range keys {
		last := i == len(keys)-1

		if cur.Kind == yaml.SequenceNode {
			idx, err := strconv.Atoi(k)
			if err != nil {
				return fmt.Errorf("expected numeric index for sequence, got %q", k)
			}
			if idx < 0 || idx >= len(cur.Content) {
				return fmt.Errorf("index %d out of range (len %d)", idx, len(cur.Content))
			}
			if last {
				return setScalar(cur.Content[idx], value)
			}
			cur = cur.Content[idx]
			continue
		}

		if cur.Kind != yaml.MappingNode {
			return fmt.Errorf("cannot descend into %q: not a mapping", k)
		}
		child, err := childValue(cur, k)
		if err != nil {
			return err
		}
		if last {
			return setScalar(child, value)
		}
		cur = child
	}
	return fmt.Errorf("empty key path")
}

// setScalar overwrites a scalar node's value, preserving its position and any
// line comment. The rendered form is chosen to match the value's Go type.
//
// Tag is intentionally left empty: yaml.v3 infers the correct YAML tag from
// the rendered value (e.g. "200" → !!int, "0.5" → !!float, "true" → !!bool).
// Setting an explicit tag that disagrees with the value causes yaml.v3 to
// emit the tag as visible text in the output (e.g. "!!float 200").
func setScalar(n *yaml.Node, value any) error {
	if n.Kind != yaml.ScalarNode {
		return fmt.Errorf("target is not a scalar (kind %d)", n.Kind)
	}
	n.Value = renderScalar(value)
	n.Tag = ""  // let yaml.v3 infer the tag from the value
	n.Style = 0 // plain style — no quotes for numbers/bools
	return nil
}

// renderScalar converts a Go value to its YAML scalar string representation.
// The caller is responsible for clearing the tag on the target node so that
// yaml.v3 infers the correct tag from the value itself.
func renderScalar(value any) string {
	switch v := value.(type) {
	case bool:
		return strconv.FormatBool(v)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		// 'f' with -1 precision yields the shortest exact representation,
		// so 0.5 stays "0.5" rather than "0.50000".
		return strconv.FormatFloat(v, 'f', -1, 64)
	case string:
		return v
	default:
		return fmt.Sprintf("%v", v)
	}
}

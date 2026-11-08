package app

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	webadapter "github.com/azhar/cerebro/internal/adapter/web"
	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
)

// configFileEditor implements webadapter.ConfigFileEditor. It owns the live
// config file paths and the hot-reload mechanism so the web dashboard can edit
// app.yaml / markets.yaml / strategies.yaml / secrets.env and have the change
// take effect without a restart.
//
// Safety: edits are validated by a full config.Load + Validate before they are
// accepted. A fresh strategy set is warmed (blocking REST) BEFORE the atomic
// swap, so the running engine never sees a cold strategy. Any failure restores
// the pre-edit file and leaves the live engine untouched.
type configFileEditor struct {
	ptr        *atomic.Pointer[[]strategyCandidate]
	secrets    string
	appPath    string
	markets    string
	strategies string
	env        domain.Environment
	kc         *klineClients
	symbolMeta map[domain.Symbol]symbolMeta
	web        *webadapter.Server

	mu  sync.Mutex     // serialises concurrent edits + guards cfg
	cfg *config.Config // latest applied config
}

func newConfigFileEditor(
	ptr *atomic.Pointer[[]strategyCandidate],
	cfgDir string,
	cfg *config.Config,
	env domain.Environment,
	kc *klineClients,
	symbolMeta map[domain.Symbol]symbolMeta,
	web *webadapter.Server,
) *configFileEditor {
	secrets, appPath, markets, strategies := BuildConfigPaths(cfgDir)
	return &configFileEditor{
		ptr:        ptr,
		secrets:    secrets,
		appPath:    appPath,
		markets:    markets,
		strategies: strategies,
		env:        env,
		kc:         kc,
		symbolMeta: symbolMeta,
		web:        web,
		cfg:        cfg,
	}
}

// List returns the names of all editable config files.
func (e *configFileEditor) List() []string {
	return []string{"app.yaml", "markets.yaml", "strategies.yaml", "secrets.env"}
}

// Read returns one config file's raw content by name.
func (e *configFileEditor) Read(name string) (webadapter.ConfigFileDTO, error) {
	path := e.pathFor(name)
	if path == "" {
		return webadapter.ConfigFileDTO{}, fmt.Errorf("unknown config file: %s", name)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return webadapter.ConfigFileDTO{}, fmt.Errorf("read %s: %w", name, err)
	}
	return webadapter.ConfigFileDTO{Name: name, Content: string(content)}, nil
}

// Save writes one config file's raw content, then hot-reloads the engine.
// On any failure the on-disk file is restored and the live engine is left
// unchanged. It returns the post-save config snapshot for broadcasting.
func (e *configFileEditor) Save(ctx context.Context, name string, content string) (webadapter.ConfigDTO, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	path := e.pathFor(name)
	if path == "" {
		return webadapter.ConfigDTO{}, fmt.Errorf("unknown config file: %s", name)
	}

	// Back up the current file so a validation failure can be restored.
	backup, err := os.ReadFile(path)
	if err != nil {
		return webadapter.ConfigDTO{}, fmt.Errorf("backup %s: %w", name, err)
	}

	// Write the new content to disk.
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return webadapter.ConfigDTO{}, fmt.Errorf("write %s: %w", name, err)
	}

	// Re-load + validate the whole config before accepting the edit.
	newCfg, err := config.Load(e.secrets, e.appPath, e.markets, e.strategies)
	if err != nil {
		_ = os.WriteFile(path, backup, 0o644)
		return webadapter.ConfigDTO{}, fmt.Errorf("reload config: %w", err)
	}
	if err := newCfg.Validate(e.env); err != nil {
		_ = os.WriteFile(path, backup, 0o644)
		return webadapter.ConfigDTO{}, fmt.Errorf("invalid config after edit: %w", err)
	}

	// Build + WARM the new strategy set before swapping it in. The old slice
	// keeps serving signals during this (blocking) warmup, so there is no gap.
	newReg := registerStrategies(newCfg)
	warmupStrategies(ctx, newReg, e.kc, newCfg.Markets, e.symbolMeta)

	// Atomic swap: the engine picks up newReg on its next candle event.
	e.ptr.Store(&newReg)
	e.cfg = newCfg

	dto := webadapter.ConfigToDTO(newCfg)
	if e.web != nil {
		e.web.SetConfig(dto)
	}
	return dto, nil
}

// convertSectionToDTO converts a config.ConfigFormSection to webadapter.ConfigFormSectionDTO
func convertSectionToDTO(section config.ConfigFormSection) webadapter.ConfigFormSectionDTO {
	// Always produce non-nil slices so JSON encodes [] instead of null.
	dtoFields := make([]webadapter.ConfigFormFieldDTO, 0, len(section.Fields))
	for _, f := range section.Fields {
		dtoFields = append(dtoFields, webadapter.ConfigFormFieldDTO{
			Name:        f.Name,
			Label:       f.Label,
			Type:        f.Type,
			Value:       f.Value,
			Options:     nonNilStringSlice(f.Options),
			Description: f.Description,
			Required:    f.Required,
			Min:         f.Min,
			Max:         f.Max,
			Step:        f.Step,
		})
	}

	dtoSubsections := make([]webadapter.ConfigFormSectionDTO, 0, len(section.Sections))
	for _, sub := range section.Sections {
		dtoSubsections = append(dtoSubsections, convertSectionToDTO(sub))
	}

	return webadapter.ConfigFormSectionDTO{
		Title:       section.Title,
		Description: section.Description,
		Key:         section.Key,
		Fields:      dtoFields,
		Sections:    dtoSubsections,
	}
}

// nonNilStringSlice returns an empty (non-nil) slice when the input is nil,
// so JSON encoding produces [] instead of null.
func nonNilStringSlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// FormSchema returns the form schema for a config file, describing all editable
// fields with their types, current values, and metadata.
func (e *configFileEditor) FormSchema(name string) (webadapter.ConfigFormSchemaDTO, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	path := e.pathFor(name)
	if path == "" {
		return webadapter.ConfigFormSchemaDTO{}, fmt.Errorf("unknown config file: %s", name)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return webadapter.ConfigFormSchemaDTO{}, fmt.Errorf("read %s: %w", name, err)
	}

	sections, err := config.GenerateSchemaFromYAML(content, name)
	if err != nil {
		return webadapter.ConfigFormSchemaDTO{}, fmt.Errorf("generate schema for %s: %w", name, err)
	}

	dtoSections := make([]webadapter.ConfigFormSectionDTO, len(sections))
	for i, s := range sections {
		dtoSections[i] = convertSectionToDTO(s)
	}

	return webadapter.ConfigFormSchemaDTO{
		File:     name,
		Sections: dtoSections,
	}, nil
}

// FormRead returns the current field values for a config file as a map.
// Keys are field names (e.g., "environment"), values are the parsed field values.
func (e *configFileEditor) FormRead(name string) (map[string]interface{}, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	path := e.pathFor(name)
	if path == "" {
		return nil, fmt.Errorf("unknown config file: %s", name)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}

	values, err := config.ExtractValuesFromYAML(content)
	if err != nil {
		return nil, fmt.Errorf("extract values from %s: %w", name, err)
	}

	return values, nil
}

// FormWrite applies field changes from a map, then hot-reloads the engine.
// The fields map contains field names as keys and their new values.
// On any failure the on-disk file is restored and the live engine is unchanged.
func (e *configFileEditor) FormWrite(ctx context.Context, name string, fields map[string]interface{}) (webadapter.ConfigDTO, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	path := e.pathFor(name)
	if path == "" {
		return webadapter.ConfigDTO{}, fmt.Errorf("unknown config file: %s", name)
	}

	// Back up the current file so a validation failure can be restored.
	backup, err := os.ReadFile(path)
	if err != nil {
		return webadapter.ConfigDTO{}, fmt.Errorf("backup %s: %w", name, err)
	}

	// Read current content and apply field updates
	currentContent, err := os.ReadFile(path)
	if err != nil {
		return webadapter.ConfigDTO{}, fmt.Errorf("read current %s: %w", name, err)
	}

	updatedContent, err := config.UpdateYAMLWithFields(currentContent, fields)
	if err != nil {
		return webadapter.ConfigDTO{}, fmt.Errorf("update %s with fields: %w", name, err)
	}

	// Write the updated content to disk
	if err := os.WriteFile(path, updatedContent, 0o644); err != nil {
		return webadapter.ConfigDTO{}, fmt.Errorf("write %s: %w", name, err)
	}

	// Re-load + validate the whole config before accepting the edit
	newCfg, err := config.Load(e.secrets, e.appPath, e.markets, e.strategies)
	if err != nil {
		_ = os.WriteFile(path, backup, 0o644)
		return webadapter.ConfigDTO{}, fmt.Errorf("reload config: %w", err)
	}
	if err := newCfg.Validate(e.env); err != nil {
		_ = os.WriteFile(path, backup, 0o644)
		return webadapter.ConfigDTO{}, fmt.Errorf("invalid config after edit: %w", err)
	}

	// Build + WARM the new strategy set before swapping it in
	newReg := registerStrategies(newCfg)
	warmupStrategies(ctx, newReg, e.kc, newCfg.Markets, e.symbolMeta)

	// Atomic swap: the engine picks up newReg on its next candle event
	e.ptr.Store(&newReg)
	e.cfg = newCfg

	dto := webadapter.ConfigToDTO(newCfg)
	if e.web != nil {
		e.web.SetConfig(dto)
	}
	return dto, nil
}

// pathFor maps a config file name to its absolute path.
func (e *configFileEditor) pathFor(name string) string {
	switch name {
	case "app.yaml":
		return e.appPath
	case "markets.yaml":
		return e.markets
	case "strategies.yaml":
		return e.strategies
	case "secrets.env":
		return e.secrets
	default:
		return ""
	}
}

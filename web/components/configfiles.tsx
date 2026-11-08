"use client";

import { useState, useEffect, useCallback } from "react";
import { Panel } from "./shared";
import {
  getConfigFiles,
  getConfigFormSchema,
  getConfigFormValues,
  saveConfigFormValues,
} from "@/lib/api";
import type {
  ConfigFormSchema,
  ConfigFormSection,
  ConfigFormField,
} from "@/lib/types";

interface ConfigFileTab {
  name: string;
  label: string;
}

const FILE_LABELS: Record<string, string> = {
  "app.yaml": "app.yaml",
  "markets.yaml": "markets.yaml",
  "strategies.yaml": "strategies.yaml",
  "secrets.env": "secrets.env",
};

function FormField({
  field,
  value,
  onChange,
}: {
  field: ConfigFormField;
  value: string | number | boolean;
  onChange: (v: string | number | boolean) => void;
}) {
  const id = `field-${field.name.replace(/\./g, "-")}`;

  switch (field.type) {
    case "bool":
    case "boolean":
      return (
        <div className="flex items-center gap-2 py-1">
          <label htmlFor={id} className="text-sm text-fg flex-1">
            {field.label}
            {field.required && <span className="text-red-400 ml-1">*</span>}
          </label>
          <input
            id={id}
            type="checkbox"
            checked={!!value}
            onChange={(e) => onChange(e.target.checked)}
            className="h-4 w-4 accent-agent"
          />
        </div>
      );

    case "select":
      return (
        <div className="flex items-center gap-2 py-1">
          <label htmlFor={id} className="text-sm text-fg flex-1">
            {field.label}
            {field.required && <span className="text-red-400 ml-1">*</span>}
          </label>
          <select
            id={id}
            value={String(value ?? "")}
            onChange={(e) => onChange(e.target.value)}
            className="w-40 rounded bg-bg border border-border px-2 py-1 text-sm text-fg"
          >
            {(field.options ?? []).map((opt) => (
              <option key={opt} value={opt}>
                {opt}
              </option>
            ))}
          </select>
        </div>
      );

    case "number":
    case "int":
    case "float":
      return (
        <div className="flex items-center gap-2 py-1">
          <label htmlFor={id} className="text-sm text-fg flex-1">
            {field.label}
            {field.required && <span className="text-red-400 ml-1">*</span>}
          </label>
          <input
            id={id}
            type="number"
            value={typeof value === "boolean" ? (value ? 1 : 0) : (value ?? "")}
            step={field.step ?? (field.type === "float" ? 0.01 : 1)}
            min={field.min}
            max={field.max}
            onChange={(e) => {
              const v = e.target.value;
              if (v === "") onChange(0);
              else if (field.type === "float" || field.step === 0.01)
                onChange(parseFloat(v));
              else onChange(parseInt(v, 10));
            }}
            className="w-40 rounded bg-bg border border-border px-2 py-1 text-sm text-fg"
          />
        </div>
      );

    case "text":
    default:
      return (
        <div className="flex items-center gap-2 py-1">
          <label htmlFor={id} className="text-sm text-fg flex-1">
            {field.label}
            {field.required && <span className="text-red-400 ml-1">*</span>}
          </label>
          <input
            id={id}
            type="text"
            value={String(value ?? "")}
            onChange={(e) => onChange(e.target.value)}
            className="w-40 rounded bg-bg border border-border px-2 py-1 text-sm text-fg"
          />
        </div>
      );

    case "textarea":
      return (
        <div className="flex flex-col gap-2 py-2">
          <label htmlFor={id} className="text-sm text-fg font-medium">
            {field.label}
            {field.required && <span className="text-red-400 ml-1">*</span>}
          </label>
          {field.description && (
            <p className="text-xs text-fg-dim">{field.description}</p>
          )}
          <textarea
            id={id}
            value={String(value ?? "")}
            onChange={(e) => onChange(e.target.value)}
            rows={12}
            className="w-full rounded bg-bg border border-border px-3 py-2 text-sm text-fg font-mono"
            style={{ minHeight: "200px", resize: "vertical" }}
          />
        </div>
      );
  }
}

function FormSection({
  section,
  values,
  onChange,
}: {
  section: ConfigFormSection;
  values: Record<string, unknown>;
  onChange: (name: string, value: string | number | boolean) => void;
}) {
  const [collapsed, setCollapsed] = useState(false);

  return (
    <div className="mb-4 border border-border rounded">
      <button
        onClick={() => setCollapsed(!collapsed)}
        className="w-full flex items-center justify-between px-3 py-2 bg-bg-alt text-sm font-semibold text-fg hover:bg-bg-alt/80"
      >
        <span>{section.title}</span>
        <span className="text-xs text-fg-dim">{collapsed ? "▸" : "▾"}</span>
      </button>

      {!collapsed && (
        <div className="px-3 py-2 space-y-1">
          {section.description && (
            <p className="text-xs text-fg-dim mb-2">{section.description}</p>
          )}

          {section.fields.map((field) => (
            <div key={field.name} className="relative group">
              <FormField
                field={field}
                value={
                  (values[field.name] as string | number | boolean) ??
                  field.value
                }
                onChange={(v) => onChange(field.name, v)}
              />
              {field.description && (
                <div className="hidden group-hover:block absolute z-10 left-0 top-full mt-1 bg-bg-alt border border-border rounded px-2 py-1 text-xs text-fg-dim max-w-xs whitespace-pre-wrap">
                  {field.description}
                </div>
              )}
            </div>
          ))}

          {section.sections?.map((sub) => (
            <FormSection
              key={sub.key}
              section={sub}
              values={values}
              onChange={onChange}
            />
          ))}
        </div>
      )}
    </div>
  );
}

export function ConfigFilesView() {
  const [files, setFiles] = useState<ConfigFileTab[]>([]);
  const [activeFile, setActiveFile] = useState<string | null>(null);
  const [schema, setSchema] = useState<ConfigFormSchema | null>(null);
  const [values, setValues] = useState<Record<string, unknown>>({});
  const [dirty, setDirty] = useState<Record<string, unknown>>({});
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [success, setSuccess] = useState("");

  // Load file list
  useEffect(() => {
    setLoading(true);
    getConfigFiles()
      .then((names) => {
        const tabs = names.map((name) => ({
          name,
          label: FILE_LABELS[name] ?? name,
        }));
        setFiles(tabs);
        if (tabs.length > 0) setActiveFile(tabs[0].name);
      })
      .catch((e) => setError(e.message))
      .finally(() => setLoading(false));
  }, []);

  // Load schema + values when active file changes
  const loadFile = useCallback(async (name: string) => {
    setLoading(true);
    setError("");
    setSuccess("");
    try {
      const [s, v] = await Promise.all([
        getConfigFormSchema(name),
        getConfigFormValues(name),
      ]);
      setSchema(s);
      setValues(v);
      setDirty({});
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (activeFile) loadFile(activeFile);
  }, [activeFile, loadFile]);

  const handleFieldChange = (name: string, value: string | number | boolean) => {
    setValues((prev) => ({ ...prev, [name]: value }));
    setDirty((prev) => ({ ...prev, [name]: value }));
  };

  const handleSave = async () => {
    if (!activeFile || Object.keys(dirty).length === 0 || saving) return;
    setSaving(true);
    setError("");
    setSuccess("");
    try {
      await saveConfigFormValues(activeFile, dirty);
      setDirty({});
      setSuccess("Saved & hot-reloaded");
      // Reload values to confirm
      const v = await getConfigFormValues(activeFile);
      setValues(v);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  };

  const handleReset = () => {
    loadFile(activeFile!);
  };

  const isDirty = Object.keys(dirty).length > 0;

  return (
    <div className="h-full flex flex-col">
      {/* File tabs */}
      <div className="flex gap-1 px-2 py-1 border-b border-border">
        {files.map((f) => (
          <button
            key={f.name}
            onClick={() => setActiveFile(f.name)}
            className={`px-3 py-1 text-sm rounded ${
              activeFile === f.name
                ? "bg-agent text-bg"
                : "bg-bg-alt text-fg-dim hover:text-fg"
            }`}
          >
            {f.label}
          </button>
        ))}
      </div>

      {/* Error / success banners */}
      {error && (
        <div className="mx-2 mt-2 px-3 py-2 bg-red-900/30 border border-red-700 rounded text-sm text-red-300">
          {error}
        </div>
      )}
      {success && (
        <div className="mx-2 mt-2 px-3 py-2 bg-green-900/30 border border-green-700 rounded text-sm text-green-300">
          ✓ {success}
        </div>
      )}

      {/* Main content */}
      {loading && !schema ? (
        <div className="flex-1 flex items-center justify-center text-fg-dim">
          Loading...
        </div>
      ) : schema ? (
        <div className="flex-1 overflow-y-auto px-4 py-3">
          {schema.sections.map((section) => (
            <FormSection
              key={section.key}
              section={section}
              values={values}
              onChange={handleFieldChange}
            />
          ))}
        </div>
      ) : (
        <div className="flex-1 flex items-center justify-center text-fg-dim">
          No config files found
        </div>
      )}

      {/* Bottom bar */}
      <div className="flex items-center justify-between px-4 py-2 border-t border-border">
        <span className="text-xs text-fg-dim">
          {activeFile && FILE_LABELS[activeFile]}
          {isDirty && " (unsaved changes)"}
        </span>
        <div className="flex gap-2">
          {isDirty && (
            <button
              onClick={handleReset}
              className="px-3 py-1 text-sm rounded border border-border text-fg-dim hover:text-fg"
            >
              Reset
            </button>
          )}
          <button
            onClick={handleSave}
            disabled={saving || !isDirty}
            className="px-3 py-1 text-sm rounded bg-agent text-bg disabled:opacity-50 disabled:cursor-not-allowed hover:opacity-90"
          >
            {saving ? "Saving..." : "Save & Reload"}
          </button>
        </div>
      </div>
    </div>
  );
}

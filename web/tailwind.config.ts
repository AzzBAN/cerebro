import type { Config } from "tailwindcss";

/**
 * Cerebro terminal palette — mirrors the TUI lipgloss colors and the Stitch
 * "Cerebro Terminal" design system. Dark, monospace, 1px borders, semantic
 * cyan/green/red/yellow/magenta. No shadows, minimal radii.
 */
const config: Config = {
  content: ["./app/**/*.{ts,tsx}", "./components/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        bg: "#1c1c1c",
        "bg-alt": "#262626",
        panel: "#1f1f1f",
        border: "#3a3a3a",
        "border-focus": "#5FAFD7",
        fg: "#d4d4d4",
        "fg-dim": "#767676",
        accent: "#5FAFD7",
        bull: "#87D787",
        bear: "#FF5F5F",
        warn: "#FFD75F",
        agent: "#D78FD7",
        teal: "#5FD7D7",
      },
      fontFamily: {
        mono: ["var(--font-jetbrains)", "JetBrains Mono", "ui-monospace", "monospace"],
      },
      fontSize: {
        "2xs": ["10px", "14px"],
        xs: ["11px", "15px"],
        sm: ["12px", "16px"],
        base: ["13px", "18px"],
      },
    },
  },
  plugins: [],
};

export default config;

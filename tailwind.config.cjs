/** UI theme derived from CC Switch, used under its MIT license. */
module.exports = {
  content: ["./index.html", "./src/**/*.{js,ts,jsx,tsx}"],
  darkMode: ["selector", ".dark"],
  theme: {
    extend: {
      colors: {
        background: "hsl(var(--background))",
        foreground: "hsl(var(--foreground))",
        card: { DEFAULT: "hsl(var(--card))", foreground: "hsl(var(--card-foreground))" },
        muted: { DEFAULT: "hsl(var(--muted))", foreground: "hsl(var(--muted-foreground))" },
        primary: { DEFAULT: "hsl(var(--primary))", foreground: "hsl(var(--primary-foreground))" },
        secondary: { DEFAULT: "hsl(var(--secondary))", foreground: "hsl(var(--secondary-foreground))" },
        destructive: { DEFAULT: "hsl(var(--destructive))", foreground: "hsl(var(--destructive-foreground))" },
        border: "hsl(var(--border))",
        input: "hsl(var(--input))",
        ring: "hsl(var(--ring))",
        blue: { 400: "#409CFF", 500: "#0A84FF", 600: "#0060DF", 700: "#0047A3" },
        gray: { 50: "#fafafa", 100: "#f4f4f5", 200: "#e4e4e7", 300: "#d4d4d8", 400: "#a1a1aa", 500: "#71717a", 600: "#636366", 700: "#48484A", 800: "#3A3A3C", 900: "#2C2C2E", 950: "#1C1C1E" },
      },
      borderRadius: { lg: "0.75rem", xl: "0.875rem" },
      fontFamily: { sans: ["-apple-system", "BlinkMacSystemFont", "Segoe UI", "sans-serif"] },
    },
  },
  plugins: [],
};

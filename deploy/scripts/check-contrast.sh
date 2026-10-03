#!/bin/bash
# check-contrast.sh - Verify WCAG AA contrast ratios for theme tokens
# Per PRD 18.2#10: Light/Dark two-state theme must meet contrast requirements
#   - Text on background: ≥4.5:1 (WCAG AA)
#   - Large text/graphics: ≥3:1 (WCAG AA)
#
# Usage: ./deploy/scripts/check-contrast.sh [theme-file]
# Default theme file: web/src/styles/theme.css

set -euo pipefail

THEME_FILE="${1:-web/src/styles/theme.css}"

if [ ! -f "$THEME_FILE" ]; then
  echo "ERROR: Theme file not found: $THEME_FILE"
  exit 1
fi

echo "Checking contrast ratios in: $THEME_FILE"
echo ""

# Check if Node.js is available
if ! command -v node &> /dev/null; then
  echo "WARNING: Node.js not found. Using basic grep-based checks."
  echo ""

  # Basic checks: ensure no hardcoded colors in sub-packages
  echo "Checking for hardcoded color values in source files..."

  # Find Vue files that might have hardcoded colors
  HARDCODED=$(find web/src/pages -name "*.vue" -type f -exec grep -l 'color:\s*#[0-9a-fA-F]\{6\}\|background-color:\s*#[0-9a-fA-F]\{6\}' {} \; 2>/dev/null || true)

  if [ -n "$HARDCODED" ]; then
    echo "FAIL: Found hardcoded color values in:"
    echo "$HARDCODED"
    echo ""
    echo "These files should use CSS variables from theme.css instead."
    exit 1
  else
    echo "PASS: No hardcoded color values found in page components."
  fi

  # Check that theme.css exists and has both light and dark modes
  if grep -q ":root" "$THEME_FILE" && grep -q "\[data-theme='dark'\]" "$THEME_FILE"; then
    echo "PASS: Theme file contains both light and dark mode definitions."
  else
    echo "FAIL: Theme file missing light or dark mode definitions."
    exit 1
  fi

  echo ""
  echo "All basic checks passed!"
  exit 0
fi

# Use Node.js for proper contrast ratio calculation
node <<'NODE_SCRIPT'
const fs = require('fs');
const path = require('path');

// Parse hex color to RGB
function hexToRgb(hex) {
  const result = /^#?([a-f\d]{2})([a-f\d]{2})([a-f\d]{2})$/i.exec(hex);
  return result ? {
    r: parseInt(result[1], 16),
    g: parseInt(result[2], 16),
    b: parseInt(result[3], 16)
  } : null;
}

// Calculate relative luminance (WCAG 2.0)
function getLuminance(r, g, b) {
  const a = [r, g, b].map(function (v) {
    v /= 255;
    return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4);
  });
  return a[0] * 0.2126 + a[1] * 0.7152 + a[2] * 0.0722;
}

// Calculate contrast ratio between two colors
function getContrastRatio(rgb1, rgb2) {
  const lum1 = getLuminance(rgb1.r, rgb1.g, rgb1.b);
  const lum2 = getLuminance(rgb2.r, rgb2.g, rgb2.b);
  const brightest = Math.max(lum1, lum2);
  const darkest = Math.min(lum1, lum2);
  return (brightest + 0.05) / (darkest + 0.05);
}

// Read theme file
const themeFile = process.argv[2] || 'web/src/styles/theme.css';
const content = fs.readFileSync(themeFile, 'utf8');

// Extract color variables from :root (light mode)
const lightVars = {};
const rootMatch = content.match(/:root\s*\{([^}]+)\}/s);
if (rootMatch) {
  const rootContent = rootMatch[1];
  let match;
  const varRegex = /--([\w-]+):\s*(#[0-9a-fA-F]{6})\s*;/g;
  while ((match = varRegex.exec(rootContent)) !== null) {
    lightVars[match[1]] = match[2];
  }
}

console.log(`Found ${Object.keys(lightVars).length} light mode color variables`);
console.log('');

// Define critical text/background pairs to check for light mode
const checks = [
  // Light mode checks
  { name: 'Light: text-primary on bg-primary', fg: 'text-primary', bg: 'bg-primary', minRatio: 4.5 },
  { name: 'Light: text-secondary on bg-primary', fg: 'text-secondary', bg: 'bg-primary', minRatio: 4.5 },
  { name: 'Light: text-tertiary on bg-primary', fg: 'text-tertiary', bg: 'bg-primary', minRatio: 4.5 },
];

let allPassed = true;

checks.forEach(check => {
  const fgColor = lightVars[check.fg];
  const bgColor = lightVars[check.bg];

  if (!fgColor || !bgColor) {
    console.log(`SKIP: ${check.name} (missing color variable)`);
    return;
  }

  const fgRgb = hexToRgb(fgColor);
  const bgRgb = hexToRgb(bgColor);

  if (!fgRgb || !bgRgb) {
    console.log(`SKIP: ${check.name} (invalid hex color)`);
    return;
  }

  const ratio = getContrastRatio(fgRgb, bgRgb);
  const passed = ratio >= check.minRatio;

  if (!passed) {
    allPassed = false;
  }

  const status = passed ? 'PASS' : 'FAIL';
  console.log(`${status}: ${check.name}`);
  console.log(`  Foreground: ${fgColor}, Background: ${bgColor}`);
  console.log(`  Contrast ratio: ${ratio.toFixed(2)}:1 (required: ${check.minRatio}:1)`);
  console.log('');
});

// Manual check for dark mode by parsing the dark mode section
const darkModeMatch = content.match(/\[data-theme='dark'\]\s*\{([^}]+)\}/s);
if (darkModeMatch) {
  const darkContent = darkModeMatch[1];
  const darkVars = {};
  let match;
  const varRegex = /--([\w-]+):\s*(#[0-9a-fA-F]{6})\s*;/g;

  while ((match = varRegex.exec(darkContent)) !== null) {
    darkVars[match[1]] = match[2];
  }

  console.log('Dark mode checks:');
  const darkChecks = [
    { name: 'Dark: text-primary on bg-primary', fg: 'text-primary', bg: 'bg-primary', minRatio: 4.5 },
    { name: 'Dark: text-secondary on bg-primary', fg: 'text-secondary', bg: 'bg-primary', minRatio: 4.5 },
    { name: 'Dark: text-tertiary on bg-primary', fg: 'text-tertiary', bg: 'bg-primary', minRatio: 4.5 },
  ];

  darkChecks.forEach(check => {
    const fgColor = darkVars[check.fg];
    const bgColor = darkVars[check.bg];

    if (!fgColor || !bgColor) {
      console.log(`SKIP: ${check.name} (missing color variable)`);
      return;
    }

    const fgRgb = hexToRgb(fgColor);
    const bgRgb = hexToRgb(bgColor);

    if (!fgRgb || !bgRgb) {
      console.log(`SKIP: ${check.name} (invalid hex color)`);
      return;
    }

    const ratio = getContrastRatio(fgRgb, bgRgb);
    const passed = ratio >= check.minRatio;

    if (!passed) {
      allPassed = false;
    }

    const status = passed ? 'PASS' : 'FAIL';
    console.log(`${status}: ${check.name}`);
    console.log(`  Foreground: ${fgColor}, Background: ${bgColor}`);
    console.log(`  Contrast ratio: ${ratio.toFixed(2)}:1 (required: ${check.minRatio}:1)`);
    console.log('');
  });
} else {
  console.log('WARNING: Dark mode section not found in theme file');
}

// Check for hardcoded colors in sub-packages
const { execSync } = require('child_process');
try {
  const result = execSync(
    'find web/src/pages -name "*.vue" -type f -exec grep -l "color:\\s*#[0-9a-fA-F]\\{6\\}\\|background-color:\\s*#[0-9a-fA-F]\\{6\\}" {} \\; 2>/dev/null || true',
    { encoding: 'utf8' }
  );

  if (result.trim()) {
    console.log('FAIL: Found hardcoded color values in:');
    console.log(result);
    console.log('These files should use CSS variables from theme.css instead.');
    allPassed = false;
  } else {
    console.log('PASS: No hardcoded color values found in page components.');
  }
} catch (e) {
  console.log('WARNING: Could not check for hardcoded colors');
}

console.log('');
if (allPassed) {
  console.log('✓ All contrast checks passed!');
  process.exit(0);
} else {
  console.log('✗ Some contrast checks failed!');
  process.exit(1);
}
NODE_SCRIPT
